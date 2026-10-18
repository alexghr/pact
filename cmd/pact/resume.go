package main

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/alexghr/pact/internal/state"
)

func parseResumeArgs(args []string) (agentConfig, error) {
	var cfg agentConfig
	if len(args) == 0 {
		return cfg, fmt.Errorf("usage: pact resume SESSION_ID[:THREAD_ID] [--config PATH] [--refresh-auth]")
	}
	sessionID, threadID, combined := strings.Cut(args[0], ":")
	cfg.resumeID, _ = strconv.ParseInt(sessionID, 10, 64)
	if cfg.resumeID <= 0 || (combined && threadID == "") {
		return cfg, fmt.Errorf("resume requires a positive session ID, optionally followed by :THREAD_ID")
	}
	cfg.threadID = threadID
	flags := flag.NewFlagSet("pact resume", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	flags.StringVar(&cfg.config, "config", "", "Config file (default: current host Pact config)")
	flags.BoolVar(&cfg.refreshAuth, "refresh-auth", false, "Replace saved authentication with the current host Codex login")
	if err := flags.Parse(args[1:]); err != nil {
		return cfg, err
	}
	if flags.NArg() != 0 {
		return cfg, fmt.Errorf("unexpected argument: %s", flags.Arg(0))
	}
	return cfg, nil
}

func exactThread(threads []state.Thread, id string) (state.Thread, error) {
	for _, thread := range threads {
		if thread.ID != id {
			continue
		}
		if thread.Missing {
			return state.Thread{}, fmt.Errorf("thread %s is missing from native history", id)
		}
		if thread.Archived {
			return state.Thread{}, fmt.Errorf("thread %s is archived", id)
		}
		if thread.Internal {
			return state.Thread{}, fmt.Errorf("thread %s belongs to an internal subagent", id)
		}
		return thread, nil
	}
	return state.Thread{}, fmt.Errorf("thread %s does not belong to this session", id)
}
