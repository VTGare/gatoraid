# GatorAid

A Discord bot that relays VTuber YouTube live chat — translations, streamer messages and mod
messages — into Discord channels, and sends stream and community post notifications.

> Work in progress. The skeleton (config, database, guild lifecycle, `/help`) is in place; relays and
> notifications are next.

## Running

Requires Go 1.27 and [Task](https://taskfile.dev).

```sh
cp .env.example .env   # fill in GATORAID_DISCORD_TOKEN at least
task run
```

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
| `GATORAID_LOG_LEVEL` | `log.level` | `info` |
| `GATORAID_LOG_FORMAT` | `log.format` | `json` |
| `GATORAID_EMOJIS` | `emojis` | |

## Development

```sh
task            # list tasks
task test       # tests, 60 s timeout
task test:race  # with the race detector (slower first build)
task test:live  # against the real Holodex API, needs GATORAID_HOLODEX_API_KEY
task lint       # golangci-lint, pinned version
task check      # format check, vet, lint, race tests
task build      # static binary in out/bin/
```

## Layout

```
cmd/gatoraid/      entry point
bot/               Discord session, router wiring, guild lifecycle
commands/             slash commands
holodex/              Holodex API client
stream/               stream tracker: Holodex polls into live/prechat/ended events
internal/config/      env-first configuration
internal/logging/     slog setup
internal/discordtest/ fake Discord API for tests
store/                persistence interfaces and models
store/sqlite/         SQLite implementation and migrations
streamers/            streamer registry; seed/ holds the curated list
```

### Streamer registry

[`streamers/seed/`](streamers/seed/README.md) is the curated list of streamers, one TOML file per
group. It's synced into the database on
startup: new entries are added, changed ones updated and removed ones deleted. Streamers edited with
`/owner streamers` belong to the owner from then on and the seed leaves them alone.

Commands are built on [gumi](https://github.com/VTGare/gumi).
