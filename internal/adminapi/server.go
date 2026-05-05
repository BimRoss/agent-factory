package adminapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/slack-go/slack"

	"github.com/bimross/agent-factory/internal/runtime"
)

const (
	defaultListenAddr                    = ":8091"
	defaultCompanyChannelsRedisKey       = "agent-factory:company_channels"
	defaultCapabilityRoutingEventsKey    = "agent-factory:capability_routing_events"
	defaultChannelKnowledgeRedisKeyFmt   = "agent-factory:channel_knowledge:%s:markdown"
	defaultMakeACompanyProfileKeyPrefix  = "makeacompany:user_profile:"
	defaultHumansWelcomeDedupeKeyPrefix  = "agent-factory:humans_terms_welcome:"
	defaultHumansWelcomeDedupeTTL        = 400 * 24 * time.Hour
	defaultMaxCompanyChannelsList        = 200
	defaultMaxCapabilityRoutingEventList = 200
)

type Config struct {
	ListenAddr                       string
	RedisURL                         string
	InternalServiceToken             string
	CapabilityCatalogReadToken       string
	RequireCapabilityCatalogReadAuth bool
	SharedContractsDir               string
	SkillToolSpecsDir                string
	CompanyChannelsRedisKey          string
	CapabilityRoutingEventsRedisKey  string
	ChannelKnowledgeRedisKeyFmt      string
	// OrchestratorSlackBotToken is the slack-orchestrator bot token (channels:history) for channel-knowledge refresh.
	OrchestratorSlackBotToken string
	MakeACompanyProfileKeyPrefix     string
	OnboardingChannelID              string
	JoanneSlackBotToken              string
	MakeACompanyAppBaseURL           string
}

func LoadConfigFromEnv() (Config, error) {
	redisURL := strings.TrimSpace(os.Getenv("REDIS_URL"))
	if redisURL == "" {
		return Config{}, fmt.Errorf("REDIS_URL is required")
	}
	sharedContractsDir := strings.TrimSpace(os.Getenv("SHARED_CONTRACTS_DIR"))
	if sharedContractsDir == "" {
		sharedContractsDir = "../shared-contracts"
	}
	skillToolSpecsDir := strings.TrimSpace(os.Getenv("SKILL_TOOL_SPECS_DIR"))
	if skillToolSpecsDir == "" {
		skillToolSpecsDir = "../skill-factory/tools/v1"
	}
	return Config{
		ListenAddr:                       firstNonEmpty(os.Getenv("AGENT_FACTORY_ADMIN_HTTP_ADDR"), defaultListenAddr),
		RedisURL:                         redisURL,
		InternalServiceToken:             strings.TrimSpace(os.Getenv("BACKEND_INTERNAL_SERVICE_TOKEN")),
		CapabilityCatalogReadToken:       strings.TrimSpace(os.Getenv("CAPABILITY_CATALOG_READ_TOKEN")),
		RequireCapabilityCatalogReadAuth: parseBoolEnv(os.Getenv("REQUIRE_CAPABILITY_CATALOG_READ_TOKEN"), false),
		SharedContractsDir:               sharedContractsDir,
		SkillToolSpecsDir:                skillToolSpecsDir,
		CompanyChannelsRedisKey:          firstNonEmpty(os.Getenv("COMPANY_CHANNELS_REDIS_KEY"), defaultCompanyChannelsRedisKey),
		CapabilityRoutingEventsRedisKey:  firstNonEmpty(os.Getenv("CAPABILITY_ROUTING_EVENTS_REDIS_KEY"), defaultCapabilityRoutingEventsKey),
		ChannelKnowledgeRedisKeyFmt:      firstNonEmpty(os.Getenv("AGENT_FACTORY_CHANNEL_KNOWLEDGE_REDIS_KEY_FMT"), defaultChannelKnowledgeRedisKeyFmt),
		OrchestratorSlackBotToken:        strings.TrimSpace(os.Getenv("ORCHESTRATOR_SLACK_BOT_TOKEN")),
		MakeACompanyProfileKeyPrefix:     firstNonEmpty(os.Getenv("AGENT_FACTORY_MAKEACOMPANY_PROFILE_KEY_PREFIX"), defaultMakeACompanyProfileKeyPrefix),
		OnboardingChannelID:              strings.TrimSpace(os.Getenv("ONBOARDING_CHANNEL")),
		JoanneSlackBotToken:              strings.TrimSpace(os.Getenv("JOANNE_SLACK_BOT_TOKEN")),
		MakeACompanyAppBaseURL:           strings.TrimSuffix(strings.TrimSpace(firstNonEmpty(os.Getenv("MAKEACOMPANY_APP_BASE_URL"), os.Getenv("APP_BASE_URL"), os.Getenv("NEXT_PUBLIC_SITE_URL"))), "/"),
	}, nil
}

