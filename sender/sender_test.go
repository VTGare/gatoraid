package sender_test

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

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

func (f *fakePoster) CreateMessage(id snowflake.ID, data discord.MessageCreate, _ ...rest.RequestOpt) (*discord.Message, error) {
	channelID := id.String()
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
	return &discord.Message{ID: snowflake.ID(f.nextID), ChannelID: id}, nil
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
	return &rest.Error{Response: &http.Response{StatusCode: code, Status: http.StatusText(code)}}
}

func msg(channel, text string) sender.Message {
	return sender.Message{ChannelID: channel, Send: discord.MessageCreate{Content: text}}
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
			m := msg("1", text)
			m.OnSent = func(sent *discord.Message) {
				mu.Lock()
				ids = append(ids, sent.ID.String())
				mu.Unlock()
			}
			Expect(s.Send(m)).To(BeTrue())
		}
		Expect(s.Send(msg("2", "x"))).To(BeTrue())

		Eventually(func() []string { return fake.messages("1") }).Should(Equal([]string{"1", "2", "3"}))
		Eventually(func() []string { return fake.messages("2") }).Should(Equal([]string{"x"}))
		Eventually(func() int { mu.Lock(); defer mu.Unlock(); return len(ids) }).Should(Equal(3))
	})

	It("keeps working after idle channels shut down", func() {
		start()

		Expect(s.Send(msg("1", "1"))).To(BeTrue())
		Eventually(func() []string { return fake.messages("1") }).Should(HaveLen(1))
		time.Sleep(50 * time.Millisecond)

		Expect(s.Send(msg("1", "2"))).To(BeTrue())
		Eventually(func() []string { return fake.messages("1") }).Should(Equal([]string{"1", "2"}))
	})

	It("retries server and network errors", func() {
		fake.errs["1"] = []error{status(http.StatusInternalServerError), errors.New("connection reset")}
		start()

		Expect(s.Send(msg("1", "1"))).To(BeTrue())
		Eventually(func() []string { return fake.messages("1") }).Should(Equal([]string{"1"}))
		Expect(fake.callCount()).To(Equal(3))
	})

	It("sends attachments whole when retrying", func() {
		fake.errs["1"] = []error{status(http.StatusInternalServerError)}
		start()

		m := msg("1", "log")
		m.Send.Files = []*discord.File{discord.NewFile("v.txt", "", strings.NewReader("lines"))}
		Expect(s.Send(m)).To(BeTrue())
		Eventually(func() []string { return fake.messages("1") }).Should(Equal([]string{"log v.txt=lines"}))
	})

	It("gives up after the retries", func() {
		cfg.Retries = 1
		fake.errs["1"] = []error{status(http.StatusServiceUnavailable), status(http.StatusServiceUnavailable)}
		start()

		Expect(s.Send(msg("1", "1"))).To(BeTrue())
		Expect(s.Send(msg("1", "2"))).To(BeTrue())
		Eventually(func() []string { return fake.messages("1") }).Should(Equal([]string{"2"}))
		Expect(fake.callCount()).To(Equal(3))
	})

	It("drops bad requests without retrying", func() {
		fake.errs["1"] = []error{status(http.StatusBadRequest)}
		start()

		Expect(s.Send(msg("1", "1"))).To(BeTrue())
		Expect(s.Send(msg("1", "2"))).To(BeTrue())
		Eventually(func() []string { return fake.messages("1") }).Should(Equal([]string{"2"}))
		Expect(fake.callCount()).To(Equal(2))
	})

	It("pauses channels that refuse the bot", func() {
		cfg.Cooldown = 100 * time.Millisecond
		refused := make(chan string, 1)
		cfg.OnRefused = func(channelID string, _ error) { refused <- channelID }
		fake.errs["1"] = []error{status(http.StatusForbidden)}
		fake.hold()
		start()

		Expect(s.Send(msg("1", "1"))).To(BeTrue())
		Expect(s.Send(msg("1", "queued"))).To(BeTrue())
		fake.release()

		Eventually(func() int { return fake.callCount() }).Should(Equal(1))
		Eventually(func() bool { return s.Send(msg("1", "paused")) }).Should(BeFalse())
		Consistently(func() int { return fake.callCount() }, 30*time.Millisecond).Should(Equal(1))
		Expect(refused).To(Receive(Equal("1")))

		Eventually(func() bool { return s.Send(msg("1", "back")) }, time.Second).Should(BeTrue())
		Eventually(func() []string { return fake.messages("1") }).Should(Equal([]string{"back"}))
	})

	It("drops messages beyond the queue size", func() {
		cfg.QueueSize = 2
		fake.hold()
		start()

		Expect(s.Send(msg("1", "1"))).To(BeTrue())
		Eventually(fake.started).Should(Receive())
		Expect(s.Send(msg("1", "2"))).To(BeTrue())
		Expect(s.Send(msg("1", "3"))).To(BeTrue())
		Expect(s.Send(msg("1", "4"))).To(BeFalse())
		fake.release()

		Eventually(func() []string { return fake.messages("1") }).Should(Equal([]string{"1", "2", "3"}))
	})

	It("refuses messages after Close", func() {
		start()
		s.Close()
		Expect(s.Send(msg("1", "1"))).To(BeFalse())
	})

	It("drops messages to channel IDs that don't parse", func() {
		start()

		Expect(s.Send(msg("general", "1"))).To(BeTrue())
		Expect(s.Send(msg("1", "2"))).To(BeTrue())
		Eventually(func() []string { return fake.messages("1") }).Should(Equal([]string{"2"}))
		Expect(fake.callCount()).To(Equal(1))
	})
})
