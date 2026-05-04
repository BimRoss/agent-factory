package slackthread

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/slack-go/slack"
)

func TestTrimByRunesTail(t *testing.T) {
	s := strings.Repeat("a", 5000) + "END"
	const max = 200 // large enough for omission prefix + tail
	out := trimByRunesTail(s, max)
	if !strings.HasPrefix(out, "…[") {
		t.Fatalf("expected truncation prefix, got %q", out)
	}
	if !strings.Contains(out, "END") {
		t.Fatalf("expected recent tail preserved")
	}
	if utf8.RuneCountInString(out) != max {
		t.Fatalf("expected length %d runes, got %d", max, utf8.RuneCountInString(out))
	}
}

func TestIsAssistantMessage(t *testing.T) {
	if !isAssistantMessage(slack.Message{Msg: slack.Msg{BotID: "B1"}}, "U1") {
		t.Fatal("bot_id => assistant")
	}
	if !isAssistantMessage(slack.Message{Msg: slack.Msg{User: "U9"}}, "U9") {
		t.Fatal("self user => assistant")
	}
	if isAssistantMessage(slack.Message{Msg: slack.Msg{User: "U2"}}, "U9") {
		t.Fatal("human not assistant")
	}
}

func TestSortMessagesOldestFirst(t *testing.T) {
	msgs := []slack.Message{
		{Msg: slack.Msg{Timestamp: "2.0"}},
		{Msg: slack.Msg{Timestamp: "1.0"}},
	}
	sortMessagesOldestFirst(msgs)
	if msgs[0].Timestamp != "1.0" || msgs[1].Timestamp != "2.0" {
		t.Fatalf("got %#v", msgs)
	}
}
