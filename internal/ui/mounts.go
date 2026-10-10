package ui

import (
	"encoding/json"
	"errors"
	"os"
	"sync"
	"time"
)

// Trophy heads: when a block is found, a signed-in user may mount the head
// of the best creature dropped since the previous block on the trophy wall
// at the back of the mine. Decisions persist in <data_dir>/mounts-<coin>.json.

// Mount is one head on the trophy wall.
type Mount struct {
	BlockHeight int64     `json:"block_height"`
	BlockHash   string    `json:"block_hash"`
	JobHeight   int64     `json:"job_height"`
	Tier        int       `json:"tier"`
	Rarity      string    `json:"rarity"`
	Creature    string    `json:"creature"`
	Species     *int      `json:"species,omitempty"` // nil: mounted before species existed (the tier's first creature, or the Dragon)
	Pct         float64   `json:"pct_of_network"`
	Variant     int       `json:"variant"`
	MountedAt   time.Time `json:"mounted_at"`
}

// MountOffer is the head the user can mount for the newest found block.
type MountOffer struct {
	BlockHeight int64   `json:"block_height"`
	BlockHash   string  `json:"block_hash"`
	JobHeight   int64   `json:"job_height"`
	Tier        int     `json:"tier"`
	Rarity      string  `json:"rarity"`
	Creature    string  `json:"creature"`
	Species     *int    `json:"species,omitempty"`
	Pct         float64 `json:"pct_of_network"`
	Variant     int     `json:"variant"`
}

type mountFile struct {
	Mounts  []Mount         `json:"mounts"`
	Decided map[string]bool `json:"decided"` // block hash -> answered (mounted or skipped)
}

type mountStore struct {
	path string
	mu   sync.Mutex
	f    mountFile
}

func newMountStore(path string) *mountStore {
	m := &mountStore{path: path, f: mountFile{Decided: map[string]bool{}}}
	if path == "" {
		return m
	}
	if b, err := os.ReadFile(path); err == nil {
		var f mountFile
		if json.Unmarshal(b, &f) == nil {
			if f.Decided == nil {
				f.Decided = map[string]bool{}
			}
			m.f = f
		}
	}
	return m
}

func (m *mountStore) mounts() []Mount {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Mount{}, m.f.Mounts...)
}

func (m *mountStore) decided(hash string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.f.Decided[hash]
}

// decide records the answer for offer o; mount adds its head to the wall.
func (m *mountStore) decide(o MountOffer, mount bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.f.Decided[o.BlockHash] {
		return errors.New("already decided for this block")
	}
	m.f.Decided[o.BlockHash] = true
	if mount {
		m.f.Mounts = append(m.f.Mounts, Mount{
			BlockHeight: o.BlockHeight, BlockHash: o.BlockHash, JobHeight: o.JobHeight,
			Tier: o.Tier, Rarity: o.Rarity, Creature: o.Creature, Species: o.Species, Pct: o.Pct, Variant: o.Variant, MountedAt: time.Now().UTC(),
		})
	}
	if m.path == "" {
		return nil
	}
	b, err := json.MarshalIndent(m.f, "", "  ")
	if err != nil {
		return err
	}
	tmp := m.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, m.path)
}
