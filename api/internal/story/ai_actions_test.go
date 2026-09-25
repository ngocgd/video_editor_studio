package story

import "testing"

func TestSelectParagraphsFiltersToRequestedIdsInOriginalOrder(t *testing.T) {
	all := []Paragraph{
		{ID: "p1", Text: "one"},
		{ID: "p2", Text: "two"},
		{ID: "p3", Text: "three"},
	}
	got := selectParagraphs(all, []string{"p3", "p1"})
	if len(got) != 2 || got[0].ID != "p1" || got[1].ID != "p3" {
		t.Fatalf("selectParagraphs = %+v, want [p1, p3] in original order", got)
	}
}

func TestSelectParagraphsReturnsEverythingWhenNoIdsRequested(t *testing.T) {
	all := []Paragraph{{ID: "p1"}, {ID: "p2"}}
	got := selectParagraphs(all, nil)
	if len(got) != 2 {
		t.Fatalf("expected the whole draft when no selection is given, got %d paragraphs", len(got))
	}
}

func TestSelectParagraphsIgnoresUnknownIds(t *testing.T) {
	all := []Paragraph{{ID: "p1"}}
	got := selectParagraphs(all, []string{"does-not-exist"})
	if len(got) != 0 {
		t.Fatalf("expected no match for an unknown id, got %+v", got)
	}
}
