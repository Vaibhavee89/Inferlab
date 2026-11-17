package benchmark

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Vaibhavee89/inferlab/internal/gateway"
	"github.com/Vaibhavee89/inferlab/internal/inference/fake"
)

func gatewayServer(t *testing.T, p *fake.Provider) *httptest.Server {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(gateway.NewRouter(logger, p))
	t.Cleanup(srv.Close)
	return srv
}

func baseConfig(url string) Config {
	return Config{URL: url, Model: "m", Prompt: "hello", Concurrency: 3, Requests: 9}
}

func TestRunAgainstGateway(t *testing.T) {
	tokens := []string{"a", "b", "c", "d"}
	tests := []struct {
		name       string
		provider   *fake.Provider
		stream     bool
		wantFailed int
		wantTokens int
		wantTPOT   bool
		wantErr    string
	}{
		{name: "stream", provider: &fake.Provider{Tokens: tokens}, stream: true, wantTokens: 4, wantTPOT: true},
		{name: "non-stream", provider: &fake.Provider{Tokens: tokens}, stream: false, wantTokens: 0},
		{name: "mid-stream error", provider: &fake.Provider{Tokens: tokens, MidStreamErr: errors.New("boom")}, stream: true, wantFailed: 9, wantErr: "stream error"},
		{name: "start error non-stream", provider: &fake.Provider{StartErr: errors.New("down")}, stream: false, wantFailed: 9, wantErr: "status 502"},
		{name: "start error stream", provider: &fake.Provider{StartErr: errors.New("down")}, stream: true, wantFailed: 9, wantErr: "status 502"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := gatewayServer(t, tt.provider)
			cfg := baseConfig(srv.URL)
			cfg.Stream = tt.stream
			res, err := Run(context.Background(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			if len(res.Samples) != 9 || res.Summary.Requests != 9 {
				t.Fatalf("samples = %d, summary requests = %d, want 9", len(res.Samples), res.Summary.Requests)
			}
			if res.Summary.Failed != tt.wantFailed {
				t.Fatalf("failed = %d, want %d (first err: %v)", res.Summary.Failed, tt.wantFailed, res.Samples[0].Err)
			}
			for _, s := range res.Samples {
				if tt.wantErr != "" {
					if s.Err == nil || !strings.Contains(s.Err.Error(), tt.wantErr) {
						t.Fatalf("err = %v, want containing %q", s.Err, tt.wantErr)
					}
					continue
				}
				if s.Err != nil {
					t.Fatal(s.Err)
				}
				if s.Tokens != tt.wantTokens || s.HasTPOT != tt.wantTPOT || !s.HasTTFT {
					t.Errorf("sample = %+v, want tokens=%d tpot=%v ttft=true", s, tt.wantTokens, tt.wantTPOT)
				}
			}
		})
	}
}

func TestRunNon2xxEnvelope(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		io.WriteString(w, `{"error":{"message":"no such model","code":"model_not_found"}}`)
	}))
	defer srv.Close()
	res, err := Run(context.Background(), baseConfig(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	if res.Summary.Failed != 9 || !strings.Contains(res.Samples[0].Err.Error(), "no such model") {
		t.Fatalf("got %+v", res.Summary)
	}
}

func TestRunRespectsConcurrency(t *testing.T) {
	var inflight, peak atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := inflight.Add(1)
		defer inflight.Add(-1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		io.WriteString(w, `{}`)
	}))
	defer srv.Close()

	tests := []struct{ concurrency, requests, wantPeak int }{
		{1, 4, 1},
		{3, 9, 3},
		{10, 2, 2}, // never more workers than requests
	}
	for _, tt := range tests {
		peak.Store(0)
		cfg := baseConfig(srv.URL)
		cfg.Concurrency, cfg.Requests = tt.concurrency, tt.requests
		if _, err := Run(context.Background(), cfg); err != nil {
			t.Fatal(err)
		}
		if got := int(peak.Load()); got > tt.concurrency || got < tt.wantPeak-1 {
			t.Errorf("concurrency=%d requests=%d: peak in-flight %d, want about %d", tt.concurrency, tt.requests, got, tt.wantPeak)
		}
	}
}

