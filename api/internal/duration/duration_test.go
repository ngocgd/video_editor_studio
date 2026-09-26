package duration

import "testing"

func TestEstimate(t *testing.T) {
	cases := []struct {
		name    string
		words   int
		lang    string
		wantMin float64
	}{
		{"en 4500 words is 30 min", 4500, "en", 30},
		{"vi uses its own rate", 1650, "vi", 10},
		{"zero words", 0, "en", 0},
		{"unknown lang falls back to 150wpm", 300, "zz", 2},
		{"negative words clamps to zero", -10, "en", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Estimate(tc.words, tc.lang, "")
			if got.Minutes != tc.wantMin {
				t.Fatalf("Minutes = %v, want %v", got.Minutes, tc.wantMin)
			}
			if !got.Uncalibrated {
				t.Fatal("expected Uncalibrated=true until phase 9b")
			}
		})
	}
}

func TestEstimateDefaultRates(t *testing.T) {
	if got := Estimate(150, "en", ""); got.WPM != 150 {
		t.Fatalf("EN wpm = %v, want 150", got.WPM)
	}
	if got := Estimate(165, "vi", ""); got.WPM != 165 {
		t.Fatalf("VI wpm = %v, want 165", got.WPM)
	}
}
