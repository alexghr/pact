package main

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
)

func parseResumeArgs(args []string) (agentConfig, error) {
	var cfg agentConfig
	if len(args) == 0 {
		return cfg, fmt.Errorf("usage: pact resume SESSION_ID [--prompt TEXT] [--config PATH]")
	}
	cfg.resumeID, _ = strconv.ParseInt(args[0], 10, 64)
	if cfg.resumeID <= 0 {
		return cfg, fmt.Errorf("resume requires a positive session ID")
	}
	flags := flag.NewFlagSet("pact resume", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	flags.StringVar(&cfg.config, "config", "", "Config file (default: current host Pact config)")
	flags.StringVar(&cfg.prompt, "prompt", "", "Continue the saved conversation with a prompt")
	flags.StringVar(&cfg.prompt, "p", "", "Alias for --prompt")
	if err := flags.Parse(args[1:]); err != nil {
		return cfg, err
	}
	if flags.NArg() != 0 {
		return cfg, fmt.Errorf("unexpected argument: %s", flags.Arg(0))
	}
	promptFlagExists := false
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "prompt" || f.Name == "p" {
			promptFlagExists = true
		}
	})
	if promptFlagExists && strings.TrimSpace(cfg.prompt) == "" {
		return cfg, fmt.Errorf("--prompt requires nonempty text")
	}
	return cfg, nil
}
