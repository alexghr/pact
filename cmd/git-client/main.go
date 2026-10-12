// pact-git-client is installed under two names: pact-git-ssh (Git's SSH
// transport) and pact-git-sign (Git's gpg.ssh.program). It holds no credentials.
package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func request(method, operation string, params url.Values, body io.Reader) (*http.Response, error) {
	endpoint, err := url.Parse(os.Getenv("PACT_GIT_URL"))
	if err != nil || endpoint.Scheme != "http" || endpoint.Host == "" {
		return nil, errors.New("PACT_GIT_URL is missing or invalid")
	}
	params.Set("op", operation)
	endpoint.RawQuery = params.Encode()
	req, err := http.NewRequest(method, endpoint.String(), body)
	if err != nil {
		return nil, err
	}
	// Never route this private bridge through HTTP_PROXY, or follow redirects.
	client := &http.Client{
		Timeout:       30 * time.Minute,
		Transport:     &http.Transport{DisableKeepAlives: true, ResponseHeaderTimeout: 30 * time.Second},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	res, err := client.Do(req)
	if err != nil {
		return nil, errors.New("cannot reach Pact's host Git broker")
	}
	if res.StatusCode != http.StatusOK {
		defer res.Body.Close()
		message, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		return nil, fmt.Errorf("Git broker: %s: %s", res.Status, strings.TrimSpace(string(message)))
	}
	return res, nil
}

func ssh(args []string) error {
	if len(args) == 1 && args[0] == "--allowed-remotes" {
		res, err := request(http.MethodGet, "allowed-remotes", url.Values{}, nil)
		if err != nil {
			return err
		}
		defer res.Body.Close()
		_, err = io.Copy(os.Stdout, res.Body)
		return err
	}
	params := url.Values{}
	for len(args) > 0 && strings.HasPrefix(args[0], "-") {
		if len(args) < 2 {
			return errors.New("unsupported SSH arguments")
		}
		switch args[0] {
		case "-p":
			params.Set("port", args[1])
		case "-o":
			if args[1] != "SendEnv=GIT_PROTOCOL" {
				return errors.New("unsupported SSH option")
			}
		default:
			return errors.New("only Git SSH transport is supported")
		}
		args = args[2:]
	}
	if len(args) != 2 {
		return errors.New("expected Git SSH destination and service")
	}
	params.Set("host", args[0])
	params.Set("command", args[1])
	res, err := request(http.MethodPost, "ssh", params, os.Stdin)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	// The broker multiplexes SSH output into frames: one channel byte
	// (1 = stdout, 2 = stderr, 3 = exit status), then a big-endian uint32
	// payload length. Exit frames contain one status byte; e.g. 03 00000001 00
	// means success. See frameWriter in internal/gitbroker for the encoder.
	for {
		var header [5]byte
		if _, err := io.ReadFull(res.Body, header[:]); err != nil {
			return fmt.Errorf("Git transport ended without exit status: %w", err)
		}
		size := binary.BigEndian.Uint32(header[1:])
		if size > 1<<20 {
			return errors.New("oversized Git transport frame")
		}
		switch header[0] {
		case 1, 2:
			out := os.Stdout
			if header[0] == 2 {
				out = os.Stderr
			}
			if _, err := io.CopyN(out, res.Body, int64(size)); err != nil {
				return err
			}
		case 3:
			var status [1]byte
			if size != 1 {
				return errors.New("invalid Git transport exit status")
			}
			if _, err := io.ReadFull(res.Body, status[:]); err != nil {
				return err
			}
			if status[0] != 0 {
				return errors.New("host Git SSH transport failed")
			}
			return nil
		default:
			return errors.New("invalid Git transport frame")
		}
	}
}

func sign(args []string) error {
	if len(args) == 1 && args[0] == "--refresh" {
		return refreshIdentity()
	}
	// Verification stays local and never needs a private key.
	if len(args) >= 2 && args[0] == "-Y" && (args[1] == "find-principals" || args[1] == "verify" || args[1] == "check-novalidate") {
		if err := refreshIdentity(); err != nil {
			fmt.Fprintln(os.Stderr, "pact-git: cannot refresh verification keys; using cached keys:", err)
		}
		cmd := exec.Command("ssh-keygen", args...)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		return cmd.Run()
	}
	// Git invokes ssh-keygen -Y sign -n git -f <public key> <temporary object>.
	if len(args) != 7 || args[0] != "-Y" || args[1] != "sign" || args[2] != "-n" || args[3] != "git" || args[4] != "-f" {
		return errors.New("only Git object signing and verification are supported")
	}
	if err := refreshIdentity(); err != nil {
		return err
	}
	data, err := os.Open(args[6])
	if err != nil {
		return err
	}
	defer data.Close()
	res, err := request(http.MethodPost, "sign", url.Values{}, data)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	signature, err := io.ReadAll(io.LimitReader(res.Body, 16384))
	if err != nil {
		return err
	}
	return os.WriteFile(args[6]+".sig", signature, 0600)
}

func main() {
	var err error
	if filepath.Base(os.Args[0]) == "pact-git-ssh" {
		err = ssh(os.Args[1:])
	} else {
		err = sign(os.Args[1:])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "pact-git:", err)
		os.Exit(1)
	}
	// SSH's HTTP request can still have an input pump waiting on Git's stdin.
	// Process exit closes it even when Git keeps stdin open until SSH exits.
	os.Exit(0)
}
