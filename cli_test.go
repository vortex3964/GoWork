package main

import (
	"reflect"
	"testing"
)

func TestParseArgs(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		want    cliOptions
		wantErr bool
	}{
		{
			name: "no arguments keeps current behavior",
			args: nil,
			want: cliOptions{},
		},
		{
			name: "short session",
			args: []string{"-S", "42"},
			want: cliOptions{sessionID: 42},
		},
		{
			name: "long session",
			args: []string{"--session", "42"},
			want: cliOptions{sessionID: 42},
		},
		{
			name: "equals session",
			args: []string{"--session=42"},
			want: cliOptions{sessionID: 42},
		},
		{
			name: "project path",
			args: []string{"/tmp/project"},
			want: cliOptions{root: "/tmp/project"},
		},
		{
			name: "session and project path",
			args: []string{"-S", "7", "/tmp/project"},
			want: cliOptions{sessionID: 7, root: "/tmp/project"},
		},
		{
			name: "help",
			args: []string{"-h"},
			want: cliOptions{showHelp: true},
		},
		{
			name: "version",
			args: []string{"--version"},
			want: cliOptions{showVersion: true},
		},
		{
			name: "double dash allows a dash-prefixed path",
			args: []string{"--", "-project"},
			want: cliOptions{root: "-project"},
		},
		{
			name:    "missing session value",
			args:    []string{"-S"},
			wantErr: true,
		},
		{
			name:    "invalid session id",
			args:    []string{"--session=abc"},
			wantErr: true,
		},
		{
			name:    "zero session id",
			args:    []string{"--session", "0"},
			wantErr: true,
		},
		{
			name:    "unknown flag",
			args:    []string{"--wat"},
			wantErr: true,
		},
		{
			name:    "extra positional argument",
			args:    []string{"/tmp/a", "/tmp/b"},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseArgs(tt.args)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseArgs(%q) = %+v, want error", tt.args, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseArgs(%q) unexpected error: %v", tt.args, err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("parseArgs(%q) = %+v, want %+v", tt.args, got, tt.want)
			}
		})
	}
}
