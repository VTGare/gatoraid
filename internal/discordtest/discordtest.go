// Package discordtest stands in for Discord's HTTP API in tests.
package discordtest

import (
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
	"sync"

	"github.com/bwmarrin/discordgo"
)

type Request struct {
	Method string
	Path   string
	Body   map[string]any
	// Uploaded files by name, for multipart requests.
	Files map[string]string
}

// Recorder records every request and replies with {}.
type Recorder struct {
	mu   sync.Mutex
	reqs []Request
}

func (r *Recorder) RoundTrip(req *http.Request) (*http.Response, error) {
	rec := Request{Method: req.Method, Path: req.URL.Path}
	if req.Body != nil {
		rec.Body, rec.Files = readBody(req)
	}

	r.mu.Lock()
	r.reqs = append(r.reqs, rec)
	r.mu.Unlock()

	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader("{}")),
		Request:    req,
	}, nil
}

// With attachments, discordgo sends multipart bodies with the JSON in a
// payload_json part.
func readBody(req *http.Request) (map[string]any, map[string]string) {
	var body map[string]any

	mediaType, params, _ := mime.ParseMediaType(req.Header.Get("Content-Type"))
	if !strings.HasPrefix(mediaType, "multipart/") {
		raw, _ := io.ReadAll(req.Body)
		_ = json.Unmarshal(raw, &body)
		return body, nil
	}

	files := map[string]string{}
	mr := multipart.NewReader(req.Body, params["boundary"])
	for {
		part, err := mr.NextPart()
		if err != nil {
			break
		}

		raw, _ := io.ReadAll(part)
		if part.FormName() == "payload_json" {
			_ = json.Unmarshal(raw, &body)
		} else if part.FileName() != "" {
			files[part.FileName()] = string(raw)
		}
	}

	return body, files
}

func (r *Recorder) Requests() []Request {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Request(nil), r.reqs...)
}

func (r *Recorder) Messages(channelID string) []string {
	var out []string
	for _, req := range r.Requests() {
		if strings.HasSuffix(req.Path, "/channels/"+channelID+"/messages") {
			content, _ := req.Body["content"].(string)
			out = append(out, content)
		}
	}
	return out
}

func (r *Recorder) Responses() []map[string]any {
	var out []map[string]any
	for _, req := range r.Requests() {
		if strings.HasSuffix(req.Path, "/callback") {
			data, _ := req.Body["data"].(map[string]any)
			out = append(out, data)
		}
	}
	return out
}

// Edits returns edits of the original response, which is how replies to
// deferred commands arrive.
func (r *Recorder) Edits() []Request {
	var out []Request
	for _, req := range r.Requests() {
		if req.Method == http.MethodPatch && strings.HasSuffix(req.Path, "/messages/@original") {
			out = append(out, req)
		}
	}
	return out
}

// Attach routes the session's HTTP calls to a new Recorder.
func Attach(s *discordgo.Session) *Recorder {
	rec := &Recorder{}
	s.Client = &http.Client{Transport: rec}
	return rec
}

func Command(userID, name string, opts ...*discordgo.ApplicationCommandInteractionDataOption) *discordgo.InteractionCreate {
	return interaction(discordgo.InteractionApplicationCommand, userID, name, opts)
}

// Mark the option being typed with Focused.
func Autocomplete(userID, name string, opts ...*discordgo.ApplicationCommandInteractionDataOption) *discordgo.InteractionCreate {
	return interaction(discordgo.InteractionApplicationCommandAutocomplete, userID, name, opts)
}

func interaction(t discordgo.InteractionType, userID, name string, opts []*discordgo.ApplicationCommandInteractionDataOption) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		ID: "1", AppID: "app", Token: "token", Type: t, GuildID: "guild", ChannelID: "channel",
		Member: &discordgo.Member{User: &discordgo.User{ID: userID}},
		Data: discordgo.ApplicationCommandInteractionData{
			Name: name, CommandType: discordgo.ChatApplicationCommand, Options: opts,
		},
	}}
}

func Sub(name string, opts ...*discordgo.ApplicationCommandInteractionDataOption) *discordgo.ApplicationCommandInteractionDataOption {
	return &discordgo.ApplicationCommandInteractionDataOption{
		Name: name, Type: discordgo.ApplicationCommandOptionSubCommand, Options: opts,
	}
}

func Group(name string, opts ...*discordgo.ApplicationCommandInteractionDataOption) *discordgo.ApplicationCommandInteractionDataOption {
	return &discordgo.ApplicationCommandInteractionDataOption{
		Name: name, Type: discordgo.ApplicationCommandOptionSubCommandGroup, Options: opts,
	}
}

func String(name, value string) *discordgo.ApplicationCommandInteractionDataOption {
	return &discordgo.ApplicationCommandInteractionDataOption{Name: name, Type: discordgo.ApplicationCommandOptionString, Value: value}
}

func Bool(name string, value bool) *discordgo.ApplicationCommandInteractionDataOption {
	return &discordgo.ApplicationCommandInteractionDataOption{Name: name, Type: discordgo.ApplicationCommandOptionBoolean, Value: value}
}

func Focused(name, value string) *discordgo.ApplicationCommandInteractionDataOption {
	o := String(name, value)
	o.Focused = true
	return o
}
