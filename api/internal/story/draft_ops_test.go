package story

import "testing"

func strPtr(s string) *string { return &s }

func TestApplyParagraphOpsUpsertNew(t *testing.T) {
	current := []Paragraph{{ID: "p1", Text: "hello"}}
	ops := []ParagraphOp{{Op: "upsert", ParagraphID: "p2", Text: strPtr("world")}}
	got, err := ApplyParagraphOps(current, ops)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[1].ID != "p2" || got[1].Text != "world" || got[1].Origin != "user" || got[1].Tainted {
		t.Fatalf("unexpected result: %+v", got)
	}
}

func TestApplyParagraphOpsUpsertExistingPreservesTaint(t *testing.T) {
	current := []Paragraph{{ID: "p1", Text: "old", Origin: "import", Tainted: true}}
	ops := []ParagraphOp{{Op: "upsert", ParagraphID: "p1", Text: strPtr("edited by human")}}
	got, err := ApplyParagraphOps(current, ops)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Text != "edited by human" {
		t.Fatalf("text not updated: %+v", got[0])
	}
	if !got[0].Tainted || got[0].Origin != "import" {
		t.Fatalf("expected taint/origin preserved across a human edit: %+v", got[0])
	}
}

func TestApplyParagraphOpsDelete(t *testing.T) {
	current := []Paragraph{{ID: "p1"}, {ID: "p2"}, {ID: "p3"}}
	got, err := ApplyParagraphOps(current, []ParagraphOp{{Op: "delete", ParagraphID: "p2"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "p1" || got[1].ID != "p3" {
		t.Fatalf("unexpected result: %+v", got)
	}
}

func TestApplyParagraphOpsDeleteUnknownFails(t *testing.T) {
	_, err := ApplyParagraphOps([]Paragraph{{ID: "p1"}}, []ParagraphOp{{Op: "delete", ParagraphID: "missing"}})
	if err != ErrUnknownParagraph {
		t.Fatalf("expected ErrUnknownParagraph, got %v", err)
	}
}

func TestApplyParagraphOpsMove(t *testing.T) {
	current := []Paragraph{{ID: "p1"}, {ID: "p2"}, {ID: "p3"}}
	got, err := ApplyParagraphOps(current, []ParagraphOp{{Op: "move", ParagraphID: "p1", AfterParagraphID: strPtr("p3")}})
	if err != nil {
		t.Fatal(err)
	}
	wantOrder := []string{"p2", "p3", "p1"}
	for i, id := range wantOrder {
		if got[i].ID != id {
			t.Fatalf("order = %v, want %v", idsOf(got), wantOrder)
		}
	}
}

func TestApplyParagraphOpsMoveToFront(t *testing.T) {
	current := []Paragraph{{ID: "p1"}, {ID: "p2"}}
	got, err := ApplyParagraphOps(current, []ParagraphOp{{Op: "move", ParagraphID: "p2", AfterParagraphID: strPtr("")}})
	if err != nil {
		t.Fatal(err)
	}
	if idsOf(got)[0] != "p2" {
		t.Fatalf("expected p2 moved to front, got %v", idsOf(got))
	}
}

func TestApplyParagraphOpsUnknownOpFails(t *testing.T) {
	_, err := ApplyParagraphOps(nil, []ParagraphOp{{Op: "bogus"}})
	if err != ErrInvalidOp {
		t.Fatalf("expected ErrInvalidOp, got %v", err)
	}
}

func TestApplyParagraphOpsSequential(t *testing.T) {
	current := []Paragraph{{ID: "p1", Text: "a"}}
	ops := []ParagraphOp{
		{Op: "upsert", ParagraphID: "p2", Text: strPtr("b")},
		{Op: "upsert", ParagraphID: "p3", Text: strPtr("c")},
		{Op: "delete", ParagraphID: "p1"},
		{Op: "move", ParagraphID: "p3", AfterParagraphID: strPtr("")},
	}
	got, err := ApplyParagraphOps(current, ops)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"p3", "p2"}
	if idsOf(got)[0] != want[0] || idsOf(got)[1] != want[1] {
		t.Fatalf("got order %v, want %v", idsOf(got), want)
	}
}

func TestWordCount(t *testing.T) {
	got := WordCount([]Paragraph{{Text: "hello world"}, {Text: "one two three"}})
	if got != 5 {
		t.Fatalf("WordCount = %d, want 5", got)
	}
}

func idsOf(paragraphs []Paragraph) []string {
	out := make([]string, len(paragraphs))
	for i, p := range paragraphs {
		out[i] = p.ID
	}
	return out
}
