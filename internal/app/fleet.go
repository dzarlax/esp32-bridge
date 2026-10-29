package app

import (
	"errors"
	"esp32-bridge/internal/config"
	"esp32-bridge/internal/settings"
	"net/http"
	"regexp"
	"sync/atomic"
)

type fleetState struct{ nodes map[string]*Manager }

// Fleet publishes a complete collection of independent display runtimes atomically.
type Fleet struct {
	current atomic.Pointer[fleetState]
	strict  atomic.Bool
}

func NewFleet(c *config.Config, client *http.Client) (*Fleet, error) {
	legacy, err := New(c, client)
	if err != nil {
		return nil, err
	}
	f := &Fleet{}
	f.current.Store(&fleetState{nodes: map[string]*Manager{"": legacy}})
	return f, nil
}
func (f *Fleet) EnableManaged() { f.strict.Store(true) }
func (f *Fleet) Prepare(profiles map[string]settings.Values, secrets map[string]string) (func(), func(), error) {
	old := f.current.Load()
	legacy := old.nodes[""]
	if len(profiles) > 9 {
		return nil, nil, errors.New("too many displays")
	}
	if _, ok := profiles[""]; !ok {
		return nil, nil, errors.New("legacy defaults missing")
	}
	next := &fleetState{nodes: map[string]*Manager{}}
	created := map[*runtime]bool{}
	abort := func() {
		for r := range created {
			r.cancel()
		}
	}
	for id, v := range profiles {
		if id != "" && !deviceID.MatchString(id) {
			abort()
			return nil, nil, errors.New("invalid device identity")
		}
		m := &Manager{defaults: legacy.defaults, client: legacy.client, started: legacy.started, shared: legacy.shared, managed: id != ""}
		previous := old.nodes[id]
		if previous != nil {
			m.current.Store(previous.current.Load())
		}
		cfg, err := m.configFor(v, secrets)
		if err != nil {
			abort()
			return nil, nil, err
		}
		r, err := m.build(cfg, v)
		if err != nil {
			abort()
			return nil, nil, err
		}
		if previous == nil || previous.current.Load().photos != r.photos {
			created[r] = true
		}
		m.current.Store(r)
		next.nodes[id] = m
	}
	commit := func() {
		f.current.Store(next)
		for r := range created {
			r.start()
		}
		for id, m := range old.nodes {
			replacement := next.nodes[id]
			if replacement == nil || replacement.current.Load().photos != m.current.Load().photos {
				m.current.Load().cancel()
			}
		}
	}
	return commit, abort, nil
}

var deviceID = regexp.MustCompile(`^[0-9a-f]{32}$`)

func (f *Fleet) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	state := f.current.Load()
	id := ""
	if f.strict.Load() && r.URL.Path != "/health" {
		id = r.Header.Get("X-Device-ID")
		if id != "" && !deviceID.MatchString(id) {
			http.Error(w, "invalid device identity", http.StatusBadRequest)
			return
		}
	}
	m := state.nodes[id]
	if m == nil {
		http.Error(w, "device must sync first", http.StatusNotFound)
		return
	}
	m.ServeHTTP(w, r)
}
func (f *Fleet) Statuses(id string) map[string]settings.Status {
	if m := f.current.Load().nodes[id]; m != nil {
		return m.Statuses()
	}
	return map[string]settings.Status{}
}
func (f *Fleet) Updates(id, current string) map[string]any {
	if m := f.current.Load().nodes[id]; m != nil {
		return m.Updates(current)
	}
	return map[string]any{"error": "unknown_device"}
}
