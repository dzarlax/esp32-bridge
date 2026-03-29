package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	ListenAddr string

	HealthBaseURL string
	HealthAPIKey  string
	HealthCacheTTL time.Duration

	TodoistToken    string
	TodoistCacheTTL time.Duration

	NewsBaseURL    string
	NewsLimit      int
	NewsSinceHours int
	NewsCacheTTL   time.Duration

	HABaseURL  string
	HAToken    string
	HASensors  []string
	HACacheTTL time.Duration

	FetchTimeout time.Duration
}

func Load() *Config {
	return &Config{
		ListenAddr: envStr("LISTEN_ADDR", ":8090"),

		HealthBaseURL:  envStr("HEALTH_BASE_URL", ""),
		HealthAPIKey:   envStr("HEALTH_API_KEY", ""),
		HealthCacheTTL: envDuration("HEALTH_CACHE_TTL", 300),

		TodoistToken:    envStr("TODOIST_TOKEN", ""),
		TodoistCacheTTL: envDuration("TODOIST_CACHE_TTL", 60),

		NewsBaseURL:    envStr("NEWS_BASE_URL", ""),
		NewsLimit:      envInt("NEWS_LIMIT", 5),
		NewsSinceHours: envInt("NEWS_SINCE_HOURS", 24),
		NewsCacheTTL:   envDuration("NEWS_CACHE_TTL", 900),

		HABaseURL:  envStr("HA_BASE_URL", ""),
		HAToken:    envStr("HA_TOKEN", ""),
		HASensors:  envList("HA_SENSORS"),
		HACacheTTL: envDuration("HA_CACHE_TTL", 120),

		FetchTimeout: envDuration("FETCH_TIMEOUT", 5),
	}
}

func envStr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envDuration(key string, defSec int) time.Duration {
	return time.Duration(envInt(key, defSec)) * time.Second
}

func envList(key string) []string {
	v := os.Getenv(key)
	if v == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			out = append(out, s)
		}
	}
	return out
}
