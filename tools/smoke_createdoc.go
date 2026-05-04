//go:build ignore

// Local smoke: loads the same registry/tool specs as cmd/agent-factory and runs
// create-doc once with an explicit body (no Gemini draft). Usage from repo root:
//
//	set -a && source .env.dev && set +a
//	export SHARED_CONTRACTS_DIR=/path/to/shared-contracts SKILL_FACTORY_DIR=/path/to/skill-factory
//	go run tools/smoke_createdoc.go
//
// (Compose `.env.dev` often sets SHARED_CONTRACTS_DIR=/workspace/...; override for host runs.)
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/bimross/agent-factory/internal/runtime"
)

func main() {
	sharedContractsDir := firstNonEmpty(os.Getenv("SHARED_CONTRACTS_DIR"), filepath.Join("..", "shared-contracts"))
	employeeInstancesPath := firstNonEmpty(os.Getenv("EMPLOYEE_INSTANCES_FILE"), filepath.Join(sharedContractsDir, "employees.instances.v1.json"))
	skillInstancesPath := firstNonEmpty(os.Getenv("SKILL_INSTANCES_FILE"), filepath.Join(sharedContractsDir, "skills.instances.v1.json"))
	skillFactoryDir := firstNonEmpty(os.Getenv("SKILL_FACTORY_DIR"), filepath.Join("..", "skill-factory"))
	toolSpecsDir := firstNonEmpty(os.Getenv("SKILL_TOOL_SPECS_DIR"), filepath.Join(skillFactoryDir, "tools", "v1"))

	registry, err := runtime.LoadRegistryFromContractFiles(employeeInstancesPath, skillInstancesPath)
	if err != nil {
		log.Fatalf("registry: %v", err)
	}
	providerConfig, err := runtime.LoadProviderConfigFromEnv()
	if err != nil {
		log.Fatalf("provider: %v", err)
	}
	toolSpecs, err := runtime.LoadToolSpecsFromDir(toolSpecsDir)
	if err != nil {
		log.Fatalf("tool specs: %v", err)
	}
	memoryBank, err := runtime.LoadMemoryBank("")
	if err != nil {
		log.Fatalf("memory bank: %v", err)
	}

	store := runtime.NewMemoryStore()
	engine := runtime.NewEngine(
		smokePublisher{},
		acceptAllHandoffBus{},
		store,
		store,
		registry,
		toolSpecs,
		providerConfig,
		memoryBank,
		nil,
		nil,
	)

	task := runtime.Task{
		ID:           "smoke-create-doc",
		ThreadAnchor: "Csmoke:1234567890.000001",
		TraceID:      "trace-smoke-createdoc",
		RequestText:  "title: Agent factory smoke; body: Smoke test body — Google Docs API path only (no Gemini draft).",
	}

	owned, err := engine.StartTask(task, "joanne")
	if err != nil {
		log.Fatalf("start task: %v", err)
	}
	owned, err = engine.ExecuteCapability(context.Background(), owned, "create-doc", nil)
	if err != nil {
		log.Fatalf("execute create-doc: %v", err)
	}
	fmt.Printf("ok task state=%s owner=%s\n", owned.LastState, owned.OwnerEmployeeID)
}

type smokePublisher struct{}

func (smokePublisher) PublishStatus(runtime.LifecycleEvent) error     { return nil }
func (smokePublisher) PublishUpdate(runtime.Task, string) error       { return nil }
func (smokePublisher) PublishThreadNotice(runtime.Task, string) error { return nil }
func (smokePublisher) ClearInboundReaction(runtime.Task) error        { return nil }
func (smokePublisher) PublishFinal(_ runtime.Task, p runtime.RenderPayload) error {
	fmt.Println(p.FallbackText)
	return nil
}

type acceptAllHandoffBus struct{}

func (acceptAllHandoffBus) RequestHandoff(req runtime.HandoffRequest) (runtime.HandoffResult, error) {
	return runtime.HandoffResult{Accepted: true, Reason: "smoke"}, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
