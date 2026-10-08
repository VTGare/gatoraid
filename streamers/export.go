package streamers

import (
	"bytes"
	"fmt"

	"github.com/BurntSushi/toml"

	"github.com/VTGare/gatoraid/store"
)

type exportEntry struct {
	Name            string   `toml:"name"`
	ChannelID       string   `toml:"channel_id"`
	ChannelName     string   `toml:"channel_name,omitempty"`
	Twitter         string   `toml:"twitter,omitempty"`
	Twitch          string   `toml:"twitch,omitempty"`
	Aliases         []string `toml:"aliases,omitempty"`
	FreeChatStreams bool     `toml:"free_chat_streams,omitempty"`
}

// Each group's entries start with a comment naming the seed file they
// belong in.
func (r *Registry) Export(sts []*store.Streamer) (string, error) {
	byGroup := map[string][]*store.Streamer{}
	for _, st := range sts {
		byGroup[st.GroupID] = append(byGroup[st.GroupID], st)
	}

	order := make([]string, 0, len(byGroup))
	for _, g := range r.Groups() {
		if byGroup[g.ID] != nil {
			order = append(order, g.ID)
		}
	}
	// Ungrouped streamers, and any whose group is gone, go last.
	for id := range byGroup {
		if _, ok := r.Group(id); !ok {
			order = append(order, id)
		}
	}

	var buf bytes.Buffer
	for i, id := range order {
		if i > 0 {
			buf.WriteString("\n\n")
		}

		switch file := r.GroupFile(id); {
		case file != "":
			fmt.Fprintf(&buf, "# streamers/seed/%s\n\n", file)
		case id == "":
			buf.WriteString("# No group: add them to the file of the group they belong to.\n\n")
		default:
			fmt.Fprintf(&buf, "# Group %q isn't in the seed. Add a file for it first.\n\n", id)
		}

		entries := make([]exportEntry, 0, len(byGroup[id]))
		for _, st := range byGroup[id] {
			entries = append(entries, exportEntry{
				Name:            st.Name,
				ChannelID:       st.ChannelID,
				ChannelName:     st.ChannelName,
				Twitter:         st.Twitter,
				Twitch:          st.Twitch,
				Aliases:         st.Aliases,
				FreeChatStreams: st.FreeChatStreams,
			})
		}

		enc := toml.NewEncoder(&buf)
		enc.Indent = ""
		if err := enc.Encode(struct {
			Streamer []exportEntry `toml:"streamer"`
		}{entries}); err != nil {
			return "", err
		}
	}

	return buf.String(), nil
}
