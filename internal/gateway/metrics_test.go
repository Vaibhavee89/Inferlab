package gateway

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Vaibhavee89/inferlab/internal/inference"
	"github.com/Vaibhavee89/inferlab/internal/inference/fake"
)

func scrape(t *testing.T, h http.Handler) string {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("/metrics status = %d", rec.Code)
	}
	return rec.Body.String()
}

// inferenceLog returns the decoded "inference" log record, or nil if none.
func inferenceLog(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("log line is not JSON: %q: %v", line, err)
		}
		if rec["msg"] == "inference" {
			return rec
		}
	}
	return nil
}

func TestChatMetricsAndLog(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		provider   *fake.Provider
		wantStream string
		wantTokens float64
		wantTPOT   bool // tokens >= 2
	}{
		{"non-stream", validBody, &fake.Provider{Tokens: []string{"a", "b", "c"}}, "false", 3, true},
		{"stream", `{"model":"m","stream":true,"messages":[{"role":"user","content":"hi"}]}`,
			&fake.Provider{Tokens: []string{"a", "b", "c", "d"}}, "true", 4, true},
		{"single token", validBody, &fake.Provider{Tokens: []string{"a"}}, "false", 1, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var logs bytes.Buffer
			h := NewRouter(slog.New(slog.NewJSONHandler(&logs, nil)), tt.provider)

			if rec := post(t, h, tt.body); rec.Code != http.StatusOK {
				t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
			}

			out := scrape(t, h)
			lbl := `{model="m",stream="` + tt.wantStream + `"}`
			for _, want := range []string{
				"inferlab_ttft_seconds_count" + lbl + " 1",
				"inferlab_request_duration_seconds_count" + lbl + " 1",
				"inferlab_output_tokens_count" + lbl + " 1",
				`inferlab_requests_total{model="m",status="ok"} 1`,
			} {
				if !strings.Contains(out, want) {
					t.Errorf("/metrics missing %q", want)
				}
			}
			if got := strings.Contains(out, "inferlab_tpot_seconds_count"+lbl+" 1"); got != tt.wantTPOT {
				t.Errorf("TPOT sample present = %v, want %v", got, tt.wantTPOT)
			}

			rec := inferenceLog(t, &logs)
			if rec == nil {
				t.Fatalf("no inference log record in %q", logs.String())
			}
			if rec["model"] != "m" || rec["output_tokens"] != tt.wantTokens {
				t.Errorf("log = %v", rec)
			}
			for _, k := range []string{"ttft_ms", "total_ms", "request_id", "stream"} {
				if _, ok := rec[k]; !ok {
					t.Errorf("log missing %q: %v", k, rec)
				}
			}
			if _, ok := rec["tpot_ms"]; ok != tt.wantTPOT {
				t.Errorf("tpot_ms present = %v, want %v", ok, tt.wantTPOT)
			}
		})
	}
}

func TestChatFailuresSkipHistograms(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		provider *fake.Provider
		wantReq  string
	}{
		{"invalid body", `{}`, &fake.Provider{}, `inferlab_requests_total{model="unknown",status="error"} 1`},
		{"start error", validBody, &fake.Provider{StartErr: inference.ErrModelNotFound}, `inferlab_requests_total{model="unknown",status="error"} 1`},
		{"mid-stream error", validBody, &fake.Provider{Tokens: []string{"a"}, MidStreamErr: errors.New("boom")}, `inferlab_requests_total{model="m",status="error"} 1`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var logs bytes.Buffer
			h := NewRouter(slog.New(slog.NewJSONHandler(&logs, nil)), tt.provider)
			post(t, h, tt.body)

			out := scrape(t, h)
			if !strings.Contains(out, tt.wantReq) {
				t.Errorf("/metrics missing %q", tt.wantReq)
			}
			for _, name := range []string{"ttft_seconds_count", "tpot_seconds_count", "request_duration_seconds_count", "output_tokens_count"} {
				if strings.Contains(out, "inferlab_"+name) {
					t.Errorf("failure recorded a %s sample", name)
				}
			}
			if rec := inferenceLog(t, &logs); rec != nil {
				t.Errorf("unexpected inference log on failure: %v", rec)
			}
		})
	}
}
