package main

import (
	"flag"
	"log"
	"net/http"

	"agentmon/hubd/internal/api"
	"agentmon/hubd/internal/config"
	"agentmon/hubd/internal/webui"
)

var version = "dev"

func main() {
	cfgPath := flag.String("config", "/data/config.yaml", "path to config.yaml")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", api.HealthHandler(version))
	// /api/v1 routes are added in M3/M4. The SPA handler is the catch-all.
	mux.Handle("/", webui.Handler())

	log.Printf("agentmon-hubd %s listening on %s (%d servers)", version, cfg.Listen, len(cfg.Servers))
	log.Fatal(http.ListenAndServe(cfg.Listen, mux))
}
