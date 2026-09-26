package modelsapi

import (
	"testing"

	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
	"loomtale/api/internal/models"
	"loomtale/api/internal/providers/workerstatus"
)

func entry(t *testing.T, name string) models.Entry {
	t.Helper()
	m, err := models.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	e, ok := m.Get(name)
	if !ok {
		t.Fatalf("no %s in the manifest", name)
	}
	return e
}

func TestToModelInfoReportsBlockedLicenceEvenWhenFilesExist(t *testing.T) {
	e := entry(t, "illustrious-xl-v1.1")
	info := toModelInfo(e, dbgen.ModelInstall{Status: models.StatusInstalled}, true, nil)
	if info.Status != models.StatusBlocked || info.Licence.Allowed {
		t.Fatalf("a non-allowlisted licence must always show as blocked, got %s", info.Status)
	}
}

func TestToModelInfoMergesInstallRowAndResidency(t *testing.T) {
	e := entry(t, "z-image-turbo")
	row := dbgen.ModelInstall{Status: models.StatusDownloading, BytesDone: 100, BytesTotal: e.SizeBytes(), Error: idconv.ToPgText("")}
	ws := &workerstatus.Status{Fresh: true, ResidentRef: "comfyui:z-image-turbo", GPU: workerstatus.GPU{BudgetMB: 9000}}
	info := toModelInfo(e, row, true, ws)
	if info.Status != models.StatusDownloading || info.BytesDone != 100 {
		t.Fatalf("install row not merged: %+v", info)
	}
	if !info.Loaded {
		t.Fatal("the worker's resident model must show as loaded")
	}
	if !info.OverBudget {
		t.Fatal("a VRAM ceiling above the measured budget must be flagged")
	}

	ws.Fresh = false
	if info := toModelInfo(e, row, true, ws); info.Loaded {
		t.Fatal("a stale worker status must never report a model as loaded")
	}
}

func TestToModelInfoWithoutRowIsNotInstalled(t *testing.T) {
	e := entry(t, "qwen-image")
	info := toModelInfo(e, dbgen.ModelInstall{}, false, nil)
	if info.Status != models.StatusNotInstalled || info.BytesTotal != e.SizeBytes() {
		t.Fatalf("got %+v", info)
	}
}
