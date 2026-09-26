package models

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"loomtale/api/internal/pipeline"
)

// DefaultHeadroomBytes is the free space the disk pre-flight keeps on top
// of what a download needs: renders, Docker image growth and the VHD's
// own overhead all live on the same disk.
const DefaultHeadroomBytes int64 = 40 << 30

// DefaultAllowedHosts are the only hosts a model download may reach,
// including across redirects (Hugging Face serves LFS files from its
// CDN and Xet storage hosts).
var DefaultAllowedHosts = []string{"huggingface.co", "hf.co"}

// stagingDir holds partial downloads, inside the models volume so the
// final rename is atomic (same filesystem). ComfyUI's folder scan never
// looks at dot-directories.
const stagingDir = ".staging"

var (
	// ErrChecksumMismatch is a downloaded or adopted file whose sha256
	// or size does not match the manifest pin.
	ErrChecksumMismatch = fmt.Errorf("models: checksum mismatch: %w", pipeline.ErrValidation)
	// ErrInsufficientDisk is the disk pre-flight refusing a download.
	ErrInsufficientDisk = fmt.Errorf("models: not enough free disk: %w", pipeline.ErrValidation)
	// ErrHostNotAllowed is a download URL or redirect outside the
	// allowlist.
	ErrHostNotAllowed = fmt.Errorf("models: download host not allowed: %w", pipeline.ErrValidation)
)

// FileStore records which files on the models volume have been verified
// against their pinned sha256, so a verified file is never re-hashed.
type FileStore interface {
	VerifiedFile(ctx context.Context, path string) (sha string, size int64, ok bool, err error)
	MarkFileVerified(ctx context.Context, path, sha string, size int64) error
}

// Downloader installs manifest entries into the models volume.
type Downloader struct {
	// Dir is the models volume root (mounted read-write only in the
	// worker and the operator CLI).
	Dir     string
	HTTP    *http.Client
	BaseURL string
	Files   FileStore
	// AllowedHosts defaults to DefaultAllowedHosts; a host matches if it
	// equals an entry or is a subdomain of one.
	AllowedHosts []string
	// FreeBytes reports free space on the filesystem holding Dir;
	// defaults to statfs.
	FreeBytes func(dir string) (int64, error)
	// HeadroomBytes defaults to DefaultHeadroomBytes.
	HeadroomBytes int64
}

// Progress is called as bytes land: done counts every byte of the entry
// already verified or downloaded, total is the entry's full size.
type Progress func(done, total int64)

// fileState is one file's plan before downloading.
type fileState struct {
	file     File
	verified bool
	partial  int64
}

// Install makes every file of e present and verified: files already
// verified are skipped, files already on disk (e.g. fetched by an
// earlier spike) are adopted after hashing, and the rest are downloaded
// resumably. The licence gate and the disk pre-flight run first.
func (d *Downloader) Install(ctx context.Context, e Entry, progress Progress) error {
	if err := Gate(e); err != nil {
		return err
	}
	if err := d.checkHost(d.baseURL()); err != nil {
		return err
	}

	total := e.SizeBytes()
	plan := make([]fileState, 0, len(e.Files))
	var done int64
	for _, f := range e.Files {
		st, err := d.inspect(ctx, f)
		if err != nil {
			return err
		}
		if st.verified {
			done += f.Size
		} else {
			done += st.partial
		}
		plan = append(plan, st)
	}
	report(progress, done, total)

	if err := d.preflight(plan); err != nil {
		return err
	}

	for _, st := range plan {
		if st.verified {
			continue
		}
		base := done - st.partial
		err := d.downloadFile(ctx, st.file, e.Source, func(fileDone int64) {
			report(progress, base+fileDone, total)
		})
		if err != nil {
			return err
		}
		done = base + st.file.Size
		if err := d.Files.MarkFileVerified(ctx, st.file.Path, st.file.SHA256, st.file.Size); err != nil {
			return err
		}
	}
	report(progress, total, total)
	return nil
}

