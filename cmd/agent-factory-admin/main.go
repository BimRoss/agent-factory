package main

import (
	"log"

	"github.com/bimross/agent-factory/internal/adminapi"
)

func main() {
	cfg, err := adminapi.LoadConfigFromEnv()
	if err != nil {
		log.Fatalf("load admin api config: %v", err)
	}
	if err := adminapi.Run(cfg); err != nil {
		log.Fatalf("run admin api: %v", err)
	}
}
