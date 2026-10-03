package tldex_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/VTGare/gatoraid/holodex/tldex"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestTLdex(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "TLdex Suite")
}

// fakeServer speaks just enough Socket.IO to stand in for Holodex.
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

	_ = f.write(conn, `0{"sid":"engine","upgrades":[],"pingInterval":200,"pingTimeout":200}`)
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			return
		}

		f.mu.Lock()
		f.received = append(f.received, string(data))
		f.mu.Unlock()

		if string(data) == "40" {
			_ = f.write(conn, `40{"sid":"socket"}`)
		}
	}
}

func (f *fakeServer) write(conn *websocket.Conn, packet string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return conn.WriteMessage(websocket.TextMessage, []byte(packet))
}

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

var _ = Describe("Client", func() {
	var (
		srv    *fakeServer
		client *tldex.Client
		run    func()
	)

	BeforeEach(func() {
		// The client sends Holodex's Origin, which gorilla rejects by default.
		srv = &fakeServer{upgrader: websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}}
		hs := httptest.NewServer(srv)
		DeferCleanup(hs.Close)

		client = tldex.New(tldex.WithURL("ws"+strings.TrimPrefix(hs.URL, "http")), tldex.WithRetry(10*time.Millisecond))

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

	subscribed := func(video string) string {
		return `42["subscribe",{"lang":"en","video_id":"` + video + `"}]`
	}

	It("connects and subscribes to streams added before and after", func() {
		updates := client.Subscribe("before")
		run()

		Eventually(srv.got).Should(ContainElements("40", subscribed("before")))

		client.Subscribe("after")
		Eventually(srv.got).Should(ContainElement(subscribed("after")))
		Expect(updates).NotTo(BeClosed())
	})

	It("delivers start times and chat lines", func() {
		updates := client.Subscribe("v")
		run()
		Eventually(srv.got).Should(ContainElement(subscribed("v")))

		conn := srv.last()
		Expect(srv.write(conn, `42["subscribeSuccess",{"id":"v","status":"live","start_actual":"2026-10-02T01:00:08.000Z"}]`)).To(Succeed())
		Expect(srv.write(conn, `42["v/en",{"name":"@translator","channel_id":"UCtl","timestamp":1790904533504,"video_offset":1708.5,"message":"[EN] hello","is_tl":true,"is_moderator":false,"is_vtuber":false,"is_verified":false}]`)).To(Succeed())
		Expect(srv.write(conn, `42["v/en",{"name":"mchad user","timestamp":1790904534000,"message":"line","source":"MChad"}]`)).To(Succeed())
		Expect(srv.write(conn, `42["v/en",{"type":"update","live_viewers":10,"status":"live"}]`)).To(Succeed())
		Expect(srv.write(conn, `42["other/en",{"name":"x","message":"not ours"}]`)).To(Succeed())
		Expect(srv.write(conn, `42["v/en",{"type":"end"}]`)).To(Succeed())

		var u tldex.Update
		Eventually(updates).Should(Receive(&u))
		Expect(u.StartedAt).To(Equal(time.Date(2026, 10, 2, 1, 0, 8, 0, time.UTC)))

		Eventually(updates).Should(Receive(&u))
		Expect(*u.Message).To(Equal(tldex.Message{
			VideoID: "v", ChannelID: "UCtl", Name: "@translator", Text: "[EN] hello",
			Time: time.UnixMilli(1790904533504), TL: true,
		}))

		Eventually(updates).Should(Receive(&u))
		Expect(u.Message.ChannelID).To(BeEmpty())
		Expect(u.Message.TL).To(BeTrue())
		Expect(u.Message.Source).To(Equal("MChad"))

		Consistently(updates).ShouldNot(Receive())
	})

	It("answers pings", func() {
		run()
		Eventually(srv.got).Should(ContainElement("40"))

		Expect(srv.write(srv.last(), "2")).To(Succeed())

		Eventually(srv.got).Should(ContainElement("3"))
	})

	It("reconnects and resubscribes when the connection drops", func() {
		client.Subscribe("v")
		run()
		Eventually(srv.got).Should(ContainElement(subscribed("v")))

		Expect(srv.last().Close()).To(Succeed())

		Eventually(srv.connections).Should(Equal(2))
		Eventually(func() int {
			n := 0
			for _, m := range srv.got() {
				if m == subscribed("v") {
					n++
				}
			}
			return n
		}).Should(Equal(2))
	})

	It("reconnects when pings stop", func() {
		run()
		Eventually(srv.got).Should(ContainElement("40"))

		// The fake promises a ping every 200ms and never sends one.
		Eventually(srv.connections, 2*time.Second).Should(BeNumerically(">=", 2))
	})

	It("closes the channel and tells the server on unsubscribe", func() {
		updates := client.Subscribe("v")
		run()
		Eventually(srv.got).Should(ContainElement(subscribed("v")))

		client.Unsubscribe("v")

		Expect(updates).To(BeClosed())
		Eventually(srv.got).Should(ContainElement(`42["unsubscribe",{"lang":"en","video_id":"v"}]`))
	})
})
