package config

import (
	"log/slog"
	"testing"
	"time"
)

func TestLoad(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		want    Config
		wantErr bool
	}{
		{
			name: "defaults",
			env:  map[string]string{},
			want: Config{Addr: ":8080", ShutdownTimeout: 10 * time.Second, LogLevel: slog.LevelInfo},
		},
		{
			name: "empty values fall back to defaults",
			env:  map[string]string{EnvAddr: "", EnvShutdownTimeout: "", EnvLogLevel: ""},
			want: Config{Addr: ":8080", ShutdownTimeout: 10 * time.Second, LogLevel: slog.LevelInfo},
		},
		{
			name: "overrides",
			env: map[string]string{
				EnvAddr:            "127.0.0.1:9000",
				EnvShutdownTimeout: "3s",
				EnvLogLevel:        "debug",
			},
			want: Config{Addr: "127.0.0.1:9000", ShutdownTimeout: 3 * time.Second, LogLevel: slog.LevelDebug},
		},
		{
			name: "log level is case-insensitive",
			env:  map[string]string{EnvLogLevel: "WARN"},
			want: Config{Addr: ":8080", ShutdownTimeout: 10 * time.Second, LogLevel: slog.LevelWarn},
		},
		{name: "invalid duration", env: map[string]string{EnvShutdownTimeout: "soon"}, wantErr: true},
		{name: "zero duration", env: map[string]string{EnvShutdownTimeout: "0s"}, wantErr: true},
		{name: "negative duration", env: map[string]string{EnvShutdownTimeout: "-5s"}, wantErr: true},
		{name: "invalid log level", env: map[string]string{EnvLogLevel: "loud"}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Load(func(k string) string { return tt.env[k] })
			if (err != nil) != tt.wantErr {
				t.Fatalf("Load() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err == nil && got != tt.want {
				t.Fatalf("Load() = %+v, want %+v", got, tt.want)
			}
		})
	}
}
