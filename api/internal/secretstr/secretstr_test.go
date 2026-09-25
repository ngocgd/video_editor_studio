package secretstr

import (
	"bytes"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

func TestRevealReturnsPlaintext(t *testing.T) {
	s := String("top-secret")
	if s.Reveal() != "top-secret" {
		t.Fatalf("Reveal() = %q, want %q", s.Reveal(), "top-secret")
	}
}

func TestStringAndFormatVerbsRedact(t *testing.T) {
	s := String("top-secret")
	for _, got := range []string{
		s.String(),
		fmt.Sprintf("%v", s),
		fmt.Sprintf("%#v", s),
	} {
		if strings.Contains(got, "top-secret") {
			t.Errorf("plaintext leaked: %q", got)
		}
	}
}

func TestSlogNeverLogsPlaintext(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	logger.Info("event", "apiKey", String("top-secret-key"))

	if strings.Contains(buf.String(), "top-secret-key") {
		t.Fatalf("plaintext leaked into log output: %s", buf.String())
	}
}

func TestMarshalJSONRedacts(t *testing.T) {
	b, err := String("top-secret").MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "top-secret") {
		t.Fatalf("plaintext leaked into JSON: %s", b)
	}
}
