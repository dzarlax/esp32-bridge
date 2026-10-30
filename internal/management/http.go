package management

import (
	"bytes"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"esp32-bridge/internal/settings"
)

type API struct {
	catalogs      catalogCache
	catalogClient *http.Client
	Store         *settings.Store
	DeviceKey     string
	Statuses      func(string) map[string]settings.Status
	Updates       func(string, string) map[string]any
}

func (a *API) Register(mux *http.ServeMux, require func(http.Handler) http.Handler) {
	admin := func(h http.Handler) http.Handler { return require(h) }
	mux.Handle("/api/admin/v1/connections", admin(http.HandlerFunc(a.connections)))
	mux.Handle("/api/admin/v1/devices", admin(http.HandlerFunc(a.devices)))
	mux.Handle("/api/admin/v1/devices/", admin(http.HandlerFunc(a.deviceRoute)))
	// V1's global management routes are deliberately closed. They cannot select
	// an arbitrary display when the caller omits a device identity.
	for _, path := range []string{"/api/admin/v1/settings", "/api/admin/v1/actions", "/api/admin/v1/diagnostics", "/api/admin/v1/updates"} {
		mux.Handle(path, admin(http.HandlerFunc(unscopedRoute)))
	}
	mux.HandleFunc("/api/device/v1/sync", a.sync)
	mux.HandleFunc("/api/device/v1/probe", a.probe)
}

func output(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func failure(w http.ResponseWriter, status int, message string) {
	output(w, status, map[string]string{"error": message})
}

func method(w http.ResponseWriter, allow string) {
	w.Header().Set("Allow", allow)
	failure(w, http.StatusMethodNotAllowed, "method_not_allowed")
}

func decode(w http.ResponseWriter, r *http.Request, v any) error {
	mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mt != "application/json" {
		return errors.New("JSON body required")
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 65536))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return errors.New("invalid JSON")
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return errors.New("invalid trailing JSON")
	}
	return nil
}

func revisionFromHeader(r *http.Request) (uint64, error) {
	match := strings.TrimSpace(r.Header.Get("If-Match"))
	if match == "" {
		return 0, errors.New("revision_required")
	}
	match = strings.Trim(match, `"`)
	if match == "" {
		return 0, errors.New("invalid_revision")
	}
	revision, err := strconv.ParseUint(match, 10, 64)
	if err != nil {
		return 0, errors.New("invalid_revision")
	}
	return revision, nil
}

func setETag(w http.ResponseWriter, revision uint64) {
	w.Header().Set("ETag", strconv.Quote(strconv.FormatUint(revision, 10)))
}

func apiError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, settings.ErrConflict):
		failure(w, http.StatusConflict, "revision_conflict")
	case errors.Is(err, settings.ErrUnavailable), errors.Is(err, settings.ErrBusy), errors.Is(err, settings.ErrConfirmationRequired):
		failure(w, http.StatusConflict, err.Error())
	case errors.Is(err, settings.ErrDeviceNotFound):
		failure(w, http.StatusNotFound, "device_not_found")
	case errors.Is(err, settings.ErrTooManyDevices):
		failure(w, http.StatusConflict, "device_limit_reached")
	case errors.Is(err, settings.ErrSharedURLMismatch):
		failure(w, http.StatusUnprocessableEntity, "shared_connection_urls_are_read_only")
	default:
		if strings.Contains(err.Error(), "cannot ") {
			failure(w, http.StatusServiceUnavailable, "settings_unavailable")
			return
		}
		failure(w, http.StatusUnprocessableEntity, "invalid_settings")
	}
}

func unscopedRoute(w http.ResponseWriter, r *http.Request) {
	failure(w, http.StatusBadRequest, "device_id_required")
}

func (a *API) devices(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		method(w, "GET")
		return
	}
	output(w, http.StatusOK, a.Store.Devices())
}

