package inference

import (
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
)

func userMsgs(content string) []Message {
	return []Message{{Role: RoleUser, Content: content}}
}

// tokenLine renders one Ollama NDJSON token line.
func tokenLine(s string) string {
	b, _ := json.Marshal(s)
	return fmt.Sprintf(`{"model":"m","message":{"role":"assistant","content":%s},"done":false}`, b)
}

const doneLine = `{"model":"m","message":{"role":"assistant","content":""},"done":true,"done_reason":"stop","eval_count":3}`

// ndjsonServer replies 200 with the given lines as an NDJSON stream.
func ndjsonServer(t *testing.T, lines ...string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		for _, l := range lines {
			fmt.Fprintln(w, l)
			w.(http.Flusher).Flush()
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func drain(t *testing.T, ch <-chan Chunk) []Chunk {
	t.Helper()
	var got []Chunk
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

func TestOllamaGenerateStream(t *testing.T) {
	tests := []struct {
		name    string
		lines   []string
		want    []Chunk
		wantErr string // substring of the final chunk's Err; empty means none
		wantIs  error  // sentinel the final chunk's Err must wrap
	}{
		{
			name:  "tokens then done",
			lines: []string{tokenLine("Hel"), tokenLine("lo"), doneLine},
			want:  []Chunk{{Text: "Hel"}, {Text: "lo"}, {Done: true}},
		},
		{
			name:  "empty content lines are skipped",
			lines: []string{tokenLine(""), tokenLine("a"), "", doneLine},
			want:  []Chunk{{Text: "a"}, {Done: true}},
		},
		{
			name:  "unicode and escapes survive",
			lines: []string{tokenLine("héllo \"w\"\n"), doneLine},
			want:  []Chunk{{Text: "héllo \"w\"\n"}, {Done: true}},
		},
		{
			name:  "content on the done line is not dropped",
			lines: []string{`{"message":{"role":"assistant","content":"z"},"done":true}`},
			want:  []Chunk{{Text: "z"}, {Done: true}},
		},
		{
			name:    "mid-stream error line",
			lines:   []string{tokenLine("a"), `{"error":"model runner crashed"}`},
			want:    []Chunk{{Text: "a"}},
			wantErr: "model runner crashed",
		},
		{
			name:    "malformed json line",
			lines:   []string{tokenLine("a"), `{not json`},
			want:    []Chunk{{Text: "a"}},
			wantErr: "decode stream line",
		},
		{
			name:    "stream ends without done",
			lines:   []string{tokenLine("a"), tokenLine("b")},
			want:    []Chunk{{Text: "a"}, {Text: "b"}},
			wantErr: "before done",
			wantIs:  io.ErrUnexpectedEOF,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := ndjsonServer(t, tt.lines...)
			p := NewOllama(srv.URL, srv.Client())

			ch, err := p.Generate(context.Background(), Request{Model: "m", Messages: userMsgs("hi")})
			if err != nil {
				t.Fatalf("Generate() error = %v", err)
			}
			got := drain(t, ch)

			var last *Chunk
			if tt.wantErr != "" {
				if len(got) == 0 {
					t.Fatal("got no chunks, want a final error chunk")
				}
				last = &got[len(got)-1]
				got = got[:len(got)-1]
				if last.Err == nil || !strings.Contains(last.Err.Error(), tt.wantErr) {
					t.Fatalf("final chunk Err = %v, want containing %q", last.Err, tt.wantErr)
				}
				if tt.wantIs != nil && !errors.Is(last.Err, tt.wantIs) {
					t.Errorf("final Err %v does not wrap %v", last.Err, tt.wantIs)
				}
			}

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

func TestOllamaRequestShape(t *testing.T) {
	tests := []struct {
		name        string
		req         Request
		wantOptions map[string]any
	}{
		{
			name:        "options set",
			req:         Request{Model: "llama3", Messages: userMsgs("hi"), MaxTokens: 64, Temperature: 0.7},
			wantOptions: map[string]any{"num_predict": float64(64), "temperature": 0.7},
		},
		{
			name: "multi-turn history is sent natively, in order",
			req: Request{Model: "llama3", Messages: []Message{
				{Role: RoleSystem, Content: "be brief"},
				{Role: RoleUser, Content: "hi"},
				{Role: RoleAssistant, Content: "hello"},
				{Role: RoleUser, Content: "bye"},
			}},
			wantOptions: map[string]any{},
		},
		{
			name:        "zero options are omitted",
			req:         Request{Model: "llama3", Messages: userMsgs("hi")},
			wantOptions: map[string]any{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			type captured struct {
				method, path, contentType string
				body                      map[string]any
			}
			gotCh := make(chan captured, 1)

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				_ = json.NewDecoder(r.Body).Decode(&body)
				gotCh <- captured{r.Method, r.URL.Path, r.Header.Get("Content-Type"), body}
				fmt.Fprintln(w, doneLine)
			}))
			defer srv.Close()

			p := NewOllama(srv.URL+"/", srv.Client()) // trailing slash must be tolerated
			ch, err := p.Generate(context.Background(), tt.req)
			if err != nil {
				t.Fatal(err)
			}
			drain(t, ch)

			got := <-gotCh
			if got.method != http.MethodPost || got.path != "/api/chat" {
				t.Errorf("request = %s %s, want POST /api/chat", got.method, got.path)
			}
			if got.contentType != "application/json" {
				t.Errorf("Content-Type = %q", got.contentType)
			}
			if got.body["model"] != tt.req.Model {
				t.Errorf("model = %v, want %q", got.body["model"], tt.req.Model)
			}
			if got.body["stream"] != true {
				t.Errorf("stream = %v, want true", got.body["stream"])
			}
			msgs, _ := got.body["messages"].([]any)
			if len(msgs) != len(tt.req.Messages) {
				t.Fatalf("messages = %v, want %d messages", got.body["messages"], len(tt.req.Messages))
			}
			for i, want := range tt.req.Messages {
				msg, _ := msgs[i].(map[string]any)
				if msg["role"] != want.Role || msg["content"] != want.Content {
					t.Errorf("messages[%d] = %v, want %s/%q", i, msg, want.Role, want.Content)
				}
			}
			opts, _ := got.body["options"].(map[string]any)
			if len(opts) != len(tt.wantOptions) {
				t.Errorf("options = %v, want %v", opts, tt.wantOptions)
			}
			for k, v := range tt.wantOptions {
				if opts[k] != v {
					t.Errorf("options[%q] = %v, want %v", k, opts[k], v)
				}
			}
		})
	}
}

func TestOllamaGenerateStartErrors(t *testing.T) {
	status := func(code int, body string) *httptest.Server {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(code)
			io.WriteString(w, body)
		}))
		t.Cleanup(srv.Close)
		return srv
	}

	tests := []struct {
		name    string
		url     func() string
		req     Request
		wantIs  error
		wantMsg string
	}{
		{
			name:    "model not found",
			url:     func() string { return status(404, `{"error":"model 'nope' not found"}`).URL },
			req:     Request{Model: "nope", Messages: userMsgs("hi")},
			wantIs:  ErrModelNotFound,
			wantMsg: "model 'nope' not found",
		},
		{
			name:    "bad request",
			url:     func() string { return status(400, `{"error":"invalid options"}`).URL },
			req:     Request{Model: "m", Messages: userMsgs("hi")},
			wantIs:  ErrInvalidRequest,
			wantMsg: "invalid options",
		},
		{
			name:    "server error with plain body",
			url:     func() string { return status(500, "kaboom").URL },
			req:     Request{Model: "m", Messages: userMsgs("hi")},
			wantMsg: "status 500: kaboom",
		},
		{
			name:    "empty model never hits the network",
			url:     func() string { return "http://127.0.0.1:1" },
			req:     Request{Messages: userMsgs("hi")},
			wantIs:  ErrInvalidRequest,
			wantMsg: "model is required",
		},
		{
			name:    "empty messages never hit the network",
			url:     func() string { return "http://127.0.0.1:1" },
			req:     Request{Model: "m"},
			wantIs:  ErrInvalidRequest,
			wantMsg: "messages must not be empty",
		},
		{
			name:    "invalid role never hits the network",
			url:     func() string { return "http://127.0.0.1:1" },
			req:     Request{Model: "m", Messages: []Message{{Role: "tool", Content: "x"}}},
			wantIs:  ErrInvalidRequest,
			wantMsg: `invalid role "tool"`,
		},
		{
			name: "connection refused",
			url: func() string {
				srv := httptest.NewServer(http.NotFoundHandler())
				srv.Close()
				return srv.URL
			},
			req:     Request{Model: "m", Messages: userMsgs("hi")},
			wantMsg: "request failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewOllama(tt.url(), nil)
			ch, err := p.Generate(context.Background(), tt.req)
			if err == nil {
				t.Fatal("Generate() error = nil, want error")
			}
			if ch != nil {
				t.Error("channel must be nil when Generate returns an error")
			}
			if tt.wantIs != nil && !errors.Is(err, tt.wantIs) {
				t.Errorf("error %v does not wrap %v", err, tt.wantIs)
			}
			if !strings.Contains(err.Error(), tt.wantMsg) {
				t.Errorf("error %q does not contain %q", err, tt.wantMsg)
			}
		})
	}
}

