package main

import (
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"

	"esp32-bridge/internal/adminauth"
	"esp32-bridge/internal/app"
	"esp32-bridge/internal/config"
	"esp32-bridge/internal/management"
	"esp32-bridge/internal/settings"
	webui "esp32-bridge/web"
)

func main() {
	cfg := config.Load()
	if e := cfg.Validate(); e != nil {
		log.Fatal(e)
	}
	client := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{MaxIdleConns: 20, MaxIdleConnsPerHost: 5, IdleConnTimeout: 90 * time.Second}}
	runtime, e := app.NewFleet(cfg, client)
	if e != nil {
		log.Fatal(e)
	}
	mux := http.NewServeMux()
	enabled, e := strconv.ParseBool(env("ADMIN_ENABLED", "false"))
	if e != nil {
		log.Fatal("invalid ADMIN_ENABLED")
	}
	if enabled {
		base := os.Getenv("ADMIN_BASE_URL")
		auth, e := adminauth.New(adminauth.Config{BaseURL: base, PasswordHashFile: os.Getenv("ADMIN_PASSWORD_HASH_FILE"), CloudflareTeamDomain: os.Getenv("ADMIN_CF_TEAM_DOMAIN"), CloudflareAudience: os.Getenv("ADMIN_CF_AUDIENCE"), CloudflareEmails: split(os.Getenv("ADMIN_CF_EMAILS"))}, client)
		if e != nil {
			log.Fatal(e)
		}
		store, e := settings.New(env("ADMIN_STATE_DIR", "/data/homedash"), settings.Defaults(cfg, base), app.DefaultSecrets(cfg), base, runtime.Prepare)
		if e != nil {
			log.Fatal(e)
		}
		values, secrets := store.Snapshot()
		commit, abort, e := runtime.Prepare(values, secrets)
		if e != nil {
			log.Fatal(e)
		}
		_ = abort
		commit()
		runtime.EnableManaged()
		auth.Register(mux)
		api := &management.API{Store: store, DeviceKey: cfg.APIKey, Statuses: runtime.Statuses, Updates: runtime.Updates}
		api.Register(mux, auth.Require)
		mux.Handle("/settings/", webui.Handler())
		mux.HandleFunc("/settings", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/settings/", http.StatusSeeOther) })
		log.Print("Administration panel enabled")
	}
	mux.Handle("/", runtime)
	server := &http.Server{Addr: cfg.ListenAddr, Handler: mux, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	log.Printf("esp32-bridge listening on %s", cfg.ListenAddr)
	log.Fatal(server.ListenAndServe())
}
func env(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}
func split(v string) []string {
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
