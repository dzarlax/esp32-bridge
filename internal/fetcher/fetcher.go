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

type Orchestrator struct {
	fetchers []Fetcher
	cache    *cache.Cache
	timeout  time.Duration
}

func NewOrchestrator(fetchers []Fetcher, c *cache.Cache, timeout time.Duration) *Orchestrator {
	return &Orchestrator{fetchers: fetchers, cache: c, timeout: timeout}
}

func (o *Orchestrator) FetchAll() map[string]json.RawMessage {
	results := make(map[string]json.RawMessage, len(o.fetchers))
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, f := range o.fetchers {
		name := f.Name()

		if data, ok := o.cache.Get(name); ok {
			mu.Lock()
			results[name] = data
			mu.Unlock()
			continue
		}

		wg.Add(1)
		go func(f Fetcher) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					log.Printf("[%s] panic: %v", f.Name(), r)
				}
			}()

			ctx, cancel := context.WithTimeout(context.Background(), o.timeout)
			defer cancel()

			data, err := f.Fetch(ctx)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				log.Printf("[%s] error: %v", f.Name(), err)
				results[f.Name()] = nil
				return
			}
			o.cache.Set(f.Name(), data, f.TTL())
			results[f.Name()] = data
		}(f)
	}

	wg.Wait()
	return results
}
