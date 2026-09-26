package youtube

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestUploadChunkOutlivesBaseClientTimeout(t *testing.T) {
	f := newFakeYouTube(t)
	f.set(func(f *fakeYouTube) { f.putDelay = 150 * time.Millisecond })
	obj, sum := testVideo(videoSize)
	var stored string
	c := f.client(testLedger())
	// The base client's whole-request timeout is shorter than any chunk
	// takes on this slow uplink; chunks must be bounded by their own
	// deadline instead.
	c.HTTP.Timeout = 50 * time.Millisecond
	res, err := c.Upload(context.Background(), newRequest(f, obj, sum, &stored))
	if err != nil {
		t.Fatal(err)
	}
	if res.VideoID != "vid-new" {
		t.Fatalf("result %+v", res)
	}
	if c.HTTP.Timeout != 50*time.Millisecond {
		t.Error("the caller's client must not be modified")
	}
}

func TestUploadChunkDeadlineIsTransient(t *testing.T) {
	f := newFakeYouTube(t)
	f.set(func(f *fakeYouTube) { f.putDelay = 300 * time.Millisecond })
	obj, sum := testVideo(videoSize)
	var stored string
	c := f.client(testLedger())
	c.chunkDeadline = func(int) time.Duration { return 50 * time.Millisecond }
	_, err := c.Upload(context.Background(), newRequest(f, obj, sum, &stored))
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Kind != KindTransient || apiErr.Reason != ReasonChunkTimeout {
		t.Fatalf("want a transient chunk timeout, got %v", err)
	}
}

func TestUploadTransportErrorHidesSessionURI(t *testing.T) {
	f := newFakeYouTube(t)
	f.set(func(f *fakeYouTube) { f.dropConn = true })
	obj, sum := testVideo(videoSize)
	var stored string
	_, err := f.client(testLedger()).Upload(context.Background(), newRequest(f, obj, sum, &stored))
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Kind != KindTransient {
		t.Fatalf("want a transient transport error, got %v", err)
	}
	if stored == "" {
		t.Fatal("no session was opened")
	}
	if msg := err.Error(); strings.Contains(msg, "upload_id") || strings.Contains(msg, "/session") {
		t.Fatalf("error text leaks the session URI: %s", msg)
	}
}

func TestCancelledUploadErrorHidesSessionURI(t *testing.T) {
	f := newFakeYouTube(t)
	f.set(func(f *fakeYouTube) { f.putDelay = 300 * time.Millisecond })
	obj, sum := testVideo(videoSize)
	var stored string
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := f.client(testLedger()).Upload(ctx, newRequest(f, obj, sum, &stored))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want the caller's deadline, got %v", err)
	}
	if msg := err.Error(); strings.Contains(msg, "upload_id") || strings.Contains(msg, "/session") {
		t.Fatalf("error text leaks the session URI: %s", msg)
	}
}
