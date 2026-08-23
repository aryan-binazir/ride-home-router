package main

import (
	"strings"
	"testing"
)

func TestValidateServerAddr(t *testing.T) {
	tests := []struct {
		name          string
		addr          string
		allowNonlocal bool
		wantErr       bool
	}{
		{name: "IPv4 loopback", addr: "127.0.0.1:8080"},
		{name: "IPv6 loopback", addr: "[::1]:8080"},
		{name: "localhost", addr: "localhost:8080"},
		{name: "LAN address", addr: "192.168.1.20:8080", wantErr: true},
		{name: "all interfaces", addr: ":8080", wantErr: true},
		{name: "explicit nonlocal opt-in", addr: "0.0.0.0:8080", allowNonlocal: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateServerAddr(tt.addr, tt.allowNonlocal)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateServerAddr() error = %v, wantErr = %v", err, tt.wantErr)
			}
			if tt.wantErr && !strings.Contains(err.Error(), "SERVER_ALLOW_NONLOCAL=1") {
				t.Fatalf("error = %q, want explicit opt-in guidance", err)
			}
		})
	}
}