type Server struct {
	cfg    Config
	log    *log.Logger
	rdb    *redis.Client
	catSvc *catalogService
}

func Run(cfg Config) error {
	opts, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		return fmt.Errorf("parse REDIS_URL: %w", err)
	}
	rdb := redis.NewClient(opts)
	defer rdb.Close()

	s := &Server{
		cfg: cfg,
		log: log.New(os.Stdout, "agent-factory-admin ", log.LstdFlags|log.LUTC),
		rdb: rdb,
		catSvc: &catalogService{
			sharedContractsDir: cfg.SharedContractsDir,
			toolSpecsDir:       cfg.SkillToolSpecsDir,
		},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/v1/public/capability-catalog", s.handlePublicCapabilityCatalog)
	mux.HandleFunc("/v1/runtime/capability-catalog", s.handleRuntimeCapabilityCatalog)
	mux.HandleFunc("/v1/admin/catalog", s.requireInternal(s.handleAdminCatalog))
	mux.HandleFunc("/v1/admin/company-channels", s.requireInternal(s.handleCompanyChannels))
	mux.HandleFunc("/v1/admin/company-channels/discover", s.requireInternal(s.handleCompanyChannelsDiscover))
	mux.HandleFunc("/v1/admin/company-channels/registry-prune", s.requireInternal(s.handleCompanyChannelsRegistryPrune))
	mux.HandleFunc("/v1/admin/company-channels/", s.requireInternal(s.handleCompanyChannelByID))
	mux.HandleFunc("POST /v1/admin/channel-knowledge/{channelId}/refresh", s.requireInternal(s.handleChannelKnowledgePostRefresh))
	mux.HandleFunc("/v1/admin/channel-knowledge/", s.requireInternal(s.handleChannelKnowledge))
	mux.HandleFunc("/v1/admin/capability-routing-events", s.requireInternal(s.handleCapabilityRoutingEvents))
	mux.HandleFunc("/v1/admin/joanne-humans-welcome-trigger", s.requireInternal(s.handleJoanneWelcomeTrigger))

	s.log.Printf("admin authority listening on %s", cfg.ListenAddr)
	return http.ListenAndServe(cfg.ListenAddr, mux)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := s.rdb.Ping(r.Context()).Err(); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"status": "degraded", "redis": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

func (s *Server) handlePublicCapabilityCatalog(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	catalog, err := s.catSvc.buildCatalog()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, catalog)
}

func (s *Server) handleRuntimeCapabilityCatalog(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.cfg.RequireCapabilityCatalogReadAuth {
		expected := strings.TrimSpace(s.cfg.CapabilityCatalogReadToken)
		if expected == "" {
			http.Error(w, "runtime catalog token is required but not configured", http.StatusServiceUnavailable)
			return
		}
		if tokenFromRequest(r) != expected {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
	}
	s.handlePublicCapabilityCatalog(w, r)
}

func (s *Server) handleAdminCatalog(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handlePublicCapabilityCatalog(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleCompanyChannels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	entries, err := s.listCompanyChannels(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"channels":  entries,
		"truncated": len(entries) >= defaultMaxCompanyChannelsList,
		"redisKey":  s.cfg.CompanyChannelsRedisKey,
	})
}

func (s *Server) handleCompanyChannelsDiscover(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Channels []companyChannel `json:"channels"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	upserted, err := s.upsertDiscoveredChannels(r.Context(), body.Channels)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	s.backgroundRefreshChannelDigests(upserted)
	writeJSON(w, http.StatusOK, map[string]any{
		"upserted":       upserted,
		"upserted_count": len(upserted),
		"requested":      len(body.Channels),
		"redisKey":       s.cfg.CompanyChannelsRedisKey,
	})
}

func (s *Server) handleCompanyChannelsRegistryPrune(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		KeepChannelIDs []string `json:"keep_channel_ids"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	removed, err := s.pruneChannels(r.Context(), body.KeepChannelIDs)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":            true,
		"removed":       removed,
		"removed_count": len(removed),
		"redisKey":      s.cfg.CompanyChannelsRedisKey,
	})
}

