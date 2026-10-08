package chat

import (
	"context"
	"sync"
	"time"

	"github.com/VTGare/gatoraid/twitch/irc"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type fakeTwitch struct {
	mu      sync.Mutex
	subs    map[string]chan *irc.Message
	stopped []string
}

func (f *fakeTwitch) Subscribe(username string) (<-chan *irc.Message, func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ch := make(chan *irc.Message, 16)
	f.subs[username] = ch
	return ch, func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.stopped = append(f.stopped, username)
	}
}

func (f *fakeTwitch) send(username string, m *irc.Message) {
	f.mu.Lock()
	ch := f.subs[username]
	f.mu.Unlock()
	ch <- m
}

func (f *fakeTwitch) subscribed(username string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.subs[username]
	return ok
}

func (f *fakeTwitch) stops() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.stopped...)
}

var _ = Describe("Manager on Twitch", func() {
	var (
		ctx     context.Context
		cancel  context.CancelFunc
		tw      *fakeTwitch
		opened  bool
		manager *Manager
	)

	BeforeEach(func() {
		ctx, cancel = context.WithCancel(context.Background())
		tw = &fakeTwitch{subs: map[string]chan *irc.Message{}}
		opened = false
		manager = NewManager(Config{
			Open: func(context.Context, string) (Reader, error) {
				opened = true
				return &fakeReader{}, nil
			},
			Twitch: tw,
		})
		DeferCleanup(func() {
			cancel()
			manager.Wait()
		})
	})

	It("reads the Twitch chat and never opens YouTube", func() {
		manager.Start(ctx, Target{VideoID: "twitch:1", TwitchUsername: "alice"})
		Eventually(func() bool { return tw.subscribed("alice") }).Should(BeTrue())

		tw.send("alice", &irc.Message{
			ID: "m1", Channel: "alice", UserID: "42", Username: "bob", DisplayName: "Bob", Text: "hi Kappa",
			Time: time.Unix(10, 0), Broadcaster: true, Moderator: true, Subscriber: true, Partner: true, Bits: 50,
			Emotes: []string{"Kappa"},
		})

		var e Event
		Eventually(manager.Events()).Should(Receive(&e))
		Expect(e.Kind).To(Equal(EventComment))
		Expect(e.VideoID).To(Equal("twitch:1"))
		Expect(*e.Comment).To(Equal(Comment{
			VideoID: "twitch:1", ID: "twitch:m1", AuthorChannelID: "twitch:42", AuthorUsername: "bob", AuthorName: "Bob",
			Text: "hi Kappa", Time: time.Unix(10, 0), Owner: true, Moderator: true, Verified: true, Member: true,
			SuperChat: "50 bits", Emotes: []string{"Kappa"}, Source: SourceTwitch,
		}))
		Expect(opened).To(BeFalse())
	})

	It("skips well-known chat bots", func() {
		manager.Start(ctx, Target{VideoID: "twitch:1", TwitchUsername: "alice"})
		Eventually(func() bool { return tw.subscribed("alice") }).Should(BeTrue())

		tw.send("alice", &irc.Message{ID: "m1", UserID: "1", Username: "streamelements", Text: "Follow on X!", Moderator: true})
		tw.send("alice", &irc.Message{ID: "m2", UserID: "2", Username: "NightBot", Text: "!uptime: 3 hours", Moderator: true})
		tw.send("alice", &irc.Message{ID: "m3", UserID: "3", Username: "real_mod", Text: "be nice", Moderator: true})

		var e Event
		Eventually(manager.Events()).Should(Receive(&e))
		Expect(e.Comment.ID).To(Equal("twitch:m3"))
		Consistently(manager.Events()).ShouldNot(Receive())
	})

	It("leaves the chat when stopped", func() {
		manager.Start(ctx, Target{VideoID: "twitch:1", TwitchUsername: "alice"})
		Eventually(func() bool { return tw.subscribed("alice") }).Should(BeTrue())

		manager.Stop("twitch:1")

		Eventually(tw.stops).Should(Equal([]string{"alice"}))
		Eventually(manager.Running).Should(BeEmpty())
		Consistently(manager.Events()).ShouldNot(Receive())
	})

	It("stops at once when Twitch chat is off", func() {
		manager = NewManager(Config{Open: func(context.Context, string) (Reader, error) { return &fakeReader{}, nil }})

		manager.Start(ctx, Target{VideoID: "twitch:1", TwitchUsername: "alice"})

		var e Event
		Eventually(manager.Events()).Should(Receive(&e))
		Expect(e.Kind).To(Equal(EventStopped))
		Expect(e.Err).To(MatchError(errNoTwitch))
	})
})
