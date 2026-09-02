package main

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"syscall"
	"testing"
)

type fakeApplicationServer struct {
	serveErrors          chan error
	serveErrorOnShutdown error
	shutdownErr          error
	shutdownCalled       bool
}

func (s *fakeApplicationServer) Start() (string, error) {
	return "127.0.0.1:12345", nil
}

func (s *fakeApplicationServer) Errors() <-chan error {
	return s.serveErrors
}

func (s *fakeApplicationServer) Shutdown(context.Context) error {
	s.shutdownCalled = true
	if s.serveErrorOnShutdown != nil {
		s.serveErrors <- s.serveErrorOnShutdown
	}
	return s.shutdownErr
}

func TestServeReturnsServerErrorThatRacesWithSignal(t *testing.T) {
	injectedErr := errors.New("concurrent serve failure")
	server := &fakeApplicationServer{
		serveErrors:          make(chan error, 1),
		serveErrorOnShutdown: injectedErr,
	}
	signals := make(chan os.Signal, 1)
	signals <- syscall.SIGTERM

	err := serve(server, signals)

	if err == nil || !errors.Is(err, injectedErr) {
		t.Fatalf("serve() error = %v, want concurrent serve failure", err)
	}
}

func TestServeJoinsShutdownError(t *testing.T) {
	serveErr := errors.New("serve failure")
	shutdownErr := errors.New("shutdown failure")
	server := &fakeApplicationServer{serveErrors: make(chan error, 1), shutdownErr: shutdownErr}
	server.serveErrors <- serveErr

	err := serve(server, make(chan os.Signal))

	if !errors.Is(err, serveErr) || !errors.Is(err, shutdownErr) {
		t.Fatalf("serve() error = %v, want serve and shutdown failures", err)
	}
}

func TestServeReturnsUnexpectedServerErrorAfterCleanup(t *testing.T) {
	injectedErr := errors.New("injected serve failure")
	server := &fakeApplicationServer{serveErrors: make(chan error, 1)}
	server.serveErrors <- injectedErr

	err := serve(server, make(chan os.Signal))

	if err == nil || !errors.Is(err, injectedErr) {
		t.Fatalf("serve() error = %v, want injected serve failure", err)
	}
	if !server.shutdownCalled {
		t.Fatal("serve() did not shut down after the server failed")
	}
}

func TestServeShutsDownCleanlyOnSignal(t *testing.T) {
	server := &fakeApplicationServer{serveErrors: make(chan error, 1)}
	signals := make(chan os.Signal, 1)
	signals <- syscall.SIGTERM

	if err := serve(server, signals); err != nil {
		t.Fatalf("serve() error = %v, want nil", err)
	}
	if !server.shutdownCalled {
		t.Fatal("serve() did not shut down after the signal")
	}
}

func TestParseArgs(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		port    string
		dbURL   string
		want    options
		wantErr string
	}{
		{name: "missing DATABASE_URL", dbURL: "-", wantErr: "DATABASE_URL"},
		{name: "defaults to loopback 8080", want: options{Addr: "127.0.0.1:8080"}},
		{name: "PORT sets the loopback port", port: "9000", want: options{Addr: "127.0.0.1:9000"}},
		{name: "addr overrides PORT", args: []string{"--addr", "[::1]:7000"}, port: "9000", want: options{Addr: "[::1]:7000"}},
		{
			name: "non-loopback bind with allowed hosts",
			args: []string{"--addr", "0.0.0.0:8080", "--allowed-hosts", "routes.example.com, healthcheck.railway.app"},
			want: options{Addr: "0.0.0.0:8080", AllowedHosts: []string{"routes.example.com", "healthcheck.railway.app"}},
		},
		{name: "non-loopback bind without allowed hosts", args: []string{"--addr", "0.0.0.0:8080"}, wantErr: "--allowed-hosts"},
		{name: "allowed host with port", args: []string{"--allowed-hosts", "routes.example.com:8443"}, wantErr: "bare hostname"},
		{name: "allowed host with scheme", args: []string{"--allowed-hosts", "https://routes.example.com"}, wantErr: "bare hostname"},
		{name: "allowed host with path", args: []string{"--allowed-hosts", "routes.example.com/path"}, wantErr: "bare hostname"},
		{name: "unbalanced IPv6 bracket", args: []string{"--allowed-hosts", "[2001:db8::10"}, wantErr: "bare hostname"},
		{name: "unbracketed IPv6 literal", args: []string{"--allowed-hosts", "2001:db8::10"}, wantErr: "bare hostname"},
		{name: "allowed IPv6 literal", args: []string{"--addr", "0.0.0.0:8080", "--allowed-hosts", "[2001:db8::10]"}, want: options{Addr: "0.0.0.0:8080", AllowedHosts: []string{"[2001:db8::10]"}}},
		{name: "invalid addr", args: []string{"--addr", "nope"}, wantErr: "invalid --addr"},
		{name: "positional args rejected", args: []string{"extra"}, wantErr: "usage"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("PORT", tt.port)
			dbURL := "postgres://test"
			if tt.dbURL == "-" {
				dbURL = ""
			}
			t.Setenv("DATABASE_URL", dbURL)
			t.Setenv("GOOGLE_MAPS_API_KEY", "maps-key")
			if tt.wantErr == "" {
				tt.want.DatabaseURL = dbURL
				tt.want.GoogleMapsAPIKey = "maps-key"
			}
			got, err := parseArgs(tt.args)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("parseArgs() error = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseArgs() error = %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("parseArgs() = %#v, want %#v", got, tt.want)
			}
		})
	}
}
