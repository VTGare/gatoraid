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

// WithPermissions sets the invoker's permissions, which interactions carry
// and permission checks read.
func WithPermissions(i *discordgo.InteractionCreate, perms int64) *discordgo.InteractionCreate {
	i.Member.Permissions = perms
	return i
}

// WithRoles gives the invoker roles in the guild.
func WithRoles(i *discordgo.InteractionCreate, roleIDs ...string) *discordgo.InteractionCreate {
	i.Member.Roles = roleIDs
	return i
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

// MessageCommand runs a message context menu command on a message in the
// interaction's channel.
func MessageCommand(userID, name, messageID string) *discordgo.InteractionCreate {
	i := interaction(discordgo.InteractionApplicationCommand, userID, name, nil)
	data := i.Data.(discordgo.ApplicationCommandInteractionData)
	data.CommandType = discordgo.MessageApplicationCommand
	data.TargetID = messageID
	data.Resolved = &discordgo.ApplicationCommandInteractionDataResolved{
		Messages: map[string]*discordgo.Message{messageID: {ID: messageID, ChannelID: "channel"}},
	}
	i.Data = data
	return i
}

// Component is a click on a button, or a pick in a select menu when values
// are given, in the interaction's guild and channel.
func Component(userID, customID string, values ...string) *discordgo.InteractionCreate {
	kind := discordgo.ButtonComponent
	if values != nil {
		kind = discordgo.SelectMenuComponent
	}

	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		ID: "1", AppID: "app", Token: "token", Type: discordgo.InteractionMessageComponent,
		GuildID: "guild", ChannelID: "channel",
		Member: &discordgo.Member{User: &discordgo.User{ID: userID}},
		Data:   discordgo.MessageComponentInteractionData{CustomID: customID, ComponentType: kind, Values: values},
	}}
}

// ModalSubmit submits a modal with text inputs by custom ID.
func ModalSubmit(userID, customID string, inputs map[string]string) *discordgo.InteractionCreate {
	var rows []discordgo.MessageComponent
	for id, value := range inputs {
		rows = append(rows, &discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			&discordgo.TextInput{CustomID: id, Value: value},
		}})
	}

	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		ID: "1", AppID: "app", Token: "token", Type: discordgo.InteractionModalSubmit,
		GuildID: "guild", ChannelID: "channel",
		Member: &discordgo.Member{User: &discordgo.User{ID: userID}},
		Data:   discordgo.ModalSubmitInteractionData{CustomID: customID, Components: rows},
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

// The ID option types (channel, role, user) carry the ID as a string.
func Channel(name, id string) *discordgo.ApplicationCommandInteractionDataOption {
	return &discordgo.ApplicationCommandInteractionDataOption{Name: name, Type: discordgo.ApplicationCommandOptionChannel, Value: id}
}

func Role(name, id string) *discordgo.ApplicationCommandInteractionDataOption {
	return &discordgo.ApplicationCommandInteractionDataOption{Name: name, Type: discordgo.ApplicationCommandOptionRole, Value: id}
}

func Focused(name, value string) *discordgo.ApplicationCommandInteractionDataOption {
	o := String(name, value)
	o.Focused = true
	return o
}