func TestRunCancel(t *testing.T) {
	before := runtime.NumGoroutine()
	srv := gatewayServer(t, &fake.Provider{Tokens: []string{"a", "b", "c", "d", "e"}, Delay: 50 * time.Millisecond})
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()

	cfg := baseConfig(srv.URL)
	cfg.Concurrency, cfg.Requests, cfg.Stream = 2, 100, true
	res, err := Run(ctx, cfg)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want deadline exceeded", err)
	}
	if len(res.Samples) == 0 || len(res.Samples) >= 100 {
		t.Fatalf("got %d samples, want partial", len(res.Samples))
	}
	srv.Close()
	http.DefaultClient.CloseIdleConnections()
	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > before+2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if after := runtime.NumGoroutine(); after > before+2 {
		t.Errorf("goroutines: before %d, after %d", before, after)
	}
}

func TestRunValidation(t *testing.T) {
	tests := []struct {
		name string
		mut  func(*Config)
	}{
		{"zero concurrency", func(c *Config) { c.Concurrency = 0 }},
		{"zero requests", func(c *Config) { c.Requests = 0 }},
		{"no url", func(c *Config) { c.URL = "" }},
		{"no model", func(c *Config) { c.Model = "" }},
		{"no prompt", func(c *Config) { c.Prompt = "" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := baseConfig("http://x")
			tt.mut(&cfg)
			if _, err := Run(context.Background(), cfg); err == nil {
				t.Fatal("want error")
			}
		})
	}
}

func TestRunRequestBody(t *testing.T) {
	tests := []struct {
		name          string
		maxTokens     int
		unique        bool
		wantMaxTokens float64 // 0 means the field must be absent
		wantUnique    bool
	}{
		{"defaults", 0, false, 0, false},
		{"max tokens", 64, false, 64, false},
		{"unique prompts", 0, true, 0, true},
		{"both", 32, true, 32, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var mu sync.Mutex
			var prompts []string
			var sawMax []any
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					MaxTokens *float64 `json:"max_tokens"`
					Messages  []struct {
						Content string `json:"content"`
					} `json:"messages"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				mu.Lock()
				prompts = append(prompts, body.Messages[0].Content)
				if body.MaxTokens != nil {
					sawMax = append(sawMax, *body.MaxTokens)
				}
				mu.Unlock()
				io.WriteString(w, `{}`)
			}))
			defer srv.Close()

			cfg := baseConfig(srv.URL)
			cfg.MaxTokens, cfg.UniquePrompts = tt.maxTokens, tt.unique
			res, err := Run(context.Background(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			if res.MaxTokens != tt.maxTokens || res.UniquePrompts != tt.unique {
				t.Errorf("result config = %d/%v", res.MaxTokens, res.UniquePrompts)
			}
			if tt.wantMaxTokens == 0 && len(sawMax) != 0 {
				t.Errorf("max_tokens sent when unset: %v", sawMax)
			}
			if tt.wantMaxTokens != 0 && len(sawMax) != cfg.Requests {
				t.Errorf("max_tokens sent %d times, want %d", len(sawMax), cfg.Requests)
			}
			for _, v := range sawMax {
				if v != tt.wantMaxTokens {
					t.Errorf("max_tokens = %v, want %v", v, tt.wantMaxTokens)
				}
			}
			seen := map[string]bool{}
			for _, p := range prompts {
				seen[p] = true
			}
			if tt.wantUnique && len(seen) != cfg.Requests {
				t.Errorf("%d distinct prompts, want %d", len(seen), cfg.Requests)
			}
			if !tt.wantUnique && (len(seen) != 1 || !seen["hello"]) {
				t.Errorf("prompts = %v, want only the base prompt", seen)
			}
		})
	}
}
