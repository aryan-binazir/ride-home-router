package main

import (
	"reflect"
	"strings"
	"testing"
)

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
