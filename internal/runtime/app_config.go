package runtime

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

type AppConfig struct {
	Mode            string
	EmployeeID      string
	NatsURL         string
	NatsStream      string
	NatsDurableName string
	NatsFetchBatch  int
	NatsFetchWaitMS int
	NatsWorkers     int
	MemoryBankFile  string
}

func LoadAppConfigFromEnv() (AppConfig, error) {
	mode := strings.ToLower(strings.TrimSpace(firstNonEmpty(
		os.Getenv("AGENT_FACTORY_MODE"),
		"serve",
	)))
	employeeID := normalizeID(firstNonEmpty(os.Getenv("EMPLOYEE_ID"), "joanne"))
	if mode == "serve" && employeeID == "" {
		return AppConfig{}, fmt.Errorf("EMPLOYEE_ID is required for serve mode")
	}
	natsURL := strings.TrimSpace(firstNonEmpty(os.Getenv("NATS_URL"), os.Getenv("ORCHESTRATOR_NATS_URL")))
	if mode == "serve" && natsURL == "" {
		return AppConfig{}, fmt.Errorf("NATS_URL (or ORCHESTRATOR_NATS_URL) is required for serve mode")
	}

	return AppConfig{
		Mode:            mode,
		EmployeeID:      employeeID,
		NatsURL:         natsURL,
		NatsStream:      strings.TrimSpace(firstNonEmpty(os.Getenv("NATS_STREAM"), "SLACK_WORK")),
		NatsDurableName: strings.TrimSpace(os.Getenv("NATS_DURABLE_NAME")),
		NatsFetchBatch:  parseIntEnv(os.Getenv("NATS_FETCH_BATCH"), 8),
		NatsFetchWaitMS: parseIntEnv(os.Getenv("NATS_FETCH_MAX_WAIT_MS"), 5000),
		NatsWorkers:     parseIntEnv(os.Getenv("ORCHESTRATOR_INGRESS_WORKERS"), 4),
		MemoryBankFile:  strings.TrimSpace(firstNonEmpty(os.Getenv("MEMORY_BANK_FILE"), os.Getenv("CHANNEL_MEMORY_BANK_FILE"))),
	}, nil
}

func parseIntEnv(raw string, def int) int {
	s := strings.TrimSpace(raw)
	if s == "" {
		return def
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return v
}
