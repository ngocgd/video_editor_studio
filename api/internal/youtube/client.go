package youtube

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
)

// Official endpoint bases. Tests point them at an httptest double.
const (
	DefaultAPIBase    = "https://www.googleapis.com/youtube/v3"
	DefaultUploadBase = "https://www.googleapis.com/upload/youtube/v3"
)

// maxErrorBody bounds how much of a failed response is read for its
// error envelope.
const maxErrorBody = 64 << 10

// Client calls the YouTube Data API for one channel. HTTP must already
// authorize requests as that channel (an oauth2 transport over the
// netguard client); Client never sees the tokens themselves.
type Client struct {
	HTTP   *http.Client
	Ledger *Ledger
	// APIBase and UploadBase default to the official endpoints.
	APIBase    string
	UploadBase string
}

func (c *Client) apiBase() string {
	if c.APIBase != "" {
		return strings.TrimRight(c.APIBase, "/")
	}
	return DefaultAPIBase
}

func (c *Client) uploadBase() string {
	if c.UploadBase != "" {
		return strings.TrimRight(c.UploadBase, "/")
	}
	return DefaultUploadBase
}

// transportError wraps a failure to get any HTTP answer at all.
func transportError(op Op, err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	// The authorizing transport fails before any request when Google
	// rejects the stored refresh token: the channel must be reconnected.
	var rn interface{ ReconnectNeeded() bool }
	if errors.As(err, &rn) && rn.ReconnectNeeded() {
		return &APIError{Kind: KindAuth, Reason: ReasonReconnectNeeded, Message: fmt.Sprintf("%s: %v", op, err)}
	}
	return &APIError{Kind: KindTransient, Reason: "transport", Message: fmt.Sprintf("%s: %v", op, err)}
}

// failure classifies a non-2xx response and, when Google says the daily
// pool is spent, records that in the ledger so later calls stop locally.
func (c *Client) failure(ctx context.Context, op Op, resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
	ae := classify(resp.StatusCode, body)
	if ae.Kind == KindQuota && c.Ledger != nil {
		// MarkExhausted always answers with an error: the local
		// QuotaExceededError on success, so only a failure to record the
		// exhaustion is worth joining.
		if err := c.Ledger.MarkExhausted(ctx, op); !IsKind(err, KindQuota) {
			return errors.Join(ae, err)
		}
	}
	return ae
}

