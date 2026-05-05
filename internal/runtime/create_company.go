package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/slack-go/slack"
)

// Redis keys and pub/sub stay aligned with the agent-factory + makeacompany stack.
const (
	defaultCompanyChannelsRedisKey       = "agent-factory:company_channels"
	defaultCompanyChannelOnboardingQueue = "agent-factory:company_channel_onboarding_queue"
	companyChannelsInvalidateChannel     = "agent-factory:company_channels:invalidate"
	companyOnboardingDedupeKeyPrefix     = "agent-factory:onboarding_enqueued:"
	companyOnboardingPostedKeyPrefix     = "agent-factory:onboarding_posted:"
	companyOnboardingStateKeyPrefix      = "agent-factory:company_onboarding_state"
	companyOnboardingDedupeTTL           = 8 * 24 * time.Hour
	companyOnboardingStateTTL            = 90 * 24 * time.Hour
	companyOnboardingSourceAgentFactory  = "agent-factory-create-company"
	companyOnboardingSourceInviteHook    = "agent-factory-joanne-invited"
	companyOnboardingSourceAfterTerms    = "agent-factory-after-terms-accept"
	defaultInviteFreshWindow             = 2 * time.Hour
)

var (
	reSlackMention        = regexp.MustCompile(`<@([UW][A-Z0-9]+)>`)
	reSlackChannelRef     = regexp.MustCompile(`<#([A-Z0-9]+)(?:\|[^>]+)?>`)
	reSlackChannelNameTok = regexp.MustCompile(`(?i)(?:channel(?:\s+name)?\s*[:=]\s*|#)([a-z0-9._-]{1,120})`)
	reChQuoted            = regexp.MustCompile(`"([^"]{1,200})"`)
	reChNamedCalled       = regexp.MustCompile(`(?i)\b(?:named|called)\s+(.+)$`)
	reChIs                = regexp.MustCompile(`(?i)\bchannel\s+(?:is|will\s+be)\s+(.+)$`)
	reNameIsInMessage     = regexp.MustCompile(`(?i)\b(?:the\s+)?(?:company\s+)?name\s+is\s+(.+)$`)
	reBareSlugLine        = regexp.MustCompile(`(?i)^[a-z0-9][a-z0-9._-]{0,79}$`)
	reSlackMemberUserID   = regexp.MustCompile(`(?i)^[uw][a-z0-9_]{3,}$`)
)

type companyChannelRuntime struct {
	CompanySlug                string   `json:"company_slug"`
	ChannelID                  string   `json:"channel_id"`
	DisplayName                string   `json:"display_name,omitempty"`
	OwnerIDs                   []string `json:"owner_ids,omitempty"`
	ThreadsEnabled             bool     `json:"threads_enabled"`
	GeneralAutoReactionEnabled bool     `json:"general_auto_reaction_enabled"`
	GeneralResponsesMuted      bool     `json:"general_responses_muted,omitempty"`
	OutOfOfficeEnabled         bool     `json:"out_of_office_enabled"`
}

type companyOnboardingQueuePayload struct {
	ChannelID    string   `json:"channel_id"`
	Source       string   `json:"source,omitempty"`
	OwnerUserIDs []string `json:"owner_user_ids,omitempty"`
}

type companyOnboardingState struct {
	Phase string `json:"phase"`
}

func (e *Engine) runCreateCompany(ctx context.Context, task Task) (RenderPayload, error) {
	if e == nil || e.slackForEmployee == nil {
		return RenderPayload{}, fmt.Errorf("create-company: slack client resolver not configured")
	}
	api := e.slackForEmployee(task.OwnerEmployeeID)
	if api == nil {
		return RenderPayload{}, fmt.Errorf("create-company: no Slack API client for employee %q", task.OwnerEmployeeID)
	}

	req := parseCreateCompanyRequest(task)
	if strings.TrimSpace(req.ChannelSlug) == "" {
		return RenderPayload{}, fmt.Errorf("create-company: could not infer company/channel name from message (need e.g. \"called legendz\" or a bare slug)")
	}
	humanOwners := dedupeStableUserIDs(req.FounderUserIDs)
	if len(humanOwners) == 0 {
		return RenderPayload{}, fmt.Errorf("create-company: missing founder Slack user id")
	}

	channelObj, err := api.CreateConversationContext(ctx, slack.CreateConversationParams{
		ChannelName: req.ChannelSlug,
		IsPrivate:   true,
	})
	if err != nil {
		return RenderPayload{}, fmt.Errorf("create-company: conversations.create: %w", err)
	}
	cid := strings.TrimSpace(channelObj.ID)
	if err := finalizeNewPrivateCompany(ctx, api, channelObj, humanOwners, ""); err != nil {
		return RenderPayload{}, fmt.Errorf("create-company: %w", err)
	}

	fallback := companyPostCreateCreatedMrkdwn(cid, channelObj.Name)
	return RenderPayload{
		OutputID:     fmt.Sprintf("%s-create-company", task.ID),
		FallbackText: fallback,
		FinalSummary: "create-company completed",
		Transport:    "slack",
	}, nil
}

