package translate

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	freeURL = "https://api-free.deepl.com"
	proURL  = "https://api.deepl.com"
)

type DeepL struct {
	key  string
	base string
	http *http.Client
}

type DeepLOption func(*DeepL)

func WithBaseURL(u string) DeepLOption { return func(d *DeepL) { d.base = strings.TrimSuffix(u, "/") } }

// Keys for the Free API end in ":fx" and only work on its own host.
func NewDeepL(key string, opts ...DeepLOption) *DeepL {
	d := &DeepL{key: key, base: proURL, http: &http.Client{Timeout: 10 * time.Second}}
	if strings.HasSuffix(key, ":fx") {
		d.base = freeURL
	}
	for _, o := range opts {
		o(d)
	}
	return d
}

type Result struct {
	Text string
	// Like "JA" or "EN", without a region.
	DetectedSource string
}

func (d *DeepL) Translate(ctx context.Context, text, target string) (Result, error) {
	body, err := json.Marshal(map[string]any{"text": []string{text}, "target_lang": target})
	if err != nil {
		return Result{}, err
	}

	var resp struct {
		Translations []struct {
			Text                   string `json:"text"`
			DetectedSourceLanguage string `json:"detected_source_language"`
		} `json:"translations"`
	}
	if err := d.do(ctx, http.MethodPost, "/v2/translate", body, &resp); err != nil {
		return Result{}, err
	}
	if len(resp.Translations) == 0 {
		return Result{}, fmt.Errorf("deepl: no translation in the response")
	}

	t := resp.Translations[0]
	return Result{Text: t.Text, DetectedSource: t.DetectedSourceLanguage}, nil
}

// Languages are the target languages the key can use.
func (d *DeepL) Languages(ctx context.Context) ([]Language, error) {
	var resp []struct {
		Language string `json:"language"`
		Name     string `json:"name"`
	}
	if err := d.do(ctx, http.MethodGet, "/v2/languages?type=target", nil, &resp); err != nil {
		return nil, err
	}

	out := make([]Language, len(resp))
	for i, l := range resp {
		out[i] = Language{Code: l.Language, Name: l.Name}
	}
	return out, nil
}

// Usage is how many characters were translated this billing period, and
// the plan's limit.
func (d *DeepL) Usage(ctx context.Context) (count, limit int64, err error) {
	var resp struct {
		CharacterCount int64 `json:"character_count"`
		CharacterLimit int64 `json:"character_limit"`
	}
	if err := d.do(ctx, http.MethodGet, "/v2/usage", nil, &resp); err != nil {
		return 0, 0, err
	}
	return resp.CharacterCount, resp.CharacterLimit, nil
}

type StatusError struct {
	Status int
}

func (e *StatusError) Error() string {
	// DeepL answers 456 when the plan's quota is used up.
	if e.Status == 456 {
		return "deepl: quota exceeded"
	}
	return fmt.Sprintf("deepl: %d %s", e.Status, http.StatusText(e.Status))
}

func (d *DeepL) do(ctx context.Context, method, path string, body []byte, into any) error {
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}

	req, err := http.NewRequestWithContext(ctx, method, d.base+path, r)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "DeepL-Auth-Key "+d.key)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := d.http.Do(req)
	if err != nil {
		// The URL never holds the key, so the error is safe to log.
		return fmt.Errorf("deepl: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return &StatusError{Status: resp.StatusCode}
	}

	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(into); err != nil {
		return fmt.Errorf("deepl: decode: %w", err)
	}
	return nil
}
