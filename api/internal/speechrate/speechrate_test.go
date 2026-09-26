package speechrate

import (
	"math"
	"strings"
	"testing"
)

func TestVoiceKeyUsesTheReferenceDigestOrTheBuiltinName(t *testing.T) {
	a := VoiceKey("chatterbox", "", []byte("clip-a"))
	b := VoiceKey("chatterbox", "", []byte("clip-b"))
	if a == b || !strings.HasPrefix(a, "chatterbox:ref:") || len(a) != len("chatterbox:ref:")+16 {
		t.Fatalf("reference keys %q %q", a, b)
	}
	if a != VoiceKey("chatterbox", "ignored", []byte("clip-a")) {
		t.Fatal("the same clip must keep its key")
	}
	if got := VoiceKey("vieneu-v3-turbo", "Mai Anh", nil); got != "vieneu-v3-turbo:voice:Mai Anh" {
		t.Fatalf("builtin key = %q", got)
	}
}

func TestCalibratePoolsWordsOverMinutes(t *testing.T) {
	c, err := Calibrate("k", "chatterbox", "en", []Sample{{Words: 150, Seconds: 60}, {Words: 50, Seconds: 30}, {Words: 0, Seconds: 5}})
	if err != nil {
		t.Fatal(err)
	}
	if c.Samples != 2 || c.Words != 200 || c.Seconds != 90 {
		t.Fatalf("pooled %+v", c)
	}
	if math.Abs(c.WPM-200.0/1.5) > 1e-9 {
		t.Fatalf("wpm = %v", c.WPM)
	}
	if _, err := Calibrate("k", "e", "en", []Sample{{Words: 0, Seconds: 1}}); err == nil {
		t.Fatal("expected an error without usable samples")
	}
}

func TestPredictAndDeviation(t *testing.T) {
	if got := PredictSeconds(4500, 150); got != 1800 {
		t.Fatalf("a 4,500-word EN episode at 150 wpm is 30 minutes, got %v s", got)
	}
	if PredictSeconds(10, 0) != 0 {
		t.Fatal("no rate predicts nothing")
	}
	if d := Deviation(1980, 1800); math.Abs(d-0.1) > 1e-9 {
		t.Fatalf("deviation = %v", d)
	}
	if !math.IsInf(Deviation(1, 0), 1) {
		t.Fatal("a zero actual duration cannot be compared")
	}
}