// finalizeNewPrivateCompany invites orchestrator + configured squad bots, registers the channel in Redis,
// enqueues onboarding, and posts the standard kickoff (same as create-company after conversations.create).
func finalizeNewPrivateCompany(ctx context.Context, api *slack.Client, channelObj *slack.Channel, humanOwners []string, onboardingSource string) error {
	if api == nil || channelObj == nil {
		return fmt.Errorf("finalize company: missing api or channel")
	}
	cid := strings.TrimSpace(channelObj.ID)
	if cid == "" {
		return fmt.Errorf("finalize company: empty channel id")
	}
	owners := dedupeStableUserIDs(humanOwners)
	cohort := expandCompanyInviteCohort(owners)
	if err := inviteUsersInBatches(ctx, api, cid, cohort, 30); err != nil {
		return fmt.Errorf("invite users: %w", err)
	}
	chName := strings.TrimSpace(channelObj.Name)
	if err := persistNewCompanyChannel(ctx, cid, chName, owners); err != nil {
		log.Printf("create-company: redis upsert: %v", err)
	}
	src := strings.TrimSpace(onboardingSource)
	if src == "" {
		src = companyOnboardingSourceAgentFactory
	}
	enqueueCompanyChannelOnboardingWithSource(ctx, cid, owners, src)
	if err := postCompanyOnboardingKickoff(ctx, api, cid, chName, owners); err != nil {
		log.Printf("create-company onboarding: post kickoff channel_id=%s err=%v", cid, err)
	}
	return nil
}

// RunCreateCompanyAfterTermsAccept provisions a private company channel named <first>-llc (then <first>-llc-1, …
// if the name is taken), invites orchestrator and squad bots, then runs the same onboarding kickoff as create-company.
// Returns Slack mrkdwn for the terms thank-you thread (Created line only — no dashboard link).
func RunCreateCompanyAfterTermsAccept(ctx context.Context, api *slack.Client, founderSlackUserID string) (epilogue string, err error) {
	if api == nil {
		return "", fmt.Errorf("terms auto create-company: missing slack client")
	}
	owner, ok := canonicalSlackUserIDForMention(founderSlackUserID)
	if !ok {
		return "", fmt.Errorf("terms auto create-company: invalid founder slack user id")
	}
	humanOwners := []string{owner}
	baseSlug := termsAutoCompanyBaseSlug(ctx, api, owner)
	channelObj, err := createPrivateCompanyChannelWithSlugBackoff(ctx, api, baseSlug)
	if err != nil {
		return "", err
	}
	if err := finalizeNewPrivateCompany(ctx, api, channelObj, humanOwners, companyOnboardingSourceAfterTerms); err != nil {
		return "", err
	}
	cid := strings.TrimSpace(channelObj.ID)
	cname := strings.TrimSpace(channelObj.Name)
	return companyPostCreateCreatedMrkdwn(cid, cname), nil
}

func termsAutoCompanyBaseSlug(ctx context.Context, api *slack.Client, founderUserID string) string {
	tok := slackUserFirstChannelToken(ctx, api, founderUserID)
	if tok == "" {
		tok = "company"
	}
	return normalizeSlackChannelName(tok + "-llc")
}

