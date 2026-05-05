package channelknowledgerefresh

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/slack-go/slack"
)

func TestTailTruncateMarkdown(t *testing.T) {
	t.Parallel()
	s := strings.Repeat("あ", 50)
	out := TailTruncateMarkdown(s, 10)
	if !strings.HasPrefix(out, "…(truncated)") {
		t.Fatalf("expected truncation prefix: %q", out)
	}
	if utf8.RuneCountInString(out) <= 10 {
		t.Fatal("expected tail to exceed raw cap due to prefix")
	}
}

func TestFilterMessagesSinceSlackTS(t *testing.T) {
	t.Parallel()
	msgs := []slack.Message{
		{Msg: slack.Msg{Timestamp: "100.0", Text: "a"}},
		{Msg: slack.Msg{Timestamp: "200.0", Text: "b"}},
	}
	out := FilterMessagesSinceSlackTS(msgs, "150.0")
	if len(out) != 1 || out[0].Timestamp != "200.0" {
		t.Fatalf("got %+v", out)
	}
}

func TestHarvestStateFromBootstrap_historyWatermark(t *testing.T) {
	t.Parallel()
	hist := []slack.Message{
		{Msg: slack.Msg{Timestamp: "1.0", Text: "root"}},
		{Msg: slack.Msg{Timestamp: "1.1", Text: "r", ThreadTimestamp: "1.0"}},
	}
	merged := append([]slack.Message{}, hist...)
	merged = append(merged, slack.Message{Msg: slack.Msg{Timestamp: "1.2", Text: "deep", ThreadTimestamp: "1.0"}})
	st := harvestStateFromBootstrap(hist, merged)
	if st.HistoryWatermark != "1.1" {
		t.Fatalf("history watermark should ignore thread-only expansion for max on hist slice: got %q", st.HistoryWatermark)
	}
	if st.ThreadCursors["1.0"] != "1.2" {
		t.Fatalf("thread cursor max: got %q", st.ThreadCursors["1.0"])
	}
}
