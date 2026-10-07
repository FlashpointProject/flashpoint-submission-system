// Package appcache provides bounded, service-owned read snapshots coordinated
// with database commits. It contains no database or HTTP dependencies.
package appcache

import (
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

// Secret identifies a session without retaining its bearer credential.
func Secret(secret string) Dependency {
	return Dependency(fmt.Sprintf("session-secret:%x", sha256.Sum256([]byte(secret))))
}

// Dependency identifies application data, e.g. submission:42 or user:9.
type Dependency string

func Key(kind string, id any) Dependency { return Dependency(fmt.Sprint(kind, ":", id)) }

// Coordinator fences commit/publication without holding a mutex across SQL.
// A global load generation avoids retaining an unbounded map of deleted keys.
// Resident entries are invalidated selectively by their dependencies.
type Coordinator struct {
	mu         sync.Mutex
	generation uint64
	caches     []invalidator
	pending    int
	settled    chan struct{}
}
type invalidator interface{ invalidate(map[Dependency]bool) }

// Commit fences affected readers while the transaction commits. An ambiguous failure
// also invalidates: the database may have committed before the connection broke.
func (c *Coordinator) Commit(changes []Dependency, commit func() error) error {
	if c == nil || len(changes) == 0 {
		return commit()
	}
	changed := make(map[Dependency]bool, len(changes))
	for _, d := range changes {
		changed[d] = true
	}
	c.mu.Lock()
	if c.pending == 0 {
		c.settled = make(chan struct{})
	}
	c.pending++
	c.generation++
	for _, cache := range c.caches {
		cache.invalidate(changed)
	}
	c.mu.Unlock()
	// No application mutex is held across SQL. Unrelated resident entries remain
	// readable; misses wait until commits settle before opening their snapshot.
	defer func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.generation++
		for _, cache := range c.caches {
			cache.invalidate(changed)
		}
		c.pending--
		if c.pending == 0 {
			close(c.settled)
		}
	}()
	return commit()
}

type Stats struct {
	Hits, Loads, Shared, Errors, Invalidations, Evictions uint64
	Entries, Bytes                                        int
	LoadDuration                                          time.Duration
}
type entry struct {
	key     string
	data    []byte
	deps    []Dependency
	expires time.Time
}
type Snapshot[T any] struct {
	Value        T
	Dependencies []Dependency
	Expires      time.Time
}

// Cache stores encoded typed data, not rendered responses. Decoding each result
// gives callers independent slices/maps/pointers; resident bytes are immutable.
type Cache[T any] struct {
	coord                *Coordinator
	entries              map[string]*list.Element
	lru                  *list.List
	maxEntries, maxBytes int
	stats                Stats
	flight               singleflight.Group
	now                  func() time.Time
}

func New[T any](c *Coordinator, maxEntries, maxBytes int) *Cache[T] {
	if c == nil || maxEntries < 1 || maxBytes < 1 {
		panic("invalid cache configuration")
	}
	v := &Cache[T]{coord: c, entries: make(map[string]*list.Element), lru: list.New(), maxEntries: maxEntries, maxBytes: maxBytes, now: time.Now}
	c.mu.Lock()
	c.caches = append(c.caches, v)
	c.mu.Unlock()
	return v
}
func (c *Cache[T]) remove(e *list.Element) {
	v := e.Value.(*entry)
	delete(c.entries, v.key)
	c.stats.Bytes -= len(v.data)
	c.lru.Remove(e)
}
func (c *Cache[T]) invalidate(changed map[Dependency]bool) {
	for _, e := range c.entries {
		for _, d := range e.Value.(*entry).deps {
			if changed[d] {
				c.remove(e)
				c.stats.Invalidations++
				break
			}
		}
	}
}
func (c *Cache[T]) lookup(key string) []byte {
	e := c.entries[key]
	if e == nil {
		return nil
	}
	v := e.Value.(*entry)
	if !v.expires.IsZero() && !c.now().Before(v.expires) {
		c.remove(e)
		return nil
	}
	c.lru.MoveToFront(e)
	return v.data
}
func (c *Cache[T]) Stats() Stats {
	c.coord.mu.Lock()
	defer c.coord.mu.Unlock()
	s := c.stats
	s.Entries = len(c.entries)
	return s
}

type loaded struct {
	data       []byte
	generation uint64
	expires    time.Time
}

func (c *Cache[T]) Get(ctx context.Context, key string, load func(context.Context) (Snapshot[T], error)) (T, error) {
	var zero T
	// Bound retries as well as each shared loader when writes remain continuous.
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	for {
		if err := ctx.Err(); err != nil {
			return zero, err
		}
		c.coord.mu.Lock()
		generation := c.coord.generation
		data := c.lookup(key)
		if data == nil && c.coord.pending > 0 {
			settled := c.coord.settled
			c.coord.mu.Unlock()
			select {
			case <-ctx.Done():
				return zero, ctx.Err()
			case <-settled:
				continue
			}
		}
		if data != nil {
			c.stats.Hits++
		}
		c.coord.mu.Unlock()
		if data != nil {
			var v T
			err := json.Unmarshal(data, &v)
			return v, err
		}
		ch := c.flight.DoChan(fmt.Sprintf("%d:%s", generation, key), func() (any, error) {
			c.coord.mu.Lock()
			if generation != c.coord.generation {
				c.coord.mu.Unlock()
				return loaded{generation: generation}, nil
			}
			if data := c.lookup(key); data != nil {
				c.coord.mu.Unlock()
				return loaded{data: data, generation: generation}, nil
			}
			c.stats.Loads++
			c.coord.mu.Unlock()
			work, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			defer cancel()
			start := time.Now()
			snapshot, err := load(work)
			if err == nil {
				err = work.Err()
			}
			var data []byte
			if err == nil {
				data, err = json.Marshal(snapshot.Value)
			}
			c.coord.mu.Lock()
			defer c.coord.mu.Unlock()
			c.stats.LoadDuration += time.Since(start)
			if err != nil {
				c.stats.Errors++
				return nil, err
			}
			if generation == c.coord.generation && (snapshot.Expires.IsZero() || c.now().Before(snapshot.Expires)) && len(data) <= c.maxBytes {
				if e := c.entries[key]; e != nil {
					c.remove(e)
				}
				e := c.lru.PushFront(&entry{key: key, data: data, deps: append([]Dependency(nil), snapshot.Dependencies...), expires: snapshot.Expires})
				c.entries[key] = e
				c.stats.Bytes += len(data)
				for len(c.entries) > c.maxEntries || c.stats.Bytes > c.maxBytes {
					c.remove(c.lru.Back())
					c.stats.Evictions++
				}
			}
			return loaded{data: data, generation: generation, expires: snapshot.Expires}, nil
		})
		select {
		case <-ctx.Done():
			return zero, ctx.Err()
		case result := <-ch:
			if result.Err != nil {
				return zero, result.Err
			}
			v := result.Val.(loaded)
			c.coord.mu.Lock()
			current := c.coord.generation
			if result.Shared {
				c.stats.Shared++
			}
			expired := !v.expires.IsZero() && !c.now().Before(v.expires)
			c.coord.mu.Unlock()
			if current != v.generation || v.data == nil || expired {
				continue
			}
			var value T
			err := json.Unmarshal(v.data, &value)
			return value, err
		}
	}
}