func slackUserFirstChannelToken(ctx context.Context, api *slack.Client, userID string) string {
	id, ok := canonicalSlackUserIDForMention(userID)
	if !ok || api == nil {
		return ""
	}
	infoCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	u, err := api.GetUserInfoContext(infoCtx, id)
	if err != nil || u == nil || u.IsBot {
		return ""
	}
	full := firstNonEmptyEnvString(
		strings.TrimSpace(u.Profile.RealName),
		strings.TrimSpace(u.RealName),
		strings.TrimSpace(u.Profile.DisplayName),
		strings.TrimSpace(u.Name),
	)
	if full == "" {
		return ""
	}
	fields := strings.Fields(full)
	if len(fields) == 0 {
		return ""
	}
	return normalizeSlackChannelName(strings.TrimSpace(fields[0]))
}

func createPrivateCompanyChannelWithSlugBackoff(ctx context.Context, api *slack.Client, baseSlug string) (*slack.Channel, error) {
	baseSlug = strings.TrimSpace(baseSlug)
	if baseSlug == "" {
		baseSlug = "company-llc"
	}
	const maxAttempts = 50
	for attempt := 0; attempt < maxAttempts; attempt++ {
		name := channelSlugWithNumericSuffix(baseSlug, attempt)
		name = truncateSlackConversationName(name)
		channelObj, err := api.CreateConversationContext(ctx, slack.CreateConversationParams{
			ChannelName: name,
			IsPrivate:   true,
		})
		if err == nil && channelObj != nil {
			return channelObj, nil
		}
		var slackErr slack.SlackErrorResponse
		if errors.As(err, &slackErr) && slackErr.Err == "name_taken" {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("terms auto create-company: conversations.create: %w", err)
		}
	}
	return nil, fmt.Errorf("terms auto create-company: exhausted channel name attempts for base %q", baseSlug)
}

func channelSlugWithNumericSuffix(base string, attempt int) string {
	if attempt == 0 {
		return base
	}
	return base + "-" + strconv.Itoa(attempt)
}

// Slack restricts conversation names (see conversations.create).
func truncateSlackConversationName(s string) string {
	const maxLen = 80
	s = strings.Trim(s, "-")
	if len(s) <= maxLen {
		return s
	}
	out := strings.Trim(strings.TrimRight(s[:maxLen], "-"), "-")
	if out == "" {
		return "ch"
	}
	return out
}

type createCompanyRequest struct {
	ChannelSlug    string
	FounderUserIDs []string
}

func parseCreateCompanyRequest(task Task) createCompanyRequest {
	raw := strings.TrimSpace(task.RequestText)
	// Strip this process's bot mention if present (same behavior as the Slack write path).
	// We do not have bot id in Task; rely on name extraction from remaining text.
	if raw == "" {
		return createCompanyRequest{}
	}
	slug := extractCreateCompanyChannelName(raw)
	if slug == "" {
		// Bare single-line slug follow-up
		if reBareSlugLine.MatchString(raw) {
			slug = normalizeSlackChannelName(raw)
		}
	}
	mentions := extractSlackMentionUserIDs(raw)
	knownBots := loadKnownBotUserIDs()
	var humanMentions []string
	for _, m := range mentions {
		if knownBots[strings.ToUpper(m)] {
			continue
		}
		humanMentions = append(humanMentions, m)
	}
	var founders []string
	author := strings.TrimSpace(task.HumanUserID)
	if author != "" {
		if c, ok := canonicalSlackUserIDForMention(author); ok {
			founders = append(founders, c)
		}
	}
	for _, m := range humanMentions {
		if c, ok := canonicalSlackUserIDForMention(m); ok {
			founders = append(founders, c)
		}
	}
	return createCompanyRequest{
		ChannelSlug:    normalizeSlackChannelName(slug),
		FounderUserIDs: dedupeStableUserIDs(founders),
	}
}

func loadKnownBotUserIDs() map[string]bool {
	out := map[string]bool{}
	if o := firstNonEmptyEnv("SLACK_ORCHESTRATOR_BOT_USER_ID", "ORCHESTRATOR_BOT_USER_ID"); o != "" {
		if c, ok := canonicalSlackUserIDForMention(o); ok {
			out[c] = true
		}
	}
	for _, part := range strings.Split(os.Getenv("MULTIAGENT_BOT_USER_IDS"), ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		pair := strings.SplitN(part, "=", 2)
		if len(pair) != 2 {
			continue
		}
		uid := strings.TrimSpace(pair[1])
		if c, ok := canonicalSlackUserIDForMention(uid); ok {
			out[c] = true
		}
	}
	return out
}