func report(p Progress, done, total int64) {
	if p != nil {
		p(done, total)
	}
}

// inspect decides whether f is already verified, can be adopted from
// disk, or needs (resumed) downloading.
func (d *Downloader) inspect(ctx context.Context, f File) (fileState, error) {
	st := fileState{file: f}
	final := d.finalPath(f)
	info, err := os.Stat(final)
	switch {
	case err == nil && info.Size() == f.Size:
		sha, size, ok, err := d.Files.VerifiedFile(ctx, f.Path)
		if err != nil {
			return st, err
		}
		if ok && sha == f.SHA256 && size == f.Size {
			st.verified = true
			return st, nil
		}
		// Present but never verified by this app (e.g. placed by the
		// phase 1b download spike): adopt it only if it hashes right.
		got, err := hashFile(final)
		if err != nil {
			return st, err
		}
		if got == f.SHA256 {
			if err := d.Files.MarkFileVerified(ctx, f.Path, f.SHA256, f.Size); err != nil {
				return st, err
			}
			st.verified = true
			return st, nil
		}
	case err != nil && !errors.Is(err, os.ErrNotExist):
		return st, err
	}
	if info, err := os.Stat(d.partPath(f)); err == nil && info.Size() <= f.Size {
		st.partial = info.Size()
	}
	return st, nil
}

// preflight refuses the install unless the disk has room for every byte
// still to download plus the headroom.
func (d *Downloader) preflight(plan []fileState) error {
	var need int64
	for _, st := range plan {
		if !st.verified {
			need += st.file.Size - st.partial
		}
	}
	if need == 0 {
		return nil
	}
	freeFn := d.FreeBytes
	if freeFn == nil {
		freeFn = diskFree
	}
	if err := os.MkdirAll(filepath.Join(d.Dir, stagingDir), 0o755); err != nil {
		return err
	}
	free, err := freeFn(d.Dir)
	if err != nil {
		return fmt.Errorf("models: disk pre-flight: %w", err)
	}
	headroom := d.HeadroomBytes
	if headroom == 0 {
		headroom = DefaultHeadroomBytes
	}
	if free < need+headroom {
		return fmt.Errorf("%w: need %d bytes plus %d headroom, %d free", ErrInsufficientDisk, need, headroom, free)
	}
	return nil
}

