package streamers

import (
	"cmp"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/VTGare/gatoraid/store"
)

//go:embed seed
var seedFS embed.FS

var channelIDRe = regexp.MustCompile(`^UC[\w-]{22}$`)

func ValidChannelID(id string) bool { return channelIDRe.MatchString(id) }

type Seed struct {
	// Parents before their subgroups.
	Groups    []store.Group
	Streamers []store.Streamer
	// Group ID to the file that defines it, relative to streamers/seed.
	Files map[string]string
}

type seedFile struct {
	Group struct {
		ID                string `toml:"id"`
		Name              string `toml:"name"`
		Parent            string `toml:"parent"`
		Order             int    `toml:"order"`
		SkipAutoTranslate bool   `toml:"skip_auto_translate"`
	} `toml:"group"`
	Streamers []struct {
		Name            string   `toml:"name"`
		ChannelID       string   `toml:"channel_id"`
		ChannelName     string   `toml:"channel_name"`
		Twitter         string   `toml:"twitter"`
		Aliases         []string `toml:"aliases"`
		FreeChatStreams bool     `toml:"free_chat_streams"`
	} `toml:"streamer"`
}

type seedGroup struct {
	store.Group
	order int
	file  string
}

// LoadSeed parses the embedded seed directory.
func LoadSeed() (*Seed, error) {
	sub, err := fs.Sub(seedFS, "seed")
	if err != nil {
		return nil, err
	}
	return ParseSeed(sub)
}

// ParseSeed reads every .toml file in fsys. Each file is one group.
func ParseSeed(fsys fs.FS) (*Seed, error) {
	var (
		errs      []error
		groups    = map[string]*seedGroup{}
		streamers []store.Streamer
		channels  = map[string]string{}
	)

	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || path.Ext(p) != ".toml" {
			return err
		}

		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}

		var f seedFile
		md, err := toml.Decode(string(data), &f)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", p, err))
			return nil
		}
		for _, key := range md.Undecoded() {
			errs = append(errs, fmt.Errorf("%s: unknown field %q", p, key.String()))
		}

		g := f.Group
		switch {
		case !md.IsDefined("group"):
			errs = append(errs, fmt.Errorf("%s: missing [group]", p))
			return nil
		case g.ID == "" || g.Name == "":
			errs = append(errs, fmt.Errorf("%s: [group] needs an id and a name", p))
			return nil
		case groups[g.ID] != nil:
			errs = append(errs, fmt.Errorf("%s: group %q is already defined in %s", p, g.ID, groups[g.ID].file))
			return nil
		}

		groups[g.ID] = &seedGroup{
			Group: store.Group{ID: g.ID, Name: g.Name, ParentID: g.Parent, SkipAutoTranslate: g.SkipAutoTranslate},
			order: g.Order,
			file:  p,
		}

		for _, s := range f.Streamers {
			switch {
			case s.Name == "":
				errs = append(errs, fmt.Errorf("%s: streamer %q has no name", p, s.ChannelID))
				continue
			case !ValidChannelID(s.ChannelID):
				errs = append(errs, fmt.Errorf("%s: %s: %q isn't a channel ID (UC followed by 22 characters)", p, s.Name, s.ChannelID))
				continue
			case channels[s.ChannelID] != "":
				errs = append(errs, fmt.Errorf("%s: %s: channel %s is already used by %s", p, s.Name, s.ChannelID, channels[s.ChannelID]))
				continue
			}
			channels[s.ChannelID] = s.Name

			streamers = append(streamers, store.Streamer{
				ChannelID:       s.ChannelID,
				Name:            s.Name,
				ChannelName:     s.ChannelName,
				GroupID:         g.ID,
				Twitter:         s.Twitter,
				Aliases:         s.Aliases,
				FreeChatStreams: s.FreeChatStreams,
				Source:          store.SourceSeed,
			})
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("streamers: seed: %w", err)
	}

	for _, g := range groups {
		if g.ParentID != "" && groups[g.ParentID] == nil {
			errs = append(errs, fmt.Errorf("%s: unknown parent %q", g.file, g.ParentID))
		}
	}
	if err := checkCycles(groups); err != nil {
		errs = append(errs, err)
	}

	if err := errors.Join(errs...); err != nil {
		return nil, fmt.Errorf("streamers: seed: %w", err)
	}

	files := make(map[string]string, len(groups))
	for id, g := range groups {
		files[id] = g.file
	}

	return &Seed{Groups: sortGroups(groups), Streamers: streamers, Files: files}, nil
}

// sortGroups walks the tree: top-level groups by name, subgroups by order
// and then name.
func sortGroups(groups map[string]*seedGroup) []store.Group {
	children := map[string][]*seedGroup{}
	for _, g := range groups {
		children[g.ParentID] = append(children[g.ParentID], g)
	}
	for _, list := range children {
		slices.SortFunc(list, func(a, b *seedGroup) int {
			return cmp.Or(cmp.Compare(a.order, b.order), cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)))
		})
	}

	out := make([]store.Group, 0, len(groups))
	var walk func(parent string)
	walk = func(parent string) {
		for _, g := range children[parent] {
			out = append(out, g.Group)
			walk(g.ID)
		}
	}
	walk("")

	return out
}

func checkCycles(groups map[string]*seedGroup) error {
	for _, g := range groups {
		seen := map[string]bool{}
		for cur := g; cur != nil; cur = groups[cur.ParentID] {
			if seen[cur.ID] {
				return fmt.Errorf("%s: group %q is its own ancestor", g.file, g.ID)
			}
			seen[cur.ID] = true
		}
	}
	return nil
}
