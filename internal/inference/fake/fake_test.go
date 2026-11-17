package fake

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Vaibhavee89/inferlab/internal/inference"
)

func collect(t *testing.T, ch <-chan inference.Chunk) []inference.Chunk {
	t.Helper()
	var got []inference.Chunk
	timeout := time.After(2 * time.Second)
	for {
		select {
		case c, ok := <-ch:
			if !ok {
				return got
			}
			got = append(got, c)
		case <-timeout:
			t.Fatal("timed out waiting for channel to close")
		}
	}
}

func TestGenerate(t *testing.T) {
	boom := errors.New("boom")

	tests := []struct {
		name    string
		p       *Provider
		req     inference.Request
		want    []inference.Chunk
		wantErr error
	}{
		{
			name: "streams tokens then done",
			p:    &Provider{Tokens: []string{"a", "b", "c"}},
			req:  inference.Request{Prompt: "hi"},
			want: []inference.Chunk{{Text: "a"}, {Text: "b"}, {Text: "c"}, {Done: true}},
		},
		{
			name: "no tokens still sends done",
			p:    &Provider{},
			req:  inference.Request{Prompt: "hi"},
			want: []inference.Chunk{{Done: true}},
		},
		{
			name: "mid-stream error is the last chunk",
			p:    &Provider{Tokens: []string{"a"}, MidStreamErr: boom},
			req:  inference.Request{Prompt: "hi"},
			want: []inference.Chunk{{Text: "a"}, {Err: boom}},
		},
		{
			name:    "empty prompt is an invalid request",
			p:       &Provider{Tokens: []string{"a"}},
			req:     inference.Request{},
			wantErr: inference.ErrInvalidRequest,
		},
		{
			name:    "start error is returned before streaming",
			p:       &Provider{StartErr: boom},
			req:     inference.Request{Prompt: "hi"},
			wantErr: boom,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ch, err := tt.p.Generate(context.Background(), tt.req)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Generate() error = %v, want %v", err, tt.wantErr)
			}
			if tt.wantErr != nil {
				if ch != nil {
					t.Fatal("channel must be nil when Generate returns an error")
				}
				return
			}

			got := collect(t, ch)
			if len(got) != len(tt.want) {
				t.Fatalf("got %d chunks %+v, want %d %+v", len(got), got, len(tt.want), tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("chunk %d = %+v, want %+v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestGenerateCancelStopsStream(t *testing.T) {
	tests := []struct {
		name string
		p    *Provider
		// consume reads this many chunks before cancelling.
		consume int
	}{
		{"cancel while waiting on delay", &Provider{Tokens: []string{"a", "b", "c"}, Delay: time.Hour}, 0},
		{"cancel with unread chunk blocked on send", &Provider{Tokens: []string{"a", "b", "c"}}, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			ch, err := tt.p.Generate(ctx, inference.Request{Prompt: "hi"})
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < tt.consume; i++ {
				<-ch
			}
			cancel()

			// The channel must close promptly even though we stop reading;
			// closure proves the provider goroutine exited.
			deadline := time.After(2 * time.Second)
			for {
				select {
				case _, ok := <-ch:
					if !ok {
						return
					}
				case <-deadline:
					t.Fatal("channel not closed after cancel; goroutine leaked")
				}
			}
		})
	}
}

func TestGenerateCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	ch, err := (&Provider{Tokens: []string{"a"}}).Generate(ctx, inference.Request{Prompt: "hi"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Generate() error = %v, want context.Canceled", err)
	}
	if ch != nil {
		t.Fatal("channel must be nil when Generate returns an error")
	}
}
