package queue

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/lavr/express-botx/internal/config"
	vlog "github.com/lavr/express-botx/internal/log"
)

// BuildCatalogSnapshot creates a CatalogSnapshot from the config's public data.
// Only public fields are included: bot name/host/id and chat name/id/bot/default.
// Secrets (secret, token) are never included.
func BuildCatalogSnapshot(cfg *config.Config) *CatalogSnapshot {
	now := time.Now().UTC()
	return &CatalogSnapshot{
		Type:        "catalog.snapshot",
		Revision:    fmt.Sprintf("%s:%d", now.Format(time.RFC3339), len(cfg.BotEntries())+len(cfg.ChatEntries())),
		GeneratedAt: now,
		Bots:        cfg.BotEntries(),
		Chats:       cfg.ChatEntries(),
	}
}

// ResolvedRoute is the result of resolving aliases through the catalog.
type ResolvedRoute struct {
	BotID           string
	ChatID          string
	Host            string
	BotName         string
	ChatAlias       string
	CatalogRevision string
}

// ResolveBot finds a bot by alias name in the snapshot.
func (s *CatalogSnapshot) ResolveBot(alias string) (config.BotEntry, error) {
	for _, b := range s.Bots {
		if b.Name == alias {
			return b, nil
		}
	}
	names := make([]string, 0, len(s.Bots))
	for _, b := range s.Bots {
		names = append(names, b.Name)
	}
	if len(names) == 0 {
		return config.BotEntry{}, fmt.Errorf("unknown bot %q (catalog has no bots)", alias)
	}
	return config.BotEntry{}, fmt.Errorf("unknown bot %q, available in catalog: %s", alias, joinSorted(names))
}

// ResolveChat finds a chat by alias name in the snapshot.
func (s *CatalogSnapshot) ResolveChat(alias string) (config.ChatEntry, error) {
	for _, c := range s.Chats {
		if c.Name == alias {
			return c, nil
		}
	}
	names := make([]string, 0, len(s.Chats))
	for _, c := range s.Chats {
		names = append(names, c.Name)
	}
	if len(names) == 0 {
		return config.ChatEntry{}, fmt.Errorf("unknown chat %q (catalog has no chats)", alias)
	}
	return config.ChatEntry{}, fmt.Errorf("unknown chat alias %q, available in catalog: %s", alias, joinSorted(names))
}

// DefaultChat returns the chat marked as default in the snapshot, if any.
func (s *CatalogSnapshot) DefaultChat() (config.ChatEntry, bool) {
	for _, c := range s.Chats {
		if c.Default {
			return c, true
		}
	}
	return config.ChatEntry{}, false
}

// ResolveBotByID finds a bot by its UUID in the snapshot.
func (s *CatalogSnapshot) ResolveBotByID(botID string) (config.BotEntry, bool) {
	for _, b := range s.Bots {
		if b.ID == botID {
			return b, true
		}
	}
	return config.BotEntry{}, false
}

func joinSorted(ss []string) string {
	sort.Strings(ss)
	return strings.Join(ss, ", ")
}

// CatalogCache is a thread-safe local cache for catalog snapshots.
// It stores the latest snapshot in memory and optionally persists to disk.
type CatalogCache struct {
	mu       sync.RWMutex
	snapshot *CatalogSnapshot
	maxAge   time.Duration
	path     string // disk cache path (empty = no disk persistence)
}

// NewCatalogCache creates a new catalog cache.
// If path is non-empty, the cache is loaded from disk on creation.
// maxAge controls how long a cached snapshot is considered valid (0 = no expiry).
func NewCatalogCache(path string, maxAge time.Duration) *CatalogCache {
	cc := &CatalogCache{
		path:   path,
		maxAge: maxAge,
	}
	if path != "" {
		cc.loadFromDisk()
	}
	return cc
}

// Update stores a new snapshot in the cache and optionally persists to disk.
func (cc *CatalogCache) Update(snap *CatalogSnapshot) {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	cc.snapshot = snap
	if cc.path != "" {
		cc.saveToDisk(snap)
	}
}

// Get returns the current snapshot if it exists and is not expired.
// Returns nil if no snapshot is available or if it has exceeded maxAge.
func (cc *CatalogCache) Get() *CatalogSnapshot {
	cc.mu.RLock()
	defer cc.mu.RUnlock()
	if cc.snapshot == nil {
		return nil
	}
	if cc.maxAge > 0 && time.Since(cc.snapshot.GeneratedAt) > cc.maxAge {
		return nil
	}
	return cc.snapshot
}

// HasValidSnapshot returns true if the cache contains a non-expired snapshot.
func (cc *CatalogCache) HasValidSnapshot() bool {
	return cc.Get() != nil
}

func (cc *CatalogCache) loadFromDisk() {
	data, err := os.ReadFile(cc.path)
	if err != nil {
		return // file doesn't exist or unreadable — start with empty cache
	}
	var snap CatalogSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return // corrupt file — start with empty cache
	}
	cc.snapshot = &snap
}

func (cc *CatalogCache) saveToDisk(snap *CatalogSnapshot) {
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return
	}
	if dir := filepath.Dir(cc.path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			vlog.Info("catalog: failed to create cache directory %s: %v", dir, err)
			return
		}
	}
	// Write atomically via temp file
	tmp := cc.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		vlog.Info("catalog: failed to write cache file %s: %v", tmp, err)
		return
	}
	if err := os.Rename(tmp, cc.path); err != nil {
		vlog.Info("catalog: failed to rename cache file %s -> %s: %v", tmp, cc.path, err)
	}
}
