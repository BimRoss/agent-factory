package channelknowledgerefresh

import (
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultHistoryPageSize = 200
	defaultHistoryMaxPages = 50
	defaultThreadMaxPages  = 20
)

// Params configures one channel digest refresh (mirrors channel-knowledge-refresh env).
type Params struct {
	HistLimit      int
	HistPageSize   int
	HistMaxPages   int
	MaxStoreRunes  int
	TTL            time.Duration
	IncludeThreads bool
	ThreadMaxRoots int
	ThreadPageSize int
	ThreadMaxPages int
	// DigestThreadMarkers appends <!--dac m=… t=…--> HTML comments for admin transcript UI
	// (grouping thread replies). Stripped in the bot before LLM injection and read-company.
	DigestThreadMarkers bool

	// Incremental harvest (minute cron); ignored during bootstrap full fetch.
	IncrHistoryMaxMsgs int
	IncrThreadPollMax  int
	// IncrThreadPollMaxWhenIdle caps thread Slack polls when conversations.history returned nothing (0 = no cap).
	IncrThreadPollMaxWhenIdle int
	MaxStoredEvents           int
	RecentWindowHours         int
	RecentMaxStoreRunes       int
	MaxTrackedThreads         int
}

func parseIntEnv(key string, defaultVal int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return defaultVal
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return defaultVal
	}
	return n
}

func parseIntEnvThreadPollIdle(key string, defaultVal int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return defaultVal
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return defaultVal
	}
	if n < 0 {
		return defaultVal
	}
	return n
}

func parseBoolEnvWithDefault(key string, defaultVal bool) bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv(key)))
	if v == "" {
		return defaultVal
	}
	switch v {
	case "0", "false", "no", "off":
		return false
	case "1", "true", "yes", "on":
		return true
	default:
		return defaultVal
	}
}

// ParamsFromEnv loads refresh tuning from the process environment (same keys as cmd/channel-knowledge-refresh).
func ParamsFromEnv() Params {
	ttl := time.Duration(parseIntEnv("CHANNEL_KNOWLEDGE_TTL_SEC", 30*24*3600)) * time.Second
	return Params{
		HistLimit:      parseIntEnv("CHANNEL_KNOWLEDGE_HISTORY_LIMIT", DefaultChannelKnowledgeHistoryLimit),
		HistPageSize:   parseIntEnv("CHANNEL_KNOWLEDGE_HISTORY_PAGE_SIZE", defaultHistoryPageSize),
		HistMaxPages:   parseIntEnv("CHANNEL_KNOWLEDGE_HISTORY_MAX_PAGES", defaultHistoryMaxPages),
		MaxStoreRunes:  parseIntEnv("CHANNEL_KNOWLEDGE_MAX_STORE_RUNES", DefaultChannelKnowledgeMaxStoreRunes),
		TTL:            ttl,
		IncludeThreads: parseBoolEnvWithDefault("CHANNEL_KNOWLEDGE_INCLUDE_THREADS", true),
		ThreadMaxRoots: parseIntEnv("CHANNEL_KNOWLEDGE_THREAD_MAX_ROOTS", 200),
		ThreadPageSize: parseIntEnv("CHANNEL_KNOWLEDGE_THREAD_PAGE_SIZE", defaultHistoryPageSize),
		ThreadMaxPages: parseIntEnv("CHANNEL_KNOWLEDGE_THREAD_MAX_PAGES", defaultThreadMaxPages),
		DigestThreadMarkers: parseBoolEnvWithDefault(
			"CHANNEL_KNOWLEDGE_DIGEST_THREAD_MARKERS",
			true,
		),
		IncrHistoryMaxMsgs: parseIntEnv(
			"CHANNEL_KNOWLEDGE_INCR_HISTORY_MAX_MSGS",
			DefaultChannelKnowledgeIncrHistoryMaxMsgs,
		),
		IncrThreadPollMax: parseIntEnv(
			"CHANNEL_KNOWLEDGE_INCR_THREAD_POLL_MAX",
			DefaultChannelKnowledgeIncrThreadPollMax,
		),
		IncrThreadPollMaxWhenIdle: parseIntEnvThreadPollIdle(
			"CHANNEL_KNOWLEDGE_INCR_THREAD_POLL_MAX_WHEN_IDLE",
			DefaultChannelKnowledgeIncrThreadPollMaxWhenIdle,
		),
		MaxStoredEvents: parseIntEnv(
			"CHANNEL_KNOWLEDGE_MAX_STORED_EVENTS",
			DefaultChannelKnowledgeMaxStoredEvents,
		),
		RecentWindowHours: parseIntEnv(
			"CHANNEL_KNOWLEDGE_RECENT_WINDOW_HOURS",
			DefaultChannelKnowledgeRecentWindowHours,
		),
		RecentMaxStoreRunes: parseIntEnv(
			"CHANNEL_KNOWLEDGE_RECENT_MAX_STORE_RUNES",
			DefaultChannelKnowledgeRecentMaxStoreRunes,
		),
		MaxTrackedThreads: parseIntEnv(
			"CHANNEL_KNOWLEDGE_MAX_TRACKED_THREADS",
			DefaultChannelKnowledgeMaxTrackedThreads,
		),
	}
}
