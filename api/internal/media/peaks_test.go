package media

import (
	"encoding/json"
	"testing"
)

func TestComputePeaksFoldsMinMaxPer10ms(t *testing.T) {
	// 8kHz: 80 samples per peak; 200 samples = 3 peaks (the last partial).
	samples := make([]int16, 200)
	samples[10] = 32767
	samples[20] = -32768
	samples[100] = 16384
	p := ComputePeaks(samples, 8000)
	if p.PeaksPerSecond != 100 || len(p.Min) != 3 || p.DurationMs != 25 {
		t.Fatalf("peaks = %+v", p)
	}
	if p.Max[0] != 126 || p.Min[0] != -127 || p.Max[1] != 63 || p.Min[2] != 0 {
		t.Fatalf("values min=%v max=%v", p.Min, p.Max)
	}
}

func TestPeaksWindowAndJSONSize(t *testing.T) {
	n := 100 * 60 * 3 // three minutes of peaks
	p := Peaks{PeaksPerSecond: 100, DurationMs: int64(n) * 10, Min: make([]int8, n), Max: make([]int8, n)}
	for i := range p.Min {
		p.Min[i], p.Max[i] = -127, 127
	}
	start, w := p.Window(1000, 2500)
	if start != 1000 || len(w.Min) != 150 || w.DurationMs != 1500 {
		t.Fatalf("window start=%d len=%d dur=%d", start, len(w.Min), w.DurationMs)
	}
	if _, all := p.Window(0, 0); len(all.Min) != n {
		t.Fatal("an open window must return everything")
	}
	if _, past := p.Window(int64(n)*20, 0); len(past.Min) != 0 {
		t.Fatal("a window past the end must be empty")
	}
	// Even three minutes at the worst case stays under the 200KB chunk cap.
	body, _ := json.Marshal(p)
	if len(body) > 200<<10 {
		t.Fatalf("three minutes of peaks = %d bytes", len(body))
	}
}

func TestVariantKeyLookup(t *testing.T) {
	v := DecodeVariants([]byte(`{"webp":{"320":"k1"},"avif":{"1280":"k2"},"peaks":"k3"}`))
	if v.VariantKey("webp-320") != "k1" || v.VariantKey("avif-1280") != "k2" || v.VariantKey("webp-640") != "" || v.VariantKey("bogus") != "" || v.Peaks != "k3" {
		t.Fatalf("variants = %+v", v)
	}
}
