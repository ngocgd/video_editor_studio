package youtube

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"net/http"
	"regexp"
	"strconv"
)

// Upload chunk sizes. Google requires every chunk but the last to be a
// multiple of 256 KiB; one chunk is the upload's only large buffer, so the
// maximum keeps an upload within its 64 MB memory budget.
const (
	DefaultChunkSize int64 = 16 << 20
	MaxChunkSize     int64 = 64 << 20
	chunkQuantum     int64 = 256 << 10
)

// RangeReader reads object bytes with one ranged GET per call
// (storage.Internal implements it).
type RangeReader interface {
	ReadRange(ctx context.Context, key string, offset int64, buf []byte) error
}

// VideoSnippet and VideoStatus are the videos.insert resource fields the
// studio sets.
type VideoSnippet struct {
	Title           string   `json:"title"`
	Description     string   `json:"description"`
	Tags            []string `json:"tags,omitempty"`
	CategoryID      string   `json:"categoryId,omitempty"`
	DefaultLanguage string   `json:"defaultLanguage,omitempty"`
}

type VideoStatus struct {
	// PrivacyStatus is private, unlisted or public. A scheduled upload is
	// private with PublishAt set.
	PrivacyStatus           string `json:"privacyStatus"`
	PublishAt               string `json:"publishAt,omitempty"`
	SelfDeclaredMadeForKids bool   `json:"selfDeclaredMadeForKids"`
	ContainsSyntheticMedia  bool   `json:"containsSyntheticMedia"`
}

// VideoMetadata is the JSON body that opens the resumable session.
type VideoMetadata struct {
	Snippet VideoSnippet `json:"snippet"`
	Status  VideoStatus  `json:"status"`
}

// UploadRequest describes one resumable upload of an object in MinIO.
type UploadRequest struct {
	Source RangeReader
	Key    string
	Size   int64
	// ExpectedSHA256 (hex) is the approved render's digest. The running
	// digest of the streamed bytes must match it before the final chunk
	// is sent, or the session is cancelled.
	ExpectedSHA256 string
	Metadata       VideoMetadata
	// SessionURI resumes an existing session; empty opens a new one.
	SessionURI string
	// PersistSession stores a newly opened session URI (encrypted: it is a
	// capability URL). It runs before the first byte is sent, and the
	// upload stops if it fails, so a crash can always resume or dedupe.
	PersistSession func(ctx context.Context, sessionURI string) error
	// ChunkSize defaults to DefaultChunkSize.
	ChunkSize int64
}

// UploadResult is the created video, or, with an error, the session that
// a retry should resume.
type UploadResult struct {
	VideoID    string
	SessionURI string
}

var sha256Hex = regexp.MustCompile(`^[0-9a-f]{64}$`)

func (r *UploadRequest) validate() error {
	switch {
	case r.Source == nil || r.Key == "":
		return errors.New("youtube: upload needs a source object")
	case r.Size <= 0:
		return errors.New("youtube: upload size must be positive")
	case !sha256Hex.MatchString(r.ExpectedSHA256):
		return errors.New("youtube: upload needs the approved sha256 (lowercase hex)")
	case r.SessionURI == "" && r.PersistSession == nil:
		return errors.New("youtube: a new upload session needs PersistSession")
	case r.ChunkSize <= 0 || r.ChunkSize > MaxChunkSize || r.ChunkSize%chunkQuantum != 0:
		return fmt.Errorf("youtube: chunk size %d must be a multiple of 256 KiB up to 64 MiB", r.ChunkSize)
	}
	return nil
}

// uploader carries one upload's state: the reusable chunk buffer and the
// running digest of every byte from 0 to hashed.
type uploader struct {
	c      *Client
	req    UploadRequest
	buf    []byte
	digest hash.Hash
	hashed int64
}

// Upload streams req's object to YouTube through a resumable session.
// Each chunk is a fresh ranged read into one reused buffer, so memory stays
// at one chunk and no temp file is written. A resumed session first asks
// Google how many bytes it holds, and continues from there.
func (c *Client) Upload(ctx context.Context, req UploadRequest) (UploadResult, error) {
	if req.ChunkSize == 0 {
		req.ChunkSize = DefaultChunkSize
	}
	if err := req.validate(); err != nil {
		return UploadResult{}, err
	}
	u := &uploader{c: c, req: req, buf: make([]byte, req.ChunkSize), digest: sha256.New()}
	res := UploadResult{SessionURI: req.SessionURI}

	var offset int64
	if res.SessionURI == "" {
		uri, err := c.openSession(ctx, req)
		if err != nil {
			return res, err
		}
		if err := req.PersistSession(ctx, uri); err != nil {
			return res, fmt.Errorf("youtube: persist upload session: %w", err)
		}
		res.SessionURI = uri
	} else {
		next, id, err := c.sendRange(ctx, res.SessionURI, fmt.Sprintf("bytes */%d", req.Size), nil)
		if err != nil {
			return res, err
		}
		if id != "" {
			res.VideoID = id
			return res, nil
		}
		offset = next
	}

	if err := u.rehash(ctx, offset); err != nil {
		return res, err
	}
	for {
		if offset >= req.Size {
			// Google holds every byte but has not answered with the video;
			// the next attempt's status query will.
			return res, &APIError{Kind: KindTransient, Reason: "upload_incomplete", Message: "all bytes sent but no video returned yet"}
		}
		n := min(req.ChunkSize, req.Size-offset)
		chunk := u.buf[:n]
		if err := req.Source.ReadRange(ctx, req.Key, offset, chunk); err != nil {
			return res, err
		}
		u.digest.Write(chunk)
		u.hashed = offset + n
		if u.hashed == req.Size {
			if got := hex.EncodeToString(u.digest.Sum(nil)); got != req.ExpectedSHA256 {
				c.cancelSession(ctx, res.SessionURI)
				return res, &APIError{
					Kind:    KindPermanent,
					Reason:  ReasonRenderChanged,
					Message: "the render's bytes no longer match the approved sha256; the upload was cancelled before its final chunk",
				}
			}
		}
		contentRange := fmt.Sprintf("bytes %d-%d/%d", offset, offset+n-1, req.Size)
		next, id, err := c.sendRange(ctx, res.SessionURI, contentRange, chunk)
		if err != nil {
			return res, err
		}
		if id != "" {
			res.VideoID = id
			return res, nil
		}
		if next > offset+n {
			return res, fmt.Errorf("youtube: server reports %d bytes after only %d were sent", next, offset+n)
		}
		if next != offset+n {
			// Google kept only part of the chunk: rebuild the digest up to
			// what it holds and resend the rest.
			if err := u.rehash(ctx, next); err != nil {
				return res, err
			}
		}
		offset = next
	}
}

