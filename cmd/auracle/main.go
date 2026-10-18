// Command auracle runs a Discord bot that answers chat messages using Ollama.
//
// Configuration is read from the environment:
//
//	DISCORD_TOKEN   Discord bot token (required)
//	OLLAMA_MODEL    model name, e.g. "gpt-oss:120b"; must support tool calling (required)
//	DOCS_DIR        directory of files the bot answers from (required)
//	OLLAMA_URL      Ollama server URL (default "http://localhost:11434")
//	SYSTEM_PROMPT   optional extra instructions appended to the built-in ones
//	MAX_HISTORY     messages of per-channel history to keep (default 20)
//	LOG_LEVEL       debug, info, warn or error (default info); debug includes message text
//	LOG_FORMAT      text or json (default text)
package main

import (
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/aufishgamelabs/auracle/internal/bot"
	"github.com/aufishgamelabs/auracle/internal/docs"
	"github.com/aufishgamelabs/auracle/internal/ollama"
)

func main() {
	setupLogging()

	token := mustEnv("DISCORD_TOKEN")
	model := mustEnv("OLLAMA_MODEL")
	docsDir := mustEnv("DOCS_DIR")
	ollamaURL := envOr("OLLAMA_URL", "http://localhost:11434")

	maxHistory, err := strconv.Atoi(envOr("MAX_HISTORY", "20"))
	if err != nil || maxHistory < 0 {
		fatal("invalid MAX_HISTORY", "value", os.Getenv("MAX_HISTORY"))
	}

	kb, err := docs.Open(docsDir)
	if err != nil {
		fatal("cannot open DOCS_DIR", "dir", docsDir, "err", err)
	}
	defer kb.Close()
	files, err := kb.Files()
	if err != nil {
		fatal("cannot scan DOCS_DIR", "dir", docsDir, "err", err)
	}

	b, err := bot.New(token, ollama.New(ollamaURL, model), kb, os.Getenv("SYSTEM_PROMPT"), maxHistory)
	if err != nil {
		fatal("cannot create bot", "err", err)
	}
	if err := b.Open(); err != nil {
		fatal("cannot connect to discord", "err", err)
	}
	defer b.Close()

	slog.Info("auracle running; press Ctrl-C to exit",
		"model", model, "ollama_url", ollamaURL, "docs_dir", docsDir, "files", len(files), "max_history", maxHistory)
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
