package scenes

import "testing"

// Stored params never reach the worker as control keys, even for a row
// written before the API refused them.
func TestMergedParamsDropsControlKeys(t *testing.T) {
	v := Voice{
		Engine:       "chatterbox",
		PresetParams: map[string]string{"reference_url": "http://internal/x.wav", "consent": "granted", "temperature": "0.7"},
		Params:       map[string]string{"output_key": "t/other/x.wav", "language": "vi", "exaggeration": "0.4", "voice": "Ly"},
	}
	got := v.MergedParams()
	for _, k := range []string{"reference_url", "consent", "output_key", "language", "voice"} {
		if _, ok := got[k]; ok {
			t.Errorf("MergedParams kept %q: %v", k, got)
		}
	}
	if got["temperature"] != "0.7" || got["exaggeration"] != "0.4" {
		t.Errorf("MergedParams lost tuning keys: %v", got)
	}
}

func TestBuiltinVoicePrefersTheAssignment(t *testing.T) {
	v := Voice{PresetParams: map[string]string{"voice": "Preset Voice"}, Params: map[string]string{"voice": "Ly"}}
	if got := v.BuiltinVoice(); got != "Ly" {
		t.Fatalf("BuiltinVoice = %q, want Ly", got)
	}
	v.Params = nil
	if got := v.BuiltinVoice(); got != "Preset Voice" {
		t.Fatalf("BuiltinVoice = %q, want the preset's", got)
	}
	v.PresetParams = map[string]string{"voice": "../x"}
	if got := v.BuiltinVoice(); got != "" {
		t.Fatalf("BuiltinVoice = %q, want empty for an invalid name", got)
	}
}
