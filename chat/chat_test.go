package chat

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/VTGare/gatoraid/holodex/tldex"
	"github.com/VTGare/gatoraid/youtube/livechat"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestChat(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Chat Suite")
}

type step struct {
	msgs []livechat.Message
	err  error
}

// fakeReader plays back polls and reopens, then blocks on empty polls.
type fakeReader struct {
	mu      sync.Mutex
	polls   []step
	reopens []step
	reopen  int
}

func (f *fakeReader) next(queue *[]step) step {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(*queue) == 0 {
		return step{}
	}
	s := (*queue)[0]
	*queue = (*queue)[1:]
	return s
}

func (f *fakeReader) Poll(context.Context) ([]livechat.Message, error) {
	s := f.next(&f.polls)
	return s.msgs, s.err
}

func (f *fakeReader) Reopen(context.Context) ([]livechat.Message, error) {
	f.mu.Lock()
	f.reopen++
	f.mu.Unlock()
	s := f.next(&f.reopens)
	return s.msgs, s.err
}

func (f *fakeReader) Wait() time.Duration { return time.Millisecond }

type fakeTLdex struct {
	mu   sync.Mutex
	subs map[string]chan tldex.Update
}

func (f *fakeTLdex) Subscribe(id string) <-chan tldex.Update {
	f.mu.Lock()
	defer f.mu.Unlock()
	ch := make(chan tldex.Update, 16)
	f.subs[id] = ch
	return ch
}

func (f *fakeTLdex) Unsubscribe(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if ch, ok := f.subs[id]; ok {
		close(ch)
		delete(f.subs, id)
	}
}

func (f *fakeTLdex) send(id string, u tldex.Update) {
	f.mu.Lock()
	ch := f.subs[id]
	f.mu.Unlock()
	ch <- u
}

func msg(id, author, text string) livechat.Message {
	return livechat.Message{ID: id, AuthorChannelID: author, AuthorName: "@" + author, Text: text, Time: time.Unix(0, 0)}
}

