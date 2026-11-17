// Package inference defines the contract between InferLab's control plane and
// the inference backends (Ollama, vLLM, ...) it orchestrates. InferLab does not
// implement inference itself; providers only translate to and from a backend.
package inference

import (
	"context"
	"errors"
)

// ErrInvalidRequest is returned (possibly wrapped) by Generate when the request
// is rejected before streaming starts, so callers can map it to HTTP 400.
var ErrInvalidRequest = errors.New("inference: invalid request")

// ErrModelNotFound is returned (possibly wrapped) by Generate when the backend
// does not have the requested model, so callers can map it to HTTP 404.
var ErrModelNotFound = errors.New("inference: model not found")

// Request describes a single text-generation call.
type Request struct {
	Model       string
	Prompt      string
	MaxTokens   int
	Temperature float64
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
