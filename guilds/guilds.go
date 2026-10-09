// Package guilds keeps every guild in memory, including guilds the bot has
// left, with their blacklists and filters. Writes go to the store and swap
// in a new index, so readers never lock or query.
package guilds

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/VTGare/gatoraid/store"
)

type State struct {
	store   store.Store
	idx     atomic.Pointer[map[string]store.Guild]
	mods    atomic.Pointer[map[string]*moderation]
	mu      sync.Mutex
	changed chan struct{}
}

func New(st store.Store) *State {
	s := &State{store: st, changed: make(chan struct{}, 1)}
	s.idx.Store(&map[string]store.Guild{})
	s.mods.Store(&map[string]*moderation{})
	return s
}

// Changed receives after settings updates. Several updates in a row may
// arrive as one, so it suits a single consumer that rereads everything.
func (s *State) Changed() <-chan struct{} { return s.changed }

func (s *State) Reload(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.reload(ctx); err != nil {
		return err
	}
	return s.reloadModeration(ctx)
}

func (s *State) reload(ctx context.Context) error {
	list, err := s.store.Guilds(ctx)
	if err != nil {
		return err
	}

	idx := make(map[string]store.Guild, len(list))
	for _, g := range list {
		idx[g.ID] = g
	}
	s.idx.Store(&idx)
	return nil
}

// Guild includes guilds the bot has left. Check Active.
func (s *State) Guild(guildID string) (store.Guild, bool) {
	g, ok := (*s.idx.Load())[guildID]
	return g, ok
}

// Update applies edit to the guild's current settings and saves them.
func (s *State) Update(ctx context.Context, guildID string, edit func(*store.Settings)) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	g, ok := (*s.idx.Load())[guildID]
	if !ok {
		return store.ErrGuildNotFound
	}

	edit(&g.Settings)
	if err := s.store.UpdateGuildSettings(ctx, guildID, g.Settings); err != nil {
		return err
	}

	if err := s.reload(ctx); err != nil {
		return err
	}

	select {
	case s.changed <- struct{}{}:
	default:
	}

	return nil
}

func (s *State) Join(ctx context.Context, guildID string) (store.JoinKind, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, kind, err := s.store.JoinGuild(ctx, guildID)
	if err != nil {
		return kind, err
	}

	return kind, s.reload(ctx)
}

func (s *State) Leave(ctx context.Context, guildID string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.store.LeaveGuild(ctx, guildID, at); err != nil {
		return err
	}
	return s.reload(ctx)
}

// Reconcile marks active guilds missing from present as left and returns
// their IDs.
func (s *State) Reconcile(ctx context.Context, present []string, at time.Time) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	left, err := s.store.ReconcileGuilds(ctx, present, at)
	if err != nil {
		return nil, err
	}
	return left, s.reload(ctx)
}

func (s *State) Purge(ctx context.Context, leftBefore time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	n, err := s.store.PurgeGuilds(ctx, leftBefore)
	if err != nil {
		return n, err
	}

	if err := s.reload(ctx); err != nil {
		return n, err
	}

	return n, s.reloadModeration(ctx)
}
