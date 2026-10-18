package main

import (
	"slices"
	"strings"
	"testing"
)

func TestParseArgs(t *testing.T) {
	tests := []struct {
		args    []string
		want    invocation
		wantErr string
	}{
		{args: nil, want: invocation{cmd: "tui"}},
		{args: []string{"-debug"}, want: invocation{cmd: "tui", debug: true}},
		{args: []string{"login"}, want: invocation{cmd: "login"}},
		{args: []string{"login", "+15551234567"}, want: invocation{cmd: "login", args: []string{"+15551234567"}}},
		{args: []string{"-debug", "login", "+15551234567"}, want: invocation{cmd: "login", debug: true, args: []string{"+15551234567"}}},
		{args: []string{"login", "-debug", "+15551234567"}, want: invocation{cmd: "login", debug: true, args: []string{"+15551234567"}}},
		{args: []string{"-debug", "listen"}, want: invocation{cmd: "listen", debug: true}},
		{args: []string{"frob"}, wantErr: `unknown command "frob"`},
		{args: []string{"listen", "extra"}, wantErr: "unexpected arguments"},
		{args: []string{"login", "+1", "555"}, wantErr: "unexpected arguments"},
	}
	for _, tt := range tests {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			got, err := parseArgs(tt.args)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.cmd != tt.want.cmd || got.debug != tt.want.debug || !slices.Equal(got.args, tt.want.args) {
				t.Errorf("parseArgs(%q) = %+v, want %+v", tt.args, got, tt.want)
			}
		})
	}
}
