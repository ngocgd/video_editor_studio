package main

import (
	"os"
	"strings"
	"testing"
)

func TestFilterCLIEnvOnlyAllowlistedVarsPassThrough(t *testing.T) {
	t.Setenv("PATH", "/usr/bin")
	t.Setenv("HOME", "/home/llmcli")
	t.Setenv("LANG", "C.UTF-8")
	t.Setenv("HTTPS_PROXY", "http://egress-proxy:8888")
	t.Setenv("DATABASE_URL", "postgres://should-not-leak")
	t.Setenv("MASTER_KEY_PATH", "/run/secrets/master_key")

	env := filterCLIEnv("token-abc")

	joined := strings.Join(env, "\n")
	for _, leaked := range []string{"DATABASE_URL", "MASTER_KEY_PATH", "should-not-leak"} {
		if strings.Contains(joined, leaked) {
			t.Fatalf("env leaked non-allowlisted value: %s in %v", leaked, env)
		}
	}
	for _, want := range []string{"PATH=/usr/bin", "HOME=/home/llmcli", "LANG=C.UTF-8", "HTTPS_PROXY=http://egress-proxy:8888", "CLAUDE_CODE_OAUTH_TOKEN=token-abc"} {
		found := false
		for _, e := range env {
			if e == want {
				found = true
			}
		}
		if !found {
			t.Errorf("expected env to contain %q, got %v", want, env)
		}
	}
}

func TestFilterCLIEnvOmitsUnsetAllowlistedVars(t *testing.T) {
	saved := os.Environ()
	os.Clearenv()
	t.Cleanup(func() {
		os.Clearenv()
		for _, kv := range saved {
			if k, v, ok := strings.Cut(kv, "="); ok {
				_ = os.Setenv(k, v)
			}
		}
	})

	env := filterCLIEnv("")
	if len(env) != 0 {
		t.Fatalf("expected empty env with nothing set and no token, got %v", env)
	}
}
