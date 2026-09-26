package bench

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

// RSSFromSamplesFile returns an RSS getter over a samples file the host
// writes while the benchmark runs (scripts/bench-image.sh polls `docker
// stats` for the ComfyUI container; no container here can, since none
// mounts docker.sock). Each line is "<unix_ms> <rss_bytes>". The file is
// re-read on every call because the sampler keeps appending to it.
func RSSFromSamplesFile(path string) func(from, to time.Time) (int64, bool) {
	return func(from, to time.Time) (int64, bool) {
		// The sampler polls about once a second; widen the window by one
		// interval on each side so a short case still gets a sample.
		lo, hi := from.Add(-2*time.Second).UnixMilli(), to.Add(2*time.Second).UnixMilli()
		f, err := os.Open(path)
		if err != nil {
			return 0, false
		}
		defer func() { _ = f.Close() }()
		var peak int64
		found := false
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			fields := strings.Fields(sc.Text())
			if len(fields) != 2 {
				continue
			}
			ts, err1 := strconv.ParseInt(fields[0], 10, 64)
			rss, err2 := strconv.ParseInt(fields[1], 10, 64)
			if err1 != nil || err2 != nil || ts < lo || ts > hi {
				continue
			}
			peak, found = max(peak, rss>>20), true
		}
		return peak, found
	}
}

// SwapUsedMB reads the VM's swap use from /proc/meminfo. Containers see
// the Docker VM's own meminfo, which is exactly the swap the gate caps.
func SwapUsedMB() (int64, error) {
	raw, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, err
	}
	var total, free int64
	var gotTotal, gotFree bool
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		v, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			continue
		}
		switch fields[0] {
		case "SwapTotal:":
			total, gotTotal = v, true
		case "SwapFree:":
			free, gotFree = v, true
		}
	}
	if !gotTotal || !gotFree {
		return 0, fmt.Errorf("bench: /proc/meminfo has no swap lines")
	}
	return (total - free) / 1024, nil
}

func float8(v float64) pgtype.Float8 {
	return pgtype.Float8{Float64: v, Valid: true}
}
