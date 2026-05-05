package runtime

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/slack-go/slack"
)

const (
	humansTermsAcceptIndexWaitTimeout = 20 * time.Second
	humansTermsAcceptRefreshTimeout   = 45 * time.Second
	humansBySlackPoll                 = 200 * time.Millisecond
)

// Mirrors slack-orchestrator routing.{UpdateTermsIntentText,TermsDeclineIntentText}; keep aligned.
func updateTermsIntentText(text string) bool {
	lower := trimTermsIntentLower(text)
	if lower == "" {
		return false
	}
	if strings.Contains(lower, "update-terms") || strings.Contains(lower, "update terms") || strings.Contains(lower, "update_terms") {
		return true
	}
	if strings.Contains(lower, "terms and conditions") {
		return true
	}
	if strings.Contains(lower, "terms of use") || strings.Contains(lower, "terms of service") {
		return true
	}
	strip := lower
	for _, ch := range []string{"*", "_", "`"} {
		strip = strings.ReplaceAll(strip, ch, "")
	}
	strip = strings.TrimSpace(strip)
	if strip == "i agree" || strip == "i accept" ||
		strings.HasPrefix(strip, "yes i agree") || strings.HasPrefix(strip, "yes, i agree") ||
		strings.HasPrefix(strip, "yes i accept") || strings.HasPrefix(strip, "yes, i accept") {
		return true
	}
	if strings.Contains(lower, "the terms") {
		if strings.Contains(lower, "agree") || strings.Contains(lower, "accept") || strings.Contains(lower, "acknowledge") {
			return true
		}
		if strings.Contains(lower, "show me") || strings.Contains(lower, "show the") || strings.Contains(lower, "read the") ||
			strings.Contains(lower, "view the") || strings.Contains(lower, "see the") || strings.Contains(lower, "review the") {
			return true
		}
	}
	return false
}

func termsDeclineIntentText(text string) bool {
	lower := trimTermsIntentLower(text)
	if lower == "" {
		return false
	}
	strip := lower
	for _, ch := range []string{"*", "_", "`"} {
		strip = strings.ReplaceAll(strip, ch, "")
	}
	strip = strings.TrimSpace(strip)
	if strings.Contains(strip, "do not agree") {
		return true
	}
	if strings.Contains(strip, "don't agree") || strings.Contains(strip, "dont agree") {
		return true
	}
	if strings.Contains(strip, "i disagree") {
		return true
	}
	return false
}

func trimTermsIntentLower(text string) string {
	return strings.TrimSpace(strings.ToLower(strings.TrimSpace(text)))
}

func shouldRecordImmediateTermsAccept(text string) bool {
	lower := trimTermsIntentLower(text)
	for _, ch := range []string{"*", "_", "`"} {
		lower = strings.ReplaceAll(lower, ch, "")
	}
	lower = strings.TrimSpace(lower)
	if lower == "i agree" || lower == "i accept" ||
		strings.HasPrefix(lower, "yes i agree") || strings.HasPrefix(lower, "yes, i agree") ||
		strings.HasPrefix(lower, "yes i accept") || strings.HasPrefix(lower, "yes, i accept") {
		return true
	}
	return strings.Contains(lower, "agree") && strings.Contains(lower, "terms")
}

func (e *Engine) runUpdateTerms(ctx context.Context, task Task) (RenderPayload, error) {
	raw := strings.TrimSpace(task.RequestText)

	if termsDeclineIntentText(raw) {
		redisURL := strings.TrimSpace(os.Getenv("REDIS_URL"))
		if redisURL != "" && strings.TrimSpace(task.HumanUserID) != "" {
			client, cerr := redisOpen(redisURL)
			if cerr == nil {
				defer client.Close()
				_ = ClearHumansTermsAccepted(ctx, client, "", "", task.HumanUserID)
			}
		}
		joanne := joanneWorkspaceMention()
		return RenderPayload{
			OutputID:     fmt.Sprintf("%s-update-terms-decline", task.ID),
			FallbackText: humansTermsRejectedText(joanne),
			FinalSummary: "terms declined",
			Transport:    "slack",
		}, nil
	}

	if shouldRecordImmediateTermsAccept(raw) {
		msgTS := firstNonEmpty(task.MessageTS, task.ThreadTS)
		if msgTS == "" {
			msgTS = time.Now().UTC().Format(time.RFC3339Nano)
		}
		var slackAPI *slack.Client
		if e.slackForEmployee != nil {
			slackAPI = e.slackForEmployee(task.OwnerEmployeeID)
		}
		if err := recordHumansTermsAcceptedWithRetry(ctx, slackAPI, task.HumanUserID, msgTS); err != nil {
			return RenderPayload{}, fmt.Errorf("record terms acceptance: %w", err)
		}
		epilogue := ""
		if e.slackForEmployee != nil {
			if api := e.slackForEmployee(task.OwnerEmployeeID); api != nil {
				if line, cerr := RunCreateCompanyAfterTermsAccept(ctx, api, task.HumanUserID); cerr != nil {
					log.Printf("update_terms: auto create-company user=%s err=%v", task.HumanUserID, cerr)
				} else {
					epilogue = line
				}
			}
		}
		return RenderPayload{
			OutputID:     fmt.Sprintf("%s-update-terms-ok", task.ID),
			FallbackText: FormatHumansTermsAcceptThankYou(epilogue),
			FinalSummary: "terms accepted",
			Transport:    "slack",
		}, nil
	}

	ch := strings.TrimSpace(task.ChannelID)
	u := strings.TrimSpace(task.HumanUserID)
	anchor := TermsToolThreadAnchor(task.ThreadTS, task.MessageTS)
	summary := HumansTermsThreadSummaryMrkdwn(makeacompanyWebOriginForTerms())
	blocks := BuildTermsAcceptanceBlocksWithSummary(ch, u, anchor, summary)
	return RenderPayload{
		OutputID:     fmt.Sprintf("%s-update-terms-prompt", task.ID),
		FallbackText: "Terms of Use",
		FinalSummary: "terms prompt posted",
		Transport:    "slack",
		BlockKit:     blocks,
		TermsSkillPending: &TermsSkillPendingAnchor{
			ChannelID:     ch,
			RequestUserID: u,
			ThreadAnchor:  anchor,
		},
	}, nil
}

