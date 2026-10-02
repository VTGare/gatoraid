# Streamer seed

The curated list of streamers GatorAid knows about. It's built into the binary and synced into the
database on startup: new entries are added, changed ones updated, and removed ones hidden, so
subscriptions to them come back if they return.
Streamers edited with `/owner streamers` belong to the owner from then on, and the seed leaves them
alone.

Run `task test -- ./streamers/...` after editing. It loads these files and fails on typos, unknown
fields, duplicate channel IDs and broken group references.

## Layout

Every `.toml` file is one group. Agencies with branches get a folder: `hololive/hololive.toml` is
the Hololive group itself (for agency-wide channels), and `hololive/en.toml` is Hololive EN. Where
files go doesn't matter to the bot; only `[group]` does.

Section comments like `# English -Myth-` follow Holodex's branch and generation names and are just
for finding your way around. `indie.toml` also holds small agencies, under their own headers.

To check the list against Holodex, run `/owner streamers sync org:<org>`: it reports streamers
Holodex marks inactive (graduated) and members missing from the seed.

## Adding a streamer

Copy a block into the right file:

```toml
[[streamer]]
name = "Mori Calliope"
channel_id = "UCL_qhgtOy0dy1Agp8vkySQg"
channel_name = "Mori Calliope Ch. hololive-EN"
twitter = "moricalliope"
aliases = ["calliope", "calli", "mori"]
```

| Field | Required | |
|---|---|---|
| `name` | yes | Shown everywhere, and how people search for them. |
| `channel_id` | yes | The `UC…` ID from the channel URL. A handle like `@MoriCalliope` won't work. |
| `channel_name` | | The YouTube channel title. Used to spot collabs for gossip. |
| `twitter` | | Handle without the `@`. Also used to spot collabs. |
| `aliases` | | Nicknames people search and gossip by. Japanese ones match inside words too. |
| `free_chat_streams` | | `true` if they stream in rooms titled "free chat", which are otherwise skipped. |

## Moving owner entries into the seed

`/owner streamers add` and `edit` take effect right away but only live in the database. To make
them permanent:

1. Run `/owner streamers export`. It prints every owner entry as `[[streamer]]` blocks, each group
   under a comment naming its file.
2. Paste them into those files and deploy.
3. On startup, owner entries that match the seed exactly go back to being managed by the seed. The
   startup log counts them as `returned_to_seed`; ones that still differ stay owner-managed and
   count as `kept_owner_edits`.

## Adding a group

Make a new file starting with:

```toml
[group]
id = "hololive-en"     # lowercase, never change it once in use: it's stored in subscriptions
name = "Hololive EN"
parent = "hololive"    # optional
order = 2              # optional, sorts subgroups; otherwise by name
skip_auto_translate = true   # optional, don't DeepL this group's messages
```

The `id` is also the emoji key: `GATORAID_EMOJIS=hololive-en=<:HoloEN:123>`. Groups without an
emoji use their parent's.