// rehash recomputes the running digest over bytes [0, to) with ranged
// reads, for a resumed session or a partially accepted chunk.
func (u *uploader) rehash(ctx context.Context, to int64) error {
	u.digest.Reset()
	u.hashed = 0
	for u.hashed < to {
		n := min(int64(len(u.buf)), to-u.hashed)
		b := u.buf[:n]
		if err := u.req.Source.ReadRange(ctx, u.req.Key, u.hashed, b); err != nil {
			return err
		}
		u.digest.Write(b)
		u.hashed += n
	}
	return nil
}

// openSession reserves the insert's quota and opens a resumable session,
// returning its URI (the Location header).
func (c *Client) openSession(ctx context.Context, req UploadRequest) (string, error) {
	if c.Ledger != nil {
		if err := c.Ledger.Reserve(ctx, OpVideosInsert); err != nil {
			return "", err
		}
	}
	body, err := json.Marshal(req.Metadata)
	if err != nil {
		return "", err
	}
	u := c.uploadBase() + "/videos?uploadType=resumable&part=snippet,status"
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	hr.Header.Set("Content-Type", "application/json; charset=UTF-8")
	hr.Header.Set("X-Upload-Content-Type", "video/mp4")
	hr.Header.Set("X-Upload-Content-Length", strconv.FormatInt(req.Size, 10))
	resp, err := c.HTTP.Do(hr)
	if err != nil {
		return "", transportError(OpVideosInsert, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", c.failure(ctx, OpVideosInsert, resp)
	}
	loc := resp.Header.Get("Location")
	if loc == "" {
		return "", errors.New("youtube: resumable session opened without a Location")
	}
	return loc, nil
}

var rangeHeader = regexp.MustCompile(`^bytes=0-(\d+)$`)

// sendRange PUTs one chunk (or, with a nil body and "bytes */N", a status
// query) to the session. It returns the next offset Google expects on
// 308, or the video id on 200/201. A session Google no longer knows
// (404/410) is a permanent upload_session_gone: the caller dedupes by
// nonce tag before opening a new one.
func (c *Client) sendRange(ctx context.Context, sessionURI, contentRange string, chunk []byte) (next int64, videoID string, err error) {
	hr, err := http.NewRequestWithContext(ctx, http.MethodPut, sessionURI, bytes.NewReader(chunk))
	if err != nil {
		return 0, "", err
	}
	hr.ContentLength = int64(len(chunk))
	hr.Header.Set("Content-Range", contentRange)
	resp, err := c.HTTP.Do(hr)
	if err != nil {
		return 0, "", transportError(OpVideosInsert, err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusPermanentRedirect:
		m := rangeHeader.FindStringSubmatch(resp.Header.Get("Range"))
		if m == nil {
			return 0, "", nil // nothing received yet
		}
		last, perr := strconv.ParseInt(m[1], 10, 64)
		if perr != nil {
			return 0, "", fmt.Errorf("youtube: bad Range %q: %w", m[0], perr)
		}
		return last + 1, "", nil
	case http.StatusOK, http.StatusCreated:
		var v struct {
			ID string `json:"id"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&v); err != nil || v.ID == "" {
			return 0, "", fmt.Errorf("youtube: upload finished without a video id: %v", err)
		}
		return 0, v.ID, nil
	case http.StatusNotFound, http.StatusGone:
		return 0, "", &APIError{Kind: KindPermanent, Status: resp.StatusCode, Reason: ReasonUploadSessionGone, Message: "the resumable upload session expired or was cancelled"}
	default:
		return 0, "", c.failure(ctx, OpVideosInsert, resp)
	}
}

// cancelSession asks Google to drop the session. It is best effort: a
// session that survives expires on its own, and nothing was published.
func (c *Client) cancelSession(ctx context.Context, sessionURI string) {
	hr, err := http.NewRequestWithContext(context.WithoutCancel(ctx), http.MethodDelete, sessionURI, nil)
	if err != nil {
		return
	}
	if resp, err := c.HTTP.Do(hr); err == nil {
		resp.Body.Close()
	}
}
