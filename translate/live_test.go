//go:build live

package translate_test

import (
	"context"
	"os"
	"time"

	"github.com/VTGare/gatoraid/translate"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Live DeepL", func() {
	It("translates a short line and reads the usage", func() {
		key := os.Getenv("GATORAID_DEEPL_API_KEY")
		if key == "" {
			Skip("GATORAID_DEEPL_API_KEY isn't set")
		}

		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		DeferCleanup(cancel)
		d := translate.NewDeepL(key)

		r, err := d.Translate(ctx, "おはよう", "EN-US")
		Expect(err).NotTo(HaveOccurred())
		Expect(r.DetectedSource).To(Equal("JA"))
		Expect(r.Text).NotTo(BeEmpty())

		_, limit, err := d.Usage(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(limit).To(BeNumerically(">", 0))

		langs, err := d.Languages(ctx)
		Expect(err).NotTo(HaveOccurred())
		for _, c := range translate.Common {
			Expect(langs).To(ContainElement(HaveField("Code", c.Code)), c.Code)
		}
	})
})
