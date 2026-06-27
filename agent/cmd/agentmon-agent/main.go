package main

import (
	"flag"
	"log"
	"net/http"

	"agentmon/agent/internal/api"
	"agentmon/agent/internal/config"
)

var version = "dev"

func main() {
	cfgPath := flag.String("config", "/etc/agentmon/agent.toml", "path to agent.toml")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", api.HealthHandler(cfg.ServerID, version))

	log.Printf("agentmon-agent %s listening on %s (server %s)", version, cfg.Listen, cfg.ServerID)
	log.Fatal(http.ListenAndServe(cfg.Listen, mux))
}
