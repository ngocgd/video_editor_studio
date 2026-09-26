package characters

import (
	"context"
	"regexp"
	"testing"

	"github.com/google/uuid"

	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
	"loomtale/api/internal/pipeline"
	"loomtale/api/internal/providers/train"
)

// workerTriggerWord is the trigger word format the Python trainer accepts.
var workerTriggerWord = regexp.MustCompile(`^[a-z][a-z0-9_]{1,31}$`)

func TestTrainerTriggerWordPrefersTokenThenNameThenID(t *testing.T) {
	id := uuid.MustParse("0190f5a1-2b3c-7d4e-8f90-a1b2c3d4e5f6")
	cases := []struct {
		name string
		c    dbgen.Character
		want string
	}{
		{"token", dbgen.Character{TriggerToken: "Mira Vale", NameEn: "Other"}, "mira_vale"},
		{"english name", dbgen.Character{NameEn: "Lin Daiyu"}, "lin_daiyu"},
		{"id fallback", dbgen.Character{ID: idconv.ToPg(id), NameOrig: "林黛玉"}, "char_0190f5a12b3c"},
	}
	for _, tc := range cases {
		got := TrainerTriggerWord(tc.c)
		if got != tc.want {
			t.Fatalf("%s: got %q, want %q", tc.name, got, tc.want)
		}
		if !workerTriggerWord.MatchString(got) {
			t.Fatalf("%s: %q is not a valid trainer trigger word", tc.name, got)
		}
	}
}

func TestTrainerParamsPassesKnownKeysOnly(t *testing.T) {
	stored := []byte(`{"steps":"1500","rank":16,"learning_rate":0.0002,"max_minutes":"","trigger_word":"injected","output_key":"elsewhere","base_dir":"/etc"}`)
	got := TrainerParams(stored, "mira_vale", "t/x/document/y")
	want := map[string]string{"steps": "1500", "rank": "16", "learning_rate": "0.0002", "trigger_word": "mira_vale", "output_key": "t/x/document/y"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("%s = %q, want %q (all: %v)", k, got[k], v, got)
		}
	}
}

func TestTrainerParamsToleratesBadStoredJSON(t *testing.T) {
	got := TrainerParams([]byte("not json"), "mira_vale", "k")
	if len(got) != 2 || got["trigger_word"] != "mira_vale" || got["output_key"] != "k" {
		t.Fatalf("got %v", got)
	}
}

func TestLoraWeightsFileName(t *testing.T) {
	id := uuid.MustParse("0190f5a1-2b3c-7d4e-8f90-a1b2c3d4e5f6")
	if got := LoraWeightsFile(idconv.ToPg(id), 3); got != "loomtale-0190f5a1-2b3c-7d4e-8f90-a1b2c3d4e5f6-v3.safetensors" {
		t.Fatalf("got %q", got)
	}
}

func TestTrainHandlerHoldsTheTrainerResident(t *testing.T) {
	ref, err := (&TrainHandler{}).ModelRef(context.Background(), pipeline.StepRef{})
	if err != nil || ref == nil {
		t.Fatalf("ModelRef = %v, %v", ref, err)
	}
	if ref.Backend != "pyworker" || ref.Model != TrainerEngine || TrainerEngine != "z-image-turbo-trainer" {
		t.Fatalf("ModelRef = %+v", *ref)
	}
}

func TestDatasetBoundsMatchTheWorker(t *testing.T) {
	// The dataset bounds are the worker's; the API must stay inside them.
	if train.MinDatasetImages != 4 || train.MaxDatasetImages != 64 {
		t.Fatalf("dataset bounds changed: %d..%d", train.MinDatasetImages, train.MaxDatasetImages)
	}
}
