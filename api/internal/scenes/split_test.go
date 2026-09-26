package scenes

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
)

var (
	linMo    = uuid.MustParse("0192a0c0-0000-7000-8000-000000000001")
	elderQiu = uuid.MustParse("0192a0c0-0000-7000-8000-000000000002")
)

func testIndex() NameIndex {
	return NewNameIndex([]CharacterNames{
		{ID: linMo, Names: []string{"林默", "Lin Mo", "Lâm Mặc"}},
		{ID: elderQiu, Names: []string{"邱长老", "Elder Qiu", "Trưởng lão Khâu"}},
	})
}

func TestSplitSpansKeepsQuotesWithTheirMarks(t *testing.T) {
	spans := SplitSpans(`"You carry the scent of a broken meridian," Elder Qiu said. “Kneel.” He did not.`)
	want := []Span{
		{Text: `"You carry the scent of a broken meridian,"`, Quote: true},
		{Text: "Elder Qiu said."},
		{Text: "“Kneel.”", Quote: true},
		{Text: "He did not."},
	}
	if !slices.Equal(spans, want) {
		t.Fatalf("spans = %#v", spans)
	}
	if got := SplitSpans(`He shouted "never`); len(got) != 2 || !got[1].Quote {
		t.Fatalf("an unclosed quote runs to the end: %#v", got)
	}
}

func TestNameMappingIsCaseFoldedAcrossLanguages(t *testing.T) {
	idx := testIndex()
	for _, name := range []string{"lin mo", "LIN  MO", "林默", "lâm mặc", " Elder Qiu "} {
		if _, ok := idx.Resolve(name); !ok {
			t.Fatalf("%q did not resolve", name)
		}
	}
	if id, _ := idx.Resolve("elder qiu"); id != elderQiu {
		t.Fatalf("elder qiu -> %s", id)
	}
	speaker, flagged := idx.Speaker("Narrator")
	if speaker != nil || flagged != "" {
		t.Fatal("the narrator is not a flagged speaker")
	}
	speaker, flagged = idx.Speaker("Su Yao")
	if speaker != nil || flagged != "Su Yao" {
		t.Fatalf("an unknown name must become the flagged narrator, got %v %q", speaker, flagged)
	}
}

func TestForgedUUIDFromTheModelIsNeverTrustedAsAnID(t *testing.T) {
	idx := testIndex()
	// A model echoing a real character's id (or any id) gets it looked up
	// as a name: it matches nothing and is flagged, never used as an id.
	for _, forged := range []string{linMo.String(), uuid.NewString()} {
		speaker, flagged := idx.Speaker(forged)
		if speaker != nil || flagged != forged {
			t.Fatalf("forged id %s resolved to %v", forged, speaker)
		}
	}
	sp := buildSplitPrompt([]Paragraph{{ID: "a", Text: `"Go," said someone.`}})
	drafts, unrecognised := NormalizeLLMSplit(sp, decodeSplit(t, `{"scenes":[{"start":"P1","imagePrompt":"x","characters":["`+linMo.String()+`"]}],"speakers":[{"quote":"Q1","speaker":"`+linMo.String()+`"}]}`), idx, "en")
	if unrecognised != 1 || len(drafts[0].CharacterIDs) != 0 || drafts[0].Segments[0].SpeakerCharacterID != nil {
		t.Fatalf("forged ids leaked into the scene: %+v", drafts[0])
	}
}

func TestAmbiguousNameResolvesToNobody(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	idx := NewNameIndex([]CharacterNames{{ID: a, Names: []string{"Mo"}}, {ID: b, Names: []string{"mo"}}})
	if _, ok := idx.Resolve("Mo"); ok {
		t.Fatal("a name shared by two characters must not resolve")
	}
}

func TestMentionedRespectsWordBoundaries(t *testing.T) {
	idx := NewNameIndex([]CharacterNames{{ID: linMo, Names: []string{"Lin"}}})
	if got := idx.Mentioned("A lingering mist."); len(got) != 0 {
		t.Fatalf("Lin matched inside lingering: %v", got)
	}
	if got := idx.Mentioned("Lin waited."); len(got) != 1 {
		t.Fatal("Lin not found")
	}
}

