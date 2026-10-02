# GatorAid

A Discord bot for VTuber fans. It relays YouTube live chat into Discord channels (translations,
the streamer's own messages, other VTubers and moderators) and announces streams and community
posts.

- **Relays** follow a streamer, a whole group like Hololive EN, or everyone. Relaying starts when
  the waiting room opens and posts a notice that can ping a role.
- **Cameos** post what a VTuber says in other streamers' chats. **Gossip** posts lines elsewhere
  that mention them.
- **Translation**: streamer and VTuber lines get a DeepL translation into the server's language.
- **Logs**: when a stream ends, its relayed lines are posted as a text file. `/log` fetches one
  later.
- **Notifications** for streams going live and for new community posts.
- **Moderation**: a blacklist of YouTube channels and filters for banned words and translation
  prefixes.

Any YouTube channel that [Holodex](https://holodex.net) tracks can be relayed, not just the
curated streamer list.

## Commands

| Command | Who | What |
|---|---|---|
| `/relay`, `/cameos`, `/gossip` | Managers | `add`, `remove`, `clear`, `list` |
| `/notify youtube`, `/notify posts` | Managers | Live and community post notifications, same subcommands |
| `/settings` | Anyone can look, Managers change | Relay options, translation language, log channel, members-only and free chat handling, bot roles |
| `/blacklist`, "Blacklist author" (message menu) | Blacklisters | Stop relaying someone in this server |
| `/filter` | Blacklisters | Banned words and wanted translation prefixes |
| `/log <video>` | Everyone | A stream's relayed lines as a file |
| `/streamers list`, `/streamers info` | Everyone | Browse the streamer list |
| `/help` | Everyone | Every command |
| `/owner` | Bot owner, in the dev server | Edit the streamer list, look at tracked streams |

Managers have Manage Server or a Manager role. Blacklisters have Manage Messages, a Blacklister
role or a Manager role. Both roles are set in `/settings`. Administrators can always do everything.

## Running

Requires Go 1.27 and [Task](https://taskfile.dev).

```sh
cp .env.example .env   # fill in at least the Discord token and the Holodex key
task run
```

### What you need

- **A Discord application** with a bot token (`GATORAID_DISCORD_TOKEN`). The bot only uses the
  Guilds intent, so no privileged intents.
- **A Holodex API key** (`GATORAID_HOLODEX_API_KEY`), from your Holodex account settings. Without
  it the bot can't tell when streams start, so relays, notices, logs and live notifications are
  off. `/log`, community posts and the commands still work.
- **A DeepL API key** (`GATORAID_DEEPL_API_KEY`) for translation. Optional. Free keys end in `:fx`
  and allow 500,000 characters a month.

Invite the bot with the `bot` and `applications.commands` scopes:

```
https://discord.com/oauth2/authorize?client_id=APPLICATION_ID&scope=bot+applications.commands&permissions=274878090240
```

That asks for View Channel, Send Messages, Send Messages in Threads, Embed Links, Attach Files and
Mention All Roles. The last one lets relay notices and notifications ping roles that aren't set
as mentionable. The bot only ever pings the role a subscription names.

During development, set `GATORAID_DISCORD_DEV_GUILD_ID` so commands register in one server
instantly instead of globally.

### Configuration

Settings come from `GATORAID_*` environment variables first. Anything unset falls back to a JSON
file: the one passed with `-config`, else `$GATORAID_CONFIG`, else `./config.json` if it exists.
See [`.env.example`](.env.example) and [`config.example.json`](config.example.json) for every
option.

| Variable | JSON | Default |
|---|---|---|
| `GATORAID_DISCORD_TOKEN` | `discord.token` | required |
| `GATORAID_DISCORD_OWNER_IDS` | `discord.owner_ids` | |
| `GATORAID_DISCORD_DEV_GUILD_ID` | `discord.dev_guild_id` | |
| `GATORAID_DISCORD_LOG_CHANNEL_ID` | `discord.log_channel_id` | |
| `GATORAID_DATABASE_PATH` | `database.path` | `gatoraid.db` |
| `GATORAID_HOLODEX_API_KEY` | `holodex.api_key` | |
| `GATORAID_HOLODEX_TLDEX` | `holodex.tldex` | `false` |
| `GATORAID_DEEPL_API_KEY` | `deepl.api_key` | |
| `GATORAID_DEEPL_MONTHLY_CHARACTER_BUDGET` | `deepl.monthly_character_budget` | `500000` |
| `GATORAID_LIMITS_USER_CHANNELS` | `limits.user_channels` | `25` |
| `GATORAID_LOG_LEVEL` | `log.level` | `info` |
| `GATORAID_LOG_FORMAT` | `log.format` | `json` |
| `GATORAID_EMOJIS` | `emojis` | |

`holodex.tldex` also reads Holodex's TLdex feed, which adds translations posted with MChad and
the exact stream start times. `limits.user_channels` is how many channels from outside the
streamer list each server can follow.

`emojis` maps keys to custom emoji markup such as `<:holo:123>`. Keys: `deepl` (translations),
`prechat` (waiting room lines), `vtuber` and `peek` (VTubers without a group emoji, in relays and
in cameos or gossip), and any streamer group ID, like `hololive`, for that group's agency emoji.
Subgroups use their parent's emoji unless they have their own. Missing keys fall back to plain
Unicode emojis.

### Database

GatorAid keeps everything in one SQLite file at `GATORAID_DATABASE_PATH`, created on the first
run, with `-wal` and `-shm` files next to it while it runs. Migrations are built into the binary
and applied on startup. `store/sqlite/migrations/0001_baseline.sql` is the whole schema; changes
go in new numbered files after it.

Relayed lines are kept for 7 days per server and 24 hours bot-wide, for `/log`. When the bot
leaves a server, that server's settings and subscriptions are kept for 30 days in case it's
invited back.

## Development

```sh
task            # list tasks
task test       # tests, 60 s timeout
task test:race  # with the race detector (slower first build)
task test:live  # against Holodex, YouTube and DeepL; uses the keys in .env and skips what's missing
task lint       # golangci-lint, pinned version
task check      # format check, vet, lint, race tests
task build      # static binary in out/bin/
```

To see live chat the way the bot reads it, without Discord or any API key:

```sh
go run ./cmd/chatwatch [-tldex] VIDEO_ID...
```

## Layout

```
cmd/gatoraid/         entry point
cmd/chatwatch/        prints live chat as the bot reads it
bot/                  Discord session, router wiring, guild lifecycle
chat/                 one reader per stream, YouTube chat merged with TLdex
commands/             slash commands and the /settings panel
relay/                which chats to read and where their lines go: rules, formatting, engine
sender/               Discord send queue per channel
subs/                 in-memory subscription index
tllog/                stream logs from relayed lines, posted when streams end
translate/            DeepL client with a cache and a monthly character budget
holodex/              Holodex API client
holodex/tldex/        Holodex's TLdex translation feed
moderation/           in-memory blacklists and filters
notify/               live stream and community post notifications
perms/                who may manage the bot: Discord permissions or bot roles
stream/               stream tracker: Holodex polls into live/prechat/ended events
internal/config/      env-first configuration
internal/logging/     slog setup
internal/discordtest/ fake Discord API for tests
store/                persistence interfaces and models
store/sqlite/         SQLite implementation and its migrations
streamers/            streamer registry; seed/ holds the curated list
youtube/channel/      finds YouTube channels from links, @handles and IDs
youtube/livechat/     YouTube live chat reader
youtube/posts/        community posts from a channel's Posts tab
```

### Streamer registry

[`streamers/seed/`](streamers/seed/README.md) is the curated list of streamers, one TOML file per
group. It's synced into the database on startup: new entries are added, changed ones updated and
removed ones hidden, so subscriptions to them come back if they return. Hidden streamers nothing
subscribes to are deleted after 30 days. Streamers edited with `/owner streamers` belong to the
owner from then on and the seed leaves them alone.

Commands are built on [gumi](https://github.com/VTGare/gumi).
