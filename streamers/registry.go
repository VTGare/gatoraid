package streamers

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/VTGare/gatoraid/store"
)

var ErrNotFound = errors.New("streamer not found")

type AmbiguousError struct {
	Query      string
	Candidates []*store.Streamer
}

func (e *AmbiguousError) Error() string {
	names := make([]string, len(e.Candidates))
	for i, c := range e.Candidates {
		names[i] = c.Name
	}
	return fmt.Sprintf("%q matches several streamers: %s", e.Query, strings.Join(names, ", "))
}

// Returned streamers and groups are shared between callers, so don't modify
// them. Save a copy instead.
type Registry struct {
	store store.StreamerStore
	snap  atomic.Pointer[snapshot]
	files atomic.Pointer[map[string]string]
}

type snapshot struct {
	groups    []*store.Group
	groupByID map[string]*store.Group
	children  map[string][]*store.Group
	// Active streamers only. Hidden ones are just in all.
	streamers []*store.Streamer
	byChannel map[string]*store.Streamer
	byTwitch  map[string]*store.Streamer
	all       map[string]*store.Streamer
}

func New(st store.StreamerStore) *Registry {
	r := &Registry{store: st}
	r.snap.Store(&snapshot{
		groupByID: map[string]*store.Group{},
		byChannel: map[string]*store.Streamer{},
		byTwitch:  map[string]*store.Streamer{},
	})
	return r
}

func (r *Registry) Sync(ctx context.Context, seed *Seed) (store.SeedResult, error) {
	res, err := r.store.SyncSeed(ctx, seed.Groups, seed.Streamers)
	if err != nil {
		return res, err
	}
	r.files.Store(&seed.Files)

	return res, r.Reload(ctx)
}

