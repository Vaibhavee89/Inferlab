// Package fake provides an in-memory inference.Provider for tests. It is also
// the reference implementation of the inference.Provider channel contract.
package fake

import (
	"context"
	"time"

	"github.com/Vaibhavee89/inferlab/internal/inference"
)

// Provider streams a fixed list of tokens.
type Provider struct {
	// Tokens are emitted in order, one per chunk.
	Tokens []string
	// Delay is waited before each token; zero means no delay.
	Delay time.Duration
	// StartErr, if set, is returned from Generate before streaming starts.
	StartErr error
	// MidStreamErr, if set, is sent as a final Chunk.Err after all tokens
	// instead of a Done chunk.
	MidStreamErr error
}

var _ inference.Provider = (*Provider)(nil)

// Name implements inference.Provider.
func (p *Provider) Name() string { return "fake" }

// Generate implements inference.Provider.
func (p *Provider) Generate(ctx context.Context, req inference.Request) (<-chan inference.Chunk, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := req.Validate(); err != nil {
		return nil, err
	}
	if p.StartErr != nil {
		return nil, p.StartErr
	}

	out := make(chan inference.Chunk)
	go func() {
		defer close(out)

		send := func(c inference.Chunk) bool {
			select {
			case out <- c:
				return true
			case <-ctx.Done():
				return false
			}
		}

		for _, tok := range p.Tokens {
			if p.Delay > 0 {
				t := time.NewTimer(p.Delay)
				select {
				case <-t.C:
				case <-ctx.Done():
					t.Stop()
					return
				}
			}
			if !send(inference.Chunk{Text: tok}) {
				return
			}
		}

		if p.MidStreamErr != nil {
			send(inference.Chunk{Err: p.MidStreamErr})
			return
		}
		send(inference.Chunk{Done: true})
	}()

	return out, nil
}
