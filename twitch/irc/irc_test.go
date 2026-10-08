package irc_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/VTGare/gatoraid/twitch/irc"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestIRC(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "IRC Suite")
}

// fakeServer speaks just enough IRC to stand in for Twitch chat.
type fakeServer struct {
	mu       sync.Mutex
	conns    []*websocket.Conn
	received []string
	upgrader websocket.Upgrader
}

func (f *fakeServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	conn, err := f.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	f.mu.Lock()
	f.conns = append(f.conns, conn)
	f.mu.Unlock()

	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			return
		}

		for l := range strings.SplitSeq(strings.TrimSpace(string(data)), "\r\n") {
			f.mu.Lock()
			f.received = append(f.received, l)
			f.mu.Unlock()

			if strings.HasPrefix(l, "NICK ") {
				nick := strings.TrimPrefix(l, "NICK ")
				_ = f.write(conn, ":tmi.twitch.tv 001 "+nick+" :Welcome, GLHF!\r\n:tmi.twitch.tv 376 "+nick+" :>")
			}
		}
	}
}

func (f *fakeServer) write(conn *websocket.Conn, lines string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return conn.WriteMessage(websocket.TextMessage, []byte(lines+"\r\n"))
}

func (f *fakeServer) send(lines string) error { return f.write(f.last(), lines) }

func (f *fakeServer) last() *websocket.Conn {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.conns[len(f.conns)-1]
}

func (f *fakeServer) got() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.received...)
}

func (f *fakeServer) connections() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.conns)
}

func privmsg(channel, tags, text string) string {
	return "@" + tags + " :viewer!viewer@viewer.tmi.twitch.tv PRIVMSG #" + channel + " :" + text
}

