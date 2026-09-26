package youtube

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// fakeYouTube is an httptest double of the Data API and the resumable
// upload protocol, with switches for the failure modes under test.
type fakeYouTube struct {
	t   *testing.T
	srv *httptest.Server

	mu        sync.Mutex
	size      int64
	received  []byte
	metadata  VideoMetadata
	sessions  int // resumable sessions opened
	dataPuts  []int64
	cancelled bool
	videoID   string

	// failAfterPuts answers 503 to every data PUT after this many (0 = never).
	failAfterPuts int
	// keepHalf makes the server keep only half of the next data PUT.
	keepHalf bool
	// gone answers 410 to every session request.
	gone bool
	// insertStatus overrides the session-open answer (with insertBody).
	insertStatus int
	insertBody   string
	// uploads is the channel's uploads playlist: video id -> tags.
	uploads map[string][]string
	// persistedBeforeData is set when the session URI was persisted
	// before the first data byte arrived.
	persisted           bool
	persistedBeforeData bool
}

func newFakeYouTube(t *testing.T) *fakeYouTube {
	f := &fakeYouTube{t: t, videoID: "vid-new", uploads: map[string][]string{}}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /upload/youtube/v3/videos", f.openSession)
	mux.HandleFunc("PUT /session", f.put)
	mux.HandleFunc("DELETE /session", func(w http.ResponseWriter, _ *http.Request) {
		f.mu.Lock()
		f.cancelled = true
		f.mu.Unlock()
		w.WriteHeader(499)
	})
	mux.HandleFunc("GET /youtube/v3/channels", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"items":[{"id":"UC123","snippet":{"title":"Night Tales","thumbnails":{"default":{"url":"https://yt3.example/d.jpg"},"medium":{"url":"https://yt3.example/m.jpg"}}},"status":{"longUploadsStatus":"eligible"},"contentDetails":{"relatedPlaylists":{"uploads":"UU123"}}}]}`)
	})
	mux.HandleFunc("GET /youtube/v3/playlistItems", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("playlistId") != "UU123" || r.URL.Query().Get("maxResults") != "50" {
			http.Error(w, "bad query", http.StatusBadRequest)
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		var items []string
		for id := range f.uploads {
			items = append(items, fmt.Sprintf(`{"contentDetails":{"videoId":%q}}`, id))
		}
		_, _ = io.WriteString(w, `{"items":[`+strings.Join(items, ",")+`]}`)
	})
	mux.HandleFunc("GET /youtube/v3/videos", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		type item struct {
			ID      string `json:"id"`
			Snippet struct {
				Tags []string `json:"tags"`
			} `json:"snippet"`
		}
		var out struct {
			Items []item `json:"items"`
		}
		for id := range strings.SplitSeq(r.URL.Query().Get("id"), ",") {
			if tags, ok := f.uploads[id]; ok {
				it := item{ID: id}
				it.Snippet.Tags = tags
				out.Items = append(out.Items, it)
			}
		}
		_ = json.NewEncoder(w).Encode(out)
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeYouTube) client(l *Ledger) *Client {
	return &Client{
		HTTP:       f.srv.Client(),
		Ledger:     l,
		APIBase:    f.srv.URL + "/youtube/v3",
		UploadBase: f.srv.URL + "/upload/youtube/v3",
	}
}

func (f *fakeYouTube) openSession(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.insertStatus != 0 {
		w.WriteHeader(f.insertStatus)
		_, _ = io.WriteString(w, f.insertBody)
		return
	}
	if r.URL.Query().Get("uploadType") != "resumable" || r.Header.Get("X-Upload-Content-Type") != "video/mp4" {
		http.Error(w, "not a resumable video upload", http.StatusBadRequest)
		return
	}
	size, err := strconv.ParseInt(r.Header.Get("X-Upload-Content-Length"), 10, 64)
	if err != nil {
		http.Error(w, "missing length", http.StatusBadRequest)
		return
	}
	if err := json.NewDecoder(r.Body).Decode(&f.metadata); err != nil {
		http.Error(w, "bad metadata", http.StatusBadRequest)
		return
	}
	f.sessions++
	f.size = size
	f.received = f.received[:0]
	w.Header().Set("Location", f.srv.URL+"/session?upload_id=s"+strconv.Itoa(f.sessions))
	w.WriteHeader(http.StatusOK)
}

func (f *fakeYouTube) progress(w http.ResponseWriter) {
	if len(f.received) > 0 {
		w.Header().Set("Range", fmt.Sprintf("bytes=0-%d", len(f.received)-1))
	}
	w.WriteHeader(http.StatusPermanentRedirect)
}

func (f *fakeYouTube) done(w http.ResponseWriter) {
	f.uploads[f.videoID] = f.metadata.Snippet.Tags
	w.WriteHeader(http.StatusCreated)
	_, _ = fmt.Fprintf(w, `{"id":%q}`, f.videoID)
}

func (f *fakeYouTube) put(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.gone {
		w.WriteHeader(http.StatusGone)
		return
	}
	body, _ := io.ReadAll(r.Body)
	cr := r.Header.Get("Content-Range")
	if strings.HasPrefix(cr, "bytes */") {
		if int64(len(f.received)) == f.size {
			f.done(w)
			return
		}
		f.progress(w)
		return
	}
	var start, end, total int64
	if _, err := fmt.Sscanf(cr, "bytes %d-%d/%d", &start, &end, &total); err != nil || total != f.size || end-start+1 != int64(len(body)) {
		http.Error(w, "bad Content-Range "+cr, http.StatusBadRequest)
		return
	}
	if start != int64(len(f.received)) {
		http.Error(w, "out-of-order chunk", http.StatusBadRequest)
		return
	}
	if !f.persistedBeforeData && len(f.dataPuts) == 0 {
		f.persistedBeforeData = f.persisted
	}
	f.dataPuts = append(f.dataPuts, start)
	if f.failAfterPuts > 0 && len(f.dataPuts) > f.failAfterPuts {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, `{"error":{"code":503,"message":"backend unavailable","errors":[{"reason":"backendError"}]}}`)
		return
	}
	if f.keepHalf {
		f.keepHalf = false
		body = body[:len(body)/2]
	}
	f.received = append(f.received, body...)
	if int64(len(f.received)) == f.size {
		f.done(w)
		return
	}
	f.progress(w)
}

// memObject is an in-memory RangeReader that records the largest read.
type memObject struct {
	data    []byte
	maxRead int
}

func (m *memObject) ReadRange(_ context.Context, _ string, offset int64, buf []byte) error {
	if offset+int64(len(buf)) > int64(len(m.data)) {
		return io.ErrUnexpectedEOF
	}
	copy(buf, m.data[offset:])
	m.maxRead = max(m.maxRead, len(buf))
	return nil
}

func testVideo(n int) (*memObject, string) {
	data := make([]byte, n)
	for i := range data {
		data[i] = byte(i*7 + i/1000)
	}
	sum := sha256.Sum256(data)
	return &memObject{data: data}, hex.EncodeToString(sum[:])
}

// snapshot is a copy of the fake's state, taken under its lock.
type snapshot struct {
	received            []byte
	dataPuts            []int64
	sessions            int
	size                int64
	cancelled           bool
	persistedBeforeData bool
	metadata            VideoMetadata
}

func (f *fakeYouTube) snap() snapshot {
	f.mu.Lock()
	defer f.mu.Unlock()
	return snapshot{
		received: append([]byte(nil), f.received...), dataPuts: append([]int64(nil), f.dataPuts...),
		sessions: f.sessions, size: f.size, cancelled: f.cancelled,
		persistedBeforeData: f.persistedBeforeData, metadata: f.metadata,
	}
}

// set changes the fake's switches under its lock.
func (f *fakeYouTube) set(fn func(f *fakeYouTube)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f)
}