// getJSON reserves op's quota, then GETs path?query into out.
func (c *Client) getJSON(ctx context.Context, op Op, path string, query url.Values, out any) error {
	if c.Ledger != nil {
		if err := c.Ledger.Reserve(ctx, op); err != nil {
			return err
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.apiBase()+path+"?"+query.Encode(), nil)
	if err != nil {
		return err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return transportError(op, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return c.failure(ctx, op, resp)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("youtube: decode %s: %w", op, err)
	}
	return nil
}

// Channel is the authorized user's channel as the Data API reports it.
type Channel struct {
	ID           string
	Title        string
	ThumbnailURL string
	// LongUploadsStatus is allowed, eligible, disallowed or unknown.
	LongUploadsStatus string
	// UploadsPlaylistID lists the channel's uploads, newest first.
	UploadsPlaylistID string
}

type channelsResponse struct {
	Items []struct {
		ID      string `json:"id"`
		Snippet struct {
			Title      string `json:"title"`
			Thumbnails map[string]struct {
				URL string `json:"url"`
			} `json:"thumbnails"`
		} `json:"snippet"`
		Status struct {
			LongUploadsStatus string `json:"longUploadsStatus"`
		} `json:"status"`
		ContentDetails struct {
			RelatedPlaylists struct {
				Uploads string `json:"uploads"`
			} `json:"relatedPlaylists"`
		} `json:"contentDetails"`
	} `json:"items"`
}

// ErrNoChannel means the Google account has no YouTube channel.
var ErrNoChannel = &APIError{Kind: KindPermanent, Reason: "no_channel", Message: "this Google account has no YouTube channel"}

// MyChannel reads the authorized channel (channels.list mine=true) with
// its eligibility status. It is both the connect-time fetch and the
// pre-publish eligibility precheck.
func (c *Client) MyChannel(ctx context.Context) (Channel, error) {
	q := url.Values{"part": {"snippet,status,contentDetails"}, "mine": {"true"}}
	var r channelsResponse
	if err := c.getJSON(ctx, OpChannelsList, "/channels", q, &r); err != nil {
		return Channel{}, err
	}
	if len(r.Items) == 0 {
		return Channel{}, ErrNoChannel
	}
	it := r.Items[0]
	ch := Channel{
		ID:                it.ID,
		Title:             it.Snippet.Title,
		LongUploadsStatus: normalizeLongUploads(it.Status.LongUploadsStatus),
		UploadsPlaylistID: it.ContentDetails.RelatedPlaylists.Uploads,
	}
	for _, size := range []string{"medium", "default", "high"} {
		if t, ok := it.Snippet.Thumbnails[size]; ok && t.URL != "" {
			ch.ThumbnailURL = t.URL
			break
		}
	}
	return ch, nil
}

// normalizeLongUploads maps the API's values onto the stored set.
func normalizeLongUploads(s string) string {
	switch s {
	case "allowed", "eligible", "disallowed":
		return s
	default:
		return "unknown"
	}
}

// LongUploadLimitSeconds is the longest video an unverified channel may
// upload (15 minutes).
const LongUploadLimitSeconds = 15 * 60

// CheckEligibility returns a permanent long_uploads_not_allowed error when
// a video longer than 15 minutes is headed for a channel whose long
// uploads are not allowed.
func CheckEligibility(ch Channel, durationSeconds float64) error {
	if durationSeconds > LongUploadLimitSeconds && ch.LongUploadsStatus != "allowed" {
		return &APIError{
			Kind:    KindPermanent,
			Reason:  ReasonLongUploadsNotAllowed,
			Message: "Verify your channel to upload videos longer than 15 minutes (https://www.youtube.com/verify)",
		}
	}
	return nil
}

type playlistItemsResponse struct {
	Items []struct {
		ContentDetails struct {
			VideoID string `json:"videoId"`
		} `json:"contentDetails"`
	} `json:"items"`
}

type videosResponse struct {
	Items []struct {
		ID      string `json:"id"`
		Snippet struct {
			Tags []string `json:"tags"`
		} `json:"snippet"`
	} `json:"items"`
}

// dedupeWindow is how many of the newest uploads are searched.
const dedupeWindow = 50

// FindUploadByTag searches the channel's 50 newest uploads for one tagged
// tag (the per-publication nonce tag), so an upload whose session was lost
// after YouTube had already created the video is adopted instead of being
// uploaded a second time. found is false when no upload carries the tag.
func (c *Client) FindUploadByTag(ctx context.Context, uploadsPlaylistID, tag string) (videoID string, found bool, err error) {
	var items playlistItemsResponse
	q := url.Values{"part": {"contentDetails"}, "playlistId": {uploadsPlaylistID}, "maxResults": {fmt.Sprint(dedupeWindow)}}
	if err := c.getJSON(ctx, OpPlaylistItemsList, "/playlistItems", q, &items); err != nil {
		return "", false, err
	}
	ids := make([]string, 0, len(items.Items))
	for _, it := range items.Items {
		if it.ContentDetails.VideoID != "" {
			ids = append(ids, it.ContentDetails.VideoID)
		}
	}
	if len(ids) == 0 {
		return "", false, nil
	}
	var videos videosResponse
	q = url.Values{"part": {"snippet"}, "id": {strings.Join(ids, ",")}, "maxResults": {fmt.Sprint(dedupeWindow)}}
	if err := c.getJSON(ctx, OpVideosList, "/videos", q, &videos); err != nil {
		return "", false, err
	}
	for _, v := range videos.Items {
		if slices.Contains(v.Snippet.Tags, tag) {
			return v.ID, true, nil
		}
	}
	return "", false, nil
}
