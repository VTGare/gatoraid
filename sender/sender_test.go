package sender_test

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/VTGare/gatoraid/sender"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type fakePoster struct {
	mu     sync.Mutex
	sent   map[string][]string
	calls  int
	errs   map[string][]error
	nextID int

	// While set, calls announce themselves on started and wait for release.
	block   chan struct{}
	started chan struct{}
	once    sync.Once
}

func (f *fakePoster) hold() {
	f.block = make(chan struct{})
	f.started = make(chan struct{}, 10)
	DeferCleanup(f.release)
}

func (f *fakePoster) release() { f.once.Do(func() { close(f.block) }) }

func newFake() *fakePoster {
	return &fakePoster{sent: map[string][]string{}, errs: map[string][]error{}}
}

func (f *fakePoster) ChannelMessageSendComplex(channelID string, data *discordgo.MessageSend, _ ...discordgo.RequestOption) (*discordgo.Message, error) {
	if f.block != nil {
		f.started <- struct{}{}
		<-f.block
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls++
	if errs := f.errs[channelID]; len(errs) > 0 {
		f.errs[channelID] = errs[1:]
		// Like a request that died partway through the upload.
		for _, file := range data.Files {
			_, _ = io.ReadAll(file.Reader)
		}
		return nil, errs[0]
	}

	content := data.Content
	for _, file := range data.Files {
		body, _ := io.ReadAll(file.Reader)
		content += " " + file.Name + "=" + string(body)
	}

	f.nextID++
	f.sent[channelID] = append(f.sent[channelID], content)
	return &discordgo.Message{ID: string(rune('a' + f.nextID - 1)), ChannelID: channelID}, nil
}

func (f *fakePoster) messages(channelID string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.sent[channelID]...)
}

func (f *fakePoster) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func status(code int) error {
	return &discordgo.RESTError{Response: &http.Response{StatusCode: code}}
}

func msg(channel, text string) sender.Message {
	return sender.Message{ChannelID: channel, Send: &discordgo.MessageSend{Content: text}}
}

var _ = Describe("Sender", func() {
	var (
		fake *fakePoster
		s    *sender.Sender
		cfg  sender.Config
	)

	BeforeEach(func() {
		fake = newFake()
		cfg = sender.Config{Poster: fake, Backoff: time.Millisecond, IdleTimeout: 20 * time.Millisecond}
	})

	start := func() {
		s = sender.New(cfg)
		DeferCleanup(s.Close)
	}

	It("posts in order per channel and reports the message", func() {
		start()

		var (
			mu  sync.Mutex
			ids []string
		)
		for _, text := range []string{"1", "2", "3"} {
			m := msg("a", text)
			m.OnSent = func(sent *discordgo.Message) {
				mu.Lock()
				ids = append(ids, sent.ID)
				mu.Unlock()
			}
			Expect(s.Send(m)).To(BeTrue())
		}
		Expect(s.Send(msg("b", "x"))).To(BeTrue())

		Eventually(func() []string { return fake.messages("a") }).Should(Equal([]string{"1", "2", "3"}))
		Eventually(func() []string { return fake.messages("b") }).Should(Equal([]string{"x"}))
		Eventually(func() int { mu.Lock(); defer mu.Unlock(); return len(ids) }).Should(Equal(3))
	})

	It("keeps working after idle channels shut down", func() {
		start()

		Expect(s.Send(msg("a", "1"))).To(BeTrue())
		Eventually(func() []string { return fake.messages("a") }).Should(HaveLen(1))
		time.Sleep(50 * time.Millisecond)

		Expect(s.Send(msg("a", "2"))).To(BeTrue())
		Eventually(func() []string { return fake.messages("a") }).Should(Equal([]string{"1", "2"}))
	})

	It("retries server and network errors", func() {
		fake.errs["a"] = []error{status(http.StatusInternalServerError), errors.New("connection reset")}
		start()

		Expect(s.Send(msg("a", "1"))).To(BeTrue())
		Eventually(func() []string { return fake.messages("a") }).Should(Equal([]string{"1"}))
		Expect(fake.callCount()).To(Equal(3))
	})

	It("sends attachments whole when retrying", func() {
		fake.errs["a"] = []error{status(http.StatusInternalServerError)}
		start()

		m := msg("a", "log")
		m.Send.Files = []*discordgo.File{{Name: "v.txt", Reader: strings.NewReader("lines")}}
		Expect(s.Send(m)).To(BeTrue())
		Eventually(func() []string { return fake.messages("a") }).Should(Equal([]string{"log v.txt=lines"}))
	})

	It("gives up after the retries", func() {
		cfg.Retries = 1
		fake.errs["a"] = []error{status(http.StatusServiceUnavailable), status(http.StatusServiceUnavailable)}
		start()

		Expect(s.Send(msg("a", "1"))).To(BeTrue())
		Expect(s.Send(msg("a", "2"))).To(BeTrue())
		Eventually(func() []string { return fake.messages("a") }).Should(Equal([]string{"2"}))
		Expect(fake.callCount()).To(Equal(3))
	})

	It("drops bad requests without retrying", func() {
		fake.errs["a"] = []error{status(http.StatusBadRequest)}
		start()

		Expect(s.Send(msg("a", "1"))).To(BeTrue())
		Expect(s.Send(msg("a", "2"))).To(BeTrue())
		Eventually(func() []string { return fake.messages("a") }).Should(Equal([]string{"2"}))
		Expect(fake.callCount()).To(Equal(2))
	})

	It("pauses channels that refuse the bot", func() {
		cfg.Cooldown = 100 * time.Millisecond
		refused := make(chan string, 1)
		cfg.OnRefused = func(channelID string, _ error) { refused <- channelID }
		fake.errs["a"] = []error{status(http.StatusForbidden)}
		fake.hold()
		start()

		Expect(s.Send(msg("a", "1"))).To(BeTrue())
		Expect(s.Send(msg("a", "queued"))).To(BeTrue())
		fake.release()

		Eventually(func() int { return fake.callCount() }).Should(Equal(1))
		Eventually(func() bool { return s.Send(msg("a", "paused")) }).Should(BeFalse())
		Consistently(func() int { return fake.callCount() }, 30*time.Millisecond).Should(Equal(1))
		Expect(refused).To(Receive(Equal("a")))

		Eventually(func() bool { return s.Send(msg("a", "back")) }, time.Second).Should(BeTrue())
		Eventually(func() []string { return fake.messages("a") }).Should(Equal([]string{"back"}))
	})

	It("drops messages beyond the queue size", func() {
		cfg.QueueSize = 2
		fake.hold()
		start()

		Expect(s.Send(msg("a", "1"))).To(BeTrue())
		Eventually(fake.started).Should(Receive())
		Expect(s.Send(msg("a", "2"))).To(BeTrue())
		Expect(s.Send(msg("a", "3"))).To(BeTrue())
		Expect(s.Send(msg("a", "4"))).To(BeFalse())
		fake.release()

		Eventually(func() []string { return fake.messages("a") }).Should(Equal([]string{"1", "2", "3"}))
	})

	It("refuses messages after Close", func() {
		start()
		s.Close()
		Expect(s.Send(msg("a", "1"))).To(BeFalse())
	})
})
