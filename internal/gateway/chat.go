package gateway

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/Vaibhavee89/inferlab/internal/inference"
)

// maxChatBody bounds the size of a chat completion request body.
const maxChatBody = 1 << 20

// chatRequest is the subset of the OpenAI chat completions request that
// InferLab understands. Unknown fields are ignored so stock OpenAI clients work.
type chatRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	Stream      bool          `json:"stream"`
	MaxTokens   int           `json:"max_tokens"`
	Temperature float64       `json:"temperature"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// chatCompletion is the non-streaming response (object "chat.completion").
type chatCompletion struct {
	ID      string       `json:"id"`
	Object  string       `json:"object"`
	Created int64        `json:"created"`
	Model   string       `json:"model"`
	Choices []chatChoice `json:"choices"`
}

type chatChoice struct {
	Index        int          `json:"index"`
	Message      *chatMessage `json:"message,omitempty"`
	FinishReason string       `json:"finish_reason"`
}

// chatCompletionChunk is one SSE event of a streaming response
// (object "chat.completion.chunk").
type chatCompletionChunk struct {
	ID      string            `json:"id"`
	Object  string            `json:"object"`
	Created int64             `json:"created"`
	Model   string            `json:"model"`
	Choices []chatChunkChoice `json:"choices"`
}

type chatChunkChoice struct {
	Index int       `json:"index"`
	Delta chatDelta `json:"delta"`
	// FinishReason is null until the final chunk.
	FinishReason *string `json:"finish_reason"`
}

type chatDelta struct {
	Role    string `json:"role,omitempty"`
	Content string `json:"content,omitempty"`
}

// apiError is the OpenAI error envelope.
type apiError struct {
	Error apiErrorBody `json:"error"`
}

type apiErrorBody struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code,omitempty"`
}

type chatHandler struct {
	provider inference.Provider
	logger   *slog.Logger
}

func newChatHandler(provider inference.Provider, logger *slog.Logger) *chatHandler {
	return &chatHandler{provider: provider, logger: logger}
}

func (h *chatHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	req, err := decodeChatRequest(w, r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "", err.Error())
		return
	}

	// r.Context() is cancelled when the client disconnects. Passing it (via ctx)
	// to the provider is what cancels the upstream request. The deferred cancel
	// also releases the provider if we return early for any other reason.
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	chunks, err := h.provider.Generate(ctx, inference.Request{
		Model:       req.Model,
		Messages:    toInferenceMessages(req.Messages),
		MaxTokens:   req.MaxTokens,
		Temperature: req.Temperature,
	})
	if err != nil {
		if ctx.Err() != nil {
			return // client already gone; nobody to answer
		}
		status, typ, code := classifyError(err)
		if status == http.StatusBadGateway {
			h.logger.Warn("upstream request failed", slog.String("error", err.Error()))
		}
		writeError(w, status, typ, code, cleanMessage(err))
		return
	}

	id := newCompletionID()
	created := time.Now().Unix()
	if req.Stream {
		h.stream(ctx, w, chunks, id, created, req.Model)
		return
	}
	h.respond(ctx, w, chunks, id, created, req.Model)
}

// respond collects the whole generation and writes a single JSON response.
func (h *chatHandler) respond(ctx context.Context, w http.ResponseWriter, chunks <-chan inference.Chunk, id string, created int64, model string) {
	var content strings.Builder
	for c := range chunks {
		if c.Err != nil {
			h.logger.Warn("upstream stream failed", slog.String("error", c.Err.Error()))
			writeError(w, http.StatusBadGateway, "api_error", "upstream_error", c.Err.Error())
			return
		}
		content.WriteString(c.Text)
	}
	if ctx.Err() != nil {
		return // cancelled: the channel closed early and the client is gone
	}

	writeJSON(w, http.StatusOK, chatCompletion{
		ID:      id,
		Object:  "chat.completion",
		Created: created,
		Model:   model,
		Choices: []chatChoice{{
			Message:      &chatMessage{Role: inference.RoleAssistant, Content: content.String()},
			FinishReason: "stop",
		}},
	})
}

