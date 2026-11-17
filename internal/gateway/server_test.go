package gateway

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Vaibhavee89/inferlab/internal/inference/fake"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestRouter(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		path       string
		wantStatus int
		wantBody   map[string]string
	}{
		{"healthz ok", http.MethodGet, "/healthz", http.StatusOK, map[string]string{"status": "ok"}},
		{"unknown route", http.MethodGet, "/nope", http.StatusNotFound, nil},
		{"healthz wrong method", http.MethodPost, "/healthz", http.StatusMethodNotAllowed, nil},
	}

	h := NewRouter(discardLogger(), &fake.Provider{})
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(tt.method, tt.path, nil))

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if tt.wantBody == nil {
				return
			}
			if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
				t.Errorf("Content-Type = %q, want application/json", ct)
			}
			var got map[string]string
			if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			for k, v := range tt.wantBody {
				if got[k] != v {
					t.Errorf("body[%q] = %q, want %q", k, got[k], v)
				}
			}
		})
	}
}

func TestServeGracefulShutdown(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()

	srv := NewServer(addr, 2*time.Second, NewRouter(discardLogger(), &fake.Provider{}), discardLogger())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx, ln) }()

	resp, err := http.Get("http://" + addr + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve() = %v, want nil after clean shutdown", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return after context cancel")
	}

	client := &http.Client{Timeout: time.Second}
	if resp, err := client.Get("http://" + addr + "/healthz"); err == nil {
		resp.Body.Close()
		t.Fatal("server still accepting connections after shutdown")
	}
}

func TestServeWaitsForInFlightRequests(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	slow := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		w.WriteHeader(http.StatusOK)
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := NewServer(ln.Addr().String(), 5*time.Second, slow, discardLogger())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx, ln) }()

	statusCh := make(chan int, 1)
	go func() {
		resp, err := http.Get("http://" + ln.Addr().String() + "/")
		if err != nil {
			statusCh <- -1
			return
		}
		resp.Body.Close()
		statusCh <- resp.StatusCode
	}()

	<-started
	cancel()

	select {
	case <-done:
		t.Fatal("Serve returned while a request was still in flight")
	case <-time.After(200 * time.Millisecond):
	}

	close(release)
	if status := <-statusCh; status != http.StatusOK {
		t.Fatalf("in-flight request status = %d, want 200", status)
	}
	if err := <-done; err != nil {
		t.Fatalf("Serve() = %v, want nil", err)
	}
}

func TestRunListenError(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	// Address is already taken, so Run must fail fast rather than block.
	srv := NewServer(ln.Addr().String(), time.Second, NewRouter(discardLogger(), &fake.Provider{}), discardLogger())
	if err := srv.Run(context.Background()); err == nil {
		t.Fatal("Run() = nil, want listen error")
	}
}
