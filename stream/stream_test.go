package stream_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/VTGare/gatoraid/holodex"
	"github.com/VTGare/gatoraid/stream"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestStream(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Stream Suite")
}

type fakeSource struct {
	mu       sync.Mutex
	live     []holodex.Video
	videos   map[string]*holodex.Video
	liveErr  error
	videoErr error
	asked    [][]string
}

func (f *fakeSource) UsersLive(_ context.Context, ids []string) ([]holodex.Video, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, ids)
	return f.live, f.liveErr
}

func (f *fakeSource) Video(_ context.Context, id string) (*holodex.Video, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.videoErr != nil {
		return nil, f.videoErr
	}
	if v, ok := f.videos[id]; ok {
		return v, nil
	}
	return nil, holodex.ErrNotFound
}

var now = time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)

func video(id, channel string, status holodex.Status, scheduled time.Time) holodex.Video {
	return holodex.Video{
		ID: id, Title: "stream " + id, Type: "stream", Status: status,
		StartScheduled: scheduled, AvailableAt: scheduled,
		Channel: holodex.Channel{ID: channel, Name: channel + " ch", Photo: "https://img/" + channel},
	}
}

var _ = Describe("Tracker", func() {
	var (
		src      *fakeSource
		clock    time.Time
		channels []string
		avatars  map[string]string
		tracker  *stream.Tracker
		ctx      context.Context
	)

	BeforeEach(func() {
		ctx = context.Background()
		src = &fakeSource{videos: map[string]*holodex.Video{}}
		clock = now
		channels = []string{"UCa", "UCb"}
		avatars = nil

		tracker = stream.NewTracker(stream.Config{
			Source:      src,
			Channels:    func() []string { return channels },
			PrechatLead: 24 * time.Hour,
			Now:         func() time.Time { return clock },
			OnAvatars: func(_ context.Context, a map[string]string) {
				avatars = a
			},
		})
	})

	poll := func() []stream.Event {
		ExpectWithOffset(1, tracker.Poll(ctx)).To(Succeed())
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

	kinds := func(events []stream.Event) map[string]stream.EventKind {
		out := map[string]stream.EventKind{}
		for _, e := range events {
			out[e.Stream.VideoID] = e.Kind
		}
		return out
	}

	It("reports what's live or upcoming on the first poll", func() {
		src.live = []holodex.Video{
			video("live", "UCa", holodex.StatusLive, now.Add(-time.Hour)),
			video("soon", "UCa", holodex.StatusUpcoming, now.Add(23*time.Hour)),
			video("late", "UCb", holodex.StatusUpcoming, now.Add(-5*time.Hour)),
			video("later", "UCb", holodex.StatusUpcoming, now.Add(25*time.Hour)),
			video("abandoned", "UCb", holodex.StatusUpcoming, now.Add(-7*time.Hour)),
			video("unscheduled", "UCb", holodex.StatusUpcoming, time.Time{}),
		}

		Expect(kinds(poll())).To(Equal(map[string]stream.EventKind{
			"live":        stream.EventLive,
			"soon":        stream.EventPrechat,
			"late":        stream.EventPrechat,
			"later":       stream.EventPrechat,
			"unscheduled": stream.EventPrechat,
		}))
		Expect(tracker.Streams()).To(HaveLen(6))
		Expect(tracker.Streams()[0].VideoID).To(Equal("live"))
	})

	DescribeTable("marks upcoming streams past the prechat lead as distant",
		func(lead time.Duration, want map[string]bool) {
			tracker = stream.NewTracker(stream.Config{
				Source:      src,
				Channels:    func() []string { return channels },
				PrechatLead: lead,
				Now:         func() time.Time { return clock },
			})
			src.live = []holodex.Video{
				video("live", "UCa", holodex.StatusLive, now.Add(-time.Hour)),
				video("soon", "UCa", holodex.StatusUpcoming, now.Add(time.Hour)),
				video("tomorrow", "UCa", holodex.StatusUpcoming, now.Add(47*time.Hour)),
				video("next-year", "UCb", holodex.StatusUpcoming, now.Add(365*24*time.Hour)),
				video("unscheduled", "UCb", holodex.StatusUpcoming, time.Time{}),
			}

			distant := map[string]bool{}
			for _, e := range poll() {
				distant[e.Stream.VideoID] = e.Stream.Distant
				Expect(tracker.Distant(e.Stream.VideoID)).To(Equal(e.Stream.Distant))
			}
			Expect(distant).To(Equal(want))
		},
		Entry("two hours", 2*time.Hour, map[string]bool{
			"live": false, "soon": false, "tomorrow": true, "next-year": true, "unscheduled": true,
		}),
		Entry("two days", 48*time.Hour, map[string]bool{
			"live": false, "soon": false, "tomorrow": false, "next-year": true, "unscheduled": true,
		}),
	)

	It("sends prechat again when a distant stream gets near, then live", func() {
		src.live = []holodex.Video{video("v", "UCa", holodex.StatusUpcoming, now.Add(30*time.Hour))}

		events := poll()
		Expect(events).To(HaveLen(1))
		Expect(events[0].Kind).To(Equal(stream.EventPrechat))
		Expect(events[0].Stream.Distant).To(BeTrue())
		Expect(poll()).To(BeEmpty())

		clock = now.Add(7 * time.Hour)
		events = poll()
		Expect(events).To(HaveLen(1))
		Expect(events[0].Kind).To(Equal(stream.EventPrechat))
		Expect(events[0].Stream.Distant).To(BeFalse())
		Expect(tracker.Distant("v")).To(BeFalse())
		Expect(poll()).To(BeEmpty())

		src.live = []holodex.Video{video("v", "UCa", holodex.StatusLive, now.Add(30*time.Hour))}
		events = poll()
		Expect(events).To(HaveLen(1))
		Expect(events[0].Kind).To(Equal(stream.EventLive))
		Expect(events[0].Stream.StartedAt).To(Equal(now.Add(30 * time.Hour)))
		Expect(tracker.Distant("unknown")).To(BeFalse())
	})

	It("sends events live first, then near rooms, then distant ones", func() {
		src.live = []holodex.Video{
			video("unscheduled", "UCb", holodex.StatusUpcoming, time.Time{}),
			video("next-month", "UCb", holodex.StatusUpcoming, now.Add(30*24*time.Hour)),
			video("tonight", "UCa", holodex.StatusUpcoming, now.Add(5*time.Hour)),
			video("next-week", "UCb", holodex.StatusUpcoming, now.Add(7*24*time.Hour)),
			video("soon", "UCa", holodex.StatusUpcoming, now.Add(time.Hour)),
			video("live", "UCa", holodex.StatusLive, now.Add(-time.Hour)),
		}

		var order []string
		for _, e := range poll() {
			order = append(order, e.Stream.VideoID)
		}
		Expect(order).To(Equal([]string{"live", "soon", "tonight", "next-week", "next-month", "unscheduled"}))
	})

	It("only reports changes on later polls", func() {
		src.live = []holodex.Video{
			video("live", "UCa", holodex.StatusLive, now),
			video("soon", "UCa", holodex.StatusUpcoming, now.Add(time.Hour)),
		}
		poll()

		Expect(poll()).To(BeEmpty())
	})

	Describe("streams that disappear", func() {
		BeforeEach(func() {
			src.live = []holodex.Video{
				video("live", "UCa", holodex.StatusLive, now),
				video("upcoming", "UCb", holodex.StatusUpcoming, now.Add(time.Hour)),
			}
			poll()
			src.live = nil
		})

		It("ends ones Holodex says are over or gone", func() {
			past := video("live", "UCa", holodex.StatusPast, now)
			src.videos["live"] = &past

			events := poll()

			Expect(events).To(HaveLen(2))
			byID := map[string]stream.Event{}
			for _, e := range events {
				Expect(e.Kind).To(Equal(stream.EventEnded))
				byID[e.Stream.VideoID] = e
			}
			Expect(byID["live"].WasLive).To(BeTrue())
			Expect(byID["upcoming"].WasLive).To(BeFalse())
			Expect(tracker.Streams()).To(BeEmpty())
		})

		It("keeps ones that only fell out of the list", func() {
			stillLive := video("live", "UCa", holodex.StatusLive, now)
			stillUp := video("upcoming", "UCb", holodex.StatusUpcoming, now.Add(time.Hour))
			src.videos["live"], src.videos["upcoming"] = &stillLive, &stillUp

			Expect(poll()).To(BeEmpty())
			Expect(tracker.Streams()).To(HaveLen(2))
		})

		It("ends ones whose channel isn't tracked anymore", func() {
			stillLive := video("live", "UCa", holodex.StatusLive, now)
			stillUp := video("upcoming", "UCb", holodex.StatusUpcoming, now.Add(time.Hour))
			src.videos["live"], src.videos["upcoming"] = &stillLive, &stillUp
			channels = []string{"UCb"}

			events := poll()

			Expect(events).To(HaveLen(1))
			Expect(events[0].Stream.VideoID).To(Equal("live"))
			Expect(events[0].Kind).To(Equal(stream.EventEnded))
		})

		It("keeps them when Holodex can't be asked", func() {
			src.videoErr = errors.New("boom")

			Expect(poll()).To(BeEmpty())
			Expect(tracker.Streams()).To(HaveLen(2))
		})
	})

	It("keeps collabs on untracked channels while they mention a tracked one", func() {
		collab := video("collab", "UCother", holodex.StatusLive, now)
		collab.Mentions = []holodex.Channel{{ID: "UCa"}}
		src.live = []holodex.Video{collab}
		Expect(kinds(poll())).To(Equal(map[string]stream.EventKind{"collab": stream.EventLive}))

		src.live = nil
		src.videos["collab"] = &collab
		Expect(poll()).To(BeEmpty())
	})

	It("changes nothing when a poll fails", func() {
		src.live = []holodex.Video{video("live", "UCa", holodex.StatusLive, now)}
		poll()

		src.liveErr = errors.New("holodex is down")
		Expect(tracker.Poll(ctx)).To(MatchError("holodex is down"))
		Expect(tracker.Streams()).To(HaveLen(1))
	})

	It("ignores placeholders and reports avatars of tracked channels only", func() {
		placeholder := video("twitch", "UCa", holodex.StatusLive, now)
		placeholder.Type = "placeholder"
		src.live = []holodex.Video{
			placeholder,
			video("mine", "UCa", holodex.StatusUpcoming, now.Add(48*time.Hour)),
			video("theirs", "UCother", holodex.StatusUpcoming, now.Add(48*time.Hour)),
		}

		poll()

		Expect(tracker.Streams()).To(HaveLen(2))
		Expect(avatars).To(Equal(map[string]string{"UCa": "https://img/UCa"}))
	})

	It("asks for the current channel list every poll", func() {
		poll()
		channels = []string{"UCc"}
		poll()

		Expect(src.asked).To(Equal([][]string{{"UCa", "UCb"}, {"UCc"}}))
	})

	It("stops when the context ends", func() {
		ctx, cancel := context.WithCancel(ctx)
		done := make(chan error)
		go func() { done <- tracker.Run(ctx) }()

		cancel()
		Eventually(done).Should(Receive(MatchError(context.Canceled)))
	})
})

var _ = Describe("Classifier", func() {
	exempt := stream.Classifier{FreeChatStreams: func(id string) bool { return id == "UCkson" }}

	classify := func(title, topic string, status holodex.Status, channel string) stream.Stream {
		return exempt.Classify(holodex.Video{
			ID: "v", Title: title, TopicID: topic, Status: status, Type: "stream",
			Channel: holodex.Channel{ID: channel},
		})
	}

	DescribeTable("members-only",
		func(title, topic string, want bool) {
			Expect(classify(title, topic, holodex.StatusUpcoming, "UCa").MembersOnly).To(Equal(want))
		},
		Entry("tag", "Karaoke", "membersonly", true),
		Entry("bracketed", "【MEMBERS】more chekis", "", true),
		Entry("membership", "【Membership】 fortnite again", "", true),
		Entry("phrase", "Members Only karaoke", "", true),
		Entry("japanese", "【メン限】雑談", "", true),
		Entry("remember isn't members", "Do you remember? Minecraft", "", false),
		Entry("thanking members", "Thank you new members!", "", false),
	)

	DescribeTable("free chat",
		func(title, topic string, status holodex.Status, channel string, want bool) {
			Expect(classify(title, topic, status, channel).FreeChat).To(Equal(want))
		},
		Entry("Holodex topic", "Weekly schedule", "FreeChat", holodex.StatusUpcoming, "UCa", true),
		Entry("title", "【FREE CHAT】hang out here", "", holodex.StatusUpcoming, "UCa", true),
		Entry("free talk", "FREE TALK ‧₊˚♪", "", holodex.StatusUpcoming, "UCa", true),
		Entry("japanese", "フリーチャット", "", holodex.StatusUpcoming, "UCa", true),
		Entry("exempt channel, title only", "FREE CHAT and SCHEDULE", "", holodex.StatusUpcoming, "UCkson", false),
		Entry("exempt channel, Holodex topic", "FREE CHAT and SCHEDULE", "FreeChat", holodex.StatusUpcoming, "UCkson", true),
		Entry("live is never free chat", "free chat", "FreeChat", holodex.StatusLive, "UCa", false),
		Entry("normal stream", "Minecraft", "", holodex.StatusUpcoming, "UCa", false),
	)

	It("copies the basics and mentions", func() {
		s := exempt.Classify(holodex.Video{
			ID: "abc", Title: "t", Status: holodex.StatusLive, AvailableAt: now, StartScheduled: now.Add(-time.Minute),
			Channel: holodex.Channel{ID: "UCa", Name: "A ch"}, Mentions: []holodex.Channel{{ID: "UCb"}},
		})

		Expect(s.Status).To(Equal(stream.Live))
		Expect(s.StartedAt).To(Equal(now))
		Expect(s.ScheduledAt).To(Equal(now.Add(-time.Minute)))
		Expect(s.Mentions).To(Equal([]string{"UCb"}))
		Expect(s.URL()).To(Equal("https://youtu.be/abc"))
	})
})
