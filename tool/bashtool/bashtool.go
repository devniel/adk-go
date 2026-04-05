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

// Package bashtool provides a tool that executes bash commands in a workspace
// directory. This is the Go port of the Python
// google.adk.tools.bash_tool.ExecuteBashTool.
//
// Usage:
//
//	shell, err := bashtool.New(bashtool.Config{
//	    Workspace: "/home/user/project",
//	    Policy: bashtool.Policy{
//	        AllowedCommandPrefixes: []string{"git", "go", "ls", "cat"},
//	        TimeoutSeconds:         60,
//	    },
//	})
//	// register shell as a tool on an LLMAgent
package bashtool

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
)

// Policy controls what commands the bash tool will execute.
type Policy struct {
	// AllowedCommandPrefixes restricts which commands may run. Each entry is
	// a prefix the raw command string must start with (e.g. "git ", "go ").
	// An empty slice or a single "*" entry allows any command.
	AllowedCommandPrefixes []string

	// TimeoutSeconds is the per-command wall-clock limit. Zero means 30s.
	TimeoutSeconds int

	// WorkingDirectory is the directory commands run in. If empty, the
	// current process working directory is used.
	WorkingDirectory string
}

// Config configures the bash tool.
type Config struct {
	// Workspace is the directory commands execute in. Defaults to ".".
	Workspace string
	// Policy controls command restrictions and resource limits.
	Policy Policy
}

// Input is the LLM-facing argument schema.
type Input struct {
	Command string `json:"command" jsonschema:"The bash command to execute."`
}

// Output is the LLM-facing result schema.
type Output struct {
	Stdout     string `json:"stdout,omitempty"`
	Stderr     string `json:"stderr,omitempty"`
	ReturnCode int    `json:"returncode"`
	Error      string `json:"error,omitempty"`
}

// New creates an ExecuteBash tool with the given config.
func New(cfg Config) (tool.Tool, error) {
	workspace := cfg.Workspace
	if workspace == "" {
		workspace = "."
	}

	allowedHint := describeAllowedPrefixes(cfg.Policy.AllowedCommandPrefixes)
	description := fmt.Sprintf(
		"Executes a bash command with the working directory set to the workspace. Allowed: %s. Returns stdout, stderr, and the exit code.",
		allowedHint,
	)

	handler := func(_ tool.Context, input Input) (Output, error) {
		return execute(cfg, workspace, input.Command)
	}

	t, err := functiontool.New(functiontool.Config{
		Name:        "execute_bash",
		Description: description,
	}, handler)
	if err != nil {
		return nil, fmt.Errorf("bashtool: %w", err)
	}
	return t, nil
}

// describeAllowedPrefixes returns a human-readable hint for the tool description.
func describeAllowedPrefixes(prefixes []string) string {
	if len(prefixes) == 0 {
		return "any command"
	}
	for _, p := range prefixes {
		if p == "*" {
			return "any command"
		}
	}
	return "commands matching prefixes: " + strings.Join(prefixes, ", ")
}

// validateCommand enforces the allowed-prefix policy. Returns an error string
// describing the rejection reason, or the empty string if the command is ok.
func validateCommand(cmd string, policy Policy) string {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return "Command is required."
	}
	// No allowlist = allow all
	if len(policy.AllowedCommandPrefixes) == 0 {
		return ""
	}
	for _, p := range policy.AllowedCommandPrefixes {
		if p == "*" {
			return ""
		}
		if strings.HasPrefix(cmd, p) {
			return ""
		}
	}
	return "Command blocked. Permitted prefixes are: " + strings.Join(policy.AllowedCommandPrefixes, ", ")
}

// execute runs the command under the given policy.
func execute(cfg Config, workspace, command string) (Output, error) {
	if reason := validateCommand(command, cfg.Policy); reason != "" {
		return Output{Error: reason, ReturnCode: -1}, nil
	}

	timeout := time.Duration(cfg.Policy.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "bash", "-c", command)
	cmd.Dir = workspace
	// Start in a new process group so we can kill the whole group on timeout.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	out := Output{
		Stdout: stdout.String(),
		Stderr: stderr.String(),
	}

	if err != nil {
		// Timeout?
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			// Kill the whole process group in case the command forked
			if cmd.Process != nil {
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			}
			out.Error = fmt.Sprintf("Command timed out after %d seconds.", int(timeout.Seconds()))
			out.ReturnCode = -1
			return out, nil
		}
		// Regular exit error with non-zero code
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			out.ReturnCode = exitErr.ExitCode()
			return out, nil
		}
		// Other error (couldn't spawn, etc.)
		out.Error = fmt.Sprintf("Execution failed: %v", err)
		out.ReturnCode = -1
		return out, nil
	}

	return out, nil
}
