package commands_test

import (
	"log/slog"

	"github.com/VTGare/gatoraid/bot"
	"github.com/VTGare/gatoraid/commands"
	"github.com/VTGare/gatoraid/internal/config"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Register", func() {
	It("registers valid slash commands", func() {
		b, err := bot.New(&config.Config{Discord: config.Discord{Token: "test"}}, slog.New(slog.DiscardHandler), nil)
		Expect(err).NotTo(HaveOccurred())

		Expect(commands.Register(b)).To(Succeed())

		global, _ := b.Router.ApplicationCommands()
		names := make([]string, 0, len(global))
		for _, c := range global {
			names = append(names, c.Name)
		}
		Expect(names).To(ContainElement("help"))
	})
})
