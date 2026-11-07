package main

import (
	"errors"
	"fmt"
	"io"
	"os"
)

type srcCheck interface {
	Check(io.Writer) (bool, error)
}

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
	}

	for _, c := range checks {
		failed, err := c.Check(os.Stderr)
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
