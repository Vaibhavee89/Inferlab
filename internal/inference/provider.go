// Package inference defines the contract between InferLab's control plane and
// the inference backends (Ollama, vLLM, ...) it orchestrates. InferLab does not
// implement inference itself; providers only translate to and from a backend.
package inference

import (
	"context"
	"errors"
	"fmt"
)

// ErrInvalidRequest is returned (possibly wrapped) by Generate when the request
// is rejected before streaming starts, so callers can map it to HTTP 400.
var ErrInvalidRequest = errors.New("inference: invalid request")

// ErrModelNotFound is returned (possibly wrapped) by Generate when the backend
// does not have the requested model, so callers can map it to HTTP 404.
var ErrModelNotFound = errors.New("inference: model not found")

// Chat message roles.
const (
	RoleSystem    = "system"
	RoleUser      = "user"
	RoleAssistant = "assistant"
)

// Message is one turn of a conversation.
type Message struct {
	Role    string
	Content string
}

// Request describes a single chat-completion call. Callers that only have a
// single prompt wrap it as one user Message.
type Request struct {
	Model       string
	Messages    []Message
	MaxTokens   int
	Temperature float64
}

// Validate checks that the request is well-formed. Every returned error wraps
// ErrInvalidRequest.
func (r Request) Validate() error {
	if r.Model == "" {
		return fmt.Errorf("%w: model is required", ErrInvalidRequest)
	}
	if len(r.Messages) == 0 {
		return fmt.Errorf("%w: messages must not be empty", ErrInvalidRequest)
	}
	for i, m := range r.Messages {
		switch m.Role {
		case RoleSystem, RoleUser, RoleAssistant:
		default:
			return fmt.Errorf("%w: messages[%d]: invalid role %q (want system, user or assistant)", ErrInvalidRequest, i, m.Role)
		}
	}
	return nil
}

// Chunk is one element of a generation stream.
type Chunk struct {
	// Text is the generated token text (may be empty on the final chunk).
	Text string
	// Done marks the final chunk of a successful stream.
	Done bool
	// Err reports a failure that occurred after streaming started. It is
	// always the last chunk; the channel is closed right after it.
	Err error
}

// Provider is an inference backend.
//
// Generate contract:
//   - A non-nil error means the request failed before streaming started
//     (connection refused, bad request, model not found). The returned channel
//     is nil, and the gateway can still pick a proper HTTP status.
//   - On success the provider streams chunks on the returned channel and closes
//     it when the stream finishes, a chunk carries Err, or ctx is cancelled.
//   - After ctx is cancelled the provider must stop promptly and must not leave
//     any goroutine running. Consumers may abandon the channel after cancelling
//     ctx without draining it.
type Provider interface {
	// Name identifies the provider (e.g. "ollama", "vllm").
	Name() string
	Generate(ctx context.Context, req Request) (<-chan Chunk, error)
}
