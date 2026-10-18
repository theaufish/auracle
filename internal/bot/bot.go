// Package bot relays Discord chat messages to Ollama and posts the replies.
package bot

import (
	"context"
	"encoding/json"
	"log/slog"
	"math/rand/v2"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/aufishgamelabs/auracle/internal/ollama"
)

const (
	// Discord rejects messages longer than this many characters.
	maxDiscordMessage = 2000
	// Most rounds of tool calls the model may make while answering one message.
	maxToolRounds = 50
)

// groundingPrompt restricts the model to answering from the knowledge base.
const groundingPrompt = `You answer questions using ONLY the files in a knowledge base, which you access with the list_files, search_files and read_file tools.

Rules:
- Always use the tools to look up the answer before replying. Try several search terms and read the relevant files.
- Base your answer solely on what the files say. Do not use outside knowledge, and do not guess or fill gaps.
- If the files do not contain the answer, say you couldn't find it in the knowledge base.
- Do not mention file names, paths or line numbers, and do not cite sources. Just give the answer.
- Keep answers concise; they are posted in Discord chat.`

// unclearReplies are sent, chosen at random, when the model gives no answer.
var unclearReplies = []string{
	"The answer is unclear. Try asking again.",
	"The mists are too thick; the answer is unclear right now.",
	"The visions are clouded. Ask again later.",
	"I can't make out an answer to that just now.",
	"The signs are murky. Perhaps rephrase your question?",
	"The answer eludes me for the moment. Try again shortly.",
}

func unclearReply() string {
	return unclearReplies[rand.IntN(len(unclearReplies))]
}

// Bot answers messages that mention it (or are sent to it directly) using
// Ollama, grounded in the files exposed by its knowledge base.
type Bot struct {
	session    *discordgo.Session
	llm        *ollama.Client
	kb         ollama.ToolHandler
	system     string
	maxHistory int

	mu sync.Mutex
	// Conversation history, keyed by channel ID for public questions and by
	// whisperKey(user ID) for whispered ones, so private questions never
	// appear as context in a public channel.
	history map[string][]ollama.Message
}

// auracleCommand is the bot's slash command. Its features are subcommands,
// e.g. "/auracle whisper".
var auracleCommand = &discordgo.ApplicationCommand{
	Name:        "auracle",
	Description: "Ask Auracle a question",
	Options: []*discordgo.ApplicationCommandOption{{
		Type:        discordgo.ApplicationCommandOptionSubCommand,
		Name:        "whisper",
		Description: "Ask a question and get a reply only you can see",
		Options: []*discordgo.ApplicationCommandOption{{
			Type:        discordgo.ApplicationCommandOptionString,
			Name:        "question",
			Description: "What do you want to know?",
			Required:    true,
		}},
	}},
}

func whisperKey(userID string) string { return "whisper:" + userID }

// New creates a Bot that answers from the knowledge base kb. extraPrompt, if
// set, is appended to the grounding instructions. The bot does not connect
// until Open is called.
func New(token string, llm *ollama.Client, kb ollama.ToolHandler, extraPrompt string, maxHistory int) (*Bot, error) {
	s, err := discordgo.New("Bot " + token)
	if err != nil {
		return nil, err
	}
	// Mentions and DMs include message content without the privileged
	// MessageContent intent, which is all this bot responds to.
	s.Identify.Intents = discordgo.IntentsGuildMessages | discordgo.IntentsDirectMessages

	b := &Bot{
		session:    s,
		llm:        llm,
		kb:         kb,
		system:     strings.TrimSpace(groundingPrompt + "\n\n" + extraPrompt),
		maxHistory: maxHistory,
		history:    make(map[string][]ollama.Message),
	}
	s.AddHandler(b.onMessage)
	s.AddHandler(b.onInteraction)
	s.AddHandler(func(s *discordgo.Session, r *discordgo.Ready) {
		slog.Info("connected to discord", "user", r.User.Username, "user_id", r.User.ID, "guilds", len(r.Guilds))
		appID := r.User.ID // a bot's application ID matches its user ID
		if r.Application != nil {
			appID = r.Application.ID
		}
		// Overwriting is idempotent, so re-registering on every reconnect is safe.
		// Overwriting also removes commands that no longer exist, such as the
		// old top-level /whisper.
		cmds, err := s.ApplicationCommandBulkOverwrite(appID, "", []*discordgo.ApplicationCommand{auracleCommand})
		if err != nil {
			slog.Error("failed to register slash commands", "err", err)
			return
		}
		for _, c := range cmds {
			slog.Info("registered slash command", "command", "/"+c.Name, "id", c.ID, "application_id", appID)
		}
	})
	s.AddHandler(func(*discordgo.Session, *discordgo.Disconnect) {
		slog.Warn("disconnected from discord; reconnecting")
	})
	s.AddHandler(func(*discordgo.Session, *discordgo.Resumed) {
		slog.Info("discord session resumed")
	})
	return b, nil
}

