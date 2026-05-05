package runtime

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/slack-go/slack"
)

var reReadUserMention = regexp.MustCompile(`<@([UW][A-Za-z0-9]+)>`)

// runReadUser posts a Slack Block Kit "card" for a workspace member using users.info.
func (e *Engine) runReadUser(ctx context.Context, task Task) (RenderPayload, error) {
	api := e.slackForEmployee(strings.TrimSpace(task.OwnerEmployeeID))
	if api == nil {
		return RenderPayload{}, fmt.Errorf("read-user: Slack API client not configured for employee %q", task.OwnerEmployeeID)
	}
	targetID, err := resolveReadUserSlackID(task, slackBotUserIDSetFromEnv())
	if err != nil {
		return RenderPayload{}, err
	}

	infoCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	u, err := api.GetUserInfoContext(infoCtx, targetID)
	if err != nil {
		return RenderPayload{}, fmt.Errorf("read-user: users.info: %w", err)
	}
	if u == nil {
		return RenderPayload{}, fmt.Errorf("read-user: empty user for %s", targetID)
	}

	blocks := buildReadUserCardBlocks(u)
	fallback := readUserFallbackText(u)
	return RenderPayload{
		OutputID:     fmt.Sprintf("%s-read-user", task.ID),
		FallbackText: fallback,
		FinalSummary: "read-user card posted",
		Transport:    "slack",
		BlockKit:     blocks,
	}, nil
}

func slackBotUserIDSetFromEnv() map[string]struct{} {
	out := map[string]struct{}{}
	add := func(s string) {
		s = strings.TrimSpace(s)
		if len(s) >= 9 && (s[0] == 'U' || s[0] == 'W') {
			out[strings.ToUpper(s)] = struct{}{}
		}
	}
	raw := strings.TrimSpace(os.Getenv("MULTIAGENT_BOT_USER_IDS"))
	if raw != "" {
		for _, part := range strings.Split(raw, ",") {
			part = strings.TrimSpace(part)
			idx := strings.Index(part, ":")
			if idx <= 0 {
				continue
			}
			add(part[idx+1:])
		}
	}
	for _, key := range []string{
		"ROSS_SLACK_BOT_ID", "TIM_SLACK_BOT_ID", "ALEX_SLACK_BOT_ID",
		"GARTH_SLACK_BOT_ID", "JOANNE_SLACK_BOT_ID", "ANNA_SLACK_BOT_ID",
	} {
		add(os.Getenv(key))
	}
	return out
}

func resolveReadUserSlackID(task Task, botIDs map[string]struct{}) (string, error) {
	text := strings.TrimSpace(task.RequestText)
	lower := strings.ToLower(text)

	var nonBotMentions []string
	for _, m := range reReadUserMention.FindAllStringSubmatch(text, -1) {
		if len(m) < 2 {
			continue
		}
		uid := strings.ToUpper(strings.TrimSpace(m[1]))
		if uid == "" {
			continue
		}
		if _, isBot := botIDs[uid]; isBot {
			continue
		}
		nonBotMentions = append(nonBotMentions, uid)
	}

	// "about me" / "about myself" → message author (ignores squad @mentions in the same line).
	if strings.Contains(lower, "about me") || strings.Contains(lower, "about myself") {
		u := strings.TrimSpace(task.HumanUserID)
		if u == "" {
			return "", fmt.Errorf("read-user: missing message author user id")
		}
		return u, nil
	}

	if len(nonBotMentions) == 1 {
		return nonBotMentions[0], nil
	}
	if len(nonBotMentions) > 1 {
		self := strings.ToUpper(strings.TrimSpace(task.HumanUserID))
		for _, c := range nonBotMentions {
			if c != self {
				return c, nil
			}
		}
		return nonBotMentions[0], nil
	}

	u := strings.TrimSpace(task.HumanUserID)
	if u == "" {
		return "", fmt.Errorf("read-user: missing target slack user id")
	}
	return u, nil
}

func slackUserCardName(u *slack.User) string {
	if u == nil {
		return ""
	}
	p := u.Profile
	return firstNonEmpty(
		strings.TrimSpace(p.RealName),
		strings.TrimSpace(u.RealName),
		strings.TrimSpace(p.DisplayName),
		strings.TrimSpace(u.Name),
	)
}

func pickProfileImageURL(p slack.UserProfile) string {
	for _, u := range []string{
		strings.TrimSpace(p.Image1024),
		strings.TrimSpace(p.ImageOriginal),
		strings.TrimSpace(p.Image512),
		strings.TrimSpace(p.Image192),
		strings.TrimSpace(p.Image72),
		strings.TrimSpace(p.Image48),
		strings.TrimSpace(p.Image32),
		strings.TrimSpace(p.Image24),
	} {
		if u != "" {
			return u
		}
	}
	return ""
}

func buildReadUserCardBlocks(u *slack.User) []slack.Block {
	name := slackUserCardName(u)
	if name == "" {
		name = u.ID
	}
	header := slack.NewHeaderBlock(slack.NewTextBlockObject("plain_text", "Workspace profile", false, false))

	var accessory *slack.Accessory
	if img := pickProfileImageURL(u.Profile); img != "" {
		accessory = slack.NewAccessory(slack.NewImageBlockElement(img, "Profile photo"))
	}
	titleLine := "*" + slackMrkdwnEscape(name) + "*"
	switch {
	case u.Deleted:
		titleLine += " · *Deactivated*"
	case u.IsBot:
		titleLine += " · *Bot*"
	default:
		titleLine += " · *Member*"
	}
	sub := slack.NewTextBlockObject("mrkdwn", titleLine, false, false)

	var top *slack.SectionBlock
	if accessory != nil {
		top = slack.NewSectionBlock(sub, nil, accessory)
	} else {
		top = slack.NewSectionBlock(sub, nil, nil)
	}

	fields := []*slack.TextBlockObject{
		slackFieldMrkdwn("Username", "@"+slackMrkdwnEscape(strings.TrimSpace(u.Name))),
		slackFieldMrkdwn("User ID", slackMrkdwnEscape(strings.TrimSpace(u.ID))),
	}
	if v := strings.TrimSpace(u.Profile.Title); v != "" {
		fields = append(fields, slackFieldMrkdwn("Title", v))
	}
	if v := strings.TrimSpace(u.Profile.Email); v != "" {
		fields = append(fields, slackFieldMrkdwn("Email", v))
	}
	if v := strings.TrimSpace(u.Profile.Phone); v != "" {
		fields = append(fields, slackFieldMrkdwn("Phone", v))
	}
	if v := strings.TrimSpace(u.TZ); v != "" {
		fields = append(fields, slackFieldMrkdwn("Time zone", v))
	}
	if v := strings.TrimSpace(u.Profile.StatusText); v != "" {
		fields = append(fields, slackFieldMrkdwn("Status", v))
	}

	detail := slack.NewSectionBlock(nil, fields, nil)
	ctx := slack.NewContextBlock("read_user_ctx",
		slack.NewTextBlockObject("mrkdwn", "Source: Slack `users.info` (workspace directory)", false, false))

	return []slack.Block{header, top, detail, ctx}
}

func slackFieldMrkdwn(label, value string) *slack.TextBlockObject {
	return slack.NewTextBlockObject("mrkdwn",
		"*"+label+"*\n"+slackMrkdwnEscape(strings.TrimSpace(value)),
		false, false,
	)
}

func slackMrkdwnEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}

func readUserFallbackText(u *slack.User) string {
	name := slackUserCardName(u)
	if name == "" {
		name = u.ID
	}
	return fmt.Sprintf("Profile: %s (%s)", name, u.ID)
}
