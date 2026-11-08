// Package codex adapts Pact runs to native authentication, invocation, and protocols.
package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os/exec"
	"slices"

	"github.com/alexghr/pact/internal/harness"
)

// Run translates Pact's intent and runs the native CLI inside the container.
func Run(ctx context.Context, options harness.Options, instructions string, bind func(context.Context, string) error) error {
	if err := exec.CommandContext(ctx, "codex", "login", "status").Run(); err != nil {
		if ctx.Err() != nil {
			return context.Cause(ctx)
		}
		return errors.New("the host Codex login snapshot is unusable; renew the host's ChatGPT login with Codex CLI, then retry")
	}
	server := configArgs(options, instructions)
	native := slices.Clone(server)
	if options.BackendRef != "" {
		native = []string{"resume", "--", options.BackendRef}
	}
	return runInteractive(ctx, server, native, options.BackendRef, bind)
}

func configArgs(options harness.Options, instructions string) []string {
	// JSON strings are also valid TOML basic strings, including embedded newlines.
	quote := func(value string) string {
		encoded, _ := json.Marshal(value)
		return string(encoded)
	}
	args := []string{
		"--config", `approval_policy="never"`,
		"--config", "model=" + quote(options.Model),
		"--config", `sandbox_mode="danger-full-access"`,
		"--config", "developer_instructions=" + quote(instructions),
	}
	if options.Effort != "" {
		args = append(args, "--config", "model_reasoning_effort="+quote(options.Effort))
	}
	for _, name := range slices.Sorted(maps.Keys(options.MCP)) {
		args = append(args, "--config", fmt.Sprintf("mcp_servers.%s={url=%s}", name, quote(options.MCP[name])))
		if options.MCPApprovalTimeoutSeconds > 0 {
			// Leave Codex's usual 60 seconds for the upstream after user approval.
			args = append(args, "--config", fmt.Sprintf("mcp_servers.%s.tool_timeout_sec=%d", name, options.MCPApprovalTimeoutSeconds+60))
		}
	}
	return args
}
