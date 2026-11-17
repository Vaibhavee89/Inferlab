package benchmark

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Vaibhavee89/inferlab/internal/observability"
)

const chatPath = "/v1/chat/completions"

// Config describes one benchmark run.
type Config struct {
	// URL is the gateway base URL, e.g. http://localhost:8080.
	URL         string
	Model       string
	Prompt      string
	Concurrency int
	Requests    int
	Stream      bool
	// MaxTokens caps output length per request; 0 leaves it to the server.
	// Fix it when comparing prompt sizes so decode work stays constant.
	MaxTokens int
	// UniquePrompts prefixes each request with a distinct nonce so backends
	// cannot reuse a cached prompt prefix, keeping prefill cost real.
	UniquePrompts bool
	// Client defaults to http.DefaultClient.
	Client *http.Client
	// Now is the clock; nil means time.Now.
	Now func() time.Time
}

// Result is the outcome of a run. Samples holds only requests that were
// attempted, in dispatch order.
type Result struct {
	URL         string
	Model       string
	Concurrency int
	Requests    int
	Stream      bool
	MaxTokens   int
	// UniquePrompts records whether requests carried a cache-busting nonce.
	UniquePrompts bool
	StartedAt     time.Time
	Samples       []Sample
	Summary       Summary
}

// Run fires cfg.Requests requests through cfg.Concurrency workers. If ctx is
// cancelled it stops dispatching, waits for in-flight requests, and returns the
// partial Result together with ctx.Err().
func Run(ctx context.Context, cfg Config) (Result, error) {
	if cfg.Concurrency < 1 || cfg.Requests < 1 {
		return Result{}, errors.New("concurrency and requests must be positive")
	}
	if cfg.URL == "" || cfg.Model == "" || cfg.Prompt == "" {
		return Result{}, errors.New("url, model and prompt are required")
	}
	if cfg.Client == nil {
		cfg.Client = http.DefaultClient
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	endpoint := strings.TrimRight(cfg.URL, "/") + chatPath

	samples := make([]Sample, cfg.Requests)
	attempted := make([]bool, cfg.Requests)
	jobs := make(chan int)

	start := now()
	var wg sync.WaitGroup
	for w := 0; w < min(cfg.Concurrency, cfg.Requests); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				body, err := requestBody(cfg, i)
				if err != nil {
					samples[i] = Sample{Err: err}
				} else {
					samples[i] = doRequest(ctx, cfg.Client, endpoint, body, cfg.Stream, now)
				}
				attempted[i] = true
			}
		}()
	}
dispatch:
	for i := 0; i < cfg.Requests; i++ {
		select {
		case jobs <- i:
		case <-ctx.Done():
			break dispatch
		}
	}
	close(jobs)
	wg.Wait()
	wall := now().Sub(start)

	done := make([]Sample, 0, cfg.Requests)
	for i, ok := range attempted {
		if ok {
			done = append(done, samples[i])
		}
	}
	res := Result{
		URL: cfg.URL, Model: cfg.Model, Concurrency: cfg.Concurrency,
		Requests: cfg.Requests, Stream: cfg.Stream, MaxTokens: cfg.MaxTokens,
		UniquePrompts: cfg.UniquePrompts, StartedAt: start.UTC(),
		Samples: done, Summary: Summarize(done, wall),
	}
	return res, ctx.Err()
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model     string        `json:"model"`
	Messages  []chatMessage `json:"messages"`
	Stream    bool          `json:"stream"`
	MaxTokens int           `json:"max_tokens,omitempty"`
}

// requestBody builds the JSON body for request i. With UniquePrompts the
// nonce goes at the start so no prefix is shared between requests.
func requestBody(cfg Config, i int) ([]byte, error) {
	prompt := cfg.Prompt
	if cfg.UniquePrompts {
		prompt = fmt.Sprintf("[run %d] %s", i, prompt)
	}
	return json.Marshal(chatRequest{
		Model:     cfg.Model,
		Messages:  []chatMessage{{Role: "user", Content: prompt}},
		Stream:    cfg.Stream,
		MaxTokens: cfg.MaxTokens,
	})
}

type apiError struct {
	Message string `json:"message"`
}

// chunk decodes both SSE chunks and error events.
type chunk struct {
	Error   *apiError `json:"error"`
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
	} `json:"choices"`
}

func doRequest(ctx context.Context, client *http.Client, endpoint string, body []byte, stream bool, now func() time.Time) Sample {
	tr := observability.NewTracker(now)
	fail := func(err error) Sample {
		return Sample{Latency: tr.Finish().Total(), Err: err}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fail(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return fail(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fail(statusError(resp))
	}

	if stream {
		if err := readStream(resp.Body, tr); err != nil {
			return fail(err)
		}
	} else {
		// Non-stream responses carry no token timing or usage: the whole
		// body is one "token" so TTFT equals latency.
		if _, err := io.Copy(io.Discard, resp.Body); err != nil {
			return fail(err)
		}
		tr.OnToken()
	}
	return sampleFrom(tr.Finish(), stream)
}

func statusError(resp *http.Response) error {
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	var env struct {
		Error apiError `json:"error"`
	}
	if json.Unmarshal(raw, &env) == nil && env.Error.Message != "" {
		return fmt.Errorf("status %d: %s", resp.StatusCode, env.Error.Message)
	}
	return fmt.Errorf("status %d", resp.StatusCode)
}

// readStream consumes an SSE body, calling tr.OnToken for each non-empty
// content delta. It fails on an in-band error event or a missing [DONE].
func readStream(r io.Reader, tr *observability.Tracker) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		payload, ok := strings.CutPrefix(sc.Text(), "data: ")
		if !ok {
			continue
		}
		if payload == "[DONE]" {
			return nil
		}
		var c chunk
		if err := json.Unmarshal([]byte(payload), &c); err != nil {
			return fmt.Errorf("bad stream chunk: %w", err)
		}
		if c.Error != nil {
			return fmt.Errorf("stream error: %s", c.Error.Message)
		}
		if len(c.Choices) > 0 && c.Choices[0].Delta.Content != "" {
			tr.OnToken()
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	return errors.New("stream ended without [DONE]")
}

func sampleFrom(t observability.Timing, stream bool) Sample {
	s := Sample{Latency: t.Total(), Tokens: t.Tokens}
	s.TTFT, s.HasTTFT = t.TTFT()
	if stream {
		s.TPOT, s.HasTPOT = t.TPOT()
	} else {
		// The single OnToken above only marks TTFT; it is not a real token.
		s.Tokens = 0
	}
	return s
}
