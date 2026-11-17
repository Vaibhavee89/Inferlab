package gateway

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Vaibhavee89/inferlab/internal/inference"
	"github.com/Vaibhavee89/inferlab/internal/inference/fake"
)

const chatPath = "/v1/chat/completions"

const validBody = `{"model":"m","messages":[{"role":"user","content":"hi"}]}`

func post(t *testing.T, h http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, chatPath, strings.NewReader(body)))
	return rec
}

// sseData returns the payload of every "data: " line in body.
func sseData(body string) []string {
	var out []string
	for _, line := range strings.Split(body, "\n") {
		if p, ok := strings.CutPrefix(line, "data: "); ok {
			out = append(out, p)
		}
	}
	return out
}

func TestChatNonStream(t *testing.T) {
	h := NewRouter(discardLogger(), &fake.Provider{Tokens: []string{"Hel", "lo", "!"}})
	rec := post(t, h, validBody)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}
	var got chatCompletion
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Object != "chat.completion" || !strings.HasPrefix(got.ID, "chatcmpl-") || got.Model != "m" {
		t.Errorf("envelope = %+v", got)
	}
	if len(got.Choices) != 1 {
		t.Fatalf("choices = %d, want 1", len(got.Choices))
	}
	c := got.Choices[0]
	if c.Message == nil || c.Message.Role != "assistant" || c.Message.Content != "Hello!" || c.FinishReason != "stop" {
		t.Errorf("choice = %+v", c)
	}
}

