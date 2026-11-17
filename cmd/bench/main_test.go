package main

import (
	"bytes"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Vaibhavee89/inferlab/internal/gateway"
	"github.com/Vaibhavee89/inferlab/internal/inference/fake"
)

func TestRunFlagErrors(t *testing.T) {
	dir := t.TempDir()
	empty := filepath.Join(dir, "empty.txt")
	if err := os.WriteFile(empty, []byte("  \n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"no model", []string{"--prompt-file", empty}, "--model is required"},
		{"no prompt file", []string{"--model", "m"}, "--prompt-file is required"},
		{"bad concurrency", []string{"--model", "m", "--prompt-file", empty, "--concurrency", "0"}, "must be positive"},
		{"missing file", []string{"--model", "m", "--prompt-file", filepath.Join(dir, "nope")}, "no such file"},
		{"empty prompt", []string{"--model", "m", "--prompt-file", empty}, "is empty"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := run(tt.args, io.Discard)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func TestRunEndToEnd(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(gateway.NewRouter(logger, &fake.Provider{Tokens: []string{"a", "b", "c"}}))
	defer srv.Close()

	dir := t.TempDir()
	prompt := filepath.Join(dir, "p.txt")
	if err := os.WriteFile(prompt, []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "results")

	var buf bytes.Buffer
	args := []string{"--url", srv.URL, "--model", "m", "--prompt-file", prompt,
		"--concurrency", "2", "--requests", "6", "--stream", "--out", out}
	if err := run(args, &buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "P99") {
		t.Errorf("table missing:\n%s", buf.String())
	}
	files, err := filepath.Glob(filepath.Join(out, "bench-*.json"))
	if err != nil || len(files) != 1 {
		t.Fatalf("result files = %v, err = %v", files, err)
	}
}

func TestRunAllFail(t *testing.T) {
	dir := t.TempDir()
	prompt := filepath.Join(dir, "p.txt")
	if err := os.WriteFile(prompt, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Nothing listens on port 1.
	args := []string{"--url", "http://127.0.0.1:1", "--model", "m", "--prompt-file", prompt,
		"--requests", "2", "--out", filepath.Join(dir, "r")}
	err := run(args, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "all 2 requests failed") {
		t.Fatalf("err = %v", err)
	}
}
