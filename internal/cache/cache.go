package cache

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"
)

type entry struct {
	data                  json.RawMessage
	expiresAt, timeStored time.Time
}
type call struct {
	done       chan struct{}
	data       json.RawMessage
	at         time.Time
	err        error
	generation uint64
}
type Cache struct {
	mu         sync.RWMutex
	entries    map[string]entry
	pending    map[string]*call
	generation map[string]uint64
}

func New() *Cache {
	return &Cache{entries: map[string]entry{}, pending: map[string]*call{}, generation: map[string]uint64{}}
}
func (c *Cache) Get(key string) (json.RawMessage, bool) {
	data, _, ok := c.GetMeta(key)
	return data, ok
}
func (c *Cache) GetMeta(key string) (json.RawMessage, time.Time, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.entries[key]
	if !ok || time.Now().After(e.expiresAt) {
		return nil, time.Time{}, false
	}
	return e.data, e.timeStored, true
}
func (c *Cache) Set(key string, data json.RawMessage, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now().UTC()
	c.entries[key] = entry{data: data, expiresAt: now.Add(ttl), timeStored: now}
}
func (c *Cache) Delete(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, key)
	c.generation[key]++
}

// Fetch coalesces simultaneous requests for an identical upstream configuration.
// Invalidation starts a new generation so an older in-flight result cannot
// repopulate the cache after a device action changes that source.
func (c *Cache) Fetch(key string, ttl time.Duration, fetch func() (json.RawMessage, error)) (json.RawMessage, time.Time, error) {
	return c.FetchContext(context.Background(), key, ttl, fetch)
}

func (c *Cache) FetchContext(ctx context.Context, key string, ttl time.Duration, fetch func() (json.RawMessage, error)) (json.RawMessage, time.Time, error) {
	c.mu.Lock()
	if e, ok := c.entries[key]; ok && time.Now().Before(e.expiresAt) {
		c.mu.Unlock()
		return e.data, e.timeStored, nil
	}
	gen := c.generation[key]
	if existing := c.pending[key]; existing != nil && existing.generation == gen {
		c.mu.Unlock()
		select {
		case <-existing.done:
			return existing.data, existing.at, existing.err
		case <-ctx.Done():
			return nil, time.Time{}, ctx.Err()
		}
	}
	current := &call{done: make(chan struct{}), generation: gen}
	c.pending[key] = current
	c.mu.Unlock()
	var data json.RawMessage
	var err error
	func() {
		defer func() {
			if recover() != nil {
				err = errors.New("source fetch panic")
			}
		}()
		data, err = fetch()
	}()
	now := time.Now().UTC()
	c.mu.Lock()
	current.data, current.err, current.at = data, err, now
	if err == nil && c.generation[key] == gen {
		for k, e := range c.entries {
			if now.After(e.expiresAt) {
				delete(c.entries, k)
			}
		}
		c.entries[key] = entry{data: data, expiresAt: now.Add(ttl), timeStored: now}
	}
	if c.pending[key] == current {
		delete(c.pending, key)
	}
	close(current.done)
	c.mu.Unlock()
	return data, now, err
}

// DeletePrefix invalidates every selection sharing an upstream identity,
// including in-flight requests which must not restore pre-action data.
func (c *Cache) DeletePrefix(prefix string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	keys := map[string]bool{}
	for key := range c.entries {
		if strings.HasPrefix(key, prefix) {
			keys[key] = true
		}
	}
	for key := range c.pending {
		if strings.HasPrefix(key, prefix) {
			keys[key] = true
		}
	}
	for key := range keys {
		delete(c.entries, key)
		c.generation[key]++
	}
}

// FetchRecentContext retains the original source timestamp while coalescing refreshes.
func (c *Cache) FetchRecentContext(ctx context.Context, key string, ttl, maxAge time.Duration, fetch func() (json.RawMessage, error)) (json.RawMessage, time.Time, error) {
	c.mu.Lock()
	if e, ok := c.entries[key]; ok {
		if time.Since(e.timeStored) < maxAge {
			c.mu.Unlock()
			return e.data, e.timeStored, nil
		}
		delete(c.entries, key)
		c.generation[key]++
	}
	c.mu.Unlock()
	if ttl < maxAge {
		ttl = maxAge
	}
	return c.FetchContext(ctx, key, ttl, fetch)
}
