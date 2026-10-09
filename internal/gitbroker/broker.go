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
	url              string
	host, port, path string
}

// Broker exposes the public key, allowed remotes, Git-object signing, and two Git wire services.
type Broker struct {
	ssh, keygen, dir      string
	configPath, workspace string
	slots                 chan struct{}
}

// New prepares the host executables without requiring Git credentials yet. Each
// request loads the Git settings. Config, executables and credentials must stay outside
// the agent-writable workspace.
func New(path, workspace string) (*Broker, error) {
	path, err := config.Path(path)
	if err != nil {
		return nil, err
	}
	workspace, err = filepath.EvalSymlinks(workspace)
	if err != nil {
		return nil, err
	}
	b := &Broker{configPath: path, workspace: workspace, slots: make(chan struct{}, 8)}
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
	return b, nil
}

type gitConfig struct {
	config.Git
	remotes []remote
}

func (b *Broker) loadConfig() (gitConfig, error) {
	// Read through a host-owned directory, so the agent cannot replace the
	// config symlink between checking its target and reading it.
	dir, err := outsideWorkspace(filepath.Dir(b.configPath), b.workspace)
	if err != nil {
		return gitConfig{}, fmt.Errorf("Pact config directory: %w", err)
	}
	path := filepath.Join(dir, filepath.Base(b.configPath))
	if _, err := outsideWorkspace(path, b.workspace); err != nil {
		return gitConfig{}, fmt.Errorf("Pact config: %w", err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		return gitConfig{}, err
	}
	git := gitConfig{Git: cfg.Git}
	if git.SSHKey == "" {
		return gitConfig{}, errors.New("git.sshKey is not configured")
	}
	if git.Name == "" || git.Email == "" || strings.ContainsAny(git.Name+git.Email, "\x00\r\n<>") {
		return gitConfig{}, errors.New("Git broker requires a name and email without line breaks or angle brackets")
	}
	git.SSHKey, err = outsideWorkspace(git.SSHKey, b.workspace)
	if err != nil {
		return gitConfig{}, fmt.Errorf("git.sshKey: %w", err)
	}
	for _, target := range git.AllowedRemotes {
		r, err := parseRemote(target)
		if err != nil {
			return gitConfig{}, fmt.Errorf("git.allowedRemotes: %w", err)
		}
		git.remotes = append(git.remotes, r)
	}
	if len(git.remotes) > 0 {
		if git.KnownHosts == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return gitConfig{}, err
			}
			git.KnownHosts = filepath.Join(home, ".ssh", "known_hosts")
		}
		git.KnownHosts, err = outsideWorkspace(git.KnownHosts, b.workspace)
		if err != nil {
			return gitConfig{}, fmt.Errorf("git.knownHosts: %w", err)
		}
	}
	return git, nil
}

func (b *Broker) publicKey(ctx context.Context, key string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	publicKey, err := b.command(ctx, b.keygen, "-y", "-P", "", "-f", key).Output()
	if err != nil {
		return "", errors.New("cannot load git.sshKey (an unencrypted OpenSSH private key is required)")
	}
	return strings.TrimSpace(string(publicKey)), nil
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
	r := remote{url: raw, port: "22"}
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
	op := r.URL.Query().Get("op")
	if r.Method != http.MethodPost && !(r.Method == http.MethodGet && (op == "allowed-remotes" || op == "public-key")) {
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
	cfg, err := b.loadConfig()
	if err != nil {
		http.Error(w, "cannot load Git config: "+err.Error(), http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	switch {
	case r.Method == http.MethodGet && op == "allowed-remotes":
		w.Header().Set("Content-Type", "text/plain")
		for _, remote := range cfg.remotes {
			_, _ = fmt.Fprintln(w, remote.url)
		}
	case r.Method == http.MethodGet && op == "public-key":
		key, err := b.publicKey(r.Context(), cfg.SSHKey)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, key+"\n")
	case r.Method == http.MethodPost && op == "sign":
		b.sign(w, r, cfg)
	case r.Method == http.MethodPost && op == "ssh":
		b.transport(w, r, cfg)
	default:
		http.Error(w, "unknown Git operation", http.StatusBadRequest)
	}
}

func (b *Broker) sign(w http.ResponseWriter, r *http.Request, cfg gitConfig) {
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(15 * time.Second))
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		http.Error(w, "Git signing payload exceeds 1 MiB or is unreadable", http.StatusBadRequest)
		return
	}
	if !validObject(data, cfg.Name+" <"+cfg.Email+">") {
		http.Error(w, "expected a Git commit or tag with the configured committer or tagger", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	cmd := b.command(ctx, b.keygen, "-Y", "sign", "-n", "git", "-f", cfg.SSHKey)
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
// The committer/tagger must match the current identity; object IDs may be SHA-1 or SHA-256.
func validObject(data []byte, identity string) bool {
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
			timestamp, ok := strings.CutPrefix(line, field+identity+" ")
			if !ok || !gitTime.MatchString(timestamp) {
				return false
			}
			found = true
		}
	}
	return found
}

func (b *Broker) transport(w http.ResponseWriter, r *http.Request, cfg gitConfig) {
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
	for i := range cfg.remotes {
		candidate := &cfg.remotes[i]
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
		"-o", "StrictHostKeyChecking=yes", "-o", "UserKnownHostsFile="+strconv.Quote(strings.ReplaceAll(cfg.KnownHosts, "%", "%%")),
		"-o", "GlobalKnownHostsFile=/dev/null", "-o", "ConnectTimeout=15",
		"-o", "ServerAliveInterval=30", "-o", "ServerAliveCountMax=3",
		"-i", cfg.SSHKey, "-p", target.port, target.host, command)
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