func TestOllamaGenerateCancel(t *testing.T) {
	handlerDone := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(handlerDone)
		fmt.Fprintln(w, tokenLine("first"))
		w.(http.Flusher).Flush()
		<-r.Context().Done() // stalls until the client goes away
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	p := NewOllama(srv.URL, srv.Client())
	ch, err := p.Generate(ctx, Request{Model: "m", Messages: userMsgs("hi")})
	if err != nil {
		t.Fatal(err)
	}

	select {
	case c := <-ch:
		if c.Text != "first" {
			t.Fatalf("first chunk = %+v, want Text=first", c)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for first chunk")
	}

	cancel()

	// Cancellation is not a failure: the channel closes with no Err chunk.
	for _, c := range drain(t, ch) {
		if c.Err != nil {
			t.Errorf("got Err chunk %v after cancel, want none", c.Err)
		}
	}

	select {
	case <-handlerDone:
	case <-time.After(2 * time.Second):
		t.Fatal("server never saw the client disconnect")
	}
}

func TestOllamaGenerateCancelWithUnreadChunk(t *testing.T) {
	// The consumer stops reading while the provider is blocked on a send;
	// cancelling must still release the goroutine.
	srv := ndjsonServer(t, tokenLine("a"), tokenLine("b"), tokenLine("c"), doneLine)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, err := NewOllama(srv.URL, srv.Client()).Generate(ctx, Request{Model: "m", Messages: userMsgs("hi")})
	if err != nil {
		t.Fatal(err)
	}
	<-ch
	cancel()
	drain(t, ch) // fails the test if the channel never closes
}

func TestOllamaGenerateAlreadyCancelled(t *testing.T) {
	srv := ndjsonServer(t, doneLine)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	ch, err := NewOllama(srv.URL, srv.Client()).Generate(ctx, Request{Model: "m", Messages: userMsgs("hi")})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Generate() error = %v, want context.Canceled", err)
	}
	if ch != nil {
		t.Error("channel must be nil when Generate returns an error")
	}
}

func TestNewOllamaDefaults(t *testing.T) {
	p := NewOllama("", nil)
	if p.baseURL != DefaultOllamaURL {
		t.Errorf("baseURL = %q, want %q", p.baseURL, DefaultOllamaURL)
	}
	if p.client == nil {
		t.Error("client must default to a non-nil http.Client")
	}
	if p.Name() != "ollama" {
		t.Errorf("Name() = %q", p.Name())
	}
}
