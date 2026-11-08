package main

import (
	"errors"
	"fmt"
	"os"
)

// Run checks on the Go source
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(exitCode(err))
	} else {
		fmt.Fprintln(os.Stderr, "src check passed")
	}
}

func run() (retErr error) {
	checks := []srcCheck{
		dockerPackageCmds{},
		configCamelCase{},
		getenvOutsideConfig{},
	}

	src := newSrcFiles()

	for _, c := range checks {
		failed, err := c.Check(src, os.Stderr)
		if err != nil {
			return err
		}

		if failed {
			retErr = errFailed
		}
	}

	return retErr
}

var errFailed = errors.New("failed src check")

func exitCode(err error) int {
	if err == nil {
		return 0
	}

	if errors.Is(err, errFailed) {
		return 1
	}

	return 2
}