// stream writes the generation as server-sent events, ending with [DONE].
func (h *chatHandler) stream(ctx context.Context, w http.ResponseWriter, chunks <-chan inference.Chunk, id string, created int64, model string) {
	rc := http.NewResponseController(w)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	emit := func(v any) bool {
		payload, err := json.Marshal(v)
		if err != nil {
			h.logger.Error("encode sse event", slog.String("error", err.Error()))
			return false
		}
		if _, err := fmt.Fprintf(w, "data: %s\n\n", payload); err != nil {
			return false
		}
		return rc.Flush() == nil
	}
	emitDone := func() {
		if _, err := io.WriteString(w, "data: [DONE]\n\n"); err == nil {
			_ = rc.Flush()
		}
	}
	chunkOf := func(delta chatDelta, finish *string) chatCompletionChunk {
		return chatCompletionChunk{
			ID: id, Object: "chat.completion.chunk", Created: created, Model: model,
			Choices: []chatChunkChoice{{Delta: delta, FinishReason: finish}},
		}
	}

	// Announce the assistant role first, as OpenAI does.
	if !emit(chunkOf(chatDelta{Role: inference.RoleAssistant}, nil)) {
		return
	}

	for c := range chunks {
		switch {
		case c.Err != nil:
			h.logger.Warn("upstream stream failed", slog.String("error", c.Err.Error()))
			// Headers are already sent, so the failure travels in-band.
			if emit(apiError{Error: apiErrorBody{Message: c.Err.Error(), Type: "api_error", Code: "upstream_error"}}) {
				emitDone()
			}
			return
		case c.Done:
			stop := "stop"
			if emit(chunkOf(chatDelta{}, &stop)) {
				emitDone()
			}
			return
		case c.Text != "":
			if !emit(chunkOf(chatDelta{Content: c.Text}, nil)) {
				return // client gone; the deferred cancel stops the provider
			}
		}
	}
	// Channel closed without Done or Err: the context was cancelled and there
	// is no one left to write to.
}

func decodeChatRequest(w http.ResponseWriter, r *http.Request) (chatRequest, error) {
	var req chatRequest
	body := http.MaxBytesReader(w, r.Body, maxChatBody)
	if err := json.NewDecoder(body).Decode(&req); err != nil {
		var tooBig *http.MaxBytesError
		switch {
		case errors.As(err, &tooBig):
			return req, fmt.Errorf("request body exceeds %d bytes", maxChatBody)
		case errors.Is(err, io.EOF):
			return req, errors.New("request body is empty")
		default:
			return req, fmt.Errorf("invalid JSON body: %w", err)
		}
	}

	if req.MaxTokens < 0 {
		return req, errors.New("max_tokens must not be negative")
	}
	if req.Temperature < 0 || req.Temperature > 2 {
		return req, errors.New("temperature must be between 0 and 2")
	}
	if err := (inference.Request{Model: req.Model, Messages: toInferenceMessages(req.Messages)}).Validate(); err != nil {
		return req, errors.New(cleanMessage(err))
	}
	return req, nil
}

func toInferenceMessages(in []chatMessage) []inference.Message {
	out := make([]inference.Message, len(in))
	for i, m := range in {
		out[i] = inference.Message{Role: m.Role, Content: m.Content}
	}
	return out
}

// classifyError maps a pre-stream provider error to an HTTP status and
// OpenAI error type/code.
func classifyError(err error) (status int, typ, code string) {
	switch {
	case errors.Is(err, inference.ErrInvalidRequest):
		return http.StatusBadRequest, "invalid_request_error", ""
	case errors.Is(err, inference.ErrModelNotFound):
		return http.StatusNotFound, "invalid_request_error", "model_not_found"
	default:
		return http.StatusBadGateway, "api_error", "upstream_error"
	}
}

// cleanMessage strips the package sentinel prefix from err so clients see
// "model is required" rather than "inference: invalid request: model is required".
func cleanMessage(err error) string {
	msg := err.Error()
	for _, s := range []error{inference.ErrInvalidRequest, inference.ErrModelNotFound} {
		msg = strings.TrimPrefix(msg, s.Error()+": ")
	}
	return msg
}

func writeError(w http.ResponseWriter, status int, typ, code, msg string) {
	writeJSON(w, status, apiError{Error: apiErrorBody{Message: msg, Type: typ, Code: code}})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func newCompletionID() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return "chatcmpl-" + hex.EncodeToString(b[:])
}
