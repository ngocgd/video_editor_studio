package main

import (
	"testing"

	"github.com/caarlos0/env/v11"
)

// defaultConfig parses the API config from an environment that sets only
// the required keys, so every other field takes its shipped default.
func defaultConfig(t *testing.T) config {
	t.Helper()
	t.Setenv("DATABASE_URL", "postgres://unused")
	t.Setenv("API_RATE_LIMIT_PER_MINUTE", "")
	t.Setenv("API_MEDIA_RATE_LIMIT_PER_MINUTE", "")
	var cfg config
	if err := env.Parse(&cfg); err != nil {
		t.Fatalf("parse config: %v", err)
	}
	return cfg
}

// singleUserMinute is the general-bucket traffic one user produced in the
// busiest minute of a click-through of the app (full page loads that
// re-fetch the shell, storyboard and writer pages, GPU/jobs/readiness
// polling). The default budget must admit all of it as one burst.
const singleUserMinute = 230

func TestDefaultGeneralBudgetAdmitsASingleUserSession(t *testing.T) {
	general, _, err := newRequestLimiters(defaultConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < singleUserMinute; i++ {
		if !general.Allow("198.51.100.7") {
			t.Fatalf("request %d of a single-user minute was refused", i+1)
		}
	}
}

func TestDefaultGeneralBudgetStillRefusesAFlood(t *testing.T) {
	cfg := defaultConfig(t)
	general, _, err := newRequestLimiters(cfg)
	if err != nil {
		t.Fatal(err)
	}
	// Refill adds at most a few tokens while the loop runs, so a flood
	// well past the burst must be refused before it ends.
	flood := cfg.RateLimitPerMinute * 2
	for i := 0; i < flood; i++ {
		if !general.Allow("198.51.100.8") {
			return
		}
	}
	t.Fatalf("%d back-to-back requests from one IP were all admitted", flood)
}

func TestRequestLimitersRejectNonPositiveBudgets(t *testing.T) {
	for _, tc := range []struct {
		name           string
		general, media int
	}{{"general", 0, 10}, {"media", 10, -1}} {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := newRequestLimiters(config{RateLimitPerMinute: tc.general, MediaRateLimitPerMinute: tc.media}); err == nil {
				t.Fatal("want an error for a non-positive budget")
			}
		})
	}
}
