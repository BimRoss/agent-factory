package channelknowledge

import (
	"strconv"
	"strings"
	"time"
)

// SlackTSCompare returns -1 if a<b, 0 if equal, 1 if a>b (numeric compare on unix.micro ts).
func SlackTSCompare(a, b string) int {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	switch {
	case a == b:
		return 0
	case a == "":
		return -1
	case b == "":
		return 1
	}
	fa, fb := SlackTSScore(a), SlackTSScore(b)
	switch {
	case fa < fb:
		return -1
	case fa > fb:
		return 1
	default:
		return 0
	}
}

// SlackTSMax returns the lexicographically greater Slack timestamp.
func SlackTSMax(a, b string) string {
	if SlackTSCompare(a, b) >= 0 {
		return strings.TrimSpace(a)
	}
	return strings.TrimSpace(b)
}

// SlackTSScore converts Slack "unix.micro" ts to a float64 for Redis ZSET scores.
func SlackTSScore(ts string) float64 {
	ts = strings.TrimSpace(ts)
	if ts == "" {
		return 0
	}
	parts := strings.SplitN(ts, ".", 2)
	whole, err := strconv.ParseFloat(parts[0], 64)
	if err != nil {
		return 0
	}
	if len(parts) < 2 {
		return whole
	}
	frac, err := strconv.ParseFloat("0."+parts[1], 64)
	if err != nil {
		return whole
	}
	return whole + frac
}

// SlackTSCutoffForAge returns a Slack-style ts string at least `secondsAgo` before now (floor on whole seconds).
func SlackTSCutoffForAge(secondsAgo int64) string {
	if secondsAgo < 0 {
		secondsAgo = 0
	}
	u := time.Now().Unix() - secondsAgo
	if u < 0 {
		u = 0
	}
	return strconv.FormatInt(u, 10) + ".000000"
}
