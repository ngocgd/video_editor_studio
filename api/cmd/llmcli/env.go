package main

import "os"

// allowedEnvVars is the exact env passthrough allowlist for the spawned
// claude process, per the phase 4 contract: everything else in the
// sidecar's own environment (there should be nothing sensitive there
// besides the token, which is threaded in separately) is dropped.
var allowedEnvVars = []string{"PATH", "HOME", "LANG", "HTTPS_PROXY"}

// filterCLIEnv builds the exact env slice for the spawned claude process:
// only allowedEnvVars are passed through from the sidecar's own
// environment, plus the OAuth token passed explicitly by the caller. This
// is a from-scratch reimplementation of the allowlist pattern, not a copy
// of any reference implementation.
func filterCLIEnv(oauthToken string) []string {
	env := make([]string, 0, len(allowedEnvVars)+1)
	for _, key := range allowedEnvVars {
		if v, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+v)
		}
	}
	if oauthToken != "" {
		env = append(env, "CLAUDE_CODE_OAUTH_TOKEN="+oauthToken)
	}
	return env
}
