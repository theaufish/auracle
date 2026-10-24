// Command scribe runs a Discord bot that saves long messages from one channel
// as Markdown files, named after the time each message was sent, e.g.
// 2026-10-21T16-05-07Z.md (UTC). On startup it reads the channel's whole
// history, then saves new messages as they arrive. Messages saved before are
// skipped.
//
// Reading message text needs the privileged Message Content intent, which must
// be enabled for the bot in the Discord developer portal.
//
// Configuration is read from the environment:
//
//	DISCORD_TOKEN   Discord bot token (required)
//	CHANNEL_ID      ID of the channel to read (required)
//	OUT_DIR         directory to write the Markdown files to (required; must exist)
//	MIN_CHARS       save only messages longer than this many characters (default 500)
//	LOG_LEVEL       debug, info, warn or error (default info)
//	LOG_FORMAT      text or json (default text)
package main

import (
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/bwmarrin/discordgo"

	"github.com/aufishgamelabs/auracle/internal/scribe"
)

func main() {
	setupLogging()

	token := mustEnv("DISCORD_TOKEN")
	channelID := mustEnv("CHANNEL_ID")
	outDir := mustEnv("OUT_DIR")

	minChars, err := strconv.Atoi(envOr("MIN_CHARS", "500"))
	if err != nil || minChars < 0 {
		fatal("invalid MIN_CHARS", "value", os.Getenv("MIN_CHARS"))
	}
	if fi, err := os.Stat(outDir); err != nil || !fi.IsDir() {
		fatal("OUT_DIR is not a directory", "dir", outDir, "err", err)
	}
	w, err := scribe.Open(outDir, minChars)
	if err != nil {
		fatal("cannot read the index of saved messages", "dir", outDir, "err", err)
	}

	s, err := discordgo.New("Bot " + token)
	if err != nil {
		fatal("cannot create discord session", "err", err)
	}
	s.Identify.Intents = discordgo.IntentsGuildMessages | discordgo.IntentMessageContent

	s.AddHandler(func(s *discordgo.Session, r *discordgo.Ready) {
		slog.Info("connected to discord", "user", r.User.Username, "user_id", r.User.ID, "guilds", len(r.Guilds))
	})
	s.AddHandler(func(*discordgo.Session, *discordgo.Disconnect) {
		slog.Warn("disconnected from discord; reconnecting")
	})
	save := func(m *discordgo.Message) {
		log := slog.With("message_id", m.ID)
		if m.Author != nil {
			log = log.With("user", m.Author.Username, "user_id", m.Author.ID)
		}
		p, err := w.Save(m.ID, m.Content, m.Timestamp)
		switch {
		case err != nil:
			log.Error("failed to save message", "err", err)
		case p == "":
			log.Debug("message too short or already saved; skipped")
		default:
			log.Info("message saved", "file", p)
		}
	}
	s.AddHandler(func(s *discordgo.Session, m *discordgo.MessageCreate) {
		if m.ChannelID == channelID {
			save(m.Message)
		}
	})

	// Connect before reading the history so no message falls between the two;
	// one that shows up in both is only saved once.
	if err := s.Open(); err != nil {
		fatal("cannot connect to discord", "err", err)
	}
	defer s.Close()

	go func() {
		slog.Info("reading channel history", "channel_id", channelID)
		n, err := scribe.Backfill(func(after string) ([]*discordgo.Message, error) {
			return s.ChannelMessages(channelID, 100, "", after, "")
		}, save)
		if err != nil {
			slog.Error("failed to read channel history; new messages are still saved", "read", n, "err", err)
			return
		}
		slog.Info("finished reading channel history", "read", n)
	}()

	slog.Info("scribe running; press Ctrl-C to exit", "channel_id", channelID, "out_dir", outDir, "min_chars", minChars)
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	sig := <-stop
	slog.Info("shutting down", "signal", sig.String())
}

// setupLogging configures the default slog logger from LOG_LEVEL and LOG_FORMAT.
func setupLogging() {
	var level slog.Level
	if err := level.UnmarshalText([]byte(envOr("LOG_LEVEL", "info"))); err != nil {
		fmt.Fprintf(os.Stderr, "invalid LOG_LEVEL %q: use debug, info, warn or error\n", os.Getenv("LOG_LEVEL"))
		os.Exit(1)
	}
	opts := &slog.HandlerOptions{Level: level}

	var h slog.Handler
	switch format := envOr("LOG_FORMAT", "text"); format {
	case "text":
		h = slog.NewTextHandler(os.Stderr, opts)
	case "json":
		h = slog.NewJSONHandler(os.Stderr, opts)
	default:
		fmt.Fprintf(os.Stderr, "invalid LOG_FORMAT %q: use text or json\n", format)
		os.Exit(1)
	}
	slog.SetDefault(slog.New(h))
}

// fatal logs an error and exits.
func fatal(msg string, args ...any) {
	slog.Error(msg, args...)
	os.Exit(1)
}

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		fatal("required environment variable not set", "name", key)
	}
	return v
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
