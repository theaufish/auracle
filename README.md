# auracle

A Discord bot that answers questions using a local [Ollama](https://ollama.com) model, based **only** on the files in a directory you choose.

When someone @mentions the bot in a server channel or sends it a direct message, the model searches and reads the files in your docs directory, then replies using what it found. If the files don't contain the answer, it says so instead of guessing.

## Requirements

- Go 1.27 or newer
- [Ollama](https://ollama.com) running locally or on a reachable host, with a model that supports **tool calling** (for example `gpt-oss:120b`)
- A Discord bot token (see below)
- A directory of text files for the bot to answer from (Markdown, plain text, etc.)

## 1. Create the Discord bot

1. Go to the [Discord Developer Portal](https://discord.com/developers/applications) and click **New Application**.
2. Open the **Bot** page and click **Reset Token** to get the bot token. Keep it secret.
3. Open **OAuth2 → URL Generator**:
   - Under **Scopes**, select `bot` and `applications.commands` (needed for the `/auracle` command).
   - Under **Bot Permissions**, select **Send Messages** and **Read Message History**.
4. Open the generated URL and invite the bot to your server.

The bot only reads messages that mention it or are sent to it as DMs, so you do **not** need to enable the privileged *Message Content Intent*.

If the bot is already in your server from before slash commands were added, open a new invite URL with both scopes and authorize it again so the command appears. The bot registers the command itself each time it connects. After it changes, reload Discord (Cmd+R / Ctrl+R) if the command doesn't show up.

## 2. Prepare Ollama

Pull a model that supports tool calling, and make sure Ollama is running:

```sh
ollama pull gpt-oss:120b
ollama serve   # skip if Ollama is already running
```

## 3. Run the bot

```sh
DISCORD_TOKEN=your-bot-token \
OLLAMA_MODEL=gpt-oss:120b \
DOCS_DIR=/path/to/your/docs \
go run ./cmd/auracle
```

Or build a binary first:

```sh
go build -o auracle ./cmd/auracle
DISCORD_TOKEN=... OLLAMA_MODEL=gpt-oss:120b DOCS_DIR=/path/to/your/docs ./auracle
```

On startup it logs the model, the Ollama URL, and how many files it found in `DOCS_DIR`. Press Ctrl-C to stop it.

### Configuration

All settings are environment variables:

| Variable        | Required | Default                  | Description |
|-----------------|----------|--------------------------|-------------|
| `DISCORD_TOKEN` | yes      | none                     | Discord bot token. |
| `OLLAMA_MODEL`  | yes      | none                     | Ollama model name. Must support tool calling. |
| `DOCS_DIR`      | yes      | none                     | Directory of files the bot answers from. Subdirectories are included. |
| `OLLAMA_URL`    | no       | `http://localhost:11434` | URL of the Ollama server. |
| `SYSTEM_PROMPT` | no       | none                     | Extra instructions added after the built-in ones (e.g. tone or persona). It cannot turn off the files-only rule. |
| `MAX_HISTORY`   | no       | `20`                     | Number of messages of conversation history kept per channel. |
| `LOG_LEVEL`     | no       | `info`                   | `debug`, `info`, `warn` or `error`. See [Logging](#logging). |
| `LOG_FORMAT`    | no       | `text`                   | `text` (human-readable) or `json` (for log collectors). |

## Using the bot

- **In a server channel:** mention it, e.g. `@auracle what are the rules for dragons?`
- **In a DM:** just send your question.
- **Privately:** `/auracle whisper question: what are the rules for dragons?` replies so that only you can see the answer. The question doesn't appear in the channel either.
- **`@auracle !reset`** clears the conversation history for that channel. **`/auracle whisper question: !reset`** clears your whispered history.

The bot remembers recent messages so you can ask follow-up questions: per channel for public questions, and per person for whispers. Whispered questions are kept separate, so they're never used as context for public answers. History is kept in memory and is lost when the bot restarts.

A whispered answer has to arrive within 15 minutes of the command, Discord's limit for slash command replies. Answers that take longer are lost and logged as an error.

Answers can take a while, because the model may search and read several files before replying. The bot shows "typing…" while it works.

### What's in the docs directory

- All text files under `DOCS_DIR` are searchable, including subdirectories.
- Hidden files and directories (names starting with `.`, such as `.git`) are skipped.
- Binary files (images, PDFs, etc.) are skipped. Convert PDFs with [`pdf2md`](#converting-pdfs-pdf2md), and Word documents to text or Markdown.
- The bot can't read anything outside `DOCS_DIR`.
- Files are read when the model looks them up, so you can add or edit files without restarting the bot.

### Limits

Keeping answers to the files is enforced by the model's instructions. The model follows them well, but a determined user may still be able to coax it into answering from its general knowledge.

## Converting PDFs (`pdf2md`)

The bot can't read PDFs, so the repo includes a second command, `pdf2md`, that splits a PDF into one Markdown file per page:

```sh
go run ./cmd/pdf2md path/to/book.pdf
```

This writes `book-p001.md`, `book-p002.md`, … next to the PDF. Put the output somewhere inside `DOCS_DIR` and the bot will search it. The PDF itself can stay; the bot ignores it.

| Flag          | Default                   | Description |
|---------------|---------------------------|-------------|
| `-o dir`      | the PDF's directory       | Where to write the Markdown files. |
| `-title text` | from the file name        | Title used in each page's heading, e.g. `# Book, page 12`. |
| `-skip-empty` | `true`                    | Skip pages with no text (such as full-page images). Use `-skip-empty=false` to write them anyway. |

It rebuilds the page from the text's position and size:

- Two-column layouts are read left column first, then right.
- Larger text becomes Markdown headings.
- Wrapped lines are joined into paragraphs.
- List items and `Label: value` lines keep their own lines.
- Table rows are kept on one line with cells separated by ` | `.

Limits:

- **Scanned PDFs need a text layer.** A PDF that is only page images has no text to extract, and its pages will be skipped as empty. Run it through OCR first.
- **Complex layouts are approximate.** Table cells that wrap onto a second line, sidebars, and headings set in the same size as body text may come out imperfectly. The text is still all there and searchable.

## Saving channel messages (`scribe`)

`scribe` is a separate Discord bot that saves every message in one channel longer than a set number of characters as a Markdown file. On startup it reads the channel's whole history, oldest first, then keeps saving new messages as they arrive. Each file is named after the time the message was sent, in UTC, e.g. `2026-10-21T16-05-07Z.md`. A message sent in the same second as an earlier one gets a `-2`, `-3`, … suffix. Existing files are never overwritten.

```sh
DISCORD_TOKEN=... CHANNEL_ID=123456789012345678 OUT_DIR=~/llms/auracle/sessions \
  go run ./cmd/scribe
```

| Variable | Default | Description |
|----------|---------|-------------|
| `DISCORD_TOKEN` | required | Bot token. |
| `CHANNEL_ID` | required | ID of the channel to read. Turn on Developer Mode in Discord, then right-click the channel → **Copy Channel ID**. |
| `OUT_DIR` | required | Existing directory to write the files to. |
| `MIN_CHARS` | `500` | Save only messages longer than this many characters. |
| `LOG_LEVEL`, `LOG_FORMAT` | `info`, `text` | As for `auracle`. |

Reading message text needs the privileged **Message Content Intent**: enable it on the bot's page in the Discord developer portal, or every message arrives empty and nothing is saved. The bot also needs the **View Channel** and **Read Message History** permissions in the channel.

The file contains the message text as written in Discord. Pointing `OUT_DIR` inside `auracle`'s `DOCS_DIR` makes saved messages searchable by `auracle`. Files are written under a hidden temporary name first, so `auracle` never sees a half-written file.

`scribe` records the IDs of the messages it has saved in a hidden `.scribe-saved` file in `OUT_DIR`, so restarting it never saves a message twice. Deleting a saved Markdown file doesn't bring it back on the next start; to re-save everything, delete `.scribe-saved` along with the files. Lowering `MIN_CHARS` and restarting saves older messages that now qualify.

Edits, deletions, attachments and messages in threads are ignored. A message edited before `scribe` first reads it is saved as edited.

## Logging

Logs are written to stderr. Each line about a message includes its `message_id`, `guild_id`, `channel_id`, `user` and `user_id`, so you can follow a single question from arrival to reply.

| Level   | What's logged |
|---------|---------------|
| `info`  | Startup settings, Discord connect/resume, each question received (length only), each answer (time taken, number of tool calls, time spent in tools, length), each reply delivered (total time since the question arrived), history resets, shutdown. |
| `warn`  | Discord disconnects, tool calls that failed (e.g. the model asked for a file that doesn't exist), empty answers from the model. |
| `error` | Model requests that failed or timed out, messages that couldn't be sent to Discord, startup errors. |
| `debug` | Everything above, plus the text of each question and answer, and every tool call the model makes with its arguments and timing. |

At `info` and above, what users typed and what the bot answered are not logged. Use `debug` to see them, for example while tuning prompts:

Timings, rounded to the millisecond:

- `duration` on `question answered`: time spent generating the answer, including tool calls.
- `tool_time` on the same line: the part of that spent running tool calls. The rest was spent waiting on the model.
- `total` on `reply sent` / `whispered reply sent`: from the question arriving to the reply being posted in Discord.

```sh
LOG_LEVEL=debug DISCORD_TOKEN=... OLLAMA_MODEL=gpt-oss:120b DOCS_DIR=/path/to/your/docs go run ./cmd/auracle
```

## Deploying to Kubernetes

A Helm chart in [`charts/auracle`](charts/auracle/README.md) runs a single instance of the bot. It reads the Discord token from a Kubernetes Secret and the docs from a PersistentVolumeClaim. The repo's `Dockerfile` builds the image:

```sh
docker build -t auracle .
```

The `Makefile` wraps the whole deploy:

```sh
DISCORD_TOKEN=... make secret        # once: store the token in a Kubernetes Secret
make helm OLLAMA_URL=http://ollama.ollama.svc:11434
```

`make helm` builds the image for `linux/amd64` and `linux/arm64`, pushes it, and runs `helm upgrade --install`, so the same command installs the bot the first time and updates it afterwards. The image tag is a hash of the Go sources, so the pod is only replaced when the code changed. It deploys to the `auracle` namespace, using your current `kubectl` context.

| Variable | Default | Description |
|----------|---------|-------------|
| `IMAGE` | `ghcr.io/aufishgamelabs/auracle` | Image repository to push to. |
| `TAG` | hash of the sources | Image tag. |
| `PLATFORMS` | `linux/amd64,linux/arm64` | Platforms to build for. |
| `RELEASE` | `auracle` | Helm release name. |
| `NAMESPACE` | `auracle` | Kubernetes namespace (created if missing). |
| `DISCORD_SECRET` / `DISCORD_KEY` | `auracle-discord` / `token` | Secret holding the bot token, and its key. |
| `DOCS_PATH` | `~/llms/auracle` | Directory on the node the bot reads the docs from. Empty uses a PersistentVolumeClaim instead. |
| `OLLAMA_URL`, `OLLAMA_MODEL` | chart defaults | Ollama settings. |
| `VALUES` | none | A Helm values file for any other settings. |

Other targets: `make test`, `make build` (binaries in `bin/`), `make image` (local image only), `make push`, `make lint`, and `make helm-template` (print the manifests without applying them). `make helm` needs `docker` (with `buildx`), `kubectl` and `helm` installed.

See the [chart README](charts/auracle/README.md) for loading the docs and all chart settings.

## Running in the devcontainer

The repo includes a devcontainer (`.devcontainer/`) with Go 1.27. Inside the container, `localhost` refers to the container, not your machine, so point the bot at Ollama on the host:

```sh
OLLAMA_URL=http://host.docker.internal:11434
```

`DOCS_DIR` must also be a path the container can see, such as a folder inside the repo or one mounted into the container.

## Development

```sh
go test ./...
go vet ./...
```

Project layout:

```
cmd/auracle/       bot entry point and configuration
cmd/pdf2md/        PDF to per-page Markdown converter
cmd/scribe/        bot that saves long channel messages as Markdown files
internal/bot/      Discord handling, conversation history, built-in instructions
internal/docs/     list/search/read tools over DOCS_DIR
internal/ollama/   Ollama chat client and tool-call loop
internal/pdfmd/    rebuilds columns, paragraphs, headings and tables from PDF text
internal/scribe/   names and writes the files for scribe
charts/auracle/    Helm chart
```
