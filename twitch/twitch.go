package twitch

import (
	"regexp"
	"strings"
)

var usernameRe = regexp.MustCompile(`^[a-z0-9_]{1,25}$`)

// ValidUsername reports whether s could be a Twitch username. Twitch rejects a
// whole API request over one malformed username, and a malformed JOIN breaks
// the chat connection.
func ValidUsername(s string) bool { return usernameRe.MatchString(s) }

// Chat bots that most channels make moderators. Their timers, command
// replies and ads would otherwise be relayed as mod messages.
var bots = map[string]bool{
	"blerp":                true,
	"botrixoficial":        true,
	"creatisbot":           true,
	"fossabot":             true,
	"frostytoolsdotcom":    true,
	"kofistreambot":        true,
	"moobot":               true,
	"nightbot":             true,
	"own3d":                true,
	"pokemoncommunitygame": true,
	"sery_bot":             true,
	"soundalerts":          true,
	"streamelements":       true,
	"streamlabs":           true,
	"streamstickers":       true,
	"tangiabot":            true,
	"wizebot":              true,
}

func KnownBot(username string) bool { return bots[strings.ToLower(username)] }
