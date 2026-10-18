// Package ollama is a minimal client for the Ollama chat API.
package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Message is a single chat message in an Ollama conversation.
type Message struct {
	Role      string     `json:"role"`
	Content   string     `json:"content"`
	Thinking  string     `json:"thinking,omitempty"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
	ToolName  string     `json:"tool_name,omitempty"`
}

// ToolCall is a request from the model to run a tool.
type ToolCall struct {
	Function struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	} `json:"function"`
}

// Tool describes a function the model may call.
type Tool struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

// ToolFunction is the definition of a callable function.
type ToolFunction struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Parameters  any    `json:"parameters"`
}

// ToolHandler runs tool calls on behalf of the model.
type ToolHandler interface {
	Tools() []Tool
	// Call runs the named tool and returns its output for the model.
	Call(name string, args json.RawMessage) string
}

// Client talks to an Ollama server.
type Client struct {
	baseURL string
	model   string
	http    *http.Client
}

// New returns a Client for the Ollama server at baseURL using model.
func New(baseURL, model string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		model:   model,
		// Large local models can take a while to load and respond.
		http: &http.Client{Timeout: 5 * time.Minute},
	}
}

type chatRequest struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
	Tools    []Tool    `json:"tools,omitempty"`
	Stream   bool      `json:"stream"`
}

type chatResponse struct {
	Message Message `json:"message"`
	Error   string  `json:"error"`
}

// Chat sends the conversation to Ollama and returns the assistant's reply.
func (c *Client) Chat(ctx context.Context, messages []Message) (string, error) {
	msg, err := c.chat(ctx, chatRequest{Model: c.model, Messages: messages})
	if err != nil {
		return "", err
	}
	return msg.Content, nil
}

// ChatWithTools sends the conversation to Ollama, running any tool calls the
// model makes through h, and returns the model's final reply. It gives up after
// maxRounds rounds of tool calls.
func (c *Client) ChatWithTools(ctx context.Context, messages []Message, h ToolHandler, maxRounds int) (string, error) {
	// Copy so tool traffic never leaks into the caller's slice.
	msgs := append([]Message(nil), messages...)
	req := chatRequest{Model: c.model, Tools: h.Tools()}

	for range maxRounds {
		req.Messages = msgs
		msg, err := c.chat(ctx, req)
		if err != nil {
			return "", err
		}
		if len(msg.ToolCalls) == 0 {
			return msg.Content, nil
		}

		msgs = append(msgs, msg)
		for _, tc := range msg.ToolCalls {
			msgs = append(msgs, Message{
				Role:     "tool",
				ToolName: tc.Function.Name,
				Content:  h.Call(tc.Function.Name, tc.Function.Arguments),
			})
		}
	}
	return "", fmt.Errorf("ollama: no answer after %d rounds of tool calls", maxRounds)
}

func (c *Client) chat(ctx context.Context, cr chatRequest) (Message, error) {
	body, err := json.Marshal(cr)
	if err != nil {
		return Message{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return Message{}, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return Message{}, fmt.Errorf("ollama request: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return Message{}, fmt.Errorf("ollama response: %w", err)
	}

	var out chatResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return Message{}, fmt.Errorf("ollama response (status %d): %w", resp.StatusCode, err)
	}
	if out.Error != "" {
		return Message{}, fmt.Errorf("ollama: %s", out.Error)
	}
	if resp.StatusCode != http.StatusOK {
		return Message{}, fmt.Errorf("ollama: unexpected status %d", resp.StatusCode)
	}
	return out.Message, nil
}