func para(id, text string) Paragraph { return Paragraph{ID: id, Text: text} }

func words(n int) string { return strings.TrimSpace(strings.Repeat("word ", n)) }

func TestSplitByParagraphsFollowsTheCadence(t *testing.T) {
	// 150 wpm: 50 words = 20s, 100 words = 40s.
	ps := []Paragraph{para("a", words(30)), para("b", words(30)), para("c", words(30)), para("d", words(120)), para("e", words(10))}
	drafts := SplitByParagraphs(ps, testIndex(), Cadence{MinS: 20, MaxS: 40}, "en")
	var groups [][]string
	for _, d := range drafts {
		groups = append(groups, d.ParagraphIDs)
	}
	// a+b reach 24s (>= min); c alone (c+d would exceed 40s); d is longer
	// than the max and stands alone; e is the remainder.
	want := [][]string{{"a", "b"}, {"c"}, {"d"}, {"e"}}
	if len(groups) != len(want) {
		t.Fatalf("groups = %v", groups)
	}
	for i := range want {
		if !slices.Equal(groups[i], want[i]) {
			t.Fatalf("groups = %v, want %v", groups, want)
		}
	}
	for _, d := range drafts {
		if d.EstimatedMs <= 0 || d.ImagePrompt == "" || TextHash(d.Narration) == "" {
			t.Fatalf("incomplete draft %+v", d)
		}
	}
}

func TestSplitByParagraphsAttributesDialogueAndInheritsTaint(t *testing.T) {
	ps := []Paragraph{
		{ID: "a", Text: `"You carry the scent of a broken meridian," Elder Qiu said.`, Tainted: true},
		{ID: "b", Text: `Lin Mo looked up. "I kneel to no heaven."`},
		{ID: "c", Text: `"Enough," someone said, and Lin Mo and Elder Qiu both turned.`},
	}
	drafts := SplitByParagraphs(ps, testIndex(), Cadence{MinS: 60, MaxS: 120}, "en")
	if len(drafts) != 1 {
		t.Fatalf("expected one scene, got %d", len(drafts))
	}
	d := drafts[0]
	if !d.Tainted {
		t.Fatal("taint from paragraph a was not inherited")
	}
	var speakers []string
	for _, s := range d.Segments {
		switch {
		case s.SpeakerCharacterID == nil:
			speakers = append(speakers, "N")
		case *s.SpeakerCharacterID == elderQiu:
			speakers = append(speakers, "Q")
		case *s.SpeakerCharacterID == linMo:
			speakers = append(speakers, "L")
		}
	}
	// Paragraph c names two characters, so its quote stays with the narrator.
	if got := strings.Join(speakers, ""); got != "QNLN" {
		t.Fatalf("speakers = %s (%+v)", got, d.Segments)
	}
	if !slices.Contains(d.CharacterIDs, linMo) || !slices.Contains(d.CharacterIDs, elderQiu) {
		t.Fatalf("characters = %v", d.CharacterIDs)
	}
	if SegmentsText(d.Segments) == "" || !strings.Contains(d.Narration, "\n\n") {
		t.Fatal("narration must keep paragraph breaks")
	}
}

