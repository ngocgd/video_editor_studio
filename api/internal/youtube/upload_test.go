package youtube

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"
)

const testChunk = 256 << 10

// videoSize spans three full chunks and a short final one.
const videoSize = 3*testChunk + 1000

func newRequest(f *fakeYouTube, obj *memObject, sum string, stored *string) UploadRequest {
	return UploadRequest{
		Source:         obj,
		Key:            "tenant/render/ep1.mp4",
		Size:           int64(len(obj.data)),
		ExpectedSHA256: sum,
		ChunkSize:      testChunk,
		Metadata: VideoMetadata{
			Snippet: VideoSnippet{Title: "Episode 1", Description: "desc", Tags: []string{"story", "lt-nonce1"}},
			Status:  VideoStatus{PrivacyStatus: "private", ContainsSyntheticMedia: true},
		},
		PersistSession: func(_ context.Context, uri string) error {
			f.mu.Lock()
			f.persisted = true
			f.mu.Unlock()
			*stored = uri
			return nil
		},
	}
}

func testLedger() *Ledger {
	return &Ledger{Store: &memQuota{units: map[string]int32{}}, Config: DefaultQuotaConfig(),
		Now: func() time.Time { return time.Date(2026, 9, 26, 20, 0, 0, 0, time.UTC) }}
}

func TestUploadChunkedPersistsSessionFirst(t *testing.T) {
	f := newFakeYouTube(t)
	obj, sum := testVideo(videoSize)
	var stored string
	res, err := f.client(testLedger()).Upload(context.Background(), newRequest(f, obj, sum, &stored))
	if err != nil {
		t.Fatal(err)
	}
	if res.VideoID != "vid-new" || stored == "" || res.SessionURI != stored {
		t.Fatalf("result %+v, stored %q", res, stored)
	}
	s := f.snap()
	if !s.persistedBeforeData {
		t.Error("session URI must be persisted before the first byte is sent")
	}
	if !bytes.Equal(s.received, obj.data) {
		t.Error("uploaded bytes differ from the object")
	}
	if len(s.dataPuts) != 4 || obj.maxRead > testChunk {
		t.Errorf("puts %v, largest read %d; want 4 chunks of at most %d", s.dataPuts, obj.maxRead, testChunk)
	}
	if !s.metadata.Status.ContainsSyntheticMedia || s.metadata.Status.PrivacyStatus != "private" {
		t.Errorf("metadata status %+v", s.metadata.Status)
	}
}

func TestUploadResumesFromReportedOffset(t *testing.T) {
	f := newFakeYouTube(t)
	f.set(func(f *fakeYouTube) { f.failAfterPuts = 2 })
	obj, sum := testVideo(videoSize)
	var stored string
	c := f.client(testLedger())
	req := newRequest(f, obj, sum, &stored)

	res, err := c.Upload(context.Background(), req)
	if !IsKind(err, KindTransient) || res.SessionURI == "" {
		t.Fatalf("first attempt: %+v, %v; want a transient error with the session", res, err)
	}

	f.set(func(f *fakeYouTube) { f.failAfterPuts = 0; f.dataPuts = nil })
	req.SessionURI = res.SessionURI
	res, err = c.Upload(context.Background(), req)
	if err != nil || res.VideoID != "vid-new" {
		t.Fatalf("resume: %+v, %v", res, err)
	}
	s := f.snap()
	if len(s.dataPuts) == 0 || s.dataPuts[0] != 2*testChunk {
		t.Errorf("resumed puts %v; want the first at offset %d, not 0", s.dataPuts, 2*testChunk)
	}
	if s.sessions != 1 || !bytes.Equal(s.received, obj.data) {
		t.Errorf("sessions %d, bytes equal %v", s.sessions, bytes.Equal(s.received, obj.data))
	}
}