func (r *Registry) Reload(ctx context.Context) error {
	groups, err := r.store.StreamerGroups(ctx)
	if err != nil {
		return err
	}

	streamers, err := r.store.Streamers(ctx)
	if err != nil {
		return err
	}

	s := &snapshot{
		groupByID: make(map[string]*store.Group, len(groups)),
		children:  make(map[string][]*store.Group),
		byChannel: make(map[string]*store.Streamer, len(streamers)),
		byTwitch:  make(map[string]*store.Streamer),
		all:       make(map[string]*store.Streamer, len(streamers)),
	}

	for i := range groups {
		g := &groups[i]
		s.groups = append(s.groups, g)
		s.groupByID[g.ID] = g
		if g.ParentID != "" {
			s.children[g.ParentID] = append(s.children[g.ParentID], g)
		}
	}

	slices.SortFunc(streamers, func(a, b store.Streamer) int {
		return cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	for i := range streamers {
		st := &streamers[i]
		s.all[st.ChannelID] = st
		if st.Removed() {
			continue
		}
		s.streamers = append(s.streamers, st)
		s.byChannel[st.ChannelID] = st
		if st.Twitch != "" {
			s.byTwitch[st.Twitch] = st
		}
	}

	r.snap.Store(s)
	return nil
}

func (r *Registry) Streamer(channelID string) (*store.Streamer, bool) {
	st, ok := r.snap.Load().byChannel[channelID]
	return st, ok
}

func (r *Registry) TwitchStreamer(username string) (*store.Streamer, bool) {
	st, ok := r.snap.Load().byTwitch[strings.ToLower(username)]
	return st, ok
}

// TwitchChannels maps the Twitch usernames of active streamers to their
// channel IDs.
func (r *Registry) TwitchChannels() map[string]string {
	out := map[string]string{}
	for username, st := range r.snap.Load().byTwitch {
		out[username] = st.ChannelID
	}
	return out
}

// Lookup also finds hidden streamers, for showing what a subscription
// points at.
func (r *Registry) Lookup(channelID string) (*store.Streamer, bool) {
	st, ok := r.snap.Load().all[channelID]
	return st, ok
}

func (r *Registry) Streamers() []*store.Streamer { return r.snap.Load().streamers }

func (r *Registry) Group(id string) (*store.Group, bool) {
	g, ok := r.snap.Load().groupByID[id]
	return g, ok
}

// Parents come before their subgroups.
func (r *Registry) Groups() []*store.Group { return r.snap.Load().groups }

func (r *Registry) Subgroups(id string) []*store.Group { return r.snap.Load().children[id] }

// Empty for groups that aren't from the seed.
func (r *Registry) GroupFile(id string) string {
	if files := r.files.Load(); files != nil {
		return (*files)[id]
	}
	return ""
}

// Includes subgroups' members, sorted by name.
func (r *Registry) Members(groupID string) []*store.Streamer {
	s := r.snap.Load()

	in := map[string]bool{}
	var mark func(id string)
	mark = func(id string) {
		in[id] = true
		for _, c := range s.children[id] {
			mark(c.ID)
		}
	}
	mark(groupID)

	var out []*store.Streamer
	for _, st := range s.streamers {
		if in[st.GroupID] {
			out = append(out, st)
		}
	}

	return out
}

// Lineage is the group followed by its parents, e.g. Hololive EN, Hololive.
// Emoji and auto-translate lookups walk it to fall back to the parent.
func (r *Registry) Lineage(groupID string) []*store.Group {
	s := r.snap.Load()

	var out []*store.Group
	for g, ok := s.groupByID[groupID]; ok && len(out) <= len(s.groups); g, ok = s.groupByID[g.ParentID] {
		out = append(out, g)
	}

	return out
}

func (r *Registry) SkipAutoTranslate(st *store.Streamer) bool {
	for _, g := range r.Lineage(st.GroupID) {
		if g.SkipAutoTranslate {
			return true
		}
	}
	return false
}

// Resolve finds one streamer by channel ID, full name, alias, or a single
// word of the name ("kiara"), in that order, all case-insensitive except
// the ID. A tie at any step is an *AmbiguousError.
func (r *Registry) Resolve(query string) (*store.Streamer, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, ErrNotFound
	}

	if st, ok := r.Streamer(query); ok {
		return st, nil
	}

	steps := []func(*store.Streamer) bool{
		func(st *store.Streamer) bool { return strings.EqualFold(st.Name, query) },
		func(st *store.Streamer) bool {
			return slices.ContainsFunc(st.Aliases, func(a string) bool { return strings.EqualFold(a, query) })
		},
		func(st *store.Streamer) bool {
			return slices.ContainsFunc(strings.Fields(st.Name), func(w string) bool { return strings.EqualFold(w, query) })
		},
	}

	for _, match := range steps {
		var found []*store.Streamer
		for _, st := range r.Streamers() {
			if match(st) {
				found = append(found, st)
			}
		}

		switch len(found) {
		case 0:
			continue
		case 1:
			return found[0], nil
		default:
			return nil, &AmbiguousError{Query: query, Candidates: found}
		}
	}

	return nil, ErrNotFound
}

// Exact matches rank first, then name prefixes, word and alias prefixes,
// and finally substrings.
func (r *Registry) Search(query string, limit int) []*store.Streamer {
	q := strings.ToLower(strings.TrimSpace(query))
	all := r.Streamers()
	if q == "" {
		return all[:min(limit, len(all))]
	}

	type hit struct {
		st    *store.Streamer
		score int
	}

	var hits []hit
	for _, st := range all {
		if score, ok := matchScore(st, query, q); ok {
			hits = append(hits, hit{st, score})
		}
	}

	slices.SortStableFunc(hits, func(a, b hit) int { return cmp.Compare(a.score, b.score) })

	out := make([]*store.Streamer, 0, min(limit, len(hits)))
	for _, h := range hits[:min(limit, len(hits))] {
		out = append(out, h.st)
	}

	return out
}

func matchScore(st *store.Streamer, raw, q string) (int, bool) {
	if st.ChannelID == raw {
		return 0, true
	}

	name := strings.ToLower(st.Name)
	aliases := make([]string, len(st.Aliases))
	for i, a := range st.Aliases {
		aliases[i] = strings.ToLower(a)
	}

	switch {
	case name == q || slices.Contains(aliases, q):
		return 1, true
	case strings.HasPrefix(name, q):
		return 2, true
	case slices.ContainsFunc(strings.Fields(name), func(w string) bool { return strings.HasPrefix(w, q) }),
		slices.ContainsFunc(aliases, func(a string) bool { return strings.HasPrefix(a, q) }):
		return 3, true
	case strings.Contains(name, q),
		strings.Contains(strings.ToLower(st.ChannelName), q),
		slices.ContainsFunc(aliases, func(a string) bool { return strings.Contains(a, q) }):
		return 4, true
	}

	return 0, false
}

// Prefix matches come before substring matches.
func (r *Registry) SearchGroups(query string, limit int) []*store.Group {
	q := strings.ToLower(strings.TrimSpace(query))

	var prefix, contains []*store.Group
	for _, g := range r.Groups() {
		name := strings.ToLower(g.Name)
		switch {
		case strings.HasPrefix(name, q) || strings.HasPrefix(g.ID, q):
			prefix = append(prefix, g)
		case strings.Contains(name, q) || strings.Contains(g.ID, q):
			contains = append(contains, g)
		}
	}

	out := append(prefix, contains...)
	return out[:min(limit, len(out))]
}

// Reloads only if an avatar changed.
func (r *Registry) UpdateAvatars(ctx context.Context, avatars map[string]string) error {
	n, err := r.store.UpdateAvatars(ctx, avatars)
	if err != nil || n == 0 {
		return err
	}
	return r.Reload(ctx)
}

func (r *Registry) ChannelIDs() []string {
	sts := r.Streamers()
	ids := make([]string, len(sts))
	for i, st := range sts {
		ids[i] = st.ChannelID
	}
	return ids
}

func (r *Registry) Save(ctx context.Context, st store.Streamer) error {
	if err := r.store.SaveStreamer(ctx, st); err != nil {
		return err
	}
	return r.Reload(ctx)
}

func (r *Registry) Remove(ctx context.Context, channelID string) error {
	if err := r.store.RemoveStreamer(ctx, channelID); err != nil {
		return err
	}
	return r.Reload(ctx)
}

// Purge deletes streamers hidden before the cutoff that nothing subscribes
// to.
func (r *Registry) Purge(ctx context.Context, removedBefore time.Time) (int, error) {
	n, err := r.store.PurgeStreamers(ctx, removedBefore)
	if err != nil || n == 0 {
		return n, err
	}
	return n, r.Reload(ctx)
}

// HideUnused hides user-added channels nothing subscribes to anymore.
func (r *Registry) HideUnused(ctx context.Context) (int, error) {
	n, err := r.store.HideUnusedUserStreamers(ctx)
	if err != nil || n == 0 {
		return n, err
	}
	return n, r.Reload(ctx)
}
