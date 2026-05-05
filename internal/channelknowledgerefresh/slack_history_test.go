package channelknowledgerefresh

import (
	"errors"
	"fmt"
	"testing"

	"github.com/slack-go/slack"
)

func TestIsChannelThreadParent(t *testing.T) {
	cases := []struct {
		name string
		m    slack.Message
		want bool
	}{
		{"no replies", slack.Message{Msg: slack.Msg{ReplyCount: 0, Timestamp: "1.0"}}, false},
		{"parent root", slack.Message{Msg: slack.Msg{ReplyCount: 3, Timestamp: "1.0", ThreadTimestamp: ""}}, true},
		{"reply in thread", slack.Message{Msg: slack.Msg{ReplyCount: 0, Timestamp: "1.1", ThreadTimestamp: "1.0"}}, false},
		{"parent with thread_ts equals ts", slack.Message{Msg: slack.Msg{ReplyCount: 2, Timestamp: "1.0", ThreadTimestamp: "1.0"}}, true},
		{"deleted", slack.Message{Msg: slack.Msg{ReplyCount: 2, SubType: "message_deleted", Timestamp: "1.0"}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsChannelThreadParent(tc.m); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestIsSlackThreadNotFoundError(t *testing.T) {
	if isSlackThreadNotFoundError(nil) {
		t.Fatal("nil should be false")
	}
	if isSlackThreadNotFoundError(errors.New("other")) {
		t.Fatal("unrelated error should be false")
	}
	if !isSlackThreadNotFoundError(slack.SlackErrorResponse{Err: slackAPIErrorThreadNotFound}) {
		t.Fatal("SlackErrorResponse thread_not_found should be true")
	}
	if !isSlackThreadNotFoundError(fmt.Errorf("conversations.replies: %w", slack.SlackErrorResponse{Err: slackAPIErrorThreadNotFound})) {
		t.Fatal("wrapped SlackErrorResponse should be true")
	}
}