func extractSlackMentionUserIDs(raw string) []string {
	var out []string
	for _, m := range reSlackMention.FindAllStringSubmatch(raw, -1) {
		if len(m) < 2 {
			continue
		}
		out = append(out, strings.TrimSpace(m[1]))
	}
	return out
}

func extractCreateCompanyChannelName(raw string) string {
	if match := reSlackChannelNameTok.FindStringSubmatch(raw); len(match) >= 2 {
		return normalizeSlackChannelName(strings.TrimSpace(match[1]))
	}
	if match := reSlackChannelRef.FindStringSubmatch(raw); len(match) >= 2 {
		return ""
	}
	if match := reChQuoted.FindStringSubmatch(raw); len(match) >= 2 {
		if s := trimCompanyNameTail(strings.TrimSpace(match[1])); s != "" {
			return normalizeSlackChannelName(s)
		}
	}
	if match := reChNamedCalled.FindStringSubmatch(raw); len(match) >= 2 {
		if s := trimCompanyNameTail(match[1]); s != "" {
			return normalizeSlackChannelName(s)
		}
	}
	if match := reChIs.FindStringSubmatch(raw); len(match) >= 2 {
		if s := trimCompanyNameTail(match[1]); s != "" {
			return normalizeSlackChannelName(s)
		}
	}
	if match := reNameIsInMessage.FindStringSubmatch(raw); len(match) >= 2 {
		if s := trimCompanyNameTail(match[1]); s != "" {
			return normalizeSlackChannelName(s)
		}
	}
	return ""
}

func trimCompanyNameTail(s string) string {
	s = strings.TrimSpace(s)
	// Stop before roster / invite tails.
	cutPatterns := []*regexp.Regexp{
		regexp.MustCompile(`(?i)\s+with\s+cofounders?\b`),
		regexp.MustCompile(`(?i)\s+and\s+me(?:\s+and\s+<@[UW][A-Za-z0-9]+>|\s+are\s+(?:cofounders?|founders?)\b)`),
		regexp.MustCompile(`(?i)\s+with\s+me\b`),
		regexp.MustCompile(`(?i)\s+with\s+<@[UW][A-Za-z0-9]+>`),
		regexp.MustCompile(`(?i)\s+and\s+<@[UW][A-Za-z0-9]+>`),
		regexp.MustCompile(`(?i)\s+and\s+(?:invite|add)\b`),
	}
	for _, re := range cutPatterns {
		if loc := re.FindStringIndex(s); loc != nil {
			s = strings.TrimSpace(s[:loc[0]])
		}
	}
	s = strings.Trim(s, `"'`+".!?")
	return strings.TrimSpace(s)
}

func normalizeSlackChannelName(raw string) string {
	name := strings.ToLower(strings.TrimSpace(raw))
	name = strings.TrimPrefix(name, "#")
	name = strings.ReplaceAll(name, ".", "-")
	name = strings.ReplaceAll(name, "_", "-")
	hyph := regexp.MustCompile(`[^a-z0-9-]+`)
	name = hyph.ReplaceAllString(name, "-")
	collapse := regexp.MustCompile(`-+`)
	name = collapse.ReplaceAllString(name, "-")
	name = strings.Trim(name, "-")
	if len(name) > 80 {
		name = name[:80]
		name = collapse.ReplaceAllString(name, "-")
		name = strings.Trim(name, "-")
	}
	return name
}

func dedupeStableUserIDs(ids []string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, id := range ids {
		u := strings.TrimSpace(id)
		if u == "" {
			continue
		}
		if _, ok := seen[u]; ok {
			continue
		}
		seen[u] = struct{}{}
		out = append(out, u)
	}
	return out
}

func canonicalSlackUserIDForMention(s string) (string, bool) {
	t := strings.TrimSpace(s)
	if t == "" || !reSlackMemberUserID.MatchString(t) {
		return "", false
	}
	return strings.ToUpper(t), true
}