// Open connects to Discord.
func (b *Bot) Open() error { return b.session.Open() }

// Close disconnects from Discord.
func (b *Bot) Close() error { return b.session.Close() }

func (b *Bot) onMessage(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m.Author == nil || m.Author.Bot {
		return
	}

	me := s.State.User
	isDM := m.GuildID == ""
	if !isDM && !mentions(m.Message, me.ID) {
		return
	}

	prompt := stripMention(m.Content, me.ID)
	if prompt == "" {
		return
	}

	log := slog.With("message_id", m.ID, "guild_id", m.GuildID, "channel_id", m.ChannelID,
		"user", m.Author.Username, "user_id", m.Author.ID)

	if prompt == "!reset" {
		b.resetHistory(m.ChannelID)
		log.Info("conversation history reset")
		b.reply(log, s, m.Message, "Conversation history cleared.")
		return
	}

	log.Info("question received", "dm", isDM, "chars", len(prompt))

	// Handlers run on their own goroutine, so a slow model only blocks this message.
	stopTyping := keepTyping(s, m.ChannelID)
	answer := b.ask(log, m.ChannelID, m.Author.Username, prompt)
	stopTyping()
	b.reply(log, s, m.Message, answer)
}

func (b *Bot) onInteraction(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Type != discordgo.InteractionApplicationCommand {
		return
	}
	data := i.ApplicationCommandData()
	if data.Name != auracleCommand.Name || len(data.Options) == 0 {
		return
	}
	sub := data.Options[0]

	// Members are set for commands used in a server, users for DMs.
	user := i.User
	if i.Member != nil {
		user = i.Member.User
	}
	log := slog.With("interaction_id", i.ID, "command", data.Name+" "+sub.Name, "guild_id", i.GuildID,
		"channel_id", i.ChannelID, "user", user.Username, "user_id", user.ID)

	switch sub.Name {
	case "whisper":
		b.onWhisper(log, s, i, user, sub)
	default:
		log.Warn("unknown subcommand")
	}
}

// onWhisper answers "/auracle whisper" with a reply only the asker can see.
func (b *Bot) onWhisper(log *slog.Logger, s *discordgo.Session, i *discordgo.InteractionCreate,
	user *discordgo.User, sub *discordgo.ApplicationCommandInteractionDataOption) {
	var question string
	if opt := sub.GetOption("question"); opt != nil {
		question = strings.TrimSpace(opt.StringValue())
	}
	log.Info("whispered question received", "chars", len(question))

	// Discord requires a response within 3 seconds, so acknowledge now; the
	// user sees a private "thinking…" status until the answer is sent.
	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Flags: discordgo.MessageFlagsEphemeral},
	})
	if err != nil {
		log.Error("failed to acknowledge command", "err", err)
		return
	}

	switch question {
	case "":
		b.whisper(log, s, i.Interaction, unclearReply())
	case "!reset":
		b.resetHistory(whisperKey(user.ID))
		log.Info("whisper history reset")
		b.whisper(log, s, i.Interaction, "Your whispered conversation history has been cleared.")
	default:
		b.whisper(log, s, i.Interaction, b.ask(log, whisperKey(user.ID), user.Username, question))
	}
}

// ask answers a question from user, using and updating the conversation
// history stored under key. If the model gives no answer, it returns one of
// the unclear replies, which is not added to the history.
func (b *Bot) ask(log *slog.Logger, key, username, prompt string) string {
	log.Debug("question text", "text", prompt)

	start := time.Now()
	user := ollama.Message{Role: "user", Content: username + ": " + prompt}
	tools := &loggedTools{ToolHandler: b.kb, log: log}
	answer, err := b.llm.ChatWithTools(context.Background(), b.conversation(key, user), tools, maxToolRounds)
	if err != nil {
		log.Error("model request failed", "err", err, "duration", time.Since(start), "tool_calls", tools.calls)
		return unclearReply()
	}
	answer = strings.TrimSpace(answer)
	if answer == "" {
		log.Warn("model returned an empty answer", "duration", time.Since(start), "tool_calls", tools.calls)
		return unclearReply()
	}

	log.Info("question answered", "duration", time.Since(start), "tool_calls", tools.calls, "chars", len(answer))
	log.Debug("answer text", "text", answer)

	b.remember(key, user, ollama.Message{Role: "assistant", Content: answer})
	return answer
}

