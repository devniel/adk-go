// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package bashtool

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestValidateCommand(t *testing.T) {
	cases := []struct {
		name     string
		cmd      string
		prefixes []string
		want     string // substring of rejection reason, or "" for accepted
	}{
		{
			name:     "empty command",
			cmd:      "",
			prefixes: nil,
			want:     "required",
		},
		{
			name:     "whitespace-only command",
			cmd:      "   \t  ",
			prefixes: nil,
			want:     "required",
		},
		{
			name:     "no allowlist allows anything",
			cmd:      "rm -rf /",
			prefixes: nil,
			want:     "",
		},
		{
			name:     "wildcard allows anything",
			cmd:      "cat /etc/passwd",
			prefixes: []string{"*"},
			want:     "",
		},
		{
			name:     "matching prefix",
			cmd:      "git status",
			prefixes: []string{"git ", "go "},
			want:     "",
		},
		{
			name:     "non-matching prefix",
			cmd:      "rm -rf /",
			prefixes: []string{"git ", "go "},
			want:     "blocked",
		},
		{
			name:     "exact prefix (no trailing space)",
			cmd:      "go test",
			prefixes: []string{"go"},
			want:     "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := validateCommand(tc.cmd, Policy{AllowedCommandPrefixes: tc.prefixes})
			if tc.want == "" && got != "" {
				t.Errorf("expected acceptance, got rejection: %q", got)
			}
			if tc.want != "" && !strings.Contains(got, tc.want) {
				t.Errorf("expected rejection containing %q, got %q", tc.want, got)
			}
		})
	}
}

func TestDescribeAllowedPrefixes(t *testing.T) {
	cases := []struct {
		name     string
		prefixes []string
		want     string
	}{
		{"empty", nil, "any command"},
		{"wildcard", []string{"*"}, "any command"},
		{"wildcard mixed", []string{"git ", "*"}, "any command"},
		{"single prefix", []string{"git "}, "commands matching prefixes: git "},
		{"multiple prefixes", []string{"git ", "go "}, "commands matching prefixes: git , go "},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := describeAllowedPrefixes(tc.prefixes)
			if got != tc.want {
				t.Errorf("describeAllowedPrefixes(%v) = %q, want %q", tc.prefixes, got, tc.want)
			}
		})
	}
}

func TestExecuteSuccess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash not available on Windows")
	}
	out, err := execute(Config{}, ".", "echo hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.ReturnCode != 0 {
		t.Errorf("expected exit 0, got %d", out.ReturnCode)
	}
	if !strings.Contains(out.Stdout, "hello") {
		t.Errorf("expected stdout to contain 'hello', got %q", out.Stdout)
	}
	if out.Error != "" {
		t.Errorf("expected no error, got %q", out.Error)
	}
}

func TestExecuteNonZeroExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash not available on Windows")
	}
	out, err := execute(Config{}, ".", "exit 7")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.ReturnCode != 7 {
		t.Errorf("expected exit 7, got %d", out.ReturnCode)
	}
	if out.Error != "" {
		t.Errorf("expected no error (just non-zero exit), got %q", out.Error)
	}
}

func TestExecuteCapturesStderr(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash not available on Windows")
	}
	out, err := execute(Config{}, ".", "echo oops >&2; exit 1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.ReturnCode != 1 {
		t.Errorf("expected exit 1, got %d", out.ReturnCode)
	}
	if !strings.Contains(out.Stderr, "oops") {
		t.Errorf("expected stderr to contain 'oops', got %q", out.Stderr)
	}
}

func TestExecuteWorkspaceDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash not available on Windows")
	}
	tmp := t.TempDir()
	marker := filepath.Join(tmp, "marker.txt")
	if err := os.WriteFile(marker, []byte("found"), 0644); err != nil {
		t.Fatal(err)
	}

	out, err := execute(Config{}, tmp, "cat marker.txt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.ReturnCode != 0 {
		t.Errorf("expected exit 0, got %d (stderr=%q)", out.ReturnCode, out.Stderr)
	}
	if !strings.Contains(out.Stdout, "found") {
		t.Errorf("expected stdout to contain 'found', got %q", out.Stdout)
	}
}

func TestExecuteTimeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash not available on Windows")
	}
	out, err := execute(Config{
		Policy: Policy{TimeoutSeconds: 1},
	}, ".", "sleep 5")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Error == "" || !strings.Contains(out.Error, "timed out") {
		t.Errorf("expected timeout error, got %q", out.Error)
	}
	if out.ReturnCode != -1 {
		t.Errorf("expected ReturnCode -1 on timeout, got %d", out.ReturnCode)
	}
}

func TestExecuteBlockedByPolicy(t *testing.T) {
	out, err := execute(Config{
		Policy: Policy{AllowedCommandPrefixes: []string{"git ", "go "}},
	}, ".", "rm -rf /")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Error == "" || !strings.Contains(out.Error, "blocked") {
		t.Errorf("expected policy rejection, got %q", out.Error)
	}
	// Blocked commands should not run, so stdout/stderr should be empty
	if out.Stdout != "" || out.Stderr != "" {
		t.Errorf("blocked command should not produce output; got stdout=%q stderr=%q", out.Stdout, out.Stderr)
	}
}

func TestNewRejectsEmptyCommand(t *testing.T) {
	// The public New() returns a tool; we can't easily invoke it without
	// constructing a tool.Context. But we can sanity-check validation at
	// the execute level with the same empty-command path.
	out, err := execute(Config{}, ".", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out.Error, "required") {
		t.Errorf("expected 'required' error, got %q", out.Error)
	}
}

func TestNewCreatesTool(t *testing.T) {
	shell, err := New(Config{
		Workspace: ".",
		Policy:    Policy{TimeoutSeconds: 5},
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	if shell == nil {
		t.Fatal("New returned nil tool")
	}
	if shell.Name() != "execute_bash" {
		t.Errorf("expected tool name 'execute_bash', got %q", shell.Name())
	}
}
