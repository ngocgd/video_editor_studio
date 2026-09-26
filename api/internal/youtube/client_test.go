package youtube

import (
	"context"
	"testing"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		status int
		body   string
		kind   Kind
		reason string
	}{
		{403, `{"error":{"code":403,"message":"q","errors":[{"reason":"quotaExceeded"}]}}`, KindQuota, "quotaExceeded"},
		{403, `{"error":{"code":403,"message":"r","errors":[{"reason":"userRateLimitExceeded"}]}}`, KindTransient, "userRateLimitExceeded"},
		{403, `{"error":{"code":403,"message":"The user has exceeded the number of videos they may upload.","errors":[{"reason":"uploadLimitExceeded"}]}}`, KindPermanent, "uploadLimitExceeded"},
		{401, `{"error":{"code":401,"message":"Invalid Credentials","errors":[{"reason":"authError"}]}}`, KindAuth, "authError"},
		{429, ``, KindTransient, ""},
		{503, `not json`, KindTransient, ""},
		{400, `{"error":{"code":400,"message":"bad title","errors":[{"reason":"invalidTitle"}]}}`, KindPermanent, "invalidTitle"},
	}
	for _, c := range cases {
		e := classify(c.status, []byte(c.body))
		if e.Kind != c.kind || e.Reason != c.reason || e.Message == "" {
			t.Errorf("classify(%d, %s) = %+v; want %s/%s", c.status, c.body, e, c.kind, c.reason)
		}
	}
}

func TestMyChannelAndEligibility(t *testing.T) {
	f := newFakeYouTube(t)
	l := testLedger()
	ch, err := f.client(l).MyChannel(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := Channel{ID: "UC123", Title: "Night Tales", ThumbnailURL: "https://yt3.example/m.jpg", LongUploadsStatus: "eligible", UploadsPlaylistID: "UU123"}
	if ch != want {
		t.Fatalf("MyChannel = %+v, want %+v", ch, want)
	}
	if u, _ := l.Usage(context.Background()); u.Used != 1 {
		t.Errorf("channels.list charged %d units, want 1", u.Used)
	}

	if err := CheckEligibility(ch, 14*60); err != nil {
		t.Errorf("a 14-minute video needs no verification: %v", err)
	}
	if err := CheckEligibility(ch, 31*60); !HasReason(err, ReasonLongUploadsNotAllowed) || !IsKind(err, KindPermanent) {
		t.Errorf("31 minutes on an unverified channel: %v", err)
	}
	ch.LongUploadsStatus = "allowed"
	if err := CheckEligibility(ch, 31*60); err != nil {
		t.Errorf("verified channel: %v", err)
	}
}