func (s *Server) handleCompanyChannelByID(w http.ResponseWriter, r *http.Request) {
	channelID := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/v1/admin/company-channels/"))
	if channelID == "" {
		http.Error(w, "bad channel id", http.StatusBadRequest)
		return
	}
	switch r.Method {
	case http.MethodGet:
		row, err := s.getCompanyChannel(r.Context(), channelID)
		if err != nil {
			if err == redis.Nil {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"channel": row, "redisKey": s.cfg.CompanyChannelsRedisKey})
	case http.MethodPatch:
		var patch struct {
			GeneralAutoReactionEnabled *bool `json:"general_auto_reaction_enabled,omitempty"`
			GeneralResponsesMuted      *bool `json:"general_responses_muted,omitempty"`
			OutOfOfficeEnabled         *bool `json:"out_of_office_enabled,omitempty"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&patch); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		row, err := s.patchCompanyChannel(r.Context(), channelID, patch.GeneralAutoReactionEnabled, patch.GeneralResponsesMuted, patch.OutOfOfficeEnabled)
		if err != nil {
			if err == redis.Nil {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"channel": row})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleChannelKnowledgePostRefresh(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ch := strings.TrimSpace(r.PathValue("channelId"))
	if ch == "" {
		http.Error(w, "bad channel id", http.StatusBadRequest)
		return
	}
	n, err := s.runChannelKnowledgeRefresh(r.Context(), ch)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "channel_id": ch})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "channel_id": ch, "digest_runes": n})
}

func (s *Server) handleChannelKnowledge(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	channelID := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/v1/admin/channel-knowledge/"))
	if channelID == "" {
		http.Error(w, "bad channel id", http.StatusBadRequest)
		return
	}
	key := fmt.Sprintf(s.cfg.ChannelKnowledgeRedisKeyFmt, channelID)
	md, err := s.rdb.Get(r.Context(), key).Result()
	if err == redis.Nil {
		md = ""
	} else if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"channel_id": channelID,
		"markdown":   md,
		"empty":      strings.TrimSpace(md) == "",
	})
}

func (s *Server) handleCapabilityRoutingEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	channelID := strings.TrimSpace(r.URL.Query().Get("channelId"))
	limit := 50
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > defaultMaxCapabilityRoutingEventList {
		limit = defaultMaxCapabilityRoutingEventList
	}
	lines, err := s.rdb.LRange(r.Context(), s.cfg.CapabilityRoutingEventsRedisKey, 0, int64(limit*5-1)).Result()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	events := make([]map[string]any, 0, limit)
	for _, line := range lines {
		var row map[string]any
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			continue
		}
		if channelID != "" && strings.TrimSpace(stringValue(row["channel_id"])) != channelID {
			continue
		}
		events = append(events, row)
		if len(events) >= limit {
			break
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"events":    events,
		"redisKey":  s.cfg.CapabilityRoutingEventsRedisKey,
		"channelId": channelID,
	})
}

func (s *Server) handleJoanneWelcomeTrigger(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.cfg.OnboardingChannelID == "" || s.cfg.JoanneSlackBotToken == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error": "ONBOARDING_CHANNEL and JOANNE_SLACK_BOT_TOKEN must be set",
		})
		return
	}
	var body struct {
		Email string `json:"email"`
		Force *bool  `json:"force"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	email := normalizeProfileEmail(body.Email)
	if email == "" {
		http.Error(w, "email is required", http.StatusBadRequest)
		return
	}
	slackUserID, err := s.rdb.HGet(r.Context(), s.cfg.MakeACompanyProfileKeyPrefix+email, "slack_user_id").Result()
	if err == redis.Nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "no slack_user_id on makeacompany profile for " + email,
		})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	force := true
	if body.Force != nil {
		force = *body.Force
	}

	slackUserID = strings.TrimSpace(slackUserID)
	dedupeKey := defaultHumansWelcomeDedupeKeyPrefix + slackUserID
	if !force {
		if _, err := s.rdb.Get(r.Context(), dedupeKey).Result(); err == nil {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error":        "welcome already recorded for this user (set force=true to resend)",
				"slackUserId":  slackUserID,
				"profileEmail": email,
			})
			return
		} else if err != redis.Nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
	}

	api := slack.New(s.cfg.JoanneSlackBotToken)

	rootText := fmt.Sprintf("Hey, welcome to the company <@%s>! We need you to accept our *terms of use* before you begin working with us. Open the thread on this message to read them.", slackUserID)
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	_, rootTS, err := api.PostMessageContext(ctx, s.cfg.OnboardingChannelID, slack.MsgOptionText(rootText, false))
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": fmt.Sprintf("slack post welcome failed: %v", err)})
		return
	}
	origin := strings.TrimSuffix(strings.TrimSpace(s.cfg.MakeACompanyAppBaseURL), "/")
	if origin == "" {
		origin = "http://localhost:3000"
	}
	threadSummary := runtime.HumansTermsThreadSummaryMrkdwn(origin)
	blocks := runtime.BuildTermsAcceptanceBlocksWithSummary(s.cfg.OnboardingChannelID, slackUserID, strings.TrimSpace(rootTS), threadSummary)
	threadTitle := "Terms of Use"
	_, termsTS, err := api.PostMessageContext(ctx, s.cfg.OnboardingChannelID,
		slack.MsgOptionText(threadTitle, false),
		slack.MsgOptionTS(strings.TrimSpace(rootTS)),
		slack.MsgOptionBlocks(blocks...),
	)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": fmt.Sprintf("slack post terms thread failed: %v", err)})
		return
	}
	if err := runtime.SetTermsSkillPendingWithClient(ctx, s.rdb, s.cfg.OnboardingChannelID, slackUserID, strings.TrimSpace(rootTS)); err != nil {
		s.log.Printf("joanne welcome trigger redis terms pending failed slack_user=%s err=%v", slackUserID, err)
	}
	if err := s.rdb.Set(r.Context(), dedupeKey, "1", defaultHumansWelcomeDedupeTTL).Err(); err != nil {
		s.log.Printf("joanne welcome trigger dedupe set failed slack_user=%s err=%v", slackUserID, err)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":           true,
		"channelId":    s.cfg.OnboardingChannelID,
		"rootTs":       strings.TrimSpace(rootTS),
		"termsTs":      strings.TrimSpace(termsTS),
		"slackUserId":  slackUserID,
		"profileEmail": email,
	})
}