var _ = Describe("Manager", func() {
	var (
		ctx     context.Context
		cancel  context.CancelFunc
		reader  *fakeReader
		opens   []error
		opened  int
		mu      sync.Mutex
		manager *Manager
		tl      *fakeTLdex
	)

	newManager := func(withTLdex bool) {
		cfg := Config{
			Open: func(context.Context, string) (Reader, error) {
				mu.Lock()
				defer mu.Unlock()
				opened++
				if len(opens) > 0 {
					err := opens[0]
					opens = opens[1:]
					if err != nil {
						return nil, err
					}
				}
				return reader, nil
			},
			Backoff:     time.Millisecond,
			MaxBackoff:  5 * time.Millisecond,
			GiveUpAfter: time.Second,
		}
		if withTLdex {
			tl = &fakeTLdex{subs: map[string]chan tldex.Update{}}
			cfg.TLdex = tl
		}
		manager = NewManager(cfg)
	}

	BeforeEach(func() {
		ctx, cancel = context.WithCancel(context.Background())
		reader = &fakeReader{}
		opens, opened = nil, 0
		newManager(false)
		DeferCleanup(func() {
			cancel()
			manager.Wait()
		})
	})

	nextEvent := func() Event {
		var e Event
		EventuallyWithOffset(1, manager.Events()).Should(Receive(&e))
		return e
	}

	comments := func(n int) []string {
		var out []string
		for len(out) < n {
			e := nextEvent()
			ExpectWithOffset(1, e.Kind).To(Equal(EventComment))
			out = append(out, e.Comment.ID)
		}
		return out
	}

	It("delivers comments in order", func() {
		reader.polls = []step{
			{msgs: []livechat.Message{msg("a", "UC1", "hi"), msg("b", "UC2", "hello")}},
			{msgs: []livechat.Message{msg("c", "UC1", "again")}},
		}

		manager.Start(ctx, "v")

		Expect(comments(3)).To(Equal([]string{"a", "b", "c"}))
		Expect(manager.Running()).To(Equal([]string{"v"}))
	})

	It("runs one session per stream", func() {
		manager.Start(ctx, "v")
		manager.Start(ctx, "v")

		opens := func() int { mu.Lock(); defer mu.Unlock(); return opened }
		Eventually(opens).Should(Equal(1))
		Consistently(opens).Should(Equal(1))
	})

	It("stops quietly when asked", func() {
		manager.Start(ctx, "v")
		Eventually(manager.Running).Should(HaveLen(1))

		manager.Stop("v")

		Eventually(manager.Running).Should(BeEmpty())
		Consistently(manager.Events()).ShouldNot(Receive())
	})

	It("gives up at once on chats that can't be read", func() {
		opens = []error{livechat.ErrUnavailable}

		manager.Start(ctx, "v")

		e := nextEvent()
		Expect(e.Kind).To(Equal(EventStopped))
		Expect(e.Err).To(MatchError(livechat.ErrUnavailable))
		Expect(opened).To(Equal(1))
	})

	It("retries opening after transient errors", func() {
		opens = []error{errors.New("timeout"), errors.New("timeout"), nil}
		reader.polls = []step{{msgs: []livechat.Message{msg("a", "UC1", "hi")}}}

		manager.Start(ctx, "v")

		Expect(comments(1)).To(Equal([]string{"a"}))
		Expect(opened).To(Equal(3))
	})

	It("reopens after a failed poll and delivers what was missed", func() {
		reader.polls = []step{{err: errors.New("connection reset")}}
		reader.reopens = []step{{msgs: []livechat.Message{msg("missed", "UC1", "hi")}}}

		manager.Start(ctx, "v")

		Expect(comments(1)).To(Equal([]string{"missed"}))
	})

	It("reports the end of the chat", func() {
		reader.polls = []step{{msgs: []livechat.Message{msg("a", "UC1", "bye")}}, {err: livechat.ErrEnded}}

		manager.Start(ctx, "v")

		Expect(comments(1)).To(Equal([]string{"a"}))
		e := nextEvent()
		Expect(e.Kind).To(Equal(EventStopped))
		Expect(e.Err).To(MatchError(livechat.ErrEnded))
		Eventually(manager.Running).Should(BeEmpty())
	})

	It("gives up on a chat that keeps failing", func() {
		manager.cfg.GiveUpAfter = 20 * time.Millisecond
		failures := make([]step, 1000)
		for i := range failures {
			failures[i] = step{err: errors.New("503")}
		}
		reader.polls, reader.reopens = failures, failures

		manager.Start(ctx, "v")

		e := nextEvent()
		Expect(e.Kind).To(Equal(EventStopped))
		Expect(e.Err).To(MatchError("503"))
	})

	Describe("with TLdex", func() {
		BeforeEach(func() { newManager(true) })

		It("reports the start time", func() {
			started := time.Date(2026, 10, 2, 1, 0, 0, 0, time.UTC)
			manager.Start(ctx, "v")
			Eventually(func() int { tl.mu.Lock(); defer tl.mu.Unlock(); return len(tl.subs) }).Should(Equal(1))

			tl.send("v", tldex.Update{StartedAt: started})

			e := nextEvent()
			Expect(e.Kind).To(Equal(EventStarted))
			Expect(e.StartedAt).To(Equal(started))
		})

		It("drops the second copy of a line from either source", func() {
			manager.Start(ctx, "v")
			Eventually(func() int { tl.mu.Lock(); defer tl.mu.Unlock(); return len(tl.subs) }).Should(Equal(1))

			tl.send("v", tldex.Update{Message: &tldex.Message{VideoID: "v", ChannelID: "UCtl", Name: "tl", Text: "[EN] Hello :_wave: world!", TL: true}})
			first := nextEvent()
			Expect(first.Comment.Source).To(Equal(SourceTLdex))
			Expect(first.Comment.TL).To(BeTrue())

			reader.mu.Lock()
			reader.polls = []step{{msgs: []livechat.Message{msg("yt-dup", "UCtl", "[EN] hello world"), msg("yt-new", "UCother", "hi")}}}
			reader.mu.Unlock()
			Expect(comments(1)).To(Equal([]string{"yt-new"}))
		})

		It("passes MChad lines through", func() {
			manager.Start(ctx, "v")
			Eventually(func() int { tl.mu.Lock(); defer tl.mu.Unlock(); return len(tl.subs) }).Should(Equal(1))

			tl.send("v", tldex.Update{Message: &tldex.Message{VideoID: "v", Name: "mchad user", Text: "TL line", Source: "MChad", TL: true}})
			tl.send("v", tldex.Update{Message: &tldex.Message{VideoID: "v", Name: "mchad user", Text: "TL line", Source: "MChad", TL: true}})

			first := nextEvent().Comment
			Expect(first.Source).To(Equal(SourceMChad))
			Expect(first.AuthorChannelID).To(Equal("mchad:mchad user"))
			Expect(nextEvent().Comment.Source).To(Equal(SourceMChad))
		})

		It("unsubscribes when the session stops", func() {
			manager.Start(ctx, "v")
			Eventually(func() int { tl.mu.Lock(); defer tl.mu.Unlock(); return len(tl.subs) }).Should(Equal(1))

			manager.Stop("v")

			Eventually(func() int { tl.mu.Lock(); defer tl.mu.Unlock(); return len(tl.subs) }).Should(BeZero())
		})
	})
})

var _ = Describe("deduper", func() {
	It("forgets lines after the window", func() {
		now := time.Unix(0, 0)
		d := newDeduper(time.Minute)
		d.now = func() time.Time { return now }
		c := &Comment{AuthorChannelID: "UC1", Text: "Hello, world!"}

		Expect(d.first(c)).To(BeTrue())
		Expect(d.first(&Comment{AuthorChannelID: "UC1", Text: "hello world"})).To(BeFalse())
		Expect(d.first(&Comment{AuthorChannelID: "UC2", Text: "hello world"})).To(BeTrue())

		now = now.Add(2 * time.Minute)
		Expect(d.first(c)).To(BeTrue())
	})
})
