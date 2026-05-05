package runtime

import (
	"testing"

	"github.com/slack-go/slack"
)

func TestResolveReadUserSlackID_AboutMeUsesAuthor(t *testing.T) {
	bots := map[string]struct{}{"UJOANNE": {}}
	task := Task{
		RequestText:     "<@UJOANNE> what do you know about me",
		HumanUserID:     "UHUMAN",
		OwnerEmployeeID: "joanne",
	}
	got, err := resolveReadUserSlackID(task, bots)
	if err != nil {
		t.Fatal(err)
	}
	if got != "UHUMAN" {
		t.Fatalf("got %q want author UHUMAN", got)
	}
}

func TestResolveReadUserSlackID_ExplicitHumanMention(t *testing.T) {
	bots := map[string]struct{}{"UJOANNE": {}}
	task := Task{
		RequestText:     "<@UJOANNE> read-user for <@UOTHER>",
		HumanUserID:     "UHUMAN",
		OwnerEmployeeID: "joanne",
	}
	got, err := resolveReadUserSlackID(task, bots)
	if err != nil {
		t.Fatal(err)
	}
	if got != "UOTHER" {
		t.Fatalf("got %q want UOTHER", got)
	}
}

func TestSlackUserCardName(t *testing.T) {
	u := &slack.User{
		ID:   "UZ",
		Name: "grant",
		Profile: slack.UserProfile{
			RealName:    "Grant Example",
			DisplayName: "grant-e",
		},
	}
	if g := slackUserCardName(u); g != "Grant Example" {
		t.Fatalf("got %q", g)
	}
}
