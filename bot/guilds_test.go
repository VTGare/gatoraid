package bot

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/VTGare/gatoraid/internal/config"
	"github.com/VTGare/gatoraid/store"
	"github.com/VTGare/gatoraid/store/sqlite"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type sent struct {
	Path string
	Body map[string]any
}

// recorder fakes Discord's API: it records requests and replies with {}.
type recorder struct {
	mu   sync.Mutex
	reqs []sent
}

func (r *recorder) RoundTrip(req *http.Request) (*http.Response, error) {
	var body map[string]any
	if req.Body != nil {
		raw, _ := io.ReadAll(req.Body)
		_ = json.Unmarshal(raw, &body)
	}

	r.mu.Lock()
	r.reqs = append(r.reqs, sent{Path: req.URL.Path, Body: body})
	r.mu.Unlock()

	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader("{}")),
		Request:    req,
	}, nil
}

func (r *recorder) notices(channel string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()

	var out []string
	for _, req := range r.reqs {
		if strings.HasSuffix(req.Path, "/channels/"+channel+"/messages") {
			content, _ := req.Body["content"].(string)
			out = append(out, content)
		}
	}
	return out
}

var _ = Describe("Guild lifecycle", func() {
	const logChannel = "999"

	var (
		b   *Bot
		s   *discordgo.Session
		rec *recorder
		ctx context.Context
	)

	BeforeEach(func() {
		ctx = context.Background()

		st, err := sqlite.Open(ctx, filepath.Join(GinkgoT().TempDir(), "bot.db"))
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(st.Close)

		cfg := &config.Config{Discord: config.Discord{Token: "test", LogChannelID: logChannel}}
		b, err = New(cfg, slog.New(slog.DiscardHandler), st)
		Expect(err).NotTo(HaveOccurred())

		rec = &recorder{}
		s = b.Session
		s.Client = &http.Client{Transport: rec}
	})

	guildCreate := func(id, name string) *discordgo.GuildCreate {
		return &discordgo.GuildCreate{Guild: &discordgo.Guild{ID: id, Name: name, MemberCount: 42}}
	}

	guild := func(id string) *store.Guild {
		g, err := b.Store.Guild(ctx, id)
		Expect(err).NotTo(HaveOccurred())
		return g
	}

	It("records and announces a new guild", func() {
		b.onGuildCreate(s, guildCreate("1", "Pomu Fan Club"))

		Expect(guild("1").Active()).To(BeTrue())
		Expect(rec.notices(logChannel)).To(ConsistOf("Joined **Pomu Fan Club** (`1`), 42 members."))
	})

	It("stays quiet for guilds it was already in, like on startup", func() {
		b.onGuildCreate(s, guildCreate("1", "Pomu Fan Club"))
		b.onGuildCreate(s, guildCreate("1", "Pomu Fan Club"))

		Expect(rec.notices(logChannel)).To(HaveLen(1))
	})

	It("ignores unavailable guilds", func() {
		b.onGuildCreate(s, &discordgo.GuildCreate{Guild: &discordgo.Guild{ID: "1", Unavailable: true}})

		_, err := b.Store.Guild(ctx, "1")
		Expect(err).To(MatchError(store.ErrGuildNotFound))
		Expect(rec.notices(logChannel)).To(BeEmpty())
	})

	It("soft-deletes on leave and restores on rejoin", func() {
		b.onGuildCreate(s, guildCreate("1", "Pomu Fan Club"))
		b.onGuildDelete(s, &discordgo.GuildDelete{
			Guild:        &discordgo.Guild{ID: "1"},
			BeforeDelete: &discordgo.Guild{ID: "1", Name: "Pomu Fan Club"},
		})

		Expect(guild("1").Active()).To(BeFalse())

		b.onGuildCreate(s, guildCreate("1", "Pomu Fan Club"))

		Expect(guild("1").Active()).To(BeTrue())
		Expect(rec.notices(logChannel)).To(Equal([]string{
			"Joined **Pomu Fan Club** (`1`), 42 members.",
			"Left **Pomu Fan Club** (`1`). Its data is kept for 30 days.",
			"Rejoined **Pomu Fan Club** (`1`), 42 members. Settings restored.",
		}))
	})

	It("keeps guilds through outages", func() {
		b.onGuildCreate(s, guildCreate("1", "Pomu Fan Club"))
		b.onGuildDelete(s, &discordgo.GuildDelete{Guild: &discordgo.Guild{ID: "1", Unavailable: true}})

		Expect(guild("1").Active()).To(BeTrue())
		Expect(rec.notices(logChannel)).To(HaveLen(1))
	})

	It("marks guilds missing from READY as left", func() {
		for _, id := range []string{"1", "2"} {
			b.onGuildCreate(s, guildCreate(id, "g"+id))
		}

		b.onReady(s, &discordgo.Ready{
			User:   &discordgo.User{Username: "GatorAid"},
			Guilds: []*discordgo.Guild{{ID: "1", Unavailable: true}},
		})

		Expect(guild("1").Active()).To(BeTrue())
		Expect(guild("2").Active()).To(BeFalse())
		Expect(rec.notices(logChannel)).To(ContainElement("Removed from `2` while offline. Its data is kept for 30 days."))
	})

	It("posts nothing without a log channel", func() {
		b.Config.Discord.LogChannelID = ""

		b.onGuildCreate(s, guildCreate("1", "Pomu Fan Club"))

		Expect(rec.reqs).To(BeEmpty())
	})

	It("purges only guilds past retention", func() {
		b.onGuildCreate(s, guildCreate("old", "old"))
		b.onGuildCreate(s, guildCreate("recent", "recent"))
		Expect(b.Store.LeaveGuild(ctx, "old", time.Now().Add(-store.GuildRetention-time.Hour))).To(Succeed())
		Expect(b.Store.LeaveGuild(ctx, "recent", time.Now())).To(Succeed())

		b.purgeGuilds(ctx)

		_, err := b.Store.Guild(ctx, "old")
		Expect(err).To(MatchError(store.ErrGuildNotFound))
		Expect(guild("recent").Active()).To(BeFalse())
	})
})
