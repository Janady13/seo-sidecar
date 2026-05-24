package seo

import (
	"encoding/json"
	"os"
	"sync"
	"time"
)

// StateCache tracks what the system has already processed.
type StateCache struct {
	mu       sync.RWMutex
	entries  map[string]*CacheEntry
	filePath string
}

// NewStateCache creates a new state cache.
func NewStateCache(filePath string) *StateCache {
	cache := &StateCache{
		entries:  make(map[string]*CacheEntry),
		filePath: filePath,
	}

	// Load from disk if exists
	cache.load()

	return cache
}

// Get retrieves a cache entry by URL.
func (c *StateCache) Get(url string) *CacheEntry {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, ok := c.entries[url]
	if !ok {
		return nil
	}

	// Return a copy to prevent mutation
	copy := *entry
	return &copy
}

// GetBySchemaKey retrieves a cache entry by schema key.
func (c *StateCache) GetBySchemaKey(schemaKey string) *CacheEntry {
	c.mu.RLock()
	defer c.mu.RUnlock()

	for _, entry := range c.entries {
		if entry.SchemaKey == schemaKey {
			copy := *entry
			return &copy
		}
	}

	return nil
}

// Set updates a cache entry.
func (c *StateCache) Set(entry *CacheEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.entries[entry.URL] = entry
}

// UpdateAfterPush updates cache after a successful push.
func (c *StateCache) UpdateAfterPush(url string, schemaKey string, schemaHash string, version int) {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.entries[url]
	if !ok {
		entry = &CacheEntry{
			URL:       url,
			SchemaKey: schemaKey,
		}
		c.entries[url] = entry
	}

	entry.LastSchemaHash = schemaHash
	entry.LastPushedVersion = version
	entry.LastPushTime = time.Now()
}

// UpdateContentHash updates the content hash for a URL.
func (c *StateCache) UpdateContentHash(url string, hash string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.entries[url]
	if !ok {
		entry = &CacheEntry{
			URL: url,
		}
		c.entries[url] = entry
	}

	entry.LastContentHash = hash
}

// UpdateValidation updates validation result.
func (c *StateCache) UpdateValidation(url string, passed bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if entry, ok := c.entries[url]; ok {
		entry.LastValidation = passed
	}
}

// UpdateDiffScore updates the last diff score.
func (c *StateCache) UpdateDiffScore(url string, score float64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if entry, ok := c.entries[url]; ok {
		entry.LastDiffScore = score
	}
}

// HasChanged checks if content has changed since last seen.
func (c *StateCache) HasChanged(url string, newHash string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, ok := c.entries[url]
	if !ok {
		return true // Never seen, counts as changed
	}

	return entry.LastContentHash != newHash
}

// ShouldProcess checks if a URL should be processed based on cache state.
func (c *StateCache) ShouldProcess(url string, contentHash string, cooldownMinutes int) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, ok := c.entries[url]
	if !ok {
		return true // Never processed
	}

	// Content changed
	if entry.LastContentHash != contentHash {
		return true
	}

	// Cooldown elapsed
	if cooldownMinutes > 0 {
		cooldown := time.Duration(cooldownMinutes) * time.Minute
		if time.Since(entry.LastPushTime) > cooldown {
			return true
		}
	}

	return false
}

// Clear removes all entries.
func (c *StateCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.entries = make(map[string]*CacheEntry)
}

// Remove removes a specific entry.
func (c *StateCache) Remove(url string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.entries, url)
}

// List returns all entries.
func (c *StateCache) List() []*CacheEntry {
	c.mu.RLock()
	defer c.mu.RUnlock()

	result := make([]*CacheEntry, 0, len(c.entries))
	for _, entry := range c.entries {
		copy := *entry
		result = append(result, &copy)
	}

	return result
}

// Save persists the cache to disk.
func (c *StateCache) Save() error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.filePath == "" {
		return nil
	}

	data, err := json.MarshalIndent(c.entries, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(c.filePath, data, 0644)
}

// load reads the cache from disk.
func (c *StateCache) load() error {
	if c.filePath == "" {
		return nil
	}

	data, err := os.ReadFile(c.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // No cache file yet
		}
		return err
	}

	return json.Unmarshal(data, &c.entries)
}

// Stats returns cache statistics.
func (c *StateCache) Stats() CacheStats {
	c.mu.RLock()
	defer c.mu.RUnlock()

	stats := CacheStats{
		TotalEntries: len(c.entries),
	}

	for _, entry := range c.entries {
		if entry.LastValidation {
			stats.ValidSchemas++
		}
		if !entry.LastPushTime.IsZero() {
			stats.PushedSchemas++
		}
	}

	return stats
}

// CacheStats contains cache statistics.
type CacheStats struct {
	TotalEntries  int
	ValidSchemas  int
	PushedSchemas int
}
