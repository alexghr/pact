// Package gitbroker keeps Git's SSH authentication and signing on the host.
// It never runs Git, reads repository configuration, or opens agent-supplied paths.
package gitbroker

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/alexghr/pact/internal/config"
)

const ServerName = "pact_git"

type remote struct {
	host, port, path string
}

// Broker exposes only the public key, Git-object signing, and two Git wire services.
type Broker struct {
	key, knownHosts, ssh, keygen, dir, identity string
	publicKey                                   string
	remotes                                     []remote
	slots                                       chan struct{}
}

// New snapshots trusted configuration before the container starts. Every host
// executable and credential path must be outside the agent-writable workspace.
func New(ctx context.Context, cfg config.Git, workspace string) (*Broker, error) {
	if cfg.Name == "" || cfg.Email == "" || strings.ContainsAny(cfg.Name+cfg.Email, "\x00\r\n<>") {
		return nil, errors.New("Git broker requires a name and email without line breaks or angle brackets")
	}
	workspace, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		return nil, err
	}
	b := &Broker{identity: cfg.Name + " <" + cfg.Email + ">", slots: make(chan struct{}, 8)}
	b.key, err = outsideWorkspace(cfg.SSHKey, workspace)
	if err != nil {
		return nil, fmt.Errorf("git.sshKey: %w", err)
	}
	for _, target := range cfg.AllowedRemotes {
		r, err := parseRemote(target)
		if err != nil {
			return nil, fmt.Errorf("git.allowedRemotes: %w", err)
		}
		b.remotes = append(b.remotes, r)
	}
	if len(b.remotes) > 0 {
		if cfg.KnownHosts == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return nil, err
			}
			cfg.KnownHosts = filepath.Join(home, ".ssh", "known_hosts")
		}
		b.knownHosts, err = outsideWorkspace(cfg.KnownHosts, workspace)
		if err != nil {
			return nil, fmt.Errorf("git.knownHosts: %w", err)
		}
	}
	for name, dest := range map[string]*string{"ssh": &b.ssh, "ssh-keygen": &b.keygen} {
		path, err := exec.LookPath(name)
		if err != nil {
			return nil, err
		}
		*dest, err = outsideWorkspace(path, workspace)
		if err != nil {
			return nil, fmt.Errorf("host %s: %w", name, err)
		}
	}
	// Ignore TMPDIR: it may be inside the mounted workspace.
	b.dir, err = os.MkdirTemp("/tmp", "pact-git-")
	if err != nil {
		return nil, err
	}
	if _, err := outsideWorkspace(b.dir, workspace); err != nil {
		b.Close()
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	publicKey, err := b.command(ctx, b.keygen, "-y", "-P", "", "-f", b.key).Output()
	if err != nil {
		b.Close()
		return nil, errors.New("cannot load git.sshKey (an unencrypted OpenSSH private key is required)")
	}
	b.publicKey = strings.TrimSpace(string(publicKey))
	return b, nil
}

func outsideWorkspace(path, workspace string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(workspace, resolved)
	if err != nil {
		return "", err
	}
	if rel == "." || filepath.IsLocal(rel) {
		return "", errors.New("must be outside the mounted workspace")
	}
	return resolved, nil
}

var (
	safeHost = regexp.MustCompile(`^[a-zA-Z0-9_][a-zA-Z0-9_.-]*@[a-zA-Z0-9][a-zA-Z0-9.-]*$`)
	safePath = regexp.MustCompile(`^/?[a-zA-Z0-9_][a-zA-Z0-9_./-]*$`)
	objectID = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)
	gitTime  = regexp.MustCompile(`^[0-9]+ [+-][0-9]{4}$`)
)

func parseRemote(raw string) (remote, error) {
	r := remote{port: "22"}
	if strings.HasPrefix(raw, "ssh://") {
		u, err := url.Parse(raw)
		if err != nil || u.User == nil || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" {
			return r, errors.New("expected ssh://user@host[:port]/repository or user@host:repository")
		}
		if _, hasPassword := u.User.Password(); hasPassword {
			return r, errors.New("passwords in Git URLs are not allowed")
		}
		r.host = u.User.Username() + "@" + u.Hostname()
		r.path = u.Path
		if u.Port() != "" {
			r.port = u.Port()
		}
	} else {
		var ok bool
		r.host, r.path, ok = strings.Cut(raw, ":")
		if !ok {
			return r, errors.New("only SSH Git URLs are supported")
		}
	}
	port, err := strconv.Atoi(r.port)
	if err != nil || port < 1 || port > 65535 || !safeHost.MatchString(r.host) || !safePath.MatchString(r.path) {
		return r, errors.New("invalid SSH remote; use an explicit user, DNS host, port, and a simple repository path")
	}
	for _, part := range strings.Split(r.path, "/") {
		if part == "." || part == ".." {
			return r, errors.New("Git remote paths cannot contain . or .. components")
		}
	}
	r.port = strconv.Itoa(port)
	return r, nil
}

