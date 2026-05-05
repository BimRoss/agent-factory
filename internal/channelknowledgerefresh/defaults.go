package channelknowledgerefresh

// Defaults for company channel knowledge harvest (aligned with legacy employee-factory config).
const (
	DefaultChannelKnowledgeMaxStoreRunes = 2_000_000

	DefaultChannelKnowledgeHistoryLimit = 5_000

	DefaultChannelKnowledgeIncrHistoryMaxMsgs = 400
	DefaultChannelKnowledgeIncrThreadPollMax  = 80
	DefaultChannelKnowledgeIncrThreadPollMaxWhenIdle = 20
	DefaultChannelKnowledgeMaxStoredEvents           = 25000
	DefaultChannelKnowledgeRecentWindowHours         = 72
	DefaultChannelKnowledgeRecentMaxStoreRunes       = 150_000
	DefaultChannelKnowledgeMaxTrackedThreads         = 2000
)