func expandCompanyInviteCohort(humanOwners []string) []string {
	cohort := append([]string(nil), humanOwners...)
	emp := strings.ToLower(strings.TrimSpace(os.Getenv("EMPLOYEE_ID")))
	var extra []string
	if o := firstNonEmptyEnv("SLACK_ORCHESTRATOR_BOT_USER_ID", "ORCHESTRATOR_BOT_USER_ID"); o != "" {
		if c, ok := canonicalSlackUserIDForMention(o); ok {
			extra = append(extra, c)
		}
	}
	order := parseMultiagentOrderEnv()
	botMap := parseMultiagentBotMap()
	if len(order) > 0 {
		for _, key := range order {
			key = strings.ToLower(strings.TrimSpace(key))
			if key == "" || key == emp {
				continue
			}
			if uid := strings.TrimSpace(botMap[key]); uid != "" {
				if c, ok := canonicalSlackUserIDForMention(uid); ok {
					extra = append(extra, c)
				}
			}
		}
	} else {
		keys := make([]string, 0, len(botMap))
		for k := range botMap {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, key := range keys {
			key = strings.ToLower(strings.TrimSpace(key))
			if key == "" || key == emp {
				continue
			}
			if uid := strings.TrimSpace(botMap[key]); uid != "" {
				if c, ok := canonicalSlackUserIDForMention(uid); ok {
					extra = append(extra, c)
				}
			}
		}
	}
	if len(extra) == 0 {
		return dedupeStableUserIDs(cohort)
	}
	return dedupeStableUserIDs(append(cohort, extra...))
}

func firstNonEmptyEnv(keys ...string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
	}
	return ""
}