// No caller environment, SSH agent, Git config, or workspace working directory
// reaches these processes. SSH config (including Match exec) is disabled below.
func (b *Broker) command(ctx context.Context, program string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, program, args...)
	cmd.Dir = b.dir
	cmd.Env = []string{"PATH=/usr/bin:/bin", "LANG=C", "SSH_ASKPASS_REQUIRE=never"}
	cmd.WaitDelay = time.Second
	return cmd
}

func (b *Broker) Close() error { return os.RemoveAll(b.dir) }

func (b *Broker) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Bound slow clients as well as subprocesses. The private bridge is not a
	// permission to hold host resources indefinitely.
	controller := http.NewResponseController(w)
	_ = controller.SetReadDeadline(time.Now().Add(30 * time.Minute))
	_ = controller.SetWriteDeadline(time.Now().Add(time.Minute))
	if r.Method == http.MethodGet && r.URL.Query().Get("op") == "public-key" {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, b.publicKey+"\n")
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	select {
	case b.slots <- struct{}{}:
		defer func() { <-b.slots }()
	default:
		http.Error(w, "Git broker is busy", http.StatusServiceUnavailable)
		return
	}
	switch r.URL.Query().Get("op") {
	case "sign":
		b.sign(w, r)
	case "ssh":
		b.transport(w, r)
	default:
		http.Error(w, "unknown Git operation", http.StatusBadRequest)
	}
}

