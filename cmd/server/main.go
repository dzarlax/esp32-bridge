package main

import (
	"log"
	"net/http"
	"time"

	"esp32-bridge/internal/cache"
	"esp32-bridge/internal/config"
	"esp32-bridge/internal/fetcher"
	"esp32-bridge/internal/handler"
)

func main() {
	cfg := config.Load()

	client := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        20,
			MaxIdleConnsPerHost: 5,
			IdleConnTimeout:     90 * time.Second,
		},
	}

	c := cache.New()
	var fetchers []fetcher.Fetcher

	if cfg.HealthBaseURL != "" {
		fetchers = append(fetchers, fetcher.NewHealth(cfg.HealthBaseURL, cfg.HealthAPIKey, client, cfg.HealthCacheTTL))
		log.Printf("Health fetcher: %s", cfg.HealthBaseURL)
	}
	if cfg.TodoistToken != "" {
		fetchers = append(fetchers, fetcher.NewTodoist(cfg.TodoistToken, client, cfg.TodoistCacheTTL))
		log.Printf("Todoist fetcher: enabled")
	}
	if cfg.NewsBaseURL != "" {
		fetchers = append(fetchers, fetcher.NewNews(cfg.NewsBaseURL, cfg.NewsLimit, cfg.NewsSinceHours, client, cfg.NewsCacheTTL))
		log.Printf("News fetcher: %s", cfg.NewsBaseURL)
	}
	if cfg.HABaseURL != "" && len(cfg.HASensors) > 0 {
		fetchers = append(fetchers, fetcher.NewHA(cfg.HABaseURL, cfg.HAToken, cfg.HASensors, client, cfg.HACacheTTL))
		log.Printf("HA fetcher: %s (%d sensors)", cfg.HABaseURL, len(cfg.HASensors))
	}

	if len(fetchers) == 0 {
		log.Println("Warning: no fetchers configured")
	}

	orch := fetcher.NewOrchestrator(fetchers, c, cfg.FetchTimeout)
	h := handler.New(orch, cfg.APIKey)

	mux := http.NewServeMux()
	mux.HandleFunc("/api/dashboard", h.Dashboard)
	mux.HandleFunc("/health", h.Health)

	log.Printf("esp32-bridge listening on %s (%d fetchers)", cfg.ListenAddr, len(fetchers))
	log.Fatal(http.ListenAndServe(cfg.ListenAddr, mux))
}