func TestUploadCrashAfterFinalChunkDoesNotReupload(t *testing.T) {
	f := newFakeYouTube(t)
	obj, sum := testVideo(videoSize)
	var stored string
	c := f.client(testLedger())
	req := newRequest(f, obj, sum, &stored)
	if _, err := c.Upload(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	// The worker died before recording the video id: a retry resumes the
	// persisted session and learns the id from the status query.
	puts := len(f.snap().dataPuts)
	req.SessionURI = stored
	res, err := c.Upload(context.Background(), req)
	if err != nil || res.VideoID != "vid-new" {
		t.Fatalf("retry: %+v, %v", res, err)
	}
	if s := f.snap(); s.sessions != 1 || len(s.dataPuts) != puts {
		t.Errorf("retry opened %d sessions and sent %d more chunks; want 1 and 0", s.sessions, len(s.dataPuts)-puts)
	}
}

func TestUploadSessionGoneThenDedupeFindsVideo(t *testing.T) {
	f := newFakeYouTube(t)
	f.set(func(f *fakeYouTube) {
		f.uploads["vid-old"] = []string{"story"}
		f.uploads["vid-done"] = []string{"story", "lt-nonce1"}
		f.gone = true
	})
	obj, sum := testVideo(videoSize)
	var stored string
	c := f.client(testLedger())
	req := newRequest(f, obj, sum, &stored)
	req.SessionURI = f.srv.URL + "/session?upload_id=expired"

	_, err := c.Upload(context.Background(), req)
	if !HasReason(err, ReasonUploadSessionGone) {
		t.Fatalf("got %v, want %s", err, ReasonUploadSessionGone)
	}
	ch, err := c.MyChannel(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	id, found, err := c.FindUploadByTag(context.Background(), ch.UploadsPlaylistID, "lt-nonce1")
	if err != nil || !found || id != "vid-done" {
		t.Fatalf("dedupe: %q %v %v; want vid-done", id, found, err)
	}
	if _, found, _ := c.FindUploadByTag(context.Background(), ch.UploadsPlaylistID, "lt-other"); found {
		t.Error("an untagged channel must not match")
	}
}

func TestUploadHashMismatchAbortsBeforeFinalChunk(t *testing.T) {
	f := newFakeYouTube(t)
	obj, sum := testVideo(videoSize)
	obj.data[len(obj.data)-1] ^= 0xff // the render changed after approval
	var stored string
	_, err := f.client(testLedger()).Upload(context.Background(), newRequest(f, obj, sum, &stored))
	if !HasReason(err, ReasonRenderChanged) || !IsKind(err, KindPermanent) {
		t.Fatalf("got %v, want permanent %s", err, ReasonRenderChanged)
	}
	s := f.snap()
	if int64(len(s.received)) >= s.size {
		t.Error("the final chunk must never be sent")
	}
	if !s.cancelled {
		t.Error("the session must be cancelled")
	}
}

func TestUploadPartialChunkAcceptance(t *testing.T) {
	f := newFakeYouTube(t)
	f.set(func(f *fakeYouTube) { f.keepHalf = true })
	obj, sum := testVideo(videoSize)
	var stored string
	res, err := f.client(testLedger()).Upload(context.Background(), newRequest(f, obj, sum, &stored))
	if err != nil || res.VideoID == "" {
		t.Fatalf("%+v, %v", res, err)
	}
	if !bytes.Equal(f.snap().received, obj.data) {
		t.Error("bytes differ after a partially accepted chunk")
	}
}

func TestUploadQuota(t *testing.T) {
	ctx := context.Background()
	obj, sum := testVideo(videoSize)
	var stored string

	t.Run("ledger refuses without calling Google", func(t *testing.T) {
		f := newFakeYouTube(t)
		l := testLedger()
		l.Config.DailyLimit = 1000
		_, err := f.client(l).Upload(ctx, newRequest(f, obj, sum, &stored))
		var qe *QuotaExceededError
		if !errors.As(err, &qe) || f.snap().sessions != 0 {
			t.Fatalf("got %v with %d sessions; want a local refusal", err, f.snap().sessions)
		}
	})

	t.Run("Google quotaExceeded marks the day exhausted", func(t *testing.T) {
		f := newFakeYouTube(t)
		f.set(func(f *fakeYouTube) {
			f.insertStatus = 403
			f.insertBody = `{"error":{"code":403,"message":"quota","errors":[{"reason":"quotaExceeded"}]}}`
		})
		l := testLedger()
		c := f.client(l)
		_, err := c.Upload(ctx, newRequest(f, obj, sum, &stored))
		if !IsKind(err, KindQuota) {
			t.Fatalf("got %v, want quota", err)
		}
		if _, err := c.MyChannel(ctx); !IsKind(err, KindQuota) {
			t.Fatalf("read after exhaustion: %v, want a local quota refusal", err)
		}
	})
}

func TestUploadRequestValidation(t *testing.T) {
	f := newFakeYouTube(t)
	obj, sum := testVideo(1000)
	var stored string
	c := f.client(nil)
	bad := []func(*UploadRequest){
		func(r *UploadRequest) { r.ChunkSize = testChunk + 1 },
		func(r *UploadRequest) { r.ChunkSize = MaxChunkSize + chunkQuantum },
		func(r *UploadRequest) { r.ExpectedSHA256 = "ABC" },
		func(r *UploadRequest) { r.PersistSession = nil },
		func(r *UploadRequest) { r.Size = 0 },
	}
	for i, mutate := range bad {
		req := newRequest(f, obj, sum, &stored)
		mutate(&req)
		if _, err := c.Upload(context.Background(), req); err == nil {
			t.Errorf("case %d: want a validation error", i)
		}
	}
	if f.snap().sessions != 0 {
		t.Error("invalid requests must not open sessions")
	}
}
