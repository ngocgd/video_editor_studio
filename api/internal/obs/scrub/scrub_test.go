package scrub

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestReplaceAttrRedactsSensitiveKeys(t *testing.T) {
	for _, key := range []string{"token", "Token", "api_token", "secret", "password", "cookie", "Authorization", "apiKey", "access_key"} {
		a := slog.Any(key, "super-secret-value")
		got := ReplaceAttr(nil, a)
		if strings.Contains(got.Value.String(), "super-secret-value") {
			t.Errorf("key %q: value not redacted: %v", key, got.Value)
		}
	}
}

func TestReplaceAttrLeavesInnocuousKeysAlone(t *testing.T) {
	a := slog.String("user_id", "abc-123")
	got := ReplaceAttr(nil, a)
	if got.Value.String() != "abc-123" {
		t.Errorf("expected innocuous key to pass through, got %v", got.Value)
	}
}

func TestURLStripsCapabilityQueryParams(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{
			"https://minio:9000/bucket/key?X-Amz-Signature=abc123&X-Amz-Credential=xyz",
			"https://minio:9000/bucket/key?[REDACTED]",
		},
		{
			"https://minio:9000/bucket/key?upload_id=abc",
			"https://minio:9000/bucket/key?[REDACTED]",
		},
		{
			"https://example.com/path?ordinary=1",
			"https://example.com/path?ordinary=1",
		},
		{
			"not a url at all",
			"not a url at all",
		},
	}
	for _, c := range cases {
		if got := URL(c.in); got != c.want {
			t.Errorf("URL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestLogOutputRedactsSecretsAndCapabilityURLs(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{ReplaceAttr: ReplaceAttr}))
	logger.Info("test", "password", "hunter2", "url", "https://x/y?X-Amz-Signature=leak")

	out := buf.String()
	if strings.Contains(out, "hunter2") {
		t.Errorf("password leaked into log output: %s", out)
	}
	if strings.Contains(out, "leak") {
		t.Errorf("capability URL leaked into log output: %s", out)
	}
}