func TestChatStream(t *testing.T) {
	h := NewRouter(discardLogger(), &fake.Provider{Tokens: []string{"Hel", "lo"}})
	rec := post(t, h, `{"model":"m","stream":true,"messages":[{"role":"user","content":"hi"}]}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %q", ct)
	}
	events := sseData(rec.Body.String())
	// role, 2 tokens, finish, [DONE]
	if len(events) != 5 {
		t.Fatalf("got %d events %q, want 5", len(events), events)
	}
	if events[len(events)-1] != "[DONE]" {
		t.Errorf("last event = %q, want [DONE]", events[len(events)-1])
	}

	var text strings.Builder
	var id string
	for i, e := range events[:len(events)-1] {
		var ch chatCompletionChunk
		if err := json.Unmarshal([]byte(e), &ch); err != nil {
			t.Fatalf("event %d: %v", i, err)
		}
		if ch.Object != "chat.completion.chunk" || len(ch.Choices) != 1 {
			t.Fatalf("event %d = %+v", i, ch)
		}
		if id == "" {
			id = ch.ID
		} else if ch.ID != id {
			t.Errorf("event %d id = %q, want %q", i, ch.ID, id)
		}
		d := ch.Choices[0]
		switch {
		case i == 0:
			if d.Delta.Role != "assistant" || d.FinishReason != nil {
				t.Errorf("first chunk = %+v", d)
			}
		case i == len(events)-2:
			if d.FinishReason == nil || *d.FinishReason != "stop" {
				t.Errorf("final chunk finish_reason = %v", d.FinishReason)
			}
		default:
			if d.FinishReason != nil {
				t.Errorf("event %d has finish_reason %q", i, *d.FinishReason)
			}
			text.WriteString(d.Delta.Content)
		}
	}
	if text.String() != "Hello" {
		t.Errorf("streamed text = %q, want Hello", text.String())
	}
}

func TestChatErrors(t *testing.T) {
	tests := []struct {
		name     string
		provider *fake.Provider
		body     string
		status   int
		typ      string
		code     string
	}{
		{"empty body", &fake.Provider{}, ``, 400, "invalid_request_error", ""},
		{"bad json", &fake.Provider{}, `{nope`, 400, "invalid_request_error", ""},
		{"missing model", &fake.Provider{}, `{"messages":[{"role":"user","content":"x"}]}`, 400, "invalid_request_error", ""},
		{"empty messages", &fake.Provider{}, `{"model":"m","messages":[]}`, 400, "invalid_request_error", ""},
		{"bad role", &fake.Provider{}, `{"model":"m","messages":[{"role":"robot","content":"x"}]}`, 400, "invalid_request_error", ""},
		{"negative max_tokens", &fake.Provider{}, `{"model":"m","max_tokens":-1,"messages":[{"role":"user","content":"x"}]}`, 400, "invalid_request_error", ""},
		{"temperature too high", &fake.Provider{}, `{"model":"m","temperature":3,"messages":[{"role":"user","content":"x"}]}`, 400, "invalid_request_error", ""},
		{"body too large", &fake.Provider{}, `{"model":"m","messages":[{"role":"user","content":"` + strings.Repeat("a", maxChatBody) + `"}]}`, 400, "invalid_request_error", ""},
		{"model not found", &fake.Provider{StartErr: fmt.Errorf("%w: nope", inference.ErrModelNotFound)}, validBody, 404, "invalid_request_error", "model_not_found"},
		{"upstream down", &fake.Provider{StartErr: errors.New("connection refused")}, validBody, 502, "api_error", "upstream_error"},
		{"mid-stream failure, non-stream", &fake.Provider{Tokens: []string{"a"}, MidStreamErr: errors.New("runner crashed")}, validBody, 502, "api_error", "upstream_error"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := post(t, NewRouter(discardLogger(), tt.provider), tt.body)
			if rec.Code != tt.status {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, tt.status, rec.Body)
			}
			var e apiError
			if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil {
				t.Fatalf("decode error envelope: %v", err)
			}
			if e.Error.Type != tt.typ || e.Error.Code != tt.code || e.Error.Message == "" {
				t.Errorf("error = %+v, want type %q code %q and a message", e.Error, tt.typ, tt.code)
			}
		})
	}
}

func TestChatStreamMidStreamError(t *testing.T) {
	h := NewRouter(discardLogger(), &fake.Provider{Tokens: []string{"a"}, MidStreamErr: errors.New("runner crashed")})
	rec := post(t, h, `{"model":"m","stream":true,"messages":[{"role":"user","content":"hi"}]}`)

	// Headers were already sent, so the status stays 200 and the error is in-band.
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	events := sseData(rec.Body.String())
	if len(events) < 2 || events[len(events)-1] != "[DONE]" {
		t.Fatalf("events = %q", events)
	}
	var e apiError
	if err := json.Unmarshal([]byte(events[len(events)-2]), &e); err != nil || e.Error.Code != "upstream_error" {
		t.Errorf("penultimate event = %q (err %v), want upstream_error envelope", events[len(events)-2], err)
	}
}

func TestChatWrongMethod(t *testing.T) {
	rec := httptest.NewRecorder()
	NewRouter(discardLogger(), &fake.Provider{}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, chatPath, nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rec.Code)
	}
}

// blockingProvider emits one token, then waits for ctx cancellation and
// reports it, so tests can observe that a client disconnect reaches upstream.
type blockingProvider struct {
	started   chan struct{}
	cancelled chan struct{}
}

func (p *blockingProvider) Name() string { return "blocking" }

func (p *blockingProvider) Generate(ctx context.Context, _ inference.Request) (<-chan inference.Chunk, error) {
	out := make(chan inference.Chunk)
	go func() {
		defer close(out)
		select {
		case out <- inference.Chunk{Text: "first"}:
		case <-ctx.Done():
			close(p.cancelled)
			return
		}
		close(p.started)
		<-ctx.Done()
		close(p.cancelled)
	}()
	return out, nil
}

func TestChatClientDisconnectCancelsUpstream(t *testing.T) {
	tests := []struct {
		name   string
		stream bool
	}{
		{"stream", true},
		{"non-stream", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &blockingProvider{started: make(chan struct{}), cancelled: make(chan struct{})}
			srv := httptest.NewServer(NewRouter(discardLogger(), p))
			defer srv.Close()

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			body := fmt.Sprintf(`{"model":"m","stream":%t,"messages":[{"role":"user","content":"hi"}]}`, tt.stream)
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+chatPath, strings.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}

			respCh := make(chan error, 1)
			go func() {
				resp, err := http.DefaultClient.Do(req)
				if err == nil {
					defer resp.Body.Close()
					if tt.stream {
						// Read the role event and the first token so the stream is live.
						sc := bufio.NewScanner(resp.Body)
						for n := 0; n < 4 && sc.Scan(); n++ {
						}
					} else {
						_, err = io.Copy(io.Discard, resp.Body)
					}
				}
				respCh <- err
			}()

			select {
			case <-p.started:
			case <-time.After(2 * time.Second):
				t.Fatal("provider never started")
			}
			// For streams give the client a moment to read events, then hang up.
			time.Sleep(50 * time.Millisecond)
			cancel()

			select {
			case <-p.cancelled:
			case <-time.After(2 * time.Second):
				t.Fatal("upstream context was not cancelled after client disconnect")
			}
			<-respCh
		})
	}
}
