package render

import (
	"fmt"
	"testing"
)

func testManifest(n int) Manifest {
	m := Manifest{Settings: testSettings()}
	for i := range n {
		m.Scenes = append(m.Scenes, ManifestScene{
			SceneID: fmt.Sprintf("scene-%d", i), Idx: i + 1,
			ImageAssetID: fmt.Sprintf("img-%d", i), ImageSha256: fmt.Sprintf("%064d", i),
			VoiceAssetID: fmt.Sprintf("voice-%d", i), VoiceSha256: fmt.Sprintf("%064d", 100+i),
			AlignAssetID: fmt.Sprintf("align-%d", i), Motion: MotionKenBurns, DurationFrames: 90 + i,
		})
	}
	return m
}

func segmentHashes(t *testing.T, m Manifest) []string {
	t.Helper()
	tl, err := m.Timeline()
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, len(tl.Segments))
	for i, s := range tl.Segments {
		out[i] = m.SegmentHash(s)
	}
	return out
}

func TestManifestHashIsDeterministic(t *testing.T) {
	a, b := testManifest(3), testManifest(3)
	if a.Hash() != b.Hash() || a.SettingsHash() != b.SettingsHash() {
		t.Fatal("equal manifests must hash equally")
	}
	// Pinned value: a change here means every cached segment and render
	// is invalidated, which must be a deliberate format version bump.
	if got := testManifest(1).SettingsHash(); got != "4fd7d4fff92da753826b70a0d8476a56a4e7f2ed842d111d0497eb7172287312" {
		t.Fatalf("settings hash = %q", got)
	}
	b.Scenes[1].VoiceSha256 = "changed"
	if a.Hash() == b.Hash() {
		t.Fatal("a changed take must change the manifest hash")
	}
	c := testManifest(3)
	c.Settings.CrossfadeMs = 500
	if a.SettingsHash() == c.SettingsHash() || a.Hash() == c.Hash() {
		t.Fatal("changed settings must change both hashes")
	}
}

func TestEditingOneSceneChangesOnlyItsSegments(t *testing.T) {
	base := testManifest(12)
	edited := testManifest(12)
	edited.Scenes[5].ImageSha256 = "new-image"
	before, after := segmentHashes(t, base), segmentHashes(t, edited)
	tl, _ := base.Timeline()
	touched := map[int]bool{}
	for _, k := range tl.SegmentsOfScene(5) {
		touched[k] = true
	}
	if len(touched) != 3 {
		t.Fatalf("scene 5 has %d segments, want body plus two transitions", len(touched))
	}
	for k := range before {
		if changed := before[k] != after[k]; changed != touched[k] {
			t.Fatalf("segment %d (%+v) changed=%v, want %v", k, tl.Segments[k], changed, touched[k])
		}
	}
}

func TestSegmentHashCoversBurnedAlignmentAndEncoder(t *testing.T) {
	m := testManifest(2)
	tl, _ := m.Timeline()
	seg := tl.Segments[0]
	plain := m.SegmentHash(seg)
	realigned := testManifest(2)
	realigned.Scenes[0].AlignAssetID = "align-new"
	if realigned.SegmentHash(seg) == plain {
		t.Fatal("the burned alignment take must be part of the segment hash")
	}
	nv := m
	nv.Settings.Encoder = EncoderNVENC
	if nv.SegmentHash(seg) == plain {
		t.Fatal("segments of different encoders must never share a cache entry")
	}
	srtOnly := m
	srtOnly.Settings.Subtitles = SubtitlesSRT
	if srtOnly.SegmentHash(seg) == plain {
		t.Fatal("the burn style is part of the video key only when burning")
	}
	srtRealigned := srtOnly
	srtRealigned.Scenes = realigned.Scenes
	if srtRealigned.SegmentHash(seg) != srtOnly.SegmentHash(seg) {
		t.Fatal("without burning, a new alignment must not re-encode the picture")
	}
}

func TestAudioAndSubtitleHashesFollowTheirInputs(t *testing.T) {
	m := testManifest(3)
	tl, _ := m.Timeline()
	audio, subs := m.AudioHash(tl), m.SubtitlesHash(tl, "en")
	img := testManifest(3)
	img.Scenes[1].ImageSha256 = "other"
	if img.AudioHash(tl) != audio || img.SubtitlesHash(tl, "en") != subs {
		t.Fatal("an image change must not touch audio or subtitles")
	}
	voice := testManifest(3)
	voice.Scenes[1].VoiceSha256 = "other"
	if voice.AudioHash(tl) == audio {
		t.Fatal("a voice change must re-master the audio")
	}
	if m.SubtitlesHash(tl, "vi") == subs {
		t.Fatal("the language is part of the subtitle hash")
	}
	loud := testManifest(3)
	loud.Settings.LoudnessLUFSx10 = -160
	if loud.AudioHash(tl) == audio {
		t.Fatal("the loudness target is part of the audio hash")
	}
}

func TestContentKeyPrefersSha(t *testing.T) {
	if contentKey("abc", "id") != "sha256:abc" || contentKey("", "id") != "asset:id" || contentKey("", "") != "none" {
		t.Fatal("content keys")
	}
}
