package channelknowledgerefresh

import (
	"testing"

	"github.com/slack-go/slack"
)

func TestIsKnowledgeCronSelfNoise(t *testing.T) {
	t.Parallel()
	notify := "📝 All caught up on our chats! Company context updated."
	mUser := slack.Message{Msg: slack.Msg{User: "UJOANNE", Text: notify}}
	if !IsKnowledgeCronSelfNoise(mUser, "UJOANNE", "B01") {
		t.Fatal("expected noise when user_id matches and text is notify-shaped")
	}
	mBot := slack.Message{Msg: slack.Msg{BotID: "B01", Text: notify}}
	if !IsKnowledgeCronSelfNoise(mBot, "UJOANNE", "B01") {
		t.Fatal("expected noise when bot_id matches")
	}
	if IsKnowledgeCronSelfNoise(mUser, "UOTHER", "B01") {
		t.Fatal("should not classify another user's 📝 line as cron noise")
	}
	if IsKnowledgeCronSelfNoise(slack.Message{Msg: slack.Msg{User: "UJOANNE", Text: "human 📝 note"}}, "UJOANNE", "B01") {
		t.Fatal("should not strip non-notify lines without leading paper emoji")
	}
	if IsKnowledgeCronSelfNoise(slack.Message{Msg: slack.Msg{User: "UJOANNE", Text: "   "}}, "UJOANNE", "B01") {
		t.Fatal("empty after trim should not match")
	}
	failLine := "❗ Channel knowledge refresh failed for #humans: redis: broken"
	mFail := slack.Message{Msg: slack.Msg{User: "UJOANNE", Text: failLine}}
	if !IsKnowledgeCronSelfNoise(mFail, "UJOANNE", "B01") {
		t.Fatal("expected ❗ failure line from bot to be omitted from digest")
	}
}
