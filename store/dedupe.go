package store

import (
	"context"
	"time"
)

// How long a key that stopped turning up is remembered.
const DedupeRetention = 30 * 24 * time.Hour

type DedupeStore interface {
	// Marks the keys as seen now and returns the ones that weren't seen
	// before, in the order given.
	MarkSeen(ctx context.Context, kind string, keys []string) ([]string, error)
	PruneSeen(ctx context.Context, before time.Time) (int, error)
}
