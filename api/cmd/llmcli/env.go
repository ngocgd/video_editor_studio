package main

import "os"

// allowedEnvVars is the exact env passthrough allowlist for the spawned
// claude process, per the phase 4 contract: everything else in the
// sidecar's own environment (there should be nothing sensitive there
// besides the token, which is threaded in separately) is dropped. The
// three DISABLE_* vars turn off the CLI's own telemetry/error-reporting/
// non-essential-model-call traffic (documented at
// https://code.claude.com/docs/en/env-vars), which is why the egress
// proxy's allowlist only needs api.anthropic.com and not
// statsig.anthropic.com/sentry.io.
var allowedEnvVars = []string{
	"PATH", "HOME", "LANG", "HTTPS_PROXY",
	"DISABLE_TELEMETRY", "DISABLE_ERROR_REPORTING", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC",
}

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
