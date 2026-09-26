package train

import (
	"archive/zip"
	"bytes"
	"fmt"
	"strings"
)

// Dataset bounds the Python worker's trainer enforces on the archive.
const (
	MinDatasetImages = 4
	MaxDatasetImages = 64
)

// DefaultTriggerWord is the worker's trigger word when none is given.
const DefaultTriggerWord = "loomtale_character"

// datasetExtByMime maps the image types the trainer reads to the file
// extension it recognises inside the archive.
var datasetExtByMime = map[string]string{
	"image/png":  ".png",
	"image/jpeg": ".jpg",
	"image/webp": ".webp",
}

// DatasetExt returns the archive extension for an image mime type and
// whether the trainer accepts that type at all.
func DatasetExt(mime string) (string, bool) {
	ext, ok := datasetExtByMime[strings.ToLower(strings.TrimSpace(mime))]
	return ext, ok
}

// DatasetImage is one training image with its caption.
type DatasetImage struct {
	// Ext is the file extension including the dot (".png").
	Ext     string
	Data    []byte
	Caption string
}

// DatasetZip builds the archive the worker's trainer accepts: a flat
// zip of numbered images, each with a same-named .txt caption. The
// numbered names keep the archive collision-free whatever the sources
// were called.
func DatasetZip(images []DatasetImage) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for i, img := range images {
		ext := strings.ToLower(img.Ext)
		if ext == ".jpeg" {
			ext = ".jpg"
		}
		if ext != ".png" && ext != ".jpg" && ext != ".webp" {
			return nil, fmt.Errorf("train: dataset image %d has unsupported extension %q", i+1, img.Ext)
		}
		stem := fmt.Sprintf("ref-%02d", i+1)
		for _, f := range []struct {
			name string
			data []byte
		}{{stem + ext, img.Data}, {stem + ".txt", []byte(img.Caption)}} {
			w, err := zw.Create(f.name)
			if err != nil {
				return nil, err
			}
			if _, err := w.Write(f.data); err != nil {
				return nil, err
			}
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// TriggerWord turns a free-text trigger token into the form the
// trainer accepts (2 to 32 lowercase letters, digits or underscores,
// starting with a letter). Other characters become underscores, runs
// of underscores collapse, and a token with no usable letters falls
// back to DefaultTriggerWord.
func TriggerWord(token string) string {
	var b strings.Builder
	lastUnderscore := false
	for _, r := range strings.ToLower(strings.TrimSpace(token)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastUnderscore = false
		case !lastUnderscore && b.Len() > 0:
			b.WriteByte('_')
			lastUnderscore = true
		}
	}
	word := strings.TrimLeft(strings.Trim(b.String(), "_"), "0123456789_")
	if len(word) > 32 {
		word = strings.TrimRight(word[:32], "_")
	}
	if len(word) < 2 {
		return DefaultTriggerWord
	}
	return word
}