func parseMultiagentOrderEnv() []string {
	raw := strings.TrimSpace(os.Getenv("MULTIAGENT_ORDER"))
	if raw == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(raw, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func parseMultiagentBotMap() map[string]string {
	out := map[string]string{}
	for _, part := range strings.Split(os.Getenv("MULTIAGENT_BOT_USER_IDS"), ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		pair := strings.SplitN(part, "=", 2)
		if len(pair) != 2 {
			continue
		}
		k := strings.ToLower(strings.TrimSpace(pair[0]))
		v := strings.TrimSpace(pair[1])
		if k != "" && v != "" {
			out[k] = v
		}
	}
	return out
}

func inviteUsersInBatches(ctx context.Context, api *slack.Client, channelID string, userIDs []string, batchSize int) error {
	if api == nil || strings.TrimSpace(channelID) == "" {
		return fmt.Errorf("invite: missing api or channel")
	}
	if batchSize < 1 {
		batchSize = 30
	}
	var chunk []string
	flush := func() error {
		if len(chunk) == 0 {
			return nil
		}
		_, err := api.InviteUsersToConversationContext(ctx, channelID, chunk...)
		chunk = chunk[:0]
		return err
	}
	for _, uid := range userIDs {
		u := strings.TrimSpace(uid)
		if u == "" {
			continue
		}
		chunk = append(chunk, u)
		if len(chunk) >= batchSize {
			if err := flush(); err != nil {
				return err
			}
		}
	}
	return flush()
}

func companyRuntimeRecordForNewChannel(channelID, channelName string, ownerUserIDs []string) companyChannelRuntime {
	slug := normalizeSlackChannelName(channelName)
	display := slug
	if display != "" {
		display = "#" + display
	} else {
		display = "#" + strings.TrimSpace(strings.TrimPrefix(channelName, "#"))
	}
	return companyChannelRuntime{
		CompanySlug:                slug,
		ChannelID:                  strings.TrimSpace(channelID),
		DisplayName:                display,
		OwnerIDs:                   dedupeStableUserIDs(ownerUserIDs),
		ThreadsEnabled:             true,
		GeneralAutoReactionEnabled: true,
		OutOfOfficeEnabled:         false,
	}
}

func persistNewCompanyChannel(ctx context.Context, channelID, channelName string, ownerUserIDs []string) error {
	redisURL := strings.TrimSpace(os.Getenv("REDIS_URL"))
	if redisURL == "" {
		return fmt.Errorf("REDIS_URL not set")
	}
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		return fmt.Errorf("redis url: %w", err)
	}
	client := redis.NewClient(opts)
	defer func() { _ = client.Close() }()

	key := strings.TrimSpace(os.Getenv("COMPANY_CHANNELS_REDIS_KEY"))
	if key == "" {
		key = defaultCompanyChannelsRedisKey
	}
	e := companyRuntimeRecordForNewChannel(channelID, channelName, ownerUserIDs)
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	cid := strings.TrimSpace(e.ChannelID)
	if err := client.HSet(ctx, key, cid, string(b)).Err(); err != nil {
		return err
	}
	payload := cid
	if payload == "" {
		payload = "*"
	}
	_ = client.Publish(ctx, companyChannelsInvalidateChannel, payload).Err()
	return nil
}

func enqueueCompanyChannelOnboarding(ctx context.Context, newChannelID string, ownerUserIDs []string) {
	enqueueCompanyChannelOnboardingWithSource(ctx, newChannelID, ownerUserIDs, companyOnboardingSourceAgentFactory)
}

// HandleJoanneInvitedCompanyChannel persists + enqueues onboarding when Joanne is invited
// to a private company channel (member_joined_channel path routed from slack-orchestrator).
func HandleJoanneInvitedCompanyChannel(ctx context.Context, api *slack.Client, channelID, inviterUserID string) error {
	if api == nil {
		return fmt.Errorf("joanne invite onboarding: missing slack client")
	}
	ch := strings.TrimSpace(channelID)
	if ch == "" {
		return fmt.Errorf("joanne invite onboarding: missing channel_id")
	}
	infoCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	chInfo, err := api.GetConversationInfoContext(infoCtx, &slack.GetConversationInfoInput{ChannelID: ch})
	if err != nil {
		return fmt.Errorf("joanne invite onboarding: conversations.info: %w", err)
	}
	if chInfo == nil {
		return fmt.Errorf("joanne invite onboarding: conversations.info: nil conversation")
	}
	privateLike := (chInfo.IsPrivate || chInfo.IsGroup) && !chInfo.IsIM && !chInfo.IsMpIM
	if !privateLike {
		return nil
	}
	chName := strings.TrimSpace(chInfo.Name)
	if chName == "" {
		chName = strings.TrimSpace(chInfo.NameNormalized)
	}
	owners := filterHumanSlackUserIDs(ctx, api, ownerCandidatesFromInvite(inviterUserID, strings.TrimSpace(chInfo.Creator)))
	if len(owners) == 0 {
		owners = ownerFallbackFromChannelMembers(ctx, api, ch)
	}
	if err := persistNewCompanyChannel(ctx, ch, chName, owners); err != nil {
		return fmt.Errorf("joanne invite onboarding: persist: %w", err)
	}
	// Safety: when Redis is fresh, member_joined_channel can replay Joanne's existing private-channel memberships.
	// Treat older channels as import/backfill only; do not post onboarding copy into already-active rooms.
	if !inviteTriggeredOnboardingEligible(chInfo) {
		log.Printf("joanne invite onboarding: import_only channel=%s source=%s created=%d", ch, companyOnboardingSourceInviteHook, chInfo.Created)
		return nil
	}
	enqueueCompanyChannelOnboardingWithSource(ctx, ch, owners, companyOnboardingSourceInviteHook)
	if err := postCompanyOnboardingKickoff(ctx, api, ch, chName, owners); err != nil {
		log.Printf("joanne invite onboarding: post kickoff channel_id=%s err=%v", ch, err)
	}
	return nil
}

func inviteTriggeredOnboardingEligible(chInfo *slack.Channel) bool {
	if chInfo == nil {
		return false
	}
	createdUnix := int64(chInfo.Created)
	if createdUnix <= 0 {
		return false
	}
	age := time.Since(time.Unix(createdUnix, 0))
	if age < 0 {
		age = 0
	}
	return age <= inviteTriggeredFreshWindow()
}

func inviteTriggeredFreshWindow() time.Duration {
	raw := strings.TrimSpace(os.Getenv("COMPANY_ONBOARDING_INVITE_FRESH_WINDOW_SEC"))
	if raw == "" {
		return defaultInviteFreshWindow
	}
	sec, err := strconv.Atoi(raw)
	if err != nil || sec < 0 {
		return defaultInviteFreshWindow
	}
	return time.Duration(sec) * time.Second
}

func ownerCandidatesFromInvite(ids ...string) []string {
	var out []string
	for _, id := range ids {
		c, ok := canonicalSlackUserIDForMention(id)
		if !ok {
			continue
		}
		out = append(out, c)
	}
	return dedupeStableUserIDs(out)
}

func ownerFallbackFromChannelMembers(ctx context.Context, api *slack.Client, channelID string) []string {
	if api == nil || strings.TrimSpace(channelID) == "" {
		return nil
	}
	listCtx, cancel := context.WithTimeout(ctx, 18*time.Second)
	defer cancel()
	var collected []string
	cursor := ""
	for page := 0; page < 5; page++ {
		ids, next, err := api.GetUsersInConversationContext(listCtx, &slack.GetUsersInConversationParameters{
			ChannelID: strings.TrimSpace(channelID),
			Cursor:    cursor,
			Limit:     200,
		})
		if err != nil {
			break
		}
		collected = append(collected, ids...)
		cursor = strings.TrimSpace(next)
		if cursor == "" {
			break
		}
	}
	return filterHumanSlackUserIDs(ctx, api, dedupeStableUserIDs(collected))
}

func filterHumanSlackUserIDs(ctx context.Context, api *slack.Client, ids []string) []string {
	if api == nil {
		return nil
	}
	var out []string
	seen := map[string]struct{}{}
	for _, raw := range dedupeStableUserIDs(ids) {
		id, ok := canonicalSlackUserIDForMention(raw)
		if !ok {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		infoCtx, cancel := context.WithTimeout(ctx, 6*time.Second)
		u, err := api.GetUserInfoContext(infoCtx, id)
		cancel()
		if err != nil || u == nil || u.IsBot || u.Deleted || u.IsStranger {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func enqueueCompanyChannelOnboardingWithSource(ctx context.Context, newChannelID string, ownerUserIDs []string, source string) {
	redisURL := strings.TrimSpace(os.Getenv("REDIS_URL"))
	if redisURL == "" {
		return
	}
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		log.Printf("create-company onboarding: redis parse: %v", err)
		return
	}
	client := redis.NewClient(opts)
	defer func() { _ = client.Close() }()

	ch := strings.TrimSpace(newChannelID)
	if ch == "" {
		return
	}
	dedupeKey := companyOnboardingDedupeKeyPrefix + ch
	ok, err := client.SetNX(ctx, dedupeKey, "1", companyOnboardingDedupeTTL).Result()
	if err != nil {
		log.Printf("create-company onboarding: setnx: %v", err)
		return
	}
	if !ok {
		return
	}
	payload := companyOnboardingQueuePayload{
		ChannelID:    ch,
		Source:       strings.TrimSpace(source),
		OwnerUserIDs: dedupeStableUserIDs(ownerUserIDs),
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		log.Printf("create-company onboarding: marshal: %v", err)
		return
	}
	q := strings.TrimSpace(os.Getenv("COMPANY_CHANNEL_ONBOARDING_QUEUE_KEY"))
	if q == "" {
		q = defaultCompanyChannelOnboardingQueue
	}
	if err := client.RPush(ctx, q, string(raw)).Err(); err != nil {
		log.Printf("create-company onboarding: rpush: %v", err)
		return
	}
	st := companyOnboardingState{Phase: "queued"}
	if rawSt, err := json.Marshal(st); err == nil {
		stateKey := companyOnboardingStateKeyPrefix + ":" + ch
		_ = client.Set(ctx, stateKey, string(rawSt), companyOnboardingStateTTL).Err()
	}
	log.Printf("create-company onboarding: queued channel_id=%s source=%s", ch, strings.TrimSpace(source))
}

func companyPostCreateCreatedMrkdwn(channelID, channelName string) string {
	cid := strings.TrimSpace(channelID)
	name := strings.TrimSpace(channelName)
	var created string
	switch {
	case cid != "" && name != "":
		created = fmt.Sprintf("<#%s|%s>", cid, name)
	case name != "":
		created = "#" + name
	default:
		created = "(channel)"
	}
	return fmt.Sprintf("Created: %s", created)
}

func postCompanyOnboardingKickoff(ctx context.Context, api *slack.Client, channelID, channelName string, ownerUserIDs []string) error {
	if api == nil {
		return fmt.Errorf("missing slack api")
	}
	ch := strings.TrimSpace(channelID)
	if ch == "" {
		return fmt.Errorf("missing channel id")
	}
	if !reserveCompanyOnboardingPost(ctx, ch) {
		return nil
	}
	label := "#your-company"
	if s := normalizeSlackChannelName(channelName); s != "" {
		label = "#" + s
	}
	introText := strings.TrimSpace(fmt.Sprintf(`Welcome to *%s*!

This channel is your company workspace. Team <!here>, introduce yourselves.`, label))
	if _, _, err := api.PostMessageContext(ctx, ch, slack.MsgOptionText(introText, false)); err != nil {
		return err
	}
	tellMeText := buildTellMeMessage(ctx, api, ownerUserIDs)
	dashboardURL := strings.TrimSpace(companyPortalDashboardURL(ch))
	secondText := tellMeText
	if dashboardURL != "" {
		secondText = secondText + "\n\nYou can always manage your company at your dashboard!"
	}
	if dashboardURL != "" {
		btn := slack.NewButtonBlockElement("makeacompany_founder_portal", "open", slack.NewTextBlockObject(slack.PlainTextType, "Dashboard", false, false))
		btn.URL = dashboardURL
		blocks := []slack.Block{
			slack.NewSectionBlock(slack.NewTextBlockObject(slack.MarkdownType, secondText, false, false), nil, nil),
			slack.NewActionBlock("makeacompany_portal_actions", btn),
		}
		_, _, err := api.PostMessageContext(ctx, ch,
			slack.MsgOptionText(secondText, false),
			slack.MsgOptionBlocks(blocks...),
		)
		return err
	}
	_, _, err := api.PostMessageContext(ctx, ch, slack.MsgOptionText(secondText, false))
	return err
}

func buildTellMeMessage(ctx context.Context, api *slack.Client, ownerUserIDs []string) string {
	vocative := "Tell me:"
	if names := onboardingFounderNames(ctx, api, ownerUserIDs); names != "" {
		vocative = "Tell me " + names + ":"
	}
	return strings.TrimSpace(vocative + `
1. Do you have an existing company?
2. Do you want to start a new company?
3. Do you need an idea for a company?`)
}

func onboardingFounderNames(ctx context.Context, api *slack.Client, ownerUserIDs []string) string {
	if api == nil {
		return ""
	}
	var names []string
	seen := map[string]struct{}{}
	for _, raw := range dedupeStableUserIDs(ownerUserIDs) {
		id, ok := canonicalSlackUserIDForMention(raw)
		if !ok {
			continue
		}
		infoCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		u, err := api.GetUserInfoContext(infoCtx, id)
		cancel()
		if err != nil || u == nil || u.IsBot || u.Deleted {
			continue
		}
		full := firstNonEmptyEnvString(
			strings.TrimSpace(u.Profile.RealName),
			strings.TrimSpace(u.RealName),
			strings.TrimSpace(u.Profile.DisplayName),
			strings.TrimSpace(u.Name),
		)
		if full == "" {
			continue
		}
		fields := strings.Fields(full)
		if len(fields) == 0 {
			continue
		}
		name := strings.TrimSpace(fields[0])
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	return strings.Join(names, ", ")
}

func firstNonEmptyEnvString(values ...string) string {
	for _, v := range values {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

// companyPortalDashboardURL returns the founder portal URL scoped to this company channel.
// Expected shape: <web-app-origin>/<channelId>, e.g. http://localhost:3000/C123 or https://makeacompany.ai/C123.
func companyPortalDashboardURL(channelID string) string {
	origin := strings.TrimSpace(firstNonEmptyEnv("MAKEACOMPANY_APP_BASE_URL", "APP_BASE_URL", "NEXT_PUBLIC_SITE_URL"))
	cid := strings.TrimSpace(channelID)
	if origin == "" || cid == "" {
		return ""
	}
	full := strings.TrimRight(origin, "/") + "/" + url.PathEscape(cid)
	parsed, err := url.Parse(full)
	if err != nil || parsed == nil {
		return full
	}
	return parsed.String()
}

func reserveCompanyOnboardingPost(ctx context.Context, channelID string) bool {
	redisURL := strings.TrimSpace(os.Getenv("REDIS_URL"))
	ch := strings.TrimSpace(channelID)
	if redisURL == "" || ch == "" {
		return true
	}
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		log.Printf("create-company onboarding: reserve parse redis: %v", err)
		return true
	}
	client := redis.NewClient(opts)
	defer func() { _ = client.Close() }()
	ok, err := client.SetNX(ctx, companyOnboardingPostedKeyPrefix+ch, "1", companyOnboardingStateTTL).Result()
	if err != nil {
		log.Printf("create-company onboarding: reserve setnx channel=%s err=%v", ch, err)
		return true
	}
	return ok
}
