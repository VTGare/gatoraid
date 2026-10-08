package stream_test

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"time"

	"github.com/VTGare/gatoraid/holodex"
	"github.com/VTGare/gatoraid/stream"
	"github.com/VTGare/gatoraid/twitch/helix"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type fakeTwitch struct {
	mu      sync.Mutex
	streams []helix.Stream
	err     error
	asked   [][]string
}

func (f *fakeTwitch) Streams(_ context.Context, usernames []string) ([]helix.Stream, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, usernames)
	return f.streams, f.err
}

func twitchStream(id, username string) helix.Stream {
	return helix.Stream{
		ID: id, Username: username, DisplayName: username + " name", Type: "live", Title: "twitch " + id,
		StartedAt:    now.Add(-time.Minute),
		ThumbnailURL: "https://static-cdn.jtvnw.net/previews-ttv/live_user_" + username + "-{width}x{height}.jpg",
	}
}

var _ = Describe("Tracker on Twitch", func() {
	var (
		ctx       context.Context
		tw        *fakeTwitch
		usernames map[string]string
		tracker   *stream.Tracker
		holo      *fakeSource
		channels  []string
	)

	BeforeEach(func() {
		ctx = context.Background()
		tw = &fakeTwitch{}
		holo = &fakeSource{videos: map[string]*holodex.Video{}}
		channels = []string{"UCa", "UCb"}
		usernames = map[string]string{"alice": "UCa", "bob": "UCb"}

		tracker = stream.NewTracker(stream.Config{
			Source:         holo,
			Channels:       func() []string { return channels },
			Twitch:         tw,
			TwitchChannels: func() map[string]string { return usernames },
			PrechatLead:    24 * time.Hour,
			Now:            func() time.Time { return now },
		})
	})

	poll := func() []stream.Event {
		ExpectWithOffset(1, tracker.PollTwitch(ctx)).To(Succeed())
		var out []stream.Event
		for {
			select {
			case e := <-tracker.Events():
				out = append(out, e)
			default:
				return out
			}
		}
	}

	It("reports live Twitch streams as the streamer's", func() {
		tw.streams = []helix.Stream{twitchStream("111", "Alice")}

		events := poll()

		Expect(events).To(HaveLen(1))
		Expect(events[0].Kind).To(Equal(stream.EventLive))
		s := events[0].Stream
		Expect(s).To(Equal(stream.Stream{
			VideoID: "twitch:111", Platform: stream.Twitch, ChannelID: "UCa", ChannelName: "Alice name",
			Title: "twitch 111", Status: stream.Live, ScheduledAt: now.Add(-time.Minute), StartedAt: now.Add(-time.Minute),
			TwitchUsername: "alice",
			Thumbnail: "https://static-cdn.jtvnw.net/previews-ttv/live_user_Alice-1280x720.jpg?s=" +
				strconv.FormatInt(now.Add(-time.Minute).Unix(), 10),
		}))
		Expect(s.URL()).To(Equal("https://www.twitch.tv/alice"))
		Expect(s.ChannelURL()).To(Equal("https://www.twitch.tv/alice"))
		Expect(tw.asked).To(Equal([][]string{{"alice", "bob"}}))
		Expect(tracker.Streams()).To(HaveLen(1))
	})

	It("only reports a stream once", func() {
		tw.streams = []helix.Stream{twitchStream("111", "alice")}
		poll()

		tw.streams[0].Title = "new title"
		Expect(poll()).To(BeEmpty())
		Expect(tracker.Streams()[0].Title).To(Equal("new title"))
	})

	It("skips channels nobody follows and streams that aren't live", func() {
		notLive := twitchStream("222", "bob")
		notLive.Type = ""
		tw.streams = []helix.Stream{twitchStream("111", "stranger"), notLive}

		Expect(poll()).To(BeEmpty())
	})

	It("ends a stream after two polls without it", func() {
		tw.streams = []helix.Stream{twitchStream("111", "alice")}
		poll()

		tw.streams = nil
		Expect(poll()).To(BeEmpty())

		events := poll()
		Expect(events).To(HaveLen(1))
		Expect(events[0].Kind).To(Equal(stream.EventEnded))
		Expect(events[0].WasLive).To(BeTrue())
		Expect(events[0].Stream.VideoID).To(Equal("twitch:111"))
		Expect(tracker.Streams()).To(BeEmpty())
	})

	It("keeps a stream that missed one poll", func() {
		tw.streams = []helix.Stream{twitchStream("111", "alice")}
		poll()
		saved := tw.streams
		tw.streams = nil
		poll()

		tw.streams = saved
		Expect(poll()).To(BeEmpty())
		tw.streams = nil
		Expect(poll()).To(BeEmpty())
	})

	It("ends a stream right away when its channel isn't followed anymore", func() {
		tw.streams = []helix.Stream{twitchStream("111", "alice")}
		poll()

		delete(usernames, "alice")
		tw.streams = nil

		events := poll()
		Expect(events).To(HaveLen(1))
		Expect(events[0].Kind).To(Equal(stream.EventEnded))
	})

	It("treats a restart as a new stream", func() {
		tw.streams = []helix.Stream{twitchStream("111", "alice")}
		poll()

		tw.streams = []helix.Stream{twitchStream("112", "alice")}
		Expect(kindsOf(poll())).To(Equal(map[string]stream.EventKind{"twitch:112": stream.EventLive}))
		Expect(kindsOf(poll())).To(Equal(map[string]stream.EventKind{"twitch:111": stream.EventEnded}))
	})

	It("changes nothing when a poll fails", func() {
		tw.streams = []helix.Stream{twitchStream("111", "alice")}
		poll()

		tw.err = errors.New("twitch is down")
		Expect(tracker.PollTwitch(ctx)).To(MatchError("twitch is down"))
		Expect(tracker.PollTwitch(ctx)).To(HaveOccurred())

		Expect(tracker.Streams()).To(HaveLen(1))
	})

	It("leaves Twitch streams alone when Holodex polls", func() {
		tw.streams = []helix.Stream{twitchStream("111", "alice")}
		poll()

		Expect(tracker.Poll(ctx)).To(Succeed())

		Expect(tracker.Streams()).To(HaveLen(1))
		Expect(holo.asked).To(HaveLen(1))
	})

	It("does nothing without a Twitch source", func() {
		tracker = stream.NewTracker(stream.Config{Source: holo, Channels: func() []string { return channels }})

		Expect(tracker.PollTwitch(ctx)).To(Succeed())
	})
})

func kindsOf(events []stream.Event) map[string]stream.EventKind {
	out := map[string]stream.EventKind{}
	for _, e := range events {
		out[e.Stream.VideoID] = e.Kind
	}
	return out
}
