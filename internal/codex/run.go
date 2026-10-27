package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"slices"
	"syscall"

	"github.com/alexghr/pact/internal/harness"
)

// Run translates Pact's intent and runs the native CLI inside the container.
// Prompted execution replaces this process; interactive execution supervises it.
func Run(ctx context.Context, options harness.Options, instructions string, bind func(context.Context, string) error) error {
	if err := exec.CommandContext(ctx, "codex", "login", "status").Run(); err != nil {
		if ctx.Err() != nil {
			return context.Cause(ctx)
		}
		return errors.New("the host Codex login snapshot is unusable; renew the host's ChatGPT login with Codex CLI, then retry")
	}
	if options.Prompt != "" {
		binary, err := exec.LookPath("codex")
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		return syscall.Exec(binary, append([]string{"codex"}, execArgs(options, instructions)...), os.Environ())
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
	}
	return args
}

func execArgs(options harness.Options, instructions string) []string {
	args := append(configArgs(options, instructions), "exec")
	if options.BackendRef != "" {
		args = append(args, "resume", "--dangerously-bypass-approvals-and-sandbox")
	}
	args = append(args, "--json", "--skip-git-repo-check", "--ignore-user-config", "--")
	if options.BackendRef != "" {
		args = append(args, options.BackendRef)
	}
	return append(args, options.Prompt)
}
