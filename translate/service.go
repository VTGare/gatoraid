// Package translate translates chat lines with DeepL, caching results and
// staying within a monthly character budget.
package translate

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	lru "github.com/hashicorp/golang-lru/v2"
)

var ErrBudget = errors.New("translate: monthly character budget reached")

const (
	cacheSize     = 10_000
	usageInterval = 10 * time.Minute
	languagesTTL  = 24 * time.Hour
)

type Client interface {
	Translate(ctx context.Context, text, target string) (Result, error)
	Languages(ctx context.Context) ([]Language, error)
	Usage(ctx context.Context) (count, limit int64, err error)
}

type key struct {
	text, target string
}

// Service translates each text once per target language. It counts the
// characters it sends and stops at the budget, rechecking DeepL's own
// count every few minutes, which also picks up the monthly reset.
type Service struct {
	client Client
	budget int64
	log    *slog.Logger
	cache  *lru.Cache[key, Result]

	mu      sync.Mutex
	used    int64
	limit   int64
	paused  bool
	langs   []Language
	langsAt time.Time
}

func NewService(client Client, budget int64, log *slog.Logger) *Service {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	cache, _ := lru.New[key, Result](cacheSize)
	return &Service{client: client, budget: budget, limit: budget, log: log, cache: cache}
}

// Run keeps the usage count in step with DeepL until ctx is done.
func (s *Service) Run(ctx context.Context) error {
	ticker := time.NewTicker(usageInterval)
	defer ticker.Stop()

	for {
		if err := s.RefreshUsage(ctx); err != nil && ctx.Err() == nil {
			s.log.Warn("couldn't check DeepL usage", slog.Any("error", err))
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (s *Service) RefreshUsage(ctx context.Context) error {
	count, limit, err := s.client.Usage(ctx)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.used = count
	s.limit = s.budget
	if limit > 0 && limit < s.limit {
		s.limit = limit
	}
	if s.paused && s.used < s.limit {
		s.paused = false
		s.log.Info("DeepL translation resumed", slog.Int64("used", s.used), slog.Int64("limit", s.limit))
	}
	return nil
}

func (s *Service) Translate(ctx context.Context, text, target string) (Result, error) {
	k := key{text, target}
	if r, ok := s.cache.Get(k); ok {
		return r, nil
	}

	if err := s.spend(int64(utf8.RuneCountInString(text))); err != nil {
		return Result{}, err
	}

	r, err := s.client.Translate(ctx, text, target)
	if err != nil {
		var status *StatusError
		if errors.As(err, &status) && status.Status == 456 {
			s.pause()
		}
		return Result{}, err
	}

	s.cache.Add(k, r)
	return r, nil
}

func (s *Service) spend(chars int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.paused {
		return ErrBudget
	}
	if s.used+chars > s.limit {
		s.paused = true
		s.log.Error("DeepL budget reached, translation is paused until the usage resets",
			slog.Int64("used", s.used), slog.Int64("limit", s.limit))
		return ErrBudget
	}

	s.used += chars
	return nil
}

func (s *Service) pause() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.paused {
		s.paused = true
		s.log.Error("DeepL says the quota is used up, translation is paused until the usage resets")
	}
}

// Languages lists DeepL's target languages, refetched once a day.
func (s *Service) Languages(ctx context.Context) ([]Language, error) {
	s.mu.Lock()
	if s.langs != nil && time.Since(s.langsAt) < languagesTTL {
		langs := s.langs
		s.mu.Unlock()
		return langs, nil
	}
	s.mu.Unlock()

	langs, err := s.client.Languages(ctx)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	s.langs, s.langsAt = langs, time.Now()
	s.mu.Unlock()
	return langs, nil
}

// SameLanguage reports whether DeepL's detected source is the target, so
// "EN" matches "EN-US".
func SameLanguage(detected, target string) bool {
	base, _, _ := strings.Cut(target, "-")
	return strings.EqualFold(detected, base)
}
