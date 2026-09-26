// Package voiceparams holds the rule for the engine parameters a user may
// store on a voice preset or a voice assignment. Only per-engine tuning
// keys are accepted. The keys the TTS worker treats as control fields
// (the reference clip URL, its consent flag, the output key and the
// language) are set by the server alone: a stored reference_url would let
// an editor clone any voice without the audited consent and make the
// worker fetch any URL on its network.
package voiceparams

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
)

// VoiceKey names a built-in voice of an engine that ships its own voices.
// It travels in the TTS request's voice field, not in its params.
const VoiceKey = "voice"

// reserved are the control keys only the server sets on a TTS request.
var reserved = map[string]bool{"reference_url": true, "consent": true, "output_key": true, "language": true}

// tuning are the keys the TTS engines read, with the check each value
// must pass. Numeric ranges are enforced by the engine itself.
var tuning = map[string]func(string) bool{
	"exaggeration": isFloat,
	"cfg_weight":   isFloat,
	"temperature":  isFloat,
	"seed":         isUint,
	VoiceKey:       voiceName.MatchString,
}

var voiceName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 _.-]{0,63}$`)

func isFloat(s string) bool { _, err := strconv.ParseFloat(s, 64); return err == nil }
func isUint(s string) bool  { _, err := strconv.ParseUint(s, 10, 31); return err == nil }

// Validate reports the first key of p a user may not store, or a tuning
// value of the wrong form. Keys are checked in sorted order so the error
// is stable.
func Validate(p map[string]string) error {
	keys := make([]string, 0, len(p))
	for k := range p {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		switch check, ok := tuning[k]; {
		case reserved[k]:
			return fmt.Errorf("the parameter %q is set by the server and cannot be stored", k)
		case !ok:
			return fmt.Errorf("the parameter %q is not a known voice tuning parameter", k)
		case !check(p[k]):
			return fmt.Errorf("the parameter %q has an invalid value", k)
		}
	}
	return nil
}

// Tuning returns the tuning keys of p, without the built-in voice name,
// dropping every other key. It guards rows stored before Validate
// existed: the caller sets the control keys after this.
func Tuning(p map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range p {
		if check, ok := tuning[k]; ok && k != VoiceKey && check(v) {
			out[k] = v
		}
	}
	return out
}