func decodeSplit(t *testing.T, raw string) LLMSplit {
	t.Helper()
	var out LLMSplit
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestNormalizeLLMSplitCoversEveryParagraphInOrder(t *testing.T) {
	ps := []Paragraph{
		para("p-a", "Dawn broke over the Azure Cloud range."),
		para("p-b", `"You are late," Elder Qiu said.`),
		para("", ""),
		para("p-c", `Lin Mo bowed. "Forgive me, Elder."`),
		para("p-d", "The bell rang nine times."),
	}
	sp := buildSplitPrompt(ps)
	if len(sp.Paragraphs) != 4 || !strings.Contains(sp.Text, "[P4] The bell") || !strings.Contains(sp.Text, "[Q2] (in P3)") {
		t.Fatalf("prompt:\n%s", sp.Text)
	}
	// Out of order, a duplicate start, an unknown label, no scene at P1,
	// and a speaker the roster does not know.
	out := decodeSplit(t, `{
		"scenes": [
			{"start": "P3", "imagePrompt": "a bowing disciple", "characters": ["Lin Mo", "Nobody"]},
			{"start": "P2", "imagePrompt": "an elder at an altar", "characters": ["elder qiu"]},
			{"start": "P3", "imagePrompt": "ignored duplicate", "characters": []},
			{"start": "P9", "imagePrompt": "unknown label", "characters": []}
		],
		"speakers": [
			{"quote": "Q1", "speaker": "Elder Qiu"},
			{"quote": "Q2", "speaker": "Su Yao"}
		]
	}`)
	drafts, unrecognised := NormalizeLLMSplit(sp, out, testIndex(), "en")
	var got [][]string
	for _, d := range drafts {
		got = append(got, d.ParagraphIDs)
	}
	want := [][]string{{"p-a"}, {"p-b"}, {"p-c", "p-d"}}
	if len(got) != 3 || !slices.Equal(got[0], want[0]) || !slices.Equal(got[1], want[1]) || !slices.Equal(got[2], want[2]) {
		t.Fatalf("scenes = %v", got)
	}
	if drafts[1].ImagePrompt != "an elder at an altar" || drafts[2].ImagePrompt != "a bowing disciple" {
		t.Fatalf("prompts = %q / %q", drafts[1].ImagePrompt, drafts[2].ImagePrompt)
	}
	if drafts[0].ImagePrompt == "" {
		t.Fatal("the implicit first scene needs a fallback prompt")
	}
	if unrecognised != 1 {
		t.Fatalf("unrecognised = %d", unrecognised)
	}
	var flagged *Segment
	for i, s := range drafts[2].Segments {
		if s.UnrecognisedName != "" {
			flagged = &drafts[2].Segments[i]
		}
	}
	if flagged == nil || flagged.UnrecognisedName != "Su Yao" || flagged.SpeakerCharacterID != nil {
		t.Fatalf("segments = %+v", drafts[2].Segments)
	}
	if s := drafts[1].Segments[0]; s.SpeakerCharacterID == nil || *s.SpeakerCharacterID != elderQiu {
		t.Fatalf("Q1 not attributed: %+v", drafts[1].Segments)
	}
	if !slices.Contains(drafts[2].CharacterIDs, linMo) {
		t.Fatalf("characters = %v", drafts[2].CharacterIDs)
	}
}

func TestPlanResplitKeepsUnchangedNarration(t *testing.T) {
	old := []Draft{{Narration: "one"}, {Narration: "two"}, {Narration: "two"}, {Narration: "three"}}
	hashes := make([]string, len(old))
	for i, d := range old {
		hashes[i] = TextHash(d.Narration)
	}
	next := []Draft{{Narration: "zero"}, {Narration: "two"}, {Narration: "three"}, {Narration: "two"}, {Narration: "two"}}
	got := PlanResplit(hashes, next)
	if want := []int{-1, 1, 3, 2, -1}; !slices.Equal(got, want) {
		t.Fatalf("plan = %v, want %v", got, want)
	}
}

func TestResegmentKeepsQuoteSpeakersByPosition(t *testing.T) {
	old := []Segment{
		{Text: `Elder Qiu turned.`},
		{SpeakerCharacterID: &elderQiu, Text: `"Kneel."`},
		{Text: `Lin Mo did not. "Never," he thought.`},
		{SpeakerCharacterID: &linMo, Text: `"I kneel to no heaven."`},
	}
	got := ResegmentKeepingSpeakers(old, `Elder Qiu turned slowly. "Kneel now." Lin Mo did not. "Never," he thought. "I kneel to no heaven." "And no elder."`)
	var who []string
	for _, s := range got {
		switch {
		case s.SpeakerCharacterID == nil:
			who = append(who, "N")
		case *s.SpeakerCharacterID == elderQiu:
			who = append(who, "Q")
		default:
			who = append(who, "L")
		}
	}
	// The narrator's own quoted "Never" keeps the narrator; the new last
	// line has no old counterpart and goes to the narrator too.
	if strings.Join(who, "") != "NQNLN" {
		t.Fatalf("speakers = %v (%+v)", who, got)
	}
}