func (a *API) connections(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		view := a.Store.ConnectionsView()
		setETag(w, view.Revision)
		output(w, http.StatusOK, view)
	case http.MethodPatch:
		revision, err := revisionFromHeader(r)
		if err != nil {
			status := http.StatusPreconditionRequired
			if err.Error() != "revision_required" {
				status = http.StatusBadRequest
			}
			failure(w, status, err.Error())
			return
		}
		patch, err := decodeConnectionPatch(w, r)
		if err != nil {
			failure(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := a.Store.UpdateConnections(revision, patch); err != nil {
			apiError(w, err)
			return
		}
		view := a.Store.ConnectionsView()
		setETag(w, view.Revision)
		output(w, http.StatusOK, view)
	default:
		method(w, "GET, PATCH")
	}
}

func decodeConnectionPatch(w http.ResponseWriter, r *http.Request) (settings.ConnectionPatch, error) {
	var wire struct {
		Connections json.RawMessage    `json:"connections"`
		Secrets     map[string]*string `json:"secrets,omitempty"`
		Confirm     bool               `json:"confirm_disconnect,omitempty"`
	}
	if err := decode(w, r, &wire); err != nil {
		return settings.ConnectionPatch{}, err
	}
	if len(wire.Connections) == 0 || strings.TrimSpace(string(wire.Connections)) == "null" {
		return settings.ConnectionPatch{}, errors.New("complete connections object required")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(wire.Connections, &fields); err != nil || fields == nil {
		return settings.ConnectionPatch{}, errors.New("invalid connections object")
	}
	required := []string{"immich", "health", "tasks", "news", "home", "calendar", "transport"}
	if len(fields) != len(required) {
		return settings.ConnectionPatch{}, errors.New("complete connections object required")
	}
	for _, name := range required {
		value, ok := fields[name]
		if !ok {
			return settings.ConnectionPatch{}, errors.New("complete connections object required")
		}
		if name == "tasks" {
			var taskObject map[string]json.RawMessage
			if err := json.Unmarshal(value, &taskObject); err != nil || taskObject == nil || len(taskObject) != 0 {
				return settings.ConnectionPatch{}, errors.New("invalid tasks connection")
			}
			continue
		}
		var endpoint map[string]json.RawMessage
		if err := json.Unmarshal(value, &endpoint); err != nil || endpoint == nil {
			return settings.ConnectionPatch{}, errors.New("invalid connection endpoint")
		}
		baseURL, ok := endpoint["base_url"]
		if !ok || !strings.HasPrefix(strings.TrimSpace(string(baseURL)), `"`) {
			return settings.ConnectionPatch{}, errors.New("complete connection endpoint required")
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(wire.Connections))
	decoder.DisallowUnknownFields()
	var connections settings.Connections
	if err := decoder.Decode(&connections); err != nil {
		return settings.ConnectionPatch{}, errors.New("invalid connections object")
	}
	return settings.ConnectionPatch{Connections: connections, Secrets: wire.Secrets, ConfirmDisconnect: wire.Confirm}, nil
}

func (a *API) deviceRoute(w http.ResponseWriter, r *http.Request) {
	const prefix = "/api/admin/v1/devices/"
	path := strings.TrimPrefix(r.URL.Path, prefix)
	parts := strings.Split(path, "/")
	if len(parts) != 2 || !validID(parts[0]) {
		http.NotFound(w, r)
		return
	}
	id := parts[0]
	switch parts[1] {
	case "albums", "calendars", "entities":
		a.catalog(w, r, id, parts[1])
	case "settings":
		a.deviceSettings(w, r, id)
	case "actions":
		a.deviceActions(w, r, id)
	case "diagnostics":
		a.deviceDiagnostics(w, r, id)
	case "updates":
		a.deviceUpdates(w, r, id)
	default:
		http.NotFound(w, r)
	}
}

func validID(id string) bool {
	if len(id) != 32 || strings.ToLower(id) != id {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
}

func (a *API) statuses(id string) map[string]settings.Status {
	if a.Statuses == nil {
		return map[string]settings.Status{}
	}
	return a.Statuses(id)
}

func (a *API) deviceSettings(w http.ResponseWriter, r *http.Request, id string) {
	switch r.Method {
	case http.MethodGet:
		view, err := a.Store.View(id, a.statuses(id))
		if err != nil {
			apiError(w, err)
			return
		}
		setETag(w, view.Revision)
		output(w, http.StatusOK, view)
	case http.MethodPatch:
		revision, err := revisionFromHeader(r)
		if err != nil {
			status := http.StatusPreconditionRequired
			if err.Error() != "revision_required" {
				status = http.StatusBadRequest
			}
			failure(w, status, err.Error())
			return
		}
		var patch settings.Patch
		if err := decode(w, r, &patch); err != nil {
			failure(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := a.Store.Update(id, revision, patch); err != nil {
			apiError(w, err)
			return
		}
		view, err := a.Store.View(id, a.statuses(id))
		if err != nil {
			apiError(w, err)
			return
		}
		setETag(w, view.Revision)
		output(w, http.StatusOK, view)
	default:
		method(w, "GET, PATCH")
	}
}

func (a *API) deviceActions(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		method(w, "POST")
		return
	}
	var req struct {
		Action  string `json:"action"`
		Confirm bool   `json:"confirm"`
		Percent *int   `json:"percent,omitempty"`
	}
	if err := decode(w, r, &req); err != nil {
		failure(w, http.StatusBadRequest, err.Error())
		return
	}
	target := ""
	if req.Action == "ota_install" && a.Updates != nil {
		view, e := a.Store.View(id, nil)
		if e == nil {
			target, _ = a.Updates(id, view.Device.Firmware)["available_version"].(string)
		}
	}
	command, err := a.Store.EnqueueOptions(id, req.Action, req.Confirm, req.Percent, target)
	if err != nil {
		apiError(w, err)
		return
	}
	output(w, http.StatusAccepted, map[string]any{"command": command})
}

func (a *API) deviceUpdates(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodGet {
		method(w, "GET")
		return
	}
	view, err := a.Store.View(id, a.statuses(id))
	if err != nil {
		apiError(w, err)
		return
	}
	result := map[string]any{}
	if a.Updates != nil {
		result = a.Updates(id, view.Device.Firmware)
	}
	if result == nil {
		result = map[string]any{}
	}
	history, err := a.Store.OTAHistory(id)
	if err != nil {
		apiError(w, err)
		return
	}
	result["history"] = history
	result["device_connected"] = view.Device.Connected
	output(w, http.StatusOK, result)
}

func (a *API) deviceDiagnostics(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodGet {
		method(w, "GET")
		return
	}
	view, err := a.Store.View(id, a.statuses(id))
	if err != nil {
		apiError(w, err)
		return
	}
	data := map[string]any{
		"device_id": id, "settings_revision": view.Revision, "applied_revision": view.Device.AppliedRevision,
		"device": map[string]any{
			"connected": view.Device.Connected, "firmware": view.Device.Firmware,
			"model": view.Device.Model, "width": view.Device.Width, "height": view.Device.Height,
			"uptime_seconds": view.Device.UptimeSeconds, "time_synced": view.Device.TimeSynced,
		},
		"statuses": view.Statuses,
	}
	w.Header().Set("Content-Disposition", `attachment; filename="homedash-diagnostics.json"`)
	output(w, http.StatusOK, data)
}

func (a *API) authorizedDevice(r *http.Request) bool {
	return a.DeviceKey != "" && subtle.ConstantTimeCompare([]byte(r.Header.Get("X-API-Key")), []byte(a.DeviceKey)) == 1
}

func (a *API) sync(w http.ResponseWriter, r *http.Request) {
	if !a.authorizedDevice(r) {
		failure(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if r.Method != http.MethodPost {
		method(w, "POST")
		return
	}
	headerID := r.Header.Get("X-Device-ID")
	if !validID(headerID) {
		failure(w, http.StatusUnauthorized, "device_identity_required")
		return
	}
	var d settings.Device
	if err := decode(w, r, &d); err != nil {
		failure(w, http.StatusBadRequest, err.Error())
		return
	}
	if d.DeviceID != headerID {
		failure(w, http.StatusUnauthorized, "device_identity_mismatch")
		return
	}
	response, err := a.Store.Sync(d)
	if err != nil {
		if errors.Is(err, settings.ErrTooManyDevices) {
			apiError(w, err)
			return
		}
		failure(w, http.StatusUnprocessableEntity, "invalid_device_state")
		return
	}
	output(w, http.StatusOK, response)
}

// Probe does not register a profile or touch heartbeat, revisions, commands,
// pending network state, or applied-revision acknowledgments.
func (a *API) probe(w http.ResponseWriter, r *http.Request) {
	if !a.authorizedDevice(r) {
		failure(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if r.Method != http.MethodGet {
		method(w, "GET")
		return
	}
	id := r.Header.Get("X-Device-ID")
	if !validID(id) {
		failure(w, http.StatusUnauthorized, "device_identity_required")
		return
	}
	output(w, http.StatusOK, map[string]any{"device_id": id, "bridge_id": a.Store.BridgeID(), "version": 1})
}