func (s *Server) listCompanyChannels(ctx context.Context) ([]companyChannel, error) {
	entries, err := s.rdb.HGetAll(ctx, s.cfg.CompanyChannelsRedisKey).Result()
	if err != nil {
		return nil, err
	}
	out := make([]companyChannel, 0, len(entries))
	for field, raw := range entries {
		var row companyChannel
		if err := json.Unmarshal([]byte(raw), &row); err != nil {
			continue
		}
		row.ChannelID = strings.TrimSpace(firstNonEmpty(row.ChannelID, field))
		if row.ChannelID == "" {
			continue
		}
		row.OwnerIDs = uniqueIDs(row.OwnerIDs)
		row.ThreadsEnabled = true
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CompanySlug != out[j].CompanySlug {
			return out[i].CompanySlug < out[j].CompanySlug
		}
		return out[i].ChannelID < out[j].ChannelID
	})
	if len(out) > defaultMaxCompanyChannelsList {
		out = out[:defaultMaxCompanyChannelsList]
	}
	return out, nil
}

func (s *Server) getCompanyChannel(ctx context.Context, channelID string) (companyChannel, error) {
	raw, err := s.rdb.HGet(ctx, s.cfg.CompanyChannelsRedisKey, channelID).Result()
	if err != nil {
		return companyChannel{}, err
	}
	var row companyChannel
	if err := json.Unmarshal([]byte(raw), &row); err != nil {
		return companyChannel{}, err
	}
	row.ChannelID = strings.TrimSpace(firstNonEmpty(row.ChannelID, channelID))
	row.OwnerIDs = uniqueIDs(row.OwnerIDs)
	row.ThreadsEnabled = true
	return row, nil
}

func (s *Server) patchCompanyChannel(ctx context.Context, channelID string, autoReact, muted, ooo *bool) (companyChannel, error) {
	row, err := s.getCompanyChannel(ctx, channelID)
	if err != nil {
		return companyChannel{}, err
	}
	if autoReact != nil {
		row.GeneralAutoReactionEnabled = *autoReact
	}
	if muted != nil {
		row.GeneralResponsesMuted = *muted
	}
	if ooo != nil {
		row.OutOfOfficeEnabled = *ooo
	}
	raw, err := json.Marshal(row)
	if err != nil {
		return companyChannel{}, err
	}
	if err := s.rdb.HSet(ctx, s.cfg.CompanyChannelsRedisKey, channelID, raw).Err(); err != nil {
		return companyChannel{}, err
	}
	return row, nil
}

