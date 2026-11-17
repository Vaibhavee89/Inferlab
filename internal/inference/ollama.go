package inference

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// DefaultOllamaURL is where a local Ollama server listens by default.
const DefaultOllamaURL = "http://localhost:11434"

const (
	// maxErrorBody bounds how much of a non-200 response body we read.
	maxErrorBody = 4 << 10
	// maxStreamLine bounds a single NDJSON line from the stream.
	maxStreamLine = 1 << 20
)

// Ollama is a Provider backed by an Ollama server's /api/chat endpoint.
type Ollama struct {
	baseURL string
	client  *http.Client
}

var _ Provider = (*Ollama)(nil)

// NewOllama returns an Ollama provider. An empty baseURL selects
// DefaultOllamaURL. A nil client selects a plain http.Client with no overall
// timeout: streams are long-lived, so cancellation is governed by the context
// passed to Generate.
func NewOllama(baseURL string, client *http.Client) *Ollama {
	if baseURL == "" {
		baseURL = DefaultOllamaURL
	}
	if client == nil {
		client = &http.Client{}
	}
	return &Ollama{baseURL: strings.TrimRight(baseURL, "/"), client: client}
}

// Name implements Provider.
func (o *Ollama) Name() string { return "ollama" }

type ollamaChatRequest struct {
	Model    string          `json:"model"`
	Messages []ollamaMessage `json:"messages"`
	Stream   bool            `json:"stream"`
	Options  ollamaOptions   `json:"options"`
}

type ollamaMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ollamaOptions fields are omitted when zero so Ollama applies its defaults.
type ollamaOptions struct {
	NumPredict  int     `json:"num_predict,omitempty"`
	Temperature float64 `json:"temperature,omitempty"`
}

// ollamaChatChunk is one NDJSON line of the /api/chat response stream.
type ollamaChatChunk struct {
	Message ollamaMessage `json:"message"`
	Done    bool          `json:"done"`
	Error   string        `json:"error"`
}

// Generate implements Provider. Failures before the first byte of the stream
// (invalid request, connection error, non-200 status) are returned as errors;
// anything after that is delivered as a final Chunk with Err set.
func (o *Ollama) Generate(ctx context.Context, req Request) (<-chan Chunk, error) {
	if req.Model == "" {
		return nil, fmt.Errorf("%w: empty model", ErrInvalidRequest)
	}
	if req.Prompt == "" {
		return nil, fmt.Errorf("%w: empty prompt", ErrInvalidRequest)
	}

	body, err := json.Marshal(ollamaChatRequest{
		Model:    req.Model,
		Messages: []ollamaMessage{{Role: "user", Content: req.Prompt}},
		Stream:   true,
		Options:  ollamaOptions{NumPredict: req.MaxTokens, Temperature: req.Temperature},
	})
	if err != nil {
		return nil, fmt.Errorf("ollama: encode request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("ollama: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := o.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("ollama: request failed: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		return nil, statusError(resp)
	}

	out := make(chan Chunk)
	go o.stream(ctx, resp.Body, out)
	return out, nil
}

// statusError maps a non-200 Ollama response to an error, wrapping the
// package sentinels where one applies.
func statusError(resp *http.Response) error {
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
	msg := strings.TrimSpace(string(raw))
	var payload struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(raw, &payload) == nil && payload.Error != "" {
		msg = payload.Error
	}
	if msg == "" {
		msg = http.StatusText(resp.StatusCode)
	}

	switch resp.StatusCode {
	case http.StatusBadRequest:
		return fmt.Errorf("%w: %s", ErrInvalidRequest, msg)
	case http.StatusNotFound:
		return fmt.Errorf("%w: %s", ErrModelNotFound, msg)
	default:
		return fmt.Errorf("ollama: unexpected status %d: %s", resp.StatusCode, msg)
	}
}

// stream reads the NDJSON body and forwards chunks until the stream ends, an
// error occurs, or ctx is cancelled. It owns body and out: both are closed
// when it returns. Cancelling ctx aborts the HTTP request, which unblocks any
// pending read.
func (o *Ollama) stream(ctx context.Context, body io.ReadCloser, out chan<- Chunk) {
	defer close(out)
	defer body.Close()

	send := func(c Chunk) bool {
		select {
		case out <- c:
			return true
		case <-ctx.Done():
			return false
		}
	}

	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 0, 64<<10), maxStreamLine)

	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}

		var msg ollamaChatChunk
		if err := json.Unmarshal(line, &msg); err != nil {
			send(Chunk{Err: fmt.Errorf("ollama: decode stream line: %w", err)})
			return
		}
		if msg.Error != "" {
			send(Chunk{Err: fmt.Errorf("ollama: %s", msg.Error)})
			return
		}
		if msg.Message.Content != "" {
			if !send(Chunk{Text: msg.Message.Content}) {
				return
			}
		}
		if msg.Done {
			send(Chunk{Done: true})
			return
		}
	}

	// A cancelled context surfaces as a read error; that is not a failure.
	if ctx.Err() != nil {
		return
	}
	if err := sc.Err(); err != nil {
		send(Chunk{Err: fmt.Errorf("ollama: read stream: %w", err)})
		return
	}
	send(Chunk{Err: fmt.Errorf("ollama: stream ended before done: %w", io.ErrUnexpectedEOF)})
}
