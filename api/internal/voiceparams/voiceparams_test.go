package voiceparams

import "testing"

func TestValidateRefusesControlAndUnknownKeys(t *testing.T) {
	for _, p := range []map[string]string{
		{"reference_url": "http://pyworker:50051/x.wav"},
		{"consent": "granted"},
		{"output_key": "t/other/x.wav"},
		{"language": "en"},
		{"temperature": "0.7", "unknown": "1"},
		{"temperature": "hot"},
		{"seed": "-1"},
		{VoiceKey: "../etc"},
	} {
		if err := Validate(p); err == nil {
			t.Errorf("Validate(%v) accepted a key or value it must refuse", p)
		}
	}
}

func TestValidateAcceptsTuningKeys(t *testing.T) {
	p := map[string]string{"exaggeration": "0.4", "cfg_weight": "0.5", "temperature": "0.8", "seed": "42", VoiceKey: "Ngoc Huyen"}
	if err := Validate(p); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if err := Validate(nil); err != nil {
		t.Fatalf("Validate(nil): %v", err)
	}
}

func TestTuningDropsControlKeysAndTheVoiceName(t *testing.T) {
	got := Tuning(map[string]string{"reference_url": "http://x", "consent": "granted", "language": "vi", "output_key": "k", "temperature": "0.7", VoiceKey: "a", "seed": "x"})
	if len(got) != 1 || got["temperature"] != "0.7" {
		t.Fatalf("Tuning = %v, want only temperature", got)
	}
}
