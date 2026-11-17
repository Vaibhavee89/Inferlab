// Command bench load-tests an InferLab gateway and reports latency percentiles
// and throughput.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/Vaibhavee89/inferlab/internal/benchmark"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "inferlab-bench:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("bench", flag.ContinueOnError)
	url := fs.String("url", "http://localhost:8080", "gateway base URL")
	model := fs.String("model", "", "model name to request (required)")
	concurrency := fs.Int("concurrency", 4, "number of concurrent workers")
	requests := fs.Int("requests", 20, "total number of requests")
	promptFile := fs.String("prompt-file", "", "file containing the prompt (required)")
	stream := fs.Bool("stream", false, "use SSE streaming (enables TPOT)")
	out := fs.String("out", "benchmarks/results", "directory for the JSON result")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *model == "" {
		return errors.New("--model is required")
	}
	if *promptFile == "" {
		return errors.New("--prompt-file is required")
	}
	if *concurrency < 1 || *requests < 1 {
		return errors.New("--concurrency and --requests must be positive")
	}
	raw, err := os.ReadFile(*promptFile)
	if err != nil {
		return err
	}
	prompt := strings.TrimSpace(string(raw))
	if prompt == "" {
		return fmt.Errorf("prompt file %s is empty", *promptFile)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	res, runErr := benchmark.Run(ctx, benchmark.Config{
		URL: *url, Model: *model, Prompt: prompt,
		Concurrency: *concurrency, Requests: *requests, Stream: *stream,
	})
	if runErr != nil && !errors.Is(runErr, context.Canceled) {
		return runErr
	}
	if len(res.Samples) == 0 {
		return errors.New("interrupted before any request completed")
	}

	if err := benchmark.WriteTable(stdout, res); err != nil {
		return err
	}
	path, err := benchmark.WriteJSON(*out, res)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "\nresults written to %s\n", path)

	if runErr != nil {
		fmt.Fprintf(stdout, "interrupted: %d of %d requests attempted\n", len(res.Samples), res.Requests)
	}
	if res.Summary.Succeeded == 0 {
		return fmt.Errorf("all %d requests failed (first error: %v)", len(res.Samples), firstErr(res))
	}
	return nil
}

func firstErr(r benchmark.Result) error {
	for _, s := range r.Samples {
		if s.Err != nil {
			return s.Err
		}
	}
	return nil
}
