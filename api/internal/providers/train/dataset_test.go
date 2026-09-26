package train

import (
	"archive/zip"
	"bytes"
	"io"
	"testing"
)

func TestDatasetZipLayout(t *testing.T) {
	data, err := DatasetZip([]DatasetImage{
		{Ext: ".PNG", Data: []byte("png-bytes"), Caption: "mira, red scarf"},
		{Ext: ".jpeg", Data: []byte("jpg-bytes"), Caption: "mira"},
		{Ext: ".webp", Data: []byte("webp-bytes"), Caption: ""},
	})
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"ref-01.png": "png-bytes", "ref-01.txt": "mira, red scarf",
		"ref-02.jpg": "jpg-bytes", "ref-02.txt": "mira",
		"ref-03.webp": "webp-bytes", "ref-03.txt": "",
	}
	if len(zr.File) != len(want) {
		t.Fatalf("got %d entries, want %d", len(zr.File), len(want))
	}
	for _, f := range zr.File {
		body, ok := want[f.Name]
		if !ok {
			t.Fatalf("unexpected entry %q", f.Name)
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		got, _ := io.ReadAll(rc)
		_ = rc.Close()
		if string(got) != body {
			t.Fatalf("%s = %q, want %q", f.Name, got, body)
		}
	}
}

func TestDatasetZipRejectsUnsupportedImages(t *testing.T) {
	if _, err := DatasetZip([]DatasetImage{{Ext: ".avif", Data: []byte("x")}}); err == nil {
		t.Fatal("an avif image must be rejected")
	}
}

func TestDatasetExt(t *testing.T) {
	for mime, want := range map[string]string{"image/png": ".png", "IMAGE/JPEG": ".jpg", "image/webp": ".webp"} {
		if got, ok := DatasetExt(mime); !ok || got != want {
			t.Fatalf("DatasetExt(%q) = %q, %v", mime, got, ok)
		}
	}
	if _, ok := DatasetExt("image/avif"); ok {
		t.Fatal("avif is not a training format")
	}
}

func TestTriggerWord(t *testing.T) {
	cases := map[string]string{
		"mira_v":        "mira_v",
		"  Mira Vale ":  "mira_vale",
		"<Mira-Vale!!>": "mira_vale",
		"42 lin":        "lin",
		"x":             DefaultTriggerWord,
		"":              DefaultTriggerWord,
		"林黛玉":           DefaultTriggerWord,
		"a very long trigger token that keeps going on": "a_very_long_trigger_token_that_k",
	}
	for in, want := range cases {
		if got := TriggerWord(in); got != want {
			t.Fatalf("TriggerWord(%q) = %q, want %q", in, got, want)
		}
	}
}
