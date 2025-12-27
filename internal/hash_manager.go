package internal

import (
	"sync"
	"time"
	"context"
	"log"
)

type HashType int

const (
	HashTrack HashType = iota
	HashAlbum
	HashPlaylist
)

func (ht HashType) String() string {
	switch ht {
	case HashTrack:
		return "track"
	case HashAlbum:
		return "album"
	case HashPlaylist:
		return "playlist"
	default:
		return "unknown"
	}
}

type HashEntry struct {
	Hash      string
	UpdatedAt time.Time
}

type HashManager struct {
	entries map[HashType]*HashEntry
	mu      sync.RWMutex
}

func NewHashManager() *HashManager {
	return &HashManager{
		entries: map[HashType]*HashEntry{
			HashTrack:    {},
			HashAlbum:    {},
			HashPlaylist: {},
		},
	}
}

func (hm *HashManager) GetHash(ht HashType) (string, bool) {
	hm.mu.RLock()
	defer hm.mu.RUnlock()
	entry := hm.entries[ht]
	if entry == nil || entry.Hash == "" {
		return "", false
	}
	if time.Since(entry.UpdatedAt) > 6*time.Hour {
		return entry.Hash, false // stale
	}
	return entry.Hash, true
}

func (hm *HashManager) SetHash(ht HashType, hash string) {
	hm.mu.Lock()
	defer hm.mu.Unlock()
	hm.entries[ht] = &HashEntry{Hash: hash, UpdatedAt: time.Now()}
}

// UpdateHashIfNeeded checks if the hash is stale and updates it by scraping if needed.
func (hm *HashManager) UpdateHashIfNeeded(ctx context.Context, ht HashType, scraper func(context.Context, HashType) (string, error)) (string, error) {
	hash, fresh := hm.GetHash(ht)
	if fresh {
		return hash, nil
	}
	newHash, err := scraper(ctx, ht)
	if err != nil {
		log.Printf("[hashmanager] failed to update %s hash: %v", ht.String(), err)
		if hash != "" {
			return hash, nil // fallback to old hash
		}
		return "", err
	}
	hm.SetHash(ht, newHash)
	return newHash, nil
}
