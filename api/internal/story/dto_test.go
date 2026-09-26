package story

import (
	"math"
	"testing"

	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/httpapi/gen"
)

func TestDraftDTOCarriesTheDurationEstimate(t *testing.T) {
	dto, err := draftToDTO(dbgen.EpisodeDraft{Lang: "en", WordCount: 4500, Paragraphs: []byte("[]")})
	if err != nil {
		t.Fatal(err)
	}
	if dto.DurationEstimateMinutes == nil || math.Abs(float64(*dto.DurationEstimateMinutes)-30) > 0.001 {
		t.Fatalf("a 4,500-word EN draft must estimate 30 minutes, got %v", dto.DurationEstimateMinutes)
	}

	zh, err := draftToDTO(dbgen.EpisodeDraft{Lang: "zh", WordCount: 4500, Paragraphs: []byte("[]")})
	if err != nil {
		t.Fatal(err)
	}
	if zh.DurationEstimateMinutes != nil {
		t.Fatal("a zh source draft counts characters and must not get a spoken-duration estimate")
	}
}

func TestEpisodeDurationEstimatesPerNarratedLanguage(t *testing.T) {
	en, vi, zh := 4500, 1650, 3000
	got := durationEstimates(map[string]gen.DraftStatus{
		"en": {WordCount: &en}, "vi": {WordCount: &vi}, "zh": {WordCount: &zh},
	})
	if got == nil {
		t.Fatal("expected estimates")
	}
	m := *got
	if math.Abs(float64(m["en"])-30) > 0.001 || math.Abs(float64(m["vi"])-10) > 0.001 {
		t.Fatalf("estimates = %v", m)
	}
	if _, ok := m["zh"]; ok {
		t.Fatal("zh must not be estimated")
	}
}