func (b *Broker) sign(w http.ResponseWriter, r *http.Request) {
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(15 * time.Second))
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		http.Error(w, "Git signing payload exceeds 1 MiB or is unreadable", http.StatusBadRequest)
		return
	}
	if !b.validObject(data) {
		http.Error(w, "expected a Git commit or tag with the configured committer or tagger", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	cmd := b.command(ctx, b.keygen, "-Y", "sign", "-n", "git", "-f", b.key)
	cmd.Stdin = bytes.NewReader(data)
	signature, err := cmd.Output()
	if err != nil {
		http.Error(w, "Git signing failed", http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	_, _ = w.Write(signature)
}

// Domain separation is enforced by ssh-keygen's fixed "git" namespace. This
// additional check binds the signature to the configured committer/tagger while
// allowing a different original author (e.g. cherry-picks).
//
// For example, an unsigned commit payload (including the blank separator) is:
//
//	tree 0123456789012345678901234567890123456789
//	parent 1234567890123456789012345678901234567890
//	author Original Author <author@example.test> 1720000000 +0000
//	committer Pact Agent <agent@example.test> 1720000000 +0000
//
//	Commit message
//
// An unsigned annotated tag payload is:
//
//	object 0123456789012345678901234567890123456789
//	type commit
//	tag v1.0
//	tagger Pact Agent <agent@example.test> 1720000000 +0000
//
//	Tag message
//
// The committer/tagger must match b.identity; object IDs may be SHA-1 or SHA-256.
func (b *Broker) validObject(data []byte) bool {
	header, _, ok := strings.Cut(string(data), "\n\n")
	if !ok || strings.ContainsRune(header, '\x00') {
		return false
	}
	lines := strings.Split(header, "\n")
	kind, oid, _ := strings.Cut(lines[0], " ")
	if !objectID.MatchString(oid) {
		return false
	}
	field := "committer "
	if kind == "object" {
		field = "tagger "
	} else if kind != "tree" {
		return false
	}
	found := false
	for _, line := range lines[1:] {
		if strings.HasPrefix(line, field) {
			if found {
				return false
			}
			timestamp, ok := strings.CutPrefix(line, field+b.identity+" ")
			if !ok || !gitTime.MatchString(timestamp) {
				return false
			}
			found = true
		}
	}
	return found
}

func (b *Broker) transport(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	port := q.Get("port")
	if port == "" {
		port = "22"
	}
	service, quoted, ok := strings.Cut(q.Get("command"), " ")
	if !ok || (service != "git-upload-pack" && service != "git-receive-pack") ||
		len(quoted) < 3 || quoted[0] != '\'' || quoted[len(quoted)-1] != '\'' ||
		!safePath.MatchString(quoted[1:len(quoted)-1]) {
		http.Error(w, "SSH Git command is not allowed; use git-upload-pack or git-receive-pack with a single quoted repository path", http.StatusForbidden)
		return
	}
	path := quoted[1 : len(quoted)-1]
	var target *remote
	for i := range b.remotes {
		candidate := &b.remotes[i]
		if q.Get("host") != candidate.host || port != candidate.port {
			continue
		}
		if path == candidate.path || strings.TrimSuffix(path, ".git") == strings.TrimSuffix(candidate.path, ".git") {
			target = candidate
			break
		}
	}
	if target == nil {
		http.Error(w, fmt.Sprintf("SSH Git remote %q port %q path %q does not match git.allowedRemotes; check the SSH user, host, port, and repository path (including its leading slash)", q.Get("host"), port, path), http.StatusForbidden)
		return
	}
	// Use the configured path even on hosts where aliases are distinct repositories.
	command := service + " '" + target.path + "'"
	if err := http.NewResponseController(w).EnableFullDuplex(); err != nil {
		http.Error(w, "Git transport requires full duplex HTTP", http.StatusInternalServerError)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Minute)
	defer cancel()
	cmd := b.command(ctx, b.ssh, "-F", "/dev/null", "-T",
		"-o", "BatchMode=yes", "-o", "IdentitiesOnly=yes", "-o", "IdentityAgent=none",
		"-o", "StrictHostKeyChecking=yes", "-o", "UserKnownHostsFile="+strconv.Quote(strings.ReplaceAll(b.knownHosts, "%", "%%")),
		"-o", "GlobalKnownHostsFile=/dev/null", "-o", "ConnectTimeout=15",
		"-o", "ServerAliveInterval=30", "-o", "ServerAliveCountMax=3",
		"-i", b.key, "-p", target.port, target.host, command)
	// Git protocol v0 is intentional: no agent-provided environment is sent to SSH.
	stdin, err := cmd.StdinPipe()
	if err != nil {
		http.Error(w, "cannot start Git transport", http.StatusBadGateway)
		return
	}
	stream := &frames{w: w}
	cmd.Stdout, cmd.Stderr = frameWriter{stream, 1}, frameWriter{stream, 2}
	// stdout/stderr pumps can start writing immediately after Start.
	stream.mu.Lock()
	if err := cmd.Start(); err != nil {
		stream.mu.Unlock()
		stdin.Close()
		http.Error(w, "cannot start Git transport", http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/x-pact-git-stream")
	w.Header().Set("Connection", "close")
	w.WriteHeader(http.StatusOK)
	_ = http.NewResponseController(w).Flush()
	stream.mu.Unlock()
	// Own the input pump so Wait does not wait for client EOF after SSH exits.
	inputDone := make(chan struct{})
	go func() {
		defer close(inputDone)
		_, _ = io.Copy(stdin, r.Body)
		_ = stdin.Close()
	}()
	err = cmd.Wait()
	_ = stdin.Close()
	status := byte(0)
	if err != nil {
		status = 1
	}
	_, _ = (frameWriter{stream, 3}).Write([]byte{status})
	// Stop any pending body read before returning ownership to net/http. Git may
	// leave stdin open until it sees our exit frame.
	_ = http.NewResponseController(w).SetReadDeadline(time.Now())
	<-inputDone
}

// Each response frame is channel (1 stdout, 2 stderr, 3 exit), uint32 length,
// then payload. Explicit exit status makes truncation fail closed on the client.
type frames struct {
	mu sync.Mutex
	w  http.ResponseWriter
}

type frameWriter struct {
	stream  *frames
	channel byte
}

func (f frameWriter) Write(p []byte) (int, error) {
	f.stream.mu.Lock()
	defer f.stream.mu.Unlock()
	_ = http.NewResponseController(f.stream.w).SetWriteDeadline(time.Now().Add(time.Minute))
	var header [5]byte
	header[0] = f.channel
	binary.BigEndian.PutUint32(header[1:], uint32(len(p)))
	if _, err := f.stream.w.Write(header[:]); err != nil {
		return 0, err
	}
	n, err := f.stream.w.Write(p)
	if err == nil {
		err = http.NewResponseController(f.stream.w).Flush()
	}
	return n, err
}