func (s *Server) upsertDiscoveredChannels(ctx context.Context, rows []companyChannel) ([]string, error) {
	upserted := make([]string, 0, len(rows))
	for _, row := range rows {
		// Admin UI + makeacompany-ai backend send Slack channel label as json "name"; match that shape so
		// proxied POST /v1/admin/company-channels/discover preserves display names (see makeacompany-ai store).
		if dn := strings.TrimSpace(row.DisplayName); dn == "" {
			row.DisplayName = strings.TrimSpace(row.Name)
		}
		row.Name = ""

		channelID := strings.TrimSpace(row.ChannelID)
		if channelID == "" {
			continue
		}
		existing, err := s.getCompanyChannel(ctx, channelID)
		if err != nil && err != redis.Nil {
			return upserted, err
		}
		if err == nil {
			if row.CompanySlug == "" {
				row.CompanySlug = existing.CompanySlug
			}
			if slackDn := strings.TrimSpace(row.DisplayName); slackDn != "" {
				if slug := slugFromSlackChannelDisplayName(slackDn); slug != "" && strings.TrimSpace(existing.CompanySlug) == "" {
					row.CompanySlug = slug
				}
			} else {
				row.DisplayName = existing.DisplayName
			}
			if len(row.OwnerIDs) == 0 {
				row.OwnerIDs = existing.OwnerIDs
			}
			row.GeneralAutoReactionEnabled = existing.GeneralAutoReactionEnabled
			row.GeneralResponsesMuted = existing.GeneralResponsesMuted
			row.OutOfOfficeEnabled = existing.OutOfOfficeEnabled
		} else if row.CompanySlug == "" {
			row.CompanySlug = slugFromSlackChannelDisplayName(row.DisplayName)
		}
		row.OwnerIDs = uniqueIDs(row.OwnerIDs)
		row.ThreadsEnabled = true
		if !row.GeneralAutoReactionEnabled {
			row.GeneralAutoReactionEnabled = true
		}
		raw, mErr := json.Marshal(row)
		if mErr != nil {
			return upserted, mErr
		}
		if err := s.rdb.HSet(ctx, s.cfg.CompanyChannelsRedisKey, channelID, raw).Err(); err != nil {
			return upserted, err
		}
		upserted = append(upserted, channelID)
	}
	sort.Strings(upserted)
	return upserted, nil
}

func (s *Server) pruneChannels(ctx context.Context, keep []string) ([]string, error) {
	keepSet := map[string]struct{}{}
	for _, id := range keep {
		id = strings.TrimSpace(id)
		if id != "" {
			keepSet[id] = struct{}{}
		}
	}
	all, err := s.rdb.HKeys(ctx, s.cfg.CompanyChannelsRedisKey).Result()
	if err != nil {
		return nil, err
	}
	toRemove := make([]string, 0)
	for _, id := range all {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := keepSet[id]; ok {
			continue
		}
		toRemove = append(toRemove, id)
	}
	if len(toRemove) == 0 {
		return []string{}, nil
	}
	if err := s.rdb.HDel(ctx, s.cfg.CompanyChannelsRedisKey, toRemove...).Err(); err != nil {
		return nil, err
	}
	sort.Strings(toRemove)
	return toRemove, nil
}

func (s *Server) requireInternal(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		expected := strings.TrimSpace(s.cfg.InternalServiceToken)
		if expected != "" && tokenFromRequest(r) != expected {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

type companyChannel struct {
	CompanySlug string `json:"company_slug"`
	ChannelID   string `json:"channel_id"`
	// Name is the Slack channel name from admin discover payloads (json "name"); not persisted when empty.
	Name                       string   `json:"name,omitempty"`
	DisplayName                string   `json:"display_name,omitempty"`
	OwnerIDs                   []string `json:"owner_ids,omitempty"`
	ThreadsEnabled             bool     `json:"threads_enabled"`
	GeneralAutoReactionEnabled bool     `json:"general_auto_reaction_enabled"`
	GeneralResponsesMuted      bool     `json:"general_responses_muted,omitempty"`
	OutOfOfficeEnabled         bool     `json:"out_of_office_enabled"`
}

func tokenFromRequest(r *http.Request) string {
	if r == nil {
		return ""
	}
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	if strings.HasPrefix(strings.ToLower(auth), "bearer ") {
		return strings.TrimSpace(auth[7:])
	}
	return ""
}

func normalizeProfileEmail(email string) string {
	email = strings.ToLower(strings.TrimSpace(email))
	if !strings.Contains(email, "@") {
		return ""
	}
	return email
}

func uniqueIDs(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(in))
	for _, id := range in {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func parseBoolEnv(raw string, def bool) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return def
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v != "" {
			return v
		}
	}
	return ""
}

func slugFromSlackChannelDisplayName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return ""
	}
	var b strings.Builder
	lastDash := false
	for _, r := range name {
		valid := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if valid {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteRune('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func stringValue(v any) string {
	switch t := v.(type) {
	case string:
		return t
	default:
		return ""
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
