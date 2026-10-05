package notify

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/VTGare/gatoraid/relay"
	"github.com/VTGare/gatoraid/sender"
	"github.com/VTGare/gatoraid/store"
	"github.com/VTGare/gatoraid/streamers"
	"github.com/VTGare/gatoraid/subs"
	"github.com/VTGare/gatoraid/youtube/posts"
)

const (
	defaultInterval = 10 * time.Minute
	// Spacing between two channels' checks when many are followed. A cycle
	// then takes longer than the interval.
	minGap = 2 * time.Second
	// Embed descriptions max out at 4096 characters.
	maxPostText = 4000

	seenChannel = "posts-channel"
	seenPost    = "post"
)

type PostSource interface {
	Posts(ctx context.Context, channelID string) ([]posts.Post, error)
}

type PostsConfig struct {
	Source   PostSource
	Registry *streamers.Registry
	Subs     *subs.Service
	Store    store.DedupeStore
	Sender   relay.Sender
	// How often each followed channel is checked.
	Interval time.Duration
	Color    int
	Log      *slog.Logger
}

// Posts checks the Posts tab of every channel a guild follows and
// announces posts it hasn't seen. The first check of a channel only
// records what's there.
type Posts struct {
	cfg      PostsConfig
	failures map[string]int
}

func NewPosts(cfg PostsConfig) *Posts {
	if cfg.Interval <= 0 {
		cfg.Interval = defaultInterval
	}
	if cfg.Log == nil {
		cfg.Log = slog.New(slog.DiscardHandler)
	}
	return &Posts{cfg: cfg, failures: map[string]int{}}
}

func (p *Posts) Run(ctx context.Context) error {
	for {
		channels := p.channels()
		if len(channels) == 0 {
			if !sleep(ctx, min(p.cfg.Interval, time.Minute)) {
				return ctx.Err()
			}
			continue
		}

		gap := max(p.cfg.Interval/time.Duration(len(channels)), minGap)
		ok, failed := 0, 0
		for _, ch := range channels {
			if p.Check(ctx, ch) {
				ok++
			} else {
				failed++
			}
			if !sleep(ctx, gap) {
				return ctx.Err()
			}
		}

		if ok == 0 && failed > 0 {
			p.cfg.Log.Error("every posts check failed this round; the Posts tab format may have changed",
				slog.Int("channels", failed))
		}
	}
}

// channels are the followed YouTube channels, groups and all expanded to
// their members.
func (p *Posts) channels() []string {
	set := map[string]bool{}
	for _, sub := range p.cfg.Subs.All(store.FeaturePosts) {
		switch sub.Target.Kind {
		case store.TargetChannel:
			if _, ok := p.cfg.Registry.Streamer(sub.Target.ID); ok {
				set[sub.Target.ID] = true
			}
		case store.TargetGroup:
			for _, st := range p.cfg.Registry.Members(sub.Target.ID) {
				set[st.ChannelID] = true
			}
		case store.TargetAll:
			for _, st := range p.cfg.Registry.Streamers() {
				if st.Curated() {
					set[st.ChannelID] = true
				}
			}
		}
	}

	out := make([]string, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	slices.Sort(out)
	return out
}

// Check reads one channel's posts, announces new ones and reports whether
// the read worked.
func (p *Posts) Check(ctx context.Context, channelID string) bool {
	log := p.cfg.Log.With(slog.String("channel_id", channelID))

	list, err := p.cfg.Source.Posts(ctx, channelID)
	if err != nil {
		if ctx.Err() == nil {
			p.failures[channelID]++
			log.Warn("couldn't read posts", slog.Int("failures_in_a_row", p.failures[channelID]), slog.Any("error", err))
		}
		return false
	}
	delete(p.failures, channelID)

	firstLook, err := p.cfg.Store.MarkSeen(ctx, seenChannel, []string{channelID})
	if err != nil {
		log.Error("failed to record a posts check", slog.Any("error", err))
		return true
	}

	ids := make([]string, len(list))
	for i, post := range list {
		ids[i] = post.ID
	}
	fresh, err := p.cfg.Store.MarkSeen(ctx, seenPost, ids)
	if err != nil {
		log.Error("failed to record seen posts", slog.Any("error", err))
		return true
	}
	if len(firstLook) > 0 {
		return true
	}

	// Oldest first, so several new posts arrive in order. Only recent posts
	// are announced, in case one comes back after being forgotten.
	for _, post := range slices.Backward(list) {
		if post.Recent && post.ChannelID == channelID && slices.Contains(fresh, post.ID) {
			p.announce(post)
		}
	}
	return true
}

func (p *Posts) announce(post posts.Post) {
	for _, sub := range p.cfg.Subs.Match(store.FeaturePosts, post.ChannelID) {
		p.cfg.Sender.Send(sender.Message{ChannelID: sub.ChannelID, Send: PostMessage(post, sub.RoleID, p.cfg.Color)})
	}
}

func PostMessage(post posts.Post, roleID string, color int) *discordgo.MessageSend {
	var desc strings.Builder
	desc.WriteString(relay.Truncate(post.Text, maxPostText))
	if post.VideoID != "" {
		desc.WriteString("\n\nhttps://youtu.be/")
		desc.WriteString(post.VideoID)
	}
	if len(post.Poll) > 0 {
		desc.WriteString("\n\n**Poll**")
		for _, choice := range post.Poll {
			desc.WriteString("\n- ")
			desc.WriteString(relay.EscapeMarkdown(choice))
		}
	}

	e := &discordgo.MessageEmbed{
		Title:       "New post",
		URL:         post.URL(),
		Description: strings.TrimSpace(desc.String()),
		Color:       color,
		Author: &discordgo.MessageEmbedAuthor{
			Name:    post.Author,
			URL:     "https://www.youtube.com/channel/" + post.ChannelID,
			IconURL: post.AvatarURL,
		},
	}
	if len(post.Images) > 0 {
		e.Image = &discordgo.MessageEmbedImage{URL: post.Images[0]}
	}
	switch more := len(post.Images) - 1; {
	case more == 1:
		e.Footer = &discordgo.MessageEmbedFooter{Text: "1 more image on YouTube"}
	case more > 1:
		e.Footer = &discordgo.MessageEmbedFooter{Text: fmt.Sprintf("%d more images on YouTube", more)}
	}

	return relay.RoleMessage(e, roleID)
}

func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