// loggedTools wraps a ToolHandler to log each tool call the model makes.
type loggedTools struct {
	ollama.ToolHandler
	log   *slog.Logger
	calls int
}

func (t *loggedTools) Call(name string, args json.RawMessage) string {
	t.calls++
	start := time.Now()
	out := t.ToolHandler.Call(name, args)
	level := slog.LevelDebug
	if strings.HasPrefix(out, "error:") {
		level = slog.LevelWarn
	}
	t.log.Log(context.Background(), level, "tool call", "tool", name, "args", string(args),
		"duration", time.Since(start), "result_chars", len(out))
	return out
}

// conversation returns the messages to send to Ollama for a new user message.
func (b *Bot) conversation(channelID string, user ollama.Message) []ollama.Message {
	b.mu.Lock()
	defer b.mu.Unlock()

	msgs := make([]ollama.Message, 0, len(b.history[channelID])+2)
	msgs = append(msgs, ollama.Message{Role: "system", Content: b.system})
	msgs = append(msgs, b.history[channelID]...)
	return append(msgs, user)
}

func (b *Bot) remember(channelID string, msgs ...ollama.Message) {
	b.mu.Lock()
	defer b.mu.Unlock()

	h := append(b.history[channelID], msgs...)
	if len(h) > b.maxHistory {
		h = h[len(h)-b.maxHistory:]
	}
	b.history[channelID] = h
}

func (b *Bot) resetHistory(channelID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.history, channelID)
}

// reply sends text as a reply to m, splitting it to fit Discord's length limit.
func (b *Bot) reply(log *slog.Logger, s *discordgo.Session, m *discordgo.Message, text string) {
	for i, chunk := range split(text, maxDiscordMessage) {
		var err error
		if i == 0 {
			_, err = s.ChannelMessageSendReply(m.ChannelID, chunk, m.Reference())
		} else {
			_, err = s.ChannelMessageSend(m.ChannelID, chunk)
		}
		if err != nil {
			log.Error("failed to send discord message", "err", err)
			return
		}
	}
}

// whisper sends text as a private reply to a deferred slash command,
// splitting it to fit Discord's length limit.
func (b *Bot) whisper(log *slog.Logger, s *discordgo.Session, i *discordgo.Interaction, text string) {
	for n, chunk := range split(text, maxDiscordMessage) {
		var err error
		if n == 0 {
			_, err = s.InteractionResponseEdit(i, &discordgo.WebhookEdit{Content: &chunk})
		} else {
			_, err = s.FollowupMessageCreate(i, false, &discordgo.WebhookParams{
				Content: chunk,
				Flags:   discordgo.MessageFlagsEphemeral,
			})
		}
		if err != nil {
			// Interaction tokens expire after 15 minutes, which a very slow
			// answer can exceed.
			log.Error("failed to send whispered reply", "err", err)
			return
		}
	}
}

// keepTyping shows the typing indicator in a channel until the returned func
// is called. Discord clears the indicator after about 10 seconds, so it is
// refreshed periodically.
func keepTyping(s *discordgo.Session, channelID string) (stop func()) {
	done := make(chan struct{})
	go func() {
		t := time.NewTicker(8 * time.Second)
		defer t.Stop()
		for {
			_ = s.ChannelTyping(channelID)
			select {
			case <-done:
				return
			case <-t.C:
			}
		}
	}()
	return func() { close(done) }
}

func mentions(m *discordgo.Message, userID string) bool {
	for _, u := range m.Mentions {
		if u.ID == userID {
			return true
		}
	}
	return false
}

func stripMention(content, userID string) string {
	content = strings.ReplaceAll(content, "<@"+userID+">", "")
	content = strings.ReplaceAll(content, "<@!"+userID+">", "")
	return strings.TrimSpace(content)
}

// split breaks text into chunks of at most limit characters, preferring to
// break on newlines, then spaces.
func split(text string, limit int) []string {
	var chunks []string
	runes := []rune(text)
	for len(runes) > limit {
		cut := limit
		window := string(runes[:limit])
		if i := strings.LastIndex(window, "\n"); i > 0 {
			cut = len([]rune(window[:i]))
		} else if i := strings.LastIndex(window, " "); i > 0 {
			cut = len([]rune(window[:i]))
		}
		chunks = append(chunks, string(runes[:cut]))
		runes = []rune(strings.TrimLeft(string(runes[cut:]), "\n "))
	}
	if len(runes) > 0 {
		chunks = append(chunks, string(runes))
	}
	return chunks
}
