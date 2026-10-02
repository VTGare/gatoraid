package translate_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"

	"github.com/VTGare/gatoraid/translate"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("DeepL", func() {
	var (
		srv    *httptest.Server
		status int
		auth   string
		body   map[string]any
	)

	BeforeEach(func() {
		status = http.StatusOK
		srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			auth = r.Header.Get("Authorization")
			body = nil
			_ = json.NewDecoder(r.Body).Decode(&body)

			if status != http.StatusOK {
				w.WriteHeader(status)
				return
			}
			switch r.URL.Path {
			case "/v2/translate":
				_, _ = w.Write([]byte(`{"translations":[{"text":"Hello, everyone!","detected_source_language":"JA"}]}`))
			case "/v2/languages":
				Expect(r.URL.Query().Get("type")).To(Equal("target"))
				_, _ = w.Write([]byte(`[{"language":"EN-US","name":"English (American)","supports_formality":false}]`))
			case "/v2/usage":
				_, _ = w.Write([]byte(`{"character_count":12,"character_limit":500000}`))
			}
		}))
		DeferCleanup(srv.Close)
	})

	It("translates, lists languages and reports usage", func() {
		d := translate.NewDeepL("key:fx", translate.WithBaseURL(srv.URL))
		ctx := context.Background()

		r, err := d.Translate(ctx, "こんにちは、みんな！", "EN-US")
		Expect(err).NotTo(HaveOccurred())
		Expect(r).To(Equal(translate.Result{Text: "Hello, everyone!", DetectedSource: "JA"}))
		Expect(auth).To(Equal("DeepL-Auth-Key key:fx"))
		Expect(body).To(Equal(map[string]any{"text": []any{"こんにちは、みんな！"}, "target_lang": "EN-US"}))

		langs, err := d.Languages(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(langs).To(Equal([]translate.Language{{Code: "EN-US", Name: "English (American)"}}))

		count, limit, err := d.Usage(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(count).To(BeEquivalentTo(12))
		Expect(limit).To(BeEquivalentTo(500000))
	})

	It("reports the quota running out", func() {
		status = 456
		_, err := translate.NewDeepL("key", translate.WithBaseURL(srv.URL)).Translate(context.Background(), "x", "EN-US")
		Expect(err).To(MatchError("deepl: quota exceeded"))
	})
})

type fakeClient struct {
	mu          sync.Mutex
	calls       int
	count       int64
	limit       int64
	err         error
	languageReq int
}

func (f *fakeClient) Translate(_ context.Context, text, target string) (translate.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return translate.Result{}, f.err
	}
	return translate.Result{Text: text + " in " + target, DetectedSource: "JA"}, nil
}

func (f *fakeClient) Languages(context.Context) ([]translate.Language, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.languageReq++
	return []translate.Language{{Code: "FI", Name: "Finnish"}}, nil
}

func (f *fakeClient) Usage(context.Context) (int64, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.count, f.limit, nil
}

var _ = Describe("Service", func() {
	ctx := context.Background()

	It("translates each text once per target language", func() {
		client := &fakeClient{limit: 1000}
		s := translate.NewService(client, 1000, nil)

		for range 3 {
			r, err := s.Translate(ctx, "やあ", "EN-US")
			Expect(err).NotTo(HaveOccurred())
			Expect(r.Text).To(Equal("やあ in EN-US"))
		}
		_, err := s.Translate(ctx, "やあ", "DE")
		Expect(err).NotTo(HaveOccurred())

		Expect(client.calls).To(Equal(2))
	})

	It("stops at the budget and resumes when the usage resets", func() {
		client := &fakeClient{limit: 500000}
		s := translate.NewService(client, 5, nil)

		_, err := s.Translate(ctx, "abc", "EN-US")
		Expect(err).NotTo(HaveOccurred())
		_, err = s.Translate(ctx, "def", "EN-US")
		Expect(err).To(MatchError(translate.ErrBudget))
		_, err = s.Translate(ctx, "g", "EN-US")
		Expect(err).To(MatchError(translate.ErrBudget))
		Expect(client.calls).To(Equal(1))

		client.count = 0
		Expect(s.RefreshUsage(ctx)).To(Succeed())
		_, err = s.Translate(ctx, "g", "EN-US")
		Expect(err).NotTo(HaveOccurred())
	})

	It("uses DeepL's limit when it's lower than the budget", func() {
		client := &fakeClient{count: 98, limit: 100}
		s := translate.NewService(client, 500000, nil)
		Expect(s.RefreshUsage(ctx)).To(Succeed())

		_, err := s.Translate(ctx, "abc", "EN-US")
		Expect(err).To(MatchError(translate.ErrBudget))
	})

	It("pauses when DeepL says the quota is gone", func() {
		client := &fakeClient{limit: 500000, err: &translate.StatusError{Status: 456}}
		s := translate.NewService(client, 500000, nil)

		_, err := s.Translate(ctx, "abc", "EN-US")
		Expect(err).To(HaveOccurred())
		_, err = s.Translate(ctx, "def", "EN-US")
		Expect(err).To(MatchError(translate.ErrBudget))
	})

	It("caches the language list", func() {
		client := &fakeClient{}
		s := translate.NewService(client, 1, nil)

		for range 2 {
			langs, err := s.Languages(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(langs).To(HaveLen(1))
		}
		Expect(client.languageReq).To(Equal(1))
	})

	DescribeTable("SameLanguage",
		func(detected, target string, want bool) {
			Expect(translate.SameLanguage(detected, target)).To(Equal(want))
		},
		Entry("region variant", "EN", "EN-US", true),
		Entry("plain", "JA", "JA", true),
		Entry("different", "JA", "EN-US", false),
		Entry("script variant", "ZH", "ZH-HANS", true),
	)
})