// downloadFile fetches f into its .part file, resuming from whatever is
// already there, verifies size and sha256, then renames it into place.
func (d *Downloader) downloadFile(ctx context.Context, f File, src Source, progress func(int64)) error {
	part := d.partPath(f)
	if err := os.MkdirAll(filepath.Dir(part), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(part, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()

	// Re-hash what is already on disk so the final checksum covers the
	// whole file, not only the resumed tail.
	hasher := sha256.New()
	offset, err := io.Copy(hasher, out)
	if err != nil {
		return err
	}
	if offset > f.Size {
		if offset, err = restart(out, &hasher); err != nil {
			return err
		}
	}

	if offset < f.Size {
		if err := d.fetch(ctx, f, src, out, &hasher, offset, progress); err != nil {
			return err
		}
	}

	info, err := out.Stat()
	if err != nil {
		return err
	}
	got := hex.EncodeToString(hasher.Sum(nil))
	if info.Size() != f.Size || got != f.SHA256 {
		_ = out.Close()
		_ = os.Remove(part)
		return fmt.Errorf("%w: %s got %d bytes sha256 %s, want %d bytes %s", ErrChecksumMismatch, f.Path, info.Size(), got, f.Size, f.SHA256)
	}
	if err := out.Sync(); err != nil {
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	final := d.finalPath(f)
	if err := os.MkdirAll(filepath.Dir(final), 0o755); err != nil {
		return err
	}
	return os.Rename(part, final)
}

// fetch requests f from offset and appends the body to out.
func (d *Downloader) fetch(ctx context.Context, f File, src Source, out *os.File, hasher *hash.Hash, offset int64, progress func(int64)) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.fileURL(f, src), nil)
	if err != nil {
		return err
	}
	if offset > 0 {
		req.Header.Set("Range", "bytes="+strconv.FormatInt(offset, 10)+"-")
	}
	resp, err := d.client().Do(req)
	if err != nil {
		return fmt.Errorf("models: download %s: %w", f.Path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch {
	case resp.StatusCode == http.StatusPartialContent && offset > 0:
		if _, err := out.Seek(offset, io.SeekStart); err != nil {
			return err
		}
	case resp.StatusCode == http.StatusOK:
		// The server ignored the range: start over from byte zero.
		if offset, err = restart(out, hasher); err != nil {
			return err
		}
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return fmt.Errorf("models: download %s: status %d: %w", f.Path, resp.StatusCode, pipeline.ErrValidation)
	default:
		return fmt.Errorf("models: download %s: unexpected status %d", f.Path, resp.StatusCode)
	}

	// Read at most one byte past the pinned size, so an oversized body
	// fails the size check instead of filling the disk.
	body := io.LimitReader(resp.Body, f.Size-offset+1)
	buf := make([]byte, 1<<20)
	written := offset
	lastReport := time.Time{}
	for {
		n, readErr := body.Read(buf)
		if n > 0 {
			if _, err := out.Write(buf[:n]); err != nil {
				return err
			}
			(*hasher).Write(buf[:n])
			written += int64(n)
			if time.Since(lastReport) >= time.Second {
				progress(written)
				lastReport = time.Now()
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return fmt.Errorf("models: download %s at byte %d: %w", f.Path, written, readErr)
		}
	}
	progress(written)
	return nil
}

func restart(out *os.File, hasher *hash.Hash) (int64, error) {
	if err := out.Truncate(0); err != nil {
		return 0, err
	}
	if _, err := out.Seek(0, io.SeekStart); err != nil {
		return 0, err
	}
	*hasher = sha256.New()
	return 0, nil
}

func (d *Downloader) client() *http.Client {
	base := d.HTTP
	if base == nil {
		base = &http.Client{}
	}
	c := *base
	c.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("models: too many redirects")
		}
		return d.checkHost(req.URL.String())
	}
	return &c
}

func (d *Downloader) checkHost(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrHostNotAllowed, err)
	}
	allowed := d.AllowedHosts
	if allowed == nil {
		allowed = DefaultAllowedHosts
	}
	host := strings.ToLower(u.Hostname())
	for _, a := range allowed {
		if host == a || strings.HasSuffix(host, "."+a) {
			if u.Scheme != "https" && !strings.HasPrefix(host, "127.") && host != "localhost" {
				return fmt.Errorf("%w: %s is not https", ErrHostNotAllowed, raw)
			}
			return nil
		}
	}
	return fmt.Errorf("%w: %s", ErrHostNotAllowed, host)
}

func (d *Downloader) baseURL() string {
	if d.BaseURL != "" {
		return strings.TrimRight(d.BaseURL, "/")
	}
	return "https://huggingface.co"
}

// fileURL is Hugging Face's resolve endpoint for f at its pinned
// revision; each path segment is escaped separately.
func (d *Downloader) fileURL(f File, src Source) string {
	repo, rev := f.RepoAndRevision(src)
	segments := strings.Split(f.Remote, "/")
	for i, s := range segments {
		segments[i] = url.PathEscape(s)
	}
	return d.baseURL() + "/" + repo + "/resolve/" + rev + "/" + strings.Join(segments, "/")
}

func (d *Downloader) finalPath(f File) string {
	return filepath.Join(d.Dir, filepath.FromSlash(f.Path))
}

// partPath keys partial downloads by checksum, so two entries sharing a
// file resume the same partial and a changed pin never resumes a stale
// one.
func (d *Downloader) partPath(f File) string {
	return filepath.Join(d.Dir, stagingDir, f.SHA256+".part")
}

func hashFile(path string) (string, error) {
	fh, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = fh.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, fh); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
