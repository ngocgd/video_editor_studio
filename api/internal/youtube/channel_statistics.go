package youtube

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ChannelStatistics is the channel's lifetime public counters.
type ChannelStatistics struct {
	ViewCount       int64
	SubscriberCount int64
	VideoCount      int64
	// SubscribersHidden: the owner hides the count, SubscriberCount is 0
	// and must be shown as unavailable, not as zero.
	SubscribersHidden bool
}

type channelStatsResponse struct {
	Items []struct {
		Statistics struct {
			ViewCount             string `json:"viewCount"`
			SubscriberCount       string `json:"subscriberCount"`
			HiddenSubscriberCount bool   `json:"hiddenSubscriberCount"`
			VideoCount            string `json:"videoCount"`
		} `json:"statistics"`
	} `json:"items"`
}

// MyChannelStatistics reads the authorized channel's counters
// (channels.list part=statistics, one Data API unit).
func (c *Client) MyChannelStatistics(ctx context.Context) (ChannelStatistics, error) {
	var r channelStatsResponse
	q := url.Values{"part": {"statistics"}, "mine": {"true"}}
	if err := c.getJSON(ctx, OpChannelsList, "/channels", q, &r); err != nil {
		return ChannelStatistics{}, err
	}
	if len(r.Items) == 0 {
		return ChannelStatistics{}, ErrNoChannel
	}
	s := r.Items[0].Statistics
	// The API sends counters as decimal strings; a malformed one reads 0.
	views, _ := strconv.ParseInt(s.ViewCount, 10, 64)
	subs, _ := strconv.ParseInt(s.SubscriberCount, 10, 64)
	vids, _ := strconv.ParseInt(s.VideoCount, 10, 64)
	return ChannelStatistics{ViewCount: views, SubscriberCount: subs, VideoCount: vids, SubscribersHidden: s.HiddenSubscriberCount}, nil
}

// VideoDetails is what analytics needs to track one video.
type VideoDetails struct {
	ID              string
	ChannelID       string
	Title           string
	PublishedAt     time.Time
	DurationSeconds int
}

type videoDetailsResponse struct {
	Items []struct {
		ID      string `json:"id"`
		Snippet struct {
			ChannelID   string    `json:"channelId"`
			Title       string    `json:"title"`
			PublishedAt time.Time `json:"publishedAt"`
		} `json:"snippet"`
		ContentDetails struct {
			Duration string `json:"duration"`
		} `json:"contentDetails"`
	} `json:"items"`
}

// maxVideoIDsPerCall is the videos.list id limit.
const maxVideoIDsPerCall = 50

// Videos reads title, owner channel, publish time and duration for up to
// 50 videos (videos.list, one Data API unit). Ids YouTube does not know,
// or that are private to another account, are simply absent.
func (c *Client) Videos(ctx context.Context, ids []string) ([]VideoDetails, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	if len(ids) > maxVideoIDsPerCall {
		return nil, fmt.Errorf("youtube: videos.list takes at most %d ids, got %d", maxVideoIDsPerCall, len(ids))
	}
	var r videoDetailsResponse
	q := url.Values{"part": {"snippet,contentDetails"}, "id": {strings.Join(ids, ",")}, "maxResults": {strconv.Itoa(maxVideoIDsPerCall)}}
	if err := c.getJSON(ctx, OpVideosList, "/videos", q, &r); err != nil {
		return nil, err
	}
	out := make([]VideoDetails, 0, len(r.Items))
	for _, it := range r.Items {
		out = append(out, VideoDetails{
			ID:              it.ID,
			ChannelID:       it.Snippet.ChannelID,
			Title:           it.Snippet.Title,
			PublishedAt:     it.Snippet.PublishedAt,
			DurationSeconds: ParseISODuration(it.ContentDetails.Duration),
		})
	}
	return out, nil
}

var isoDuration = regexp.MustCompile(`^P(?:(\d+)D)?(?:T(?:(\d+)H)?(?:(\d+)M)?(?:(\d+)S)?)?$`)

// ParseISODuration turns YouTube's ISO 8601 duration (e.g. "PT1H2M3S")
// into seconds; an unparseable value (live streams answer "P0D") is 0.
func ParseISODuration(s string) int {
	m := isoDuration.FindStringSubmatch(s)
	if m == nil {
		return 0
	}
	total := 0
	for i, mul := range []int{86400, 3600, 60, 1} {
		if m[i+1] != "" {
			n, _ := strconv.Atoi(m[i+1])
			total += n * mul
		}
	}
	return total
}
