package fetcher

import (
	"context"
	"encoding/json"
	"log"
	"sync"
	"time"

	"esp32-bridge/internal/cache"
)

type Fetcher interface {
	Name() string
	TTL() time.Duration
	Fetch(ctx context.Context) (json.RawMessage, error)
}

type Status struct {
	State       string
	LastSuccess time.Time
}

type Orchestrator struct {
	statusMu sync.Mutex
	statuses map[string]Status
	fetchers []Fetcher
	cache    *cache.Cache
	timeout  time.Duration
}

func NewOrchestrator(fetchers []Fetcher, c *cache.Cache, timeout time.Duration) *Orchestrator {
	return &Orchestrator{fetchers: fetchers, cache: c, timeout: timeout, statuses: map[string]Status{}}
}

func (o *Orchestrator) FetchAll() map[string]json.RawMessage {
	results, _ := o.FetchAllWithStatus()
	return results
}

// FetchAllWithStatus keeps acquisition metadata paired with this response's data.
func (o *Orchestrator) FetchAllWithStatus() (map[string]json.RawMessage, map[string]Status) {
	states := make(map[string]Status, len(o.fetchers))
	for _, f := range o.fetchers {
		states[f.Name()] = Status{State: "error"}
	}
	results := make(map[string]json.RawMessage, len(o.fetchers))
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, f := range o.fetchers {
		name := f.Name()

		if data, at, ok := o.cache.GetMeta(cacheKey(f)); ok {
			o.recordAt(name, at)
			mu.Lock()
			results[name] = data
			states[name] = Status{State: "ready", LastSuccess: at}
			mu.Unlock()
			continue
		}

		wg.Add(1)
		go func(f Fetcher) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					log.Printf("[%s] fetch panic", f.Name())
				}
			}()

			ctx, cancel := context.WithTimeout(context.Background(), o.timeout)
			defer cancel()

			data, at, err := o.cache.Fetch(cacheKey(f), f.TTL(), func() (json.RawMessage, error) { return f.Fetch(ctx) })
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				o.record(f.Name(), false)
				log.Printf("[%s] fetch failed", f.Name())
				results[f.Name()] = nil
				states[f.Name()] = Status{State: "error"}
				return
			}
			o.recordAt(f.Name(), at)
			results[f.Name()] = data
			states[f.Name()] = Status{State: "ready", LastSuccess: at}
		}(f)
	}

	wg.Wait()
	return results, states
}

func (o *Orchestrator) Invalidate(name string) {
	for _, f := range o.fetchers {
		if f.Name() == name {
			o.invalidateFetcher(f)
		}
	}
}

// FetchOne fetches a single named fetcher, bypassing cache, and updates it.
func (o *Orchestrator) FetchOne(name string) json.RawMessage {
	return o.FetchOneContext(context.Background(), name)
}

func (o *Orchestrator) FetchOneContext(parent context.Context, name string) json.RawMessage {
	for _, f := range o.fetchers {
		if f.Name() == name {
			o.invalidateFetcher(f)
			ctx, cancel := context.WithTimeout(parent, o.timeout)
			defer cancel()
			data, at, err := o.cache.FetchContext(ctx, cacheKey(f), f.TTL(), func() (json.RawMessage, error) { return f.Fetch(ctx) })
			if err != nil {
				o.record(name, false)
				log.Printf("[%s] fetch failed", name)
				return nil
			}
			o.recordAt(name, at)
			return data
		}
	}
	return nil
}
func cacheKey(f Fetcher) string {
	if keyed, ok := f.(interface{ CacheKey() string }); ok {
		return keyed.CacheKey()
	}
	return f.Name()
}
func (o *Orchestrator) recordAt(name string, at time.Time) {
	o.statusMu.Lock()
	defer o.statusMu.Unlock()
	previous := o.statuses[name]
	if previous.LastSuccess.IsZero() || at.After(previous.LastSuccess) {
		previous.State = "ready"
		previous.LastSuccess = at
		o.statuses[name] = previous
	}
}

func (o *Orchestrator) record(name string, success bool) {
	o.statusMu.Lock()
	defer o.statusMu.Unlock()
	previous := o.statuses[name]
	if success {
		previous.State = "ready"
		previous.LastSuccess = time.Now().UTC()
	} else {
		previous.State = "error"
	}
	o.statuses[name] = previous
}
func (o *Orchestrator) Statuses() map[string]Status {
	o.statusMu.Lock()
	defer o.statusMu.Unlock()
	out := map[string]Status{}
	for k, v := range o.statuses {
		out[k] = v
	}
	return out
}

func (o *Orchestrator) invalidateFetcher(f Fetcher) {
	if grouped, ok := f.(interface{ InvalidationPrefix() string }); ok && grouped.InvalidationPrefix() != "" {
		o.cache.DeletePrefix(grouped.InvalidationPrefix())
		return
	}
	o.cache.Delete(cacheKey(f))
}

// FetchRecentContext returns source data with its original acquisition time.
func (o *Orchestrator) FetchRecentContext(parent context.Context, name string, maxAge time.Duration) (json.RawMessage, time.Time) {
	for _, f := range o.fetchers {
		if f.Name() != name {
			continue
		}
		key := cacheKey(f)
		ctx, cancel := context.WithTimeout(parent, o.timeout)
		defer cancel()
		data, at, err := o.cache.FetchRecentContext(ctx, key, f.TTL(), maxAge, func() (json.RawMessage, error) { return f.Fetch(ctx) })
		if err != nil {
			o.record(name, false)
			return nil, time.Time{}
		}
		o.recordAt(name, at)
		return data, at
	}
	return nil, time.Time{}
}
