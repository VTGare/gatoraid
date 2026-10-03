// Chatwatch prints live chat the way the bot reads it, for trying the
// reader against real streams.
//
//	go run ./cmd/chatwatch [-tldex] VIDEO_ID...
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/VTGare/gatoraid/bot"
	"github.com/VTGare/gatoraid/chat"
	"github.com/VTGare/gatoraid/holodex/tldex"
)

func main() {
	withTLdex := flag.Bool("tldex", false, "merge in Holodex's TLdex feed")
	verbose := flag.Bool("v", false, "log retries and reconnects")
	flag.Parse()

	if flag.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "usage: chatwatch [-tldex] [-v] VIDEO_ID...")
		os.Exit(2)
	}

	level := slog.LevelError
	if *verbose {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	var tl *tldex.Client
	if *withTLdex {
		tl = tldex.New(tldex.WithLogger(log))
		go func() { _ = tl.Run(ctx) }()
	}

	chats := bot.NewChatManager(tl, nil, log)
	for _, id := range flag.Args() {
		chats.Start(ctx, id)
	}

	running := flag.NArg()
	for running > 0 {
		select {
		case <-ctx.Done():
			chats.Wait()
			return
		case e := <-chats.Events():
			switch e.Kind {
			case chat.EventComment:
				printComment(e.Comment, flag.NArg() > 1)
			case chat.EventStarted:
				fmt.Printf("-- %s started at %s\n", e.VideoID, e.StartedAt.Local().Format("15:04:05"))
			case chat.EventStopped:
				fmt.Printf("-- %s stopped: %v\n", e.VideoID, e.Err)
				running--
			}
		}
	}
}

func printComment(c *chat.Comment, showVideo bool) {
	var tags []string
	for _, t := range []struct {
		name string
		on   bool
	}{
		{"owner", c.Owner}, {"mod", c.Moderator}, {"verified", c.Verified}, {"member", c.Member},
		{"tl", c.TL}, {"vtuber", c.VTuber},
	} {
		if t.on {
			tags = append(tags, t.name)
		}
	}
	if c.SuperChat != "" {
		tags = append(tags, c.SuperChat)
	}

	prefix := c.Time.Local().Format("15:04:05")
	if showVideo {
		prefix += " " + c.VideoID
	}
	if c.Source != chat.SourceYouTube {
		prefix += " [" + string(c.Source) + "]"
	}

	label := ""
	if len(tags) > 0 {
		label = " (" + strings.Join(tags, ", ") + ")"
	}
	fmt.Printf("%s %s%s: %s\n", prefix, c.AuthorName, label, c.Text)
}
