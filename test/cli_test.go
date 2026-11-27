package test

import (
	"reflect"
	"testing"

	gowork "GoWork/internal/gowork"
)

func TestParseArgs(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		want    gowork.CLIOptions
		wantErr bool
	}{
		{
			name: "no arguments keeps current behavior",
			args: nil,
			want: gowork.CLIOptions{},
		},
		{
			name: "short session",
			args: []string{"-S", "42"},
			want: gowork.CLIOptions{SessionID: 42},
		},
		{
			name: "long session",
			args: []string{"--session", "42"},
			want: gowork.CLIOptions{SessionID: 42},
		},
		{
			name: "equals session",
			args: []string{"--session=42"},
			want: gowork.CLIOptions{SessionID: 42},
		},
		{
			name: "project path",
			args: []string{"/tmp/project"},
			want: gowork.CLIOptions{Root: "/tmp/project"},
		},
		{
			name: "session and project path",
			args: []string{"-S", "7", "/tmp/project"},
			want: gowork.CLIOptions{SessionID: 7, Root: "/tmp/project"},
		},
		{
			name: "help",
			args: []string{"-h"},
			want: gowork.CLIOptions{ShowHelp: true},
		},
		{
			name: "version",
			args: []string{"--version"},
			want: gowork.CLIOptions{ShowVersion: true},
		},
		{
			name: "double dash allows a dash-prefixed path",
			args: []string{"--", "-project"},
			want: gowork.CLIOptions{Root: "-project"},
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
			got, err := gowork.ParseArgs(tt.args)
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