func joanneWorkspaceMention() string {
	raw := strings.TrimSpace(os.Getenv("MULTIAGENT_BOT_USER_IDS"))
	for _, part := range strings.Split(raw, ",") {
		pair := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(pair) != 2 {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(pair[0]), "joanne") {
			id := strings.TrimSpace(pair[1])
			if id != "" {
				return "<@" + id + ">"
			}
		}
	}
	if id := strings.TrimSpace(os.Getenv("JOANNE_SLACK_BOT_USER_ID")); id != "" {
		return "<@" + id + ">"
	}
	return "@Joanne"
}

func redisOpen(redisURL string) (*redis.Client, error) {
	opts, err := redis.ParseURL(strings.TrimSpace(redisURL))
	if err != nil {
		return nil, fmt.Errorf("redis parse url: %w", err)
	}
	return redis.NewClient(opts), nil
}

// FormatHumansTermsAcceptThankYou is the Slack mrkdwn body after recording terms acceptance.
// Optional createdEpilogueMarkdown should be Slack mrkdwn (e.g. companyPostCreateCreatedMrkdwn output).
func FormatHumansTermsAcceptThankYou(createdEpilogueMarkdown string) string {
	var b strings.Builder
	b.WriteString("Thank you! Your agreement is recorded.")
	if cre := strings.TrimSpace(createdEpilogueMarkdown); cre != "" {
		b.WriteString("\n\n")
		b.WriteString(cre)
	}
	b.WriteString("\n\nWhen you're ready, say hi in *#onboarding* or ask what I can help with.")
	b.WriteString("\n\nJoin *#ideas* to help us evolve the product during this testing period!")
	return b.String()
}

func humansOnboardingThankYouText() string {
	return FormatHumansTermsAcceptThankYou("")
}

func humansTermsRejectedText(joanneMention string) string {
	j := strings.TrimSpace(joanneMention)
	if j == "" {
		j = "@Joanne"
	}
	return fmt.Sprintf(
		"No problem! Whenever you're ready, you can ask me to show you the *terms* again.\n\n*Example:* %s show me the terms.\n\nWithout agreeing, we can't onboard you onto the squad tools. Any prior agreement recorded for this workspace is withdrawn until you agree again.",
		j,
	)
}

func recordHumansTermsAcceptedWithRetry(ctx context.Context, api *slack.Client, slackUserID, messageTS string) error {
	redisURL := strings.TrimSpace(os.Getenv("REDIS_URL"))
	if redisURL == "" {
		return fmt.Errorf("REDIS_URL is not configured")
	}
	client, err := redisOpen(redisURL)
	if err != nil {
		return err
	}
	defer client.Close()
	slackUserID = strings.TrimSpace(slackUserID)
	if slackUserID == "" {
		return fmt.Errorf("missing slack user id")
	}
	if err := RecordHumansTermsAccepted(ctx, client, "", "", slackUserID, messageTS); err == nil {
		return nil
	}

	// Single-user path: users.info often exposes email before the workspace snapshot catches up.
	if api != nil {
		_ = tryUpsertMakeacompanySlackIndexFromBotUserInfo(ctx, api, client, slackUserID)
		if err := RecordHumansTermsAccepted(ctx, client, "", "", slackUserID, messageTS); err == nil {
			return nil
		}
	}

	refreshURL := strings.TrimSpace(os.Getenv("SLACK_USERS_SNAPSHOT_REFRESH_URL"))
	token := strings.TrimSpace(os.Getenv("BACKEND_INTERNAL_SERVICE_TOKEN"))
	if refreshURL != "" && token != "" {
		rctx, cancel := context.WithTimeout(ctx, humansTermsAcceptRefreshTimeout)
		_ = postSlackUsersSnapshotRefresh(rctx, refreshURL, token)
		cancel()
	}

	waitCtx, cancelWait := context.WithTimeout(context.Background(), humansTermsAcceptIndexWaitTimeout)
	defer cancelWait()
	_ = WaitForMakeacompanyBySlackIndex(waitCtx, client, slackUserID, humansBySlackPoll)
	return RecordHumansTermsAccepted(ctx, client, "", "", slackUserID, messageTS)
}
