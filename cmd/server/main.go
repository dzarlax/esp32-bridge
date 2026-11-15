package main

import (
	"crypto/tls"
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
	"esp32-bridge/internal/voicepairsetup"
	"esp32-bridge/internal/voicepush"
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
	publisherToken, subscriberToken, voiceTarget := os.Getenv("VOICE_PUSH_PUBLISHER_TOKEN"), os.Getenv("VOICE_PUSH_SUBSCRIBER_TOKEN"), os.Getenv("VOICE_PUSH_TARGET_ID")
	if publisherToken != "" && subscriberToken != "" && voiceTarget != "" {
		if len(publisherToken) < 24 || len(subscriberToken) < 24 || publisherToken == subscriberToken || publisherToken == cfg.APIKey || subscriberToken == cfg.APIKey || strings.ContainsAny(publisherToken+subscriberToken, " \t\r\n") {
			log.Fatal("voice push requires distinct private publisher and subscriber credentials")
		}
	}
	paired, e := strconv.ParseBool(env("VOICE_PAIRING_ENABLED", "false"))
	if e != nil {
		log.Fatal("invalid VOICE_PAIRING_ENABLED")
	}
	enabled, e := strconv.ParseBool(env("ADMIN_ENABLED", "false"))
	if e != nil {
		log.Fatal("invalid ADMIN_ENABLED")
	}
	if paired && !enabled {
		log.Fatal("voice pairing requires administration")
	}
	if !paired && pairingConfigurationPresent(os.Getenv) {
		log.Fatal("voice pairing is disabled but pairing configuration remains; refusing legacy startup")
	}
	var tlsConfig *tls.Config
	if !paired {
		voice := voicepush.New(publisherToken, subscriberToken, voiceTarget)
		defer voice.Close()
		voice.Register(mux)
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
		api := &management.API{Store: store, DeviceKey: cfg.APIKey, Statuses: runtime.Statuses, Updates: runtime.Updates, CalendarStatus: runtime.CalendarStatus}
		if paired {
			migration, err := strconv.ParseBool(env("VOICE_PAIRING_LEGACY_MIGRATION", "false"))
			if err != nil || publisherToken == cfg.APIKey || subscriberToken == cfg.APIKey && subscriberToken != "" || strings.ContainsAny(publisherToken, " \t\r\n") {
				log.Fatal("invalid paired voice credentials")
			}
			pairing, err := voicepairsetup.New(voicepairsetup.Config{StateDir: os.Getenv("VOICE_PAIRING_STATE_DIR"), Origin: os.Getenv("VOICE_PAIRING_HTTPS_ORIGIN"), AdminBase: base, MasterFile: os.Getenv("VOICE_PAIRING_MASTER_FILE"), MasterID: os.Getenv("VOICE_PAIRING_MASTER_ID"), IntegrityFile: os.Getenv("VOICE_PAIRING_INTEGRITY_FILE"), IntegrityID: os.Getenv("VOICE_PAIRING_INTEGRITY_ID"), SigningFile: os.Getenv("VOICE_PAIRING_SIGNING_FILE"), TLSCertFile: os.Getenv("VOICE_PAIRING_TLS_CERT_FILE"), TLSKeyFile: os.Getenv("VOICE_PAIRING_TLS_KEY_FILE"), Publisher: publisherToken, LegacyMigration: migration, LegacySubscriber: subscriberToken, LegacyTarget: voiceTarget}, store, auth.Require)
			if err != nil {
				log.Fatal("voice pairing configuration rejected")
			}
			defer pairing.Manager.Close()
			defer pairing.Hub.Close()
			pairing.Hub.Register(mux)
			mux.Handle("/api/voice/pair/v1/", voicepairsetup.TLSOnly(pairing.Adapter.DeviceHandler()))
			api.VoiceAdmin = voicepairsetup.TLSOnly(pairing.Adapter.AdminHandler())
			api.VoiceClear = pairing.Clear
			tlsConfig = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{pairing.TLS}}
		}
		api.Register(mux, auth.Require)
		mux.Handle("/settings/", webui.Handler())
		mux.HandleFunc("/settings", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/settings/", http.StatusSeeOther) })
		log.Print("Administration panel enabled")
	}
	mux.Handle("/", runtime)
	server := &http.Server{Addr: cfg.ListenAddr, Handler: mux, TLSConfig: tlsConfig, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	log.Printf("esp32-bridge listening on %s", cfg.ListenAddr)
	if paired {
		log.Fatal(server.ListenAndServeTLS("", ""))
	}
	log.Fatal(server.ListenAndServe())
}
func env(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

// A disabled pairing service must not silently revive the old bearer after a
// managed ACK. With partial configuration the persisted mode is unknowable.
func pairingConfigurationPresent(get func(string) string) bool {
	for _, name := range []string{"VOICE_PAIRING_STATE_DIR", "VOICE_PAIRING_HTTPS_ORIGIN", "VOICE_PAIRING_MASTER_FILE", "VOICE_PAIRING_MASTER_ID", "VOICE_PAIRING_INTEGRITY_FILE", "VOICE_PAIRING_INTEGRITY_ID", "VOICE_PAIRING_SIGNING_FILE", "VOICE_PAIRING_TLS_CERT_FILE", "VOICE_PAIRING_TLS_KEY_FILE"} {
		if get(name) != "" {
			return true
		}
	}
	value := get("VOICE_PAIRING_LEGACY_MIGRATION")
	if value == "" {
		return false
	}
	enabled, err := strconv.ParseBool(value)
	return err != nil || enabled
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