var _ = Describe("Client", func() {
	var (
		srv    *fakeServer
		client *irc.Client
		run    func()
	)

	BeforeEach(func() {
		srv = &fakeServer{}
		hs := httptest.NewServer(srv)
		DeferCleanup(hs.Close)

		client = irc.New(irc.WithURL("ws"+strings.TrimPrefix(hs.URL, "http")),
			irc.WithRetry(10*time.Millisecond), irc.WithJoinEvery(time.Millisecond))

		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		started := false
		run = func() {
			started = true
			go func() { done <- client.Run(ctx) }()
		}
		DeferCleanup(func() {
			cancel()
			if started {
				Eventually(done).Should(Receive(MatchError(context.Canceled)))
			}
		})
	})

	It("logs in anonymously and joins channels followed before and after", func() {
		_, stopBefore := client.Subscribe("Before")
		defer stopBefore()
		run()

		Eventually(srv.got).Should(ContainElement("JOIN #before"))
		Expect(srv.got()[:3]).To(HaveExactElements(
			"CAP REQ :twitch.tv/tags twitch.tv/commands", "PASS SCHMOOPIIE", MatchRegexp(`^NICK justinfan\d+$`)))

		_, stopAfter := client.Subscribe("after")
		defer stopAfter()
		Eventually(srv.got).Should(ContainElement("JOIN #after"))
	})

	It("never joins a malformed username", func() {
		lines, stop := client.Subscribe("two words")
		stop()
		_, stopGood := client.Subscribe("good")
		defer stopGood()
		run()

		Eventually(srv.got).Should(ContainElement("JOIN #good"))
		Expect(srv.got()).NotTo(ContainElement(ContainSubstring("two words")))
		Consistently(lines).ShouldNot(Receive())
	})

	It("delivers lines with what the tags say about the author", func() {
		lines, stop := client.Subscribe("streamer")
		defer stop()
		run()
		Eventually(srv.got).Should(ContainElement("JOIN #streamer"))

		Expect(srv.send(privmsg("streamer",
			`badge-info=subscriber/12;badges=moderator/1,subscriber/12,partner/1;bits=100;color=#FF0000;`+
				`display-name=View\sEr;emotes=25:7-11,19-23;id=abc-123;mod=1;room-id=1;subscriber=1;`+
				`tmi-sent-ts=1759996800000;user-id=42`,
			"hello! Kappa and 😀 Kappa"))).To(Succeed())

		var m *irc.Message
		Eventually(lines).Should(Receive(&m))
		Expect(*m).To(Equal(irc.Message{
			ID: "abc-123", Channel: "streamer", UserID: "42", Username: "viewer", DisplayName: "View Er",
			Text: "hello! Kappa and 😀 Kappa", Time: time.UnixMilli(1759996800000),
			Moderator: true, Subscriber: true, Partner: true, Bits: 100, Emotes: []string{"Kappa"},
		}))
	})

	It("finds emotes after emoji by rune position", func() {
		lines, stop := client.Subscribe("streamer")
		defer stop()
		run()
		Eventually(srv.got).Should(ContainElement("JOIN #streamer"))

		Expect(srv.send(privmsg("streamer", "emotes=1:3-7/2:9-12/3:40-50;id=1;user-id=2", "😀😀 Kappa LUL5"))).To(Succeed())

		var m *irc.Message
		Eventually(lines).Should(Receive(&m))
		Expect(m.Emotes).To(Equal([]string{"Kappa", "LUL5"}))
	})

	It("marks the broadcaster and unwraps /me", func() {
		lines, stop := client.Subscribe("streamer")
		defer stop()
		run()
		Eventually(srv.got).Should(ContainElement("JOIN #streamer"))

		Expect(srv.send(privmsg("streamer", "badges=;id=1;room-id=7;user-id=7;display-name=", "\x01ACTION waves\x01"))).To(Succeed())

		var m *irc.Message
		Eventually(lines).Should(Receive(&m))
		Expect(m.Broadcaster).To(BeTrue())
		Expect(m.Text).To(Equal("waves"))
		Expect(m.DisplayName).To(Equal("viewer"))
	})

	It("gives every subscriber of a channel its lines and leaves once the last one stops", func() {
		a, stopA := client.Subscribe("streamer")
		b, stopB := client.Subscribe("streamer")
		run()
		Eventually(srv.got).Should(ContainElement("JOIN #streamer"))
		Expect(srv.got()).To(HaveEach(Not(Equal("JOIN #streamer #streamer"))))

		Expect(srv.send(privmsg("streamer", "id=1;user-id=2", "hi"))).To(Succeed())
		Eventually(a).Should(Receive())
		Eventually(b).Should(Receive())

		stopA()
		Consistently(srv.got).ShouldNot(ContainElement("PART #streamer"))
		Eventually(a).Should(BeClosed())

		stopB()
		Eventually(srv.got).Should(ContainElement("PART #streamer"))
		Eventually(b).Should(BeClosed())
		stopB()
	})

	It("ignores lines from other channels and other commands", func() {
		lines, stop := client.Subscribe("streamer")
		defer stop()
		run()
		Eventually(srv.got).Should(ContainElement("JOIN #streamer"))

		Expect(srv.send(privmsg("someone_else", "id=1", "hi") + "\r\n" +
			"@msg-id=sub :tmi.twitch.tv USERNOTICE #streamer :resub message\r\n" +
			":tmi.twitch.tv CLEARCHAT #streamer :viewer")).To(Succeed())

		Consistently(lines).ShouldNot(Receive())
	})

	It("answers pings", func() {
		run()
		Eventually(srv.connections).Should(Equal(1))
		Eventually(srv.got).Should(ContainElement(HavePrefix("NICK ")))

		Expect(srv.send("PING :tmi.twitch.tv")).To(Succeed())

		Eventually(srv.got).Should(ContainElement("PONG :tmi.twitch.tv"))
	})

	It("reconnects when asked to and joins everything again", func() {
		_, stop := client.Subscribe("streamer")
		defer stop()
		run()
		Eventually(srv.got).Should(ContainElement("JOIN #streamer"))

		Expect(srv.send(":tmi.twitch.tv RECONNECT")).To(Succeed())

		Eventually(srv.connections).Should(Equal(2))
		Eventually(func() int {
			n := 0
			for _, l := range srv.got() {
				if l == "JOIN #streamer" {
					n++
				}
			}
			return n
		}).Should(Equal(2))
	})

	It("reconnects after a dropped connection", func() {
		run()
		Eventually(srv.connections).Should(Equal(1))

		Expect(srv.last().Close()).To(Succeed())

		Eventually(srv.connections).Should(Equal(2))
	})

	It("starts over when Twitch refuses the login", func() {
		run()
		Eventually(srv.connections).Should(Equal(1))

		Expect(srv.send(":tmi.twitch.tv NOTICE * :Login authentication failed")).To(Succeed())

		Eventually(srv.connections).Should(BeNumerically(">=", 2))
	})
})
