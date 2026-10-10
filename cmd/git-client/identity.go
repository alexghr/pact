package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// Keep public verification material in the container. Retaining old signers lets
// historical commits verify after the host rotates its key or signing identity.
func refreshIdentity() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	dir := filepath.Join(home, ".config", "pact")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(dir, "git-identity.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	// This helper runs in the Linux container, including on macOS hosts.
	// Serialize refreshes so concurrent Git processes cannot interleave updates.
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	res, err := request(http.MethodGet, "signing-identity", url.Values{}, nil)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	var identity struct {
		Name      string `json:"name"`
		Email     string `json:"email"`
		PublicKey string `json:"publicKey"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 16384)).Decode(&identity); err != nil {
		return fmt.Errorf("decode signing identity: %w", err)
	}
	if identity.Name == "" || strings.ContainsAny(identity.Name, "\x00\r\n<>") {
		return errors.New("invalid signing name")
	}
	// Allowed-signers principals are patterns separated by commas, not arbitrary
	// email text. Refuse syntax that could broaden trust or introduce a new line.
	if identity.Email == "" || strings.ContainsAny(identity.Email, " \t\r\n\x00,*?!\\\"") {
		return errors.New("signing email cannot be represented as a literal SSH principal")
	}
	key := strings.Fields(identity.PublicKey)
	if len(key) < 2 || strings.ContainsAny(identity.PublicKey, "\r\n\x00") {
		return errors.New("invalid signing public key")
	}
	// The first two fields are the key type and data; key[2:] is only a comment.
	publicKey := key[0] + " " + key[1] + "\n"
	entry := identity.Email + " namespaces=\"git\" " + publicKey
	path := filepath.Join(dir, "git-allowed-signers")
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if !strings.Contains("\n"+string(data), "\n"+entry) {
		if len(data) > 0 && data[len(data)-1] != '\n' {
			data = append(data, '\n')
		}
		data = append(data, entry...)
		if err := replaceFile(path, data); err != nil {
			return err
		}
	}
	if err := replaceFile(filepath.Join(dir, "git-signing.pub"), []byte(publicKey)); err != nil {
		return err
	}
	// Git re-reads this command-scope include for each invocation. Updating a
	// global config would not override an existing repository's local identity.
	return writeIdentity(filepath.Join(dir, "git-identity.config"), identity.Name, identity.Email)
}

func writeIdentity(path, name, email string) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".git-config-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if err := file.Close(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, setting := range [][2]string{{"user.name", name}, {"user.email", email}} {
		cmd := exec.CommandContext(ctx, "git", "config", "--file", file.Name(), "--replace-all", "--", setting[0], setting[1])
		cmd.Dir = filepath.Dir(path)
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("set %s: %w: %s", setting[0], err, strings.TrimSpace(string(output)))
		}
	}
	data, err := os.ReadFile(file.Name())
	if err != nil {
		return err
	}
	return replaceFile(path, data)
}

func replaceFile(path string, data []byte) error {
	previous, err := os.ReadFile(path)
	if err == nil && bytes.Equal(previous, data) {
		return nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".git-identity-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}
