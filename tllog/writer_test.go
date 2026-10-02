package tllog_test

import (
	"context"
	"io"
	"path/filepath"
	"sync"
	"time"

	"github.com/VTGare/gatoraid/relay"
	"github.com/VTGare/gatoraid/sender"
	"github.com/VTGare/gatoraid/store"
	"github.com/VTGare/gatoraid/store/sqlite"
	"github.com/VTGare/gatoraid/stream"
	"github.com/VTGare/gatoraid/tllog"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type posted struct {
	channel, content, file, body string
}

type fakeSender struct {
	mu    sync.Mutex
	posts []posted
}

func (f *fakeSender) Send(m sender.Message) bool {
	p := posted{channel: m.ChannelID, content: m.Send.Content}
	if len(m.Send.Embeds) > 0 {
		e := m.Send.Embeds[0]
		p.content = e.Title + " | " + e.Description
	}
	if len(m.Send.Files) > 0 {
		body, _ := io.ReadAll(m.Send.Files[0].Reader)
		p.file, p.body = m.Send.Files[0].Name, string(body)
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	f.posts = append(f.posts, p)
	return true
}

func (f *fakeSender) all() []posted {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]posted(nil), f.posts...)
}

var _ = Describe("Writer", func() {
	var (
		ctx   context.Context
		db    *sqlite.Store
		snd   *fakeSender
		rules map[string]*relay.Moderation
	)

	ended := stream.Stream{VideoID: "vid", Title: "Title", StartedAt: start}

	BeforeEach(func() {
		ctx = context.Background()
		var err error
		db, err = sqlite.Open(ctx, filepath.Join(GinkgoT().TempDir(), "test.db"))
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(db.Close)

		for _, g := range []string{"g", "h"} {
			_, _, err := db.JoinGuild(ctx, g)
			Expect(err).NotTo(HaveOccurred())
		}
		snd = &fakeSender{}
		rules = map[string]*relay.Moderation{}
	})

	newWriter := func() *tllog.Writer {
		w := tllog.NewWriter(tllog.Config{
			Store:      db,
			Sender:     snd,
			Moderation: func(id string) *relay.Moderation { return rules[id] },
			Delay:      time.Millisecond,
			Now:        func() time.Time { return start.Add(90 * time.Minute) },
		})
		DeferCleanup(w.Close)
		return w
	}

	save := func(guild, channel string, d time.Duration, body string) {
		GinkgoHelper()
		l := at(d, "tl", store.LineTL, body)
		l.GuildID, l.ChannelID = guild, channel
		Expect(db.SaveLines(ctx, []store.Line{l})).To(Succeed())
	}

	It("posts each relay channel its own lines, once", func() {
		save("g", "c1", time.Second, "[EN] one")
		save("g", "c2", 2*time.Second, "[EN] two")
		save("h", "c3", 3*time.Second, "[EN] three")
		rules["h"] = &relay.Moderation{Banned: []string{"three"}}

		w := newWriter()
		w.StreamEnded(ended)
		w.StreamEnded(ended)

		Eventually(snd.all).Should(HaveLen(2))
		Consistently(snd.all, 50*time.Millisecond).Should(HaveLen(2))
		Expect(snd.all()).To(ConsistOf(
			posted{"c1", "Title | Stream log · 1 h 30 min · 1 line", "vid.txt",
				"Title\nhttps://youtu.be/vid\nStarted 2026-10-02 12:00 UTC\n\n[0:00:01] @tl: [EN] one\n"},
			posted{"c2", "Title | Stream log · 1 h 30 min · 1 line", "vid.txt",
				"Title\nhttps://youtu.be/vid\nStarted 2026-10-02 12:00 UTC\n\n[0:00:02] @tl: [EN] two\n"},
		))
	})

	It("posts one log with every channel's lines to the log channel", func() {
		g, err := db.Guild(ctx, "g")
		Expect(err).NotTo(HaveOccurred())
		g.Settings.LogChannelID = "logs"
		Expect(db.UpdateGuildSettings(ctx, "g", g.Settings)).To(Succeed())

		save("g", "c1", time.Second, "[EN] one")
		save("g", "c2", 2*time.Second, "[EN] two")

		newWriter().StreamEnded(ended)

		Eventually(snd.all).Should(HaveLen(1))
		Expect(snd.all()[0].channel).To(Equal("logs"))
		Expect(snd.all()[0].body).To(HaveSuffix("[0:00:01] @tl: [EN] one\n[0:00:02] @tl: [EN] two\n"))
	})

	It("skips guilds the bot left", func() {
		save("g", "c1", time.Second, "[EN] one")
		Expect(db.LeaveGuild(ctx, "g", time.Now())).To(Succeed())

		newWriter().StreamEnded(ended)
		Consistently(snd.all, 50*time.Millisecond).Should(BeEmpty())
	})

	It("drops logs still waiting when closed", func() {
		save("g", "c1", time.Second, "[EN] one")
		w := tllog.NewWriter(tllog.Config{Store: db, Sender: snd, Delay: time.Hour})
		w.StreamEnded(ended)
		w.Close()

		Expect(snd.all()).To(BeEmpty())
	})
})
