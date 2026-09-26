package diskguard

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"loomtale/api/internal/pipeline"
)

func fixed(free uint64, err error) func(string) (uint64, error) {
	return func(string) (uint64, error) { return free, err }
}

func TestStatusLevels(t *testing.T) {
	cases := []struct {
		free uint64
		want Level
	}{
		{100 * GB, LevelOK},
		{60 * GB, LevelOK},
		{59 * GB, LevelWarning},
		{40 * GB, LevelWarning},
		{39 * GB, LevelBlocked},
	}
	for _, c := range cases {
		w := New("/data", 0, 0)
		w.FreeBytes = fixed(c.free, nil)
		s, err := w.Status(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if s.Level != c.want {
			t.Errorf("free %d GB: level %s, want %s", c.free/GB, s.Level, c.want)
		}
		if (s.Message() == "") != (c.want == LevelOK) {
			t.Errorf("free %d GB: message %q", c.free/GB, s.Message())
		}
	}
}

func TestNewKeepsWarnAboveMin(t *testing.T) {
	w := New("/data", 80, 50)
	if w.MinFree != 80*GB || w.WarnFree != 80*GB {
		t.Fatalf("min %d warn %d", w.MinFree, w.WarnFree)
	}
}

func TestGuarded(t *testing.T) {
	cases := []struct {
		k    pipeline.EnqueueKinds
		want bool
	}{
		{pipeline.EnqueueKinds{RunKind: "render"}, true},
		{pipeline.EnqueueKinds{RunKind: "models.install", StepKinds: []string{"models.pull"}}, true},
		{pipeline.EnqueueKinds{RunKind: "other", StepKinds: []string{"render.compose"}}, true},
		{pipeline.EnqueueKinds{RunKind: "scene.regenerate", StepKinds: []string{"scene.image"}}, false},
		{pipeline.EnqueueKinds{RunKind: "models.load", StepKinds: []string{"models.load"}}, false},
	}
	for _, c := range cases {
		if got := Guarded(c.k); got != c.want {
			t.Errorf("%+v: %v, want %v", c.k, got, c.want)
		}
	}
}

func TestCheck(t *testing.T) {
	render := pipeline.WithEnqueueKinds(context.Background(), pipeline.EnqueueKinds{RunKind: "render"})
	other := pipeline.WithEnqueueKinds(context.Background(), pipeline.EnqueueKinds{RunKind: "scene.regenerate"})

	full := New("/data", 0, 0)
	full.FreeBytes = fixed(10*GB, nil)
	if err := full.Check(render, nil, uuid.Nil, 3); !errors.Is(err, pipeline.ErrAdmissionDenied) {
		t.Fatalf("full disk render: %v", err)
	}
	if err := full.Check(other, nil, uuid.Nil, 3); err != nil {
		t.Fatalf("full disk, unguarded kind: %v", err)
	}
	if err := full.Check(context.Background(), nil, uuid.Nil, 3); err != nil {
		t.Fatalf("no enqueue kinds on ctx: %v", err)
	}

	roomy := New("/data", 0, 0)
	roomy.FreeBytes = fixed(50*GB, nil)
	if err := roomy.Check(render, nil, uuid.Nil, 3); err != nil {
		t.Fatalf("warning level must still admit: %v", err)
	}

	broken := New("/data", 0, 0)
	broken.FreeBytes = fixed(0, errors.New("no such file"))
	if err := broken.Check(render, nil, uuid.Nil, 3); !errors.Is(err, pipeline.ErrAdmissionDenied) {
		t.Fatalf("unreadable disk must refuse: %v", err)
	}
}

func TestStatusTakesTightestPath(t *testing.T) {
	w := New("/data, /host-disk", 0, 0)
	w.FreeBytes = func(path string) (uint64, error) {
		if path == "/host-disk" {
			return 30 * GB, nil
		}
		return 900 * GB, nil
	}
	s, err := w.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if s.FreeBytes != 30*GB || s.Level != LevelBlocked {
		t.Fatalf("status %+v, want 30 GB blocked", s)
	}
}

func TestAPIStatus(t *testing.T) {
	if got := APIStatus(context.Background(), nil); got.Level != "unknown" {
		t.Fatalf("nil watermark: level %s", got.Level)
	}
	w := New("/data", 0, 0)
	w.FreeBytes = fixed(0, errors.New("statfs failed"))
	if got := APIStatus(context.Background(), w); got.Level != "unknown" || got.Message == "" {
		t.Fatalf("unreadable disk: %+v", got)
	}
	w.FreeBytes = fixed(50*GB, nil)
	got := APIStatus(context.Background(), w)
	if got.Level != "warning" || got.FreeBytes != int64(50*GB) || got.MinFreeBytes != int64(40*GB) {
		t.Fatalf("warning disk: %+v", got)
	}
}
