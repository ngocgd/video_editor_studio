package models

import (
	"errors"
	"strings"
	"testing"

	"loomtale/api/internal/pipeline"
)

func TestEmbeddedManifestParsesAndLints(t *testing.T) {
	m, err := Embedded()
	if err != nil {
		t.Fatal(err)
	}
	templates, err := EmbeddedTemplates()
	if err != nil {
		t.Fatal(err)
	}
	if problems := Lint(m, templates); len(problems) > 0 {
		t.Fatalf("embedded manifest has lint problems: %v", problems)
	}
	for _, name := range []string{"z-image-turbo", "qwen-image", "qwen-image-edit-2511"} {
		e, ok := m.Get(name)
		if !ok {
			t.Fatalf("manifest is missing %s", name)
		}
		if err := Gate(e); err != nil {
			t.Fatalf("%s should pass the licence gate: %v", name, err)
		}
	}
}

func TestParseRejectsUnknownKeys(t *testing.T) {
	raw := "version: 1\nmodels:\n  - name: x\n    sha265: typo\n"
	if _, err := Parse([]byte(raw)); err == nil {
		t.Fatal("expected an unknown key to be rejected")
	}
}

func TestParseRejectsUnknownVersion(t *testing.T) {
	if _, err := Parse([]byte("version: 2\nmodels: []\n")); err == nil {
		t.Fatal("expected version 2 to be rejected")
	}
}

func TestScopeIDRoundTrips(t *testing.T) {
	m, err := Embedded()
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range m.Models {
		name, ok := m.ScopeName(ScopeID(e.Name))
		if !ok || name != e.Name {
			t.Fatalf("scope id for %s maps back to %q, %v", e.Name, name, ok)
		}
	}
	if ScopeID("a") == ScopeID("b") {
		t.Fatal("different names must get different scope ids")
	}
	first, second := ScopeID("a"), ScopeID("a")
	if first != second {
		t.Fatal("scope ids must be deterministic")
	}
}

func TestVRAMByRefAndWarmups(t *testing.T) {
	m, err := Embedded()
	if err != nil {
		t.Fatal(err)
	}
	if got := m.VRAMByRef()["comfyui:z-image-turbo"]; got <= 0 {
		t.Fatalf("VRAMByRef has no ceiling for z-image-turbo: %d", got)
	}
	warm := m.Warmups()
	if warm["qwen-image-edit-2511"] != "charsheet_qwenedit" {
		t.Fatalf("qwen-image-edit warm-up workflow = %q", warm["qwen-image-edit-2511"])
	}
	if _, ok := warm["illustrious-xl-v1.1"]; ok {
		t.Fatal("an entry without workflows must not get a warm-up")
	}
}

func TestGateRefusesNonAllowlistedLicence(t *testing.T) {
	m, err := Embedded()
	if err != nil {
		t.Fatal(err)
	}
	e, _ := m.Get("illustrious-xl-v1.1")
	err = Gate(e)
	if !errors.Is(err, pipeline.ErrLicenceRefused) {
		t.Fatalf("expected a licence refusal, got %v", err)
	}
	if class, code := pipeline.Classify(err); class != pipeline.ClassPermanent || code != "licence_refused" {
		t.Fatalf("licence refusal must be permanent, got %v %s", class, code)
	}
}

func TestGateRefusesMissingLicenceURL(t *testing.T) {
	e := Entry{Name: "x", Licence: Licence{SPDX: "MIT"}}
	if err := Gate(e); err == nil || !strings.Contains(err.Error(), "no licence URL") {
		t.Fatalf("expected a missing-URL refusal, got %v", err)
	}
}
