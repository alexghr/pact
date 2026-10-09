package gitbroker

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/alexghr/pact/internal/config"
	"github.com/alexghr/pact/internal/mcphost"
	"github.com/alexghr/pact/internal/nix"
	"github.com/alexghr/pact/mcpproxy"
)

func runCommand(t *testing.T, dir string, env []string, program string, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, program, args...)
	cmd.Dir, cmd.Env = dir, env
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", program, args, err, output)
	}
	return strings.TrimSpace(string(output))
}

func writeFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func writeConfig(t *testing.T, path string, cfg config.Git) {
	t.Helper()
	data, err := json.Marshal(config.Config{Git: cfg})
	if err != nil {
		t.Fatal(err)
	}
	// Editors commonly replace the file on save; reloads must follow the path.
	writeFile(t, path+".tmp", string(data), 0600)
	if err := os.Rename(path+".tmp", path); err != nil {
		t.Fatal(err)
	}
}

func testBroker(t *testing.T, remotes ...string) (*Broker, config.Git, string) {
	t.Helper()
	host, workspace := t.TempDir(), t.TempDir()
	key := filepath.Join(host, "identity")
	runCommand(t, host, nil, "ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", key)
	cfg := config.Git{Name: "Pact Agent", Email: "agent@example.test", SSHKey: key, AllowedRemotes: remotes}
	cfg.KnownHosts = filepath.Join(host, "known_hosts")
	writeFile(t, cfg.KnownHosts, "", 0600)
	path := filepath.Join(host, "config.json")
	writeConfig(t, path, cfg)
	b, err := New(path, workspace)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close() })
	return b, cfg, workspace
}

func TestGitWorkflow(t *testing.T) {
	for _, suffix := range []string{"", ".git"} {
		t.Run("configured-suffix="+suffix, func(t *testing.T) { testGitWorkflow(t, suffix) })
	}
}

func TestAllowedRemotes(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "pact-git-ssh")
	runCommand(t, "../..", nil, "go", "build", "-o", bin, "./cmd/git-client")
	b, cfg, _ := testBroker(t)
	server := httptest.NewServer(b)
	defer server.Close()
	env := []string{"PACT_GIT_URL=" + server.URL}
	for _, remotes := range [][]string{nil, {"git@forge.test:team/repo.git", "ssh://git@forge.test:2222/team/other"}, nil} {
		t.Run(fmt.Sprintf("remotes=%d", len(remotes)), func(t *testing.T) {
			cfg.AllowedRemotes = remotes
			writeConfig(t, b.configPath, cfg)
			cmd := exec.CommandContext(t.Context(), bin, "--allowed-remotes")
			cmd.Env = env
			output, err := cmd.CombinedOutput()
			want := ""
			for _, remote := range remotes {
				want += remote + "\n"
			}
			if err != nil || string(output) != want {
				t.Fatalf("allowed remotes: %v, got %q, want %q", err, output, want)
			}
		})
	}
	server.Close()
	cmd := exec.CommandContext(t.Context(), bin, "--allowed-remotes")
	cmd.Env = env
	if output, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(output), "cannot reach") {
		t.Fatalf("unavailable broker must fail, not report an empty list: %v, %s", err, output)
	}
}

func TestConfigReloadFailsClosed(t *testing.T) {
	b, cfg, workspace := testBroker(t, "git@forge.test:team/repo")
	marker := filepath.Join(filepath.Dir(cfg.SSHKey), "executed")
	b.ssh = filepath.Join(filepath.Dir(cfg.SSHKey), "trap")
	writeFile(t, b.ssh, "#!/bin/sh\nprintf executed > '"+marker+"'\n", 0700)
	b.keygen = b.ssh
	server := httptest.NewServer(b)
	defer server.Close()
	workspaceConfig := filepath.Join(workspace, "config.json")
	writeConfig(t, workspaceConfig, cfg)
	configAlias := filepath.Join(workspace, "config-link.json")
	if err := os.Symlink(b.configPath, configAlias); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{workspaceConfig, configAlias} {
		broker, err := New(path, workspace)
		if err != nil {
			t.Fatal(err)
		}
		res := httptest.NewRecorder()
		broker.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/?op=public-key", nil))
		broker.Close()
		if res.Code != http.StatusServiceUnavailable {
			t.Fatal("read a config path inside the workspace")
		}
	}
	workspaceHosts := filepath.Join(workspace, "known_hosts")
	writeFile(t, workspaceHosts, "", 0600)
	original := cfg
	for _, scenario := range []string{"invalid JSON", "invalid remote", "missing Git config", "invalid identity", "missing key", "missing config", "workspace config", "workspace known hosts", "workspace key"} {
		t.Run(scenario, func(t *testing.T) {
			cfg = original
			writeConfig(t, b.configPath, cfg)
			switch scenario {
			case "invalid JSON":
				writeFile(t, b.configPath, "{", 0600)
			case "invalid remote":
				cfg.AllowedRemotes = append(cfg.AllowedRemotes, "https://forge.test/team/other")
				writeConfig(t, b.configPath, cfg)
			case "missing Git config":
				writeFile(t, b.configPath, "{}", 0600)
			case "invalid identity":
				cfg.Name = "Invalid\nName"
				writeConfig(t, b.configPath, cfg)
			case "missing key":
				cfg.SSHKey += ".missing"
				writeConfig(t, b.configPath, cfg)
			case "missing config", "workspace config":
				if err := os.Remove(b.configPath); err != nil {
					t.Fatal(err)
				}
				if scenario == "workspace config" {
					if err := os.Symlink(workspaceConfig, b.configPath); err != nil {
						t.Fatal(err)
					}
				}
			case "workspace known hosts", "workspace key":
				alias := filepath.Join(t.TempDir(), "workspace-link")
				if err := os.Symlink(workspaceHosts, alias); err != nil {
					t.Fatal(err)
				}
				if scenario == "workspace key" {
					cfg.SSHKey = alias
				} else {
					cfg.KnownHosts = alias
				}
				writeConfig(t, b.configPath, cfg)
			}
			for _, op := range []string{"allowed-remotes", "public-key", "sign", "ssh"} {
				q := url.Values{"op": {op}, "host": {"git@forge.test"}, "command": {"git-upload-pack 'team/repo'"}}
				method := http.MethodGet
				if op == "ssh" || op == "sign" {
					method = http.MethodPost
				}
				req, err := http.NewRequestWithContext(t.Context(), method, server.URL+"?"+q.Encode(), nil)
				if err != nil {
					t.Fatal(err)
				}
				res, err := http.DefaultClient.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				res.Body.Close()
				if res.StatusCode != http.StatusServiceUnavailable {
					t.Fatalf("%s with %s: got %s", op, scenario, res.Status)
				}
			}
		})
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("invalid config started a host executable")
	}
}

func testGitWorkflow(t *testing.T, suffix string) {
	otherSuffix := ".git"
	if suffix == ".git" {
		otherSuffix = ""
	}
	b, cfg, workspace := testBroker(t)
	host := filepath.Dir(cfg.SSHKey)
	bin := t.TempDir()
	runCommand(t, "../..", nil, "go", "build", "-o", filepath.Join(bin, "pact-git-sign"), "./cmd/git-client")
	if err := os.Symlink("pact-git-sign", filepath.Join(bin, "pact-git-ssh")); err != nil {
		t.Fatal(err)
	}

	// Run real Git wire services against a separate bare repository. The SSH
	// fixture replaces only the network hop, so this test needs no privileged sshd.
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Fatal(err)
	}
	remoteDir := filepath.Join(host, "remote.git")
	hostEnv := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + host, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null"}
	runCommand(t, host, hostEnv, git, "init", "--bare", "--initial-branch=main", remoteDir)
	fixture := filepath.Join(host, "ssh-fixture")
	writeFile(t, fixture, "#!"+bash+"\nset -eu\ncase \"${SSH_ORIGINAL_COMMAND:-${!#}}\" in\n"+
		"  \"git-upload-pack '/team/repo"+suffix+"'\") exec '"+git+"' upload-pack '"+remoteDir+"';;\n"+
		"  \"git-receive-pack '/team/repo"+suffix+"'\") exec '"+git+"' receive-pack '"+remoteDir+"';;\n"+
		"  *) exit 99;;\nesac\n", 0700)
	remoteURL := "ssh://git@forge.test:2222/team/repo"
	if os.Getenv("PACT_TEST_SSHD") == "1" {
		remoteURL = startSSHD(t, cfg, fixture)
	} else {
		b.ssh = fixture
	}

	// Exercise the production bridge and the container's Unix-to-HTTP relay.
	bridge, err := mcphost.Start([]mcpproxy.Server{{Name: ServerName, Handler: b}}, workspace)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { bridge.Close() })
	endpoint := strings.Replace(bridge.AgentURL(), "host.docker.internal", "127.0.0.1", 1)
	if mounts := bridge.ContainerConfig().ReadOnlyMounts; len(mounts) > 0 {
		endpoint = testRelay(t, mounts[0].Source)
	}
	identity, err := fs.ReadFile(container.BuildContext, "git-identity.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(bin, "git-identity.sh")
	writeFile(t, script, string(identity), 0600)
	env := []string{"PATH=" + bin + ":" + os.Getenv("PATH"), "HOME=" + workspace,
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null",
		"PACT_GIT_NAME=" + cfg.Name, "PACT_GIT_EMAIL=" + cfg.Email,
		"PACT_GIT_URL=" + endpoint + "/mcp/" + ServerName}
	gitRun := func(dir string, args ...string) string {
		t.Helper()
		return runCommand(t, dir, env, bash, append([]string{"-ec", "source \"$1\"; shift; exec git \"$@\"", "git", script}, args...)...)
	}
	denied := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), bash, append([]string{"-ec", "source \"$1\"; shift; exec git \"$@\"", "git", script}, args...)...)
		cmd.Dir, cmd.Env = dir, env
		if output, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(output), "does not match git.allowedRemotes") {
			t.Fatalf("remote must be denied: %v, %s", err, output)
		}
	}
	denied(workspace, "ls-remote", remoteURL)
	cfg.AllowedRemotes = []string{remoteURL + suffix, remoteURL + otherSuffix}
	writeConfig(t, b.configPath, cfg)
	gitRun(workspace, "clone", remoteURL, "checkout")
	checkout := filepath.Join(workspace, "checkout")
	gitRun(checkout, "checkout", "-B", "main")
	gitRun(checkout, "config", "user.name", "Wrong local identity")
	gitRun(checkout, "config", "gpg.ssh.program", "/does/not/exist")
	// A nontrivial pack exercises both directions beyond a single HTTP buffer.
	data := make([]byte, 256<<10)
	_, _ = rand.Read(data)
	writeFile(t, filepath.Join(checkout, "data"), string(data), 0600)
	gitRun(checkout, "add", "data")
	gitRun(checkout, "commit", "-m", "signed through host")
	gitRun(checkout, "verify-commit", "HEAD")
	if got := gitRun(checkout, "log", "-1", "--format=%cn <%ce>"); got != cfg.Name+" <"+cfg.Email+">" {
		t.Fatalf("identity: %q", got)
	}
	gitRun(checkout, "tag", "-s", "v1", "-m", "signed tag")
	gitRun(checkout, "verify-tag", "v1")
	// Both wire services must accept the opposite suffix spelling and still
	// invoke the first configured path, even though a later entry matches exactly:
	// the fixture rejects the alias path.
	aliasURL := remoteURL
	if suffix == "" {
		aliasURL += ".git"
	}
	gitRun(checkout, "remote", "set-url", "origin", aliasURL)
	gitRun(checkout, "push", "origin", "main", "--tags")
	gitRun(workspace, "clone", aliasURL, "second")
	second := filepath.Join(workspace, "second")
	if got := gitRun(second, "rev-parse", "HEAD"); got != gitRun(checkout, "rev-parse", "HEAD") {
		t.Fatal("clone lost the pushed commit")
	}
	gitRun(checkout, "commit", "--allow-empty", "-m", "next commit")
	gitRun(checkout, "push", "origin", "main")
	gitRun(second, "fetch", "origin")
	gitRun(second, "verify-commit", "origin/main")
	// Git returns failure when receive-pack rejects an update.
	cmd := exec.Command(bash, "-ec", "source \"$1\"; git push origin HEAD:main", "git", script)
	cmd.Dir, cmd.Env = second, env
	if output, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(output), "rejected") {
		t.Fatalf("rejected push: %v, %s", err, output)
	}
	if _, err := os.Stat(filepath.Join(workspace, ".ssh", "pact_identity")); !os.IsNotExist(err) {
		t.Fatal("private identity was installed in agent home")
	}
	// Rotate all credential settings without restarting the broker. The old
	// identity must stop signing, and the new key must sign and authenticate.
	oldKey := cfg.SSHKey
	cfg.SSHKey = filepath.Join(host, "rotated-key")
	runCommand(t, host, nil, "ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", cfg.SSHKey)
	publicKey, err := os.ReadFile(cfg.SSHKey + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	// The real sshd fixture reads its authorized key from this original path.
	writeFile(t, oldKey+".pub", string(publicKey), 0600)
	hosts, err := os.ReadFile(cfg.KnownHosts)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(cfg.KnownHosts); err != nil {
		t.Fatal(err)
	}
	cfg.KnownHosts = filepath.Join(host, "rotated-known-hosts")
	writeFile(t, cfg.KnownHosts, string(hosts), 0600)
	cfg.Name, cfg.Email = "New Agent", "new@example.test"
	writeConfig(t, b.configPath, cfg)
	cmd = exec.CommandContext(t.Context(), bash, "-ec", "source \"$1\"; git commit --allow-empty -m stale", "git", script)
	cmd.Dir, cmd.Env = checkout, env
	if output, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(output), "configured committer or tagger") {
		t.Fatalf("old identity must be denied after config change: %v, %s", err, output)
	}
	// Refresh the container's startup identity and public verification material.
	for i, value := range env {
		if strings.HasPrefix(value, "PACT_GIT_NAME=") {
			env[i] = "PACT_GIT_NAME=" + cfg.Name
		}
		if strings.HasPrefix(value, "PACT_GIT_EMAIL=") {
			env[i] = "PACT_GIT_EMAIL=" + cfg.Email
		}
	}
	gitRun(checkout, "commit", "--allow-empty", "-m", "rotated identity and key")
	gitRun(checkout, "verify-commit", "HEAD")
	if got := runCommand(t, workspace, env, filepath.Join(bin, "pact-git-sign"), "--public-key"); got != strings.TrimSpace(string(publicKey)) {
		t.Fatalf("public-key query returned stale key: %s", got)
	}
	gitRun(checkout, "push", "origin", "main")
	gitRun(second, "fetch", "origin")
	gitRun(second, "verify-commit", "origin/main")
	if os.Getenv("PACT_TEST_SSHD") == "1" {
		writeFile(t, cfg.KnownHosts, "", 0600)
		cmd = exec.Command(bash, "-ec", "source \"$1\"; git ls-remote origin", "git", script)
		cmd.Dir, cmd.Env = checkout, env
		if output, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(output), "Host key verification failed") {
			t.Fatalf("unknown SSH host was not rejected: %v, %s", err, output)
		}
	}
	cfg.AllowedRemotes = nil
	writeConfig(t, b.configPath, cfg)
	denied(checkout, "fetch", "origin")
	denied(checkout, "push", "origin", "main")
	if err := bridge.Close(); err != nil {
		t.Fatal(err)
	}
	cmd = exec.Command(bash, "-ec", "source \"$1\"; git commit --allow-empty -m revoked", "git", script)
	cmd.Dir, cmd.Env = checkout, env
	if err := cmd.Run(); err == nil {
		t.Fatal("signing succeeded after bridge revocation")
	}
}

// Opt-in real SSH authentication/host-key smoke test. The regular suite uses
// the same Git services without depending on sshd's platform privilege setup.
func startSSHD(t *testing.T, cfg config.Git, service string) string {
	t.Helper()
	host := filepath.Dir(cfg.SSHKey)
	hostKey := filepath.Join(host, "server-key")
	runCommand(t, host, nil, "ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", hostKey)
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	publicKey, err := os.ReadFile(hostKey + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, cfg.KnownHosts, fmt.Sprintf("[127.0.0.1]:%d %s", port, publicKey), 0600)
	path := filepath.Join(host, "sshd_config")
	writeFile(t, path, fmt.Sprintf(`ListenAddress 127.0.0.1
Port %d
HostKey %s
AuthorizedKeysFile %s.pub
StrictModes no
UsePAM no
PasswordAuthentication no
KbdInteractiveAuthentication no
PidFile %s/pid
ForceCommand %s
`, port, hostKey, cfg.SSHKey, host, service), 0600)
	sshd, err := exec.LookPath("sshd")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, sshd, "-D", "-e", "-f", path)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM) }
	cmd.WaitDelay = time.Second
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); _ = cmd.Wait() })
	for deadline := time.Now().Add(5 * time.Second); ; {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 100*time.Millisecond)
		if err == nil {
			conn.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("test sshd did not start")
		}
		time.Sleep(20 * time.Millisecond)
	}
	current, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("ssh://%s@127.0.0.1:%d/team/repo", current.Username, port)
}

func TestTransportCancellation(t *testing.T) {
	b, cfg, _ := testBroker(t, "git@forge.test:team/repo")
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Fatal(err)
	}
	b.ssh = filepath.Join(filepath.Dir(cfg.SSHKey), "waiting-ssh")
	writeFile(t, b.ssh, "#!/bin/sh\nexec '"+sleep+"' 60\n", 0700)
	ended := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(ended)
		b.ServeHTTP(w, r)
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	q := url.Values{"op": {"ssh"}, "host": {"git@forge.test"}, "command": {"git-upload-pack 'team/repo'"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"?"+q.Encode(), nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatal(res.Status)
	}
	cancel()
	res.Body.Close()
	select {
	case <-ended:
	case <-time.After(5 * time.Second):
		t.Fatal("host subprocess survived client cancellation")
	}
}

func testRelay(t *testing.T, socket string) string {
	t.Helper()
	// socat chooses its listening port; obtain it from a temporary listener.
	probe := httptest.NewServer(http.NotFoundHandler())
	endpoint := probe.URL
	probe.Close()
	u, _ := url.Parse(endpoint)
	script, err := fs.ReadFile(container.BuildContext, "mcp-relay.sh")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, "bash", "-e", "-s", "--", socket, u.Port())
	cmd.Stdin = strings.NewReader(string(script) + "\nstart_mcp_relay \"$1\" \"$2\"\necho ready\nwait \"$pact_mcp_relay_pid\"\n")
	cmd.Stderr = os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM) }
	cmd.WaitDelay = time.Second
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); _ = cmd.Wait() })
	var ready [6]byte
	if _, err := io.ReadFull(out, ready[:]); err != nil || string(ready[:]) != "ready\n" {
		t.Fatalf("relay startup: %v", err)
	}
	return endpoint
}

func TestBrokerRejectsCredentialAbuse(t *testing.T) {
	b, cfg, workspace := testBroker(t, "git@forge.test:team/repo")
	// If any forbidden request starts a host process, this executable leaves proof.
	marker := filepath.Join(filepath.Dir(cfg.SSHKey), "executed")
	trap := filepath.Join(filepath.Dir(cfg.SSHKey), "trap")
	writeFile(t, trap, "#!/bin/sh\nprintf executed > '"+marker+"'\n", 0700)
	keygen := b.keygen
	b.ssh, b.keygen = trap, trap
	server := httptest.NewServer(b)
	defer server.Close()
	for _, q := range []url.Values{
		{"op": {"ssh"}, "host": {"git@evil.test"}, "command": {"git-upload-pack 'team/repo'"}},
		{"op": {"ssh"}, "host": {"other@forge.test"}, "command": {"git-upload-pack 'team/repo.git'"}},
		{"op": {"ssh"}, "host": {"git@forge.test"}, "port": {"2222"}, "command": {"git-upload-pack 'team/repo'"}},
		{"op": {"ssh"}, "host": {"git@forge.test"}, "command": {"git-upload-pack 'team/other'"}},
		{"op": {"ssh"}, "host": {"git@forge.test"}, "command": {"git-upload-pack 'team/repo.git.git'"}},
		{"op": {"ssh"}, "host": {"git@forge.test"}, "command": {"git-upload-pack '/team/repo.git'"}},
		{"op": {"ssh"}, "host": {"git@forge.test"}, "command": {"git-upload-pack 'team/repo'; cat " + cfg.SSHKey}},
		{"op": {"ssh"}, "host": {"git@forge.test"}, "command": {"cat " + cfg.SSHKey}},
		{"op": {"sign"}},
	} {
		res, err := http.Post(server.URL+"?"+q.Encode(), "application/octet-stream", strings.NewReader(cfg.SSHKey))
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(res.Body)
		res.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if res.StatusCode != http.StatusForbidden {
			t.Fatalf("forbidden request got %s", res.Status)
		}
		if q.Get("command") == "git-upload-pack 'team/other'" &&
			(!strings.Contains(string(body), "team/other") || !strings.Contains(string(body), "does not match git.allowedRemotes")) {
			t.Fatalf("remote rejection lacks actionable diagnostic: %s", body)
		}
		if strings.HasPrefix(q.Get("command"), "cat ") && !strings.Contains(string(body), "command is not allowed") {
			t.Fatalf("command rejection lacks distinct diagnostic: %s", body)
		}
	}
	spoof := "tree " + strings.Repeat("a", 40) + "\nauthor Someone <someone@example.test> 1 +0000\ncommitter Someone <someone@example.test> 1 +0000\n\nmessage\n"
	for _, payload := range []string{spoof, strings.Repeat("x", (1<<20)+1)} {
		res, err := http.Post(server.URL+"?op=sign", "application/octet-stream", strings.NewReader(payload))
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode == http.StatusOK {
			t.Fatal("broker signed an invalid object")
		}
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("forbidden request started a host executable")
	}
	b.keygen = keygen
	res, err := http.Get(server.URL + "?op=public-key")
	if err != nil {
		t.Fatal(err)
	}
	public, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if !bytes.HasPrefix(public, []byte("ssh-ed25519 ")) || bytes.Contains(public, []byte("PRIVATE KEY")) {
		t.Fatal("public-key endpoint returned unexpected material")
	}
	keyInWorkspace := filepath.Join(workspace, "key")
	writeFile(t, keyInWorkspace, "secret", 0600)
	alias := filepath.Join(filepath.Dir(cfg.SSHKey), "key-link")
	if err := os.Symlink(keyInWorkspace, alias); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{keyInWorkspace, alias} {
		cfg.SSHKey = key
		writeConfig(t, b.configPath, cfg)
		res := httptest.NewRecorder()
		b.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/?op=public-key", nil))
		if res.Code != http.StatusServiceUnavailable {
			t.Fatal("read a private key inside the workspace")
		}
	}
}

func TestKeyAddedAfterStartup(t *testing.T) {
	for _, initialKey := range []string{"", "not-created-yet", "no Git section", "no config file"} {
		t.Run("key="+initialKey, func(t *testing.T) {
			host, workspace := t.TempDir(), t.TempDir()
			path := filepath.Join(host, "config.json")
			cfg := config.Git{Name: "Pact Agent", Email: "agent@example.test", SSHKey: initialKey}
			writeConfig(t, path, cfg)
			switch initialKey {
			case "no Git section":
				writeFile(t, path, "{}", 0600)
			case "no config file":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			}
			b, err := New(path, workspace)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { b.Close() })
			server := httptest.NewServer(b)
			defer server.Close()
			bin := filepath.Join(host, "pact-git-sign")
			runCommand(t, "../..", nil, "go", "build", "-o", bin, "./cmd/git-client")
			identity, err := fs.ReadFile(container.BuildContext, "git-identity.sh")
			if err != nil {
				t.Fatal(err)
			}
			script := filepath.Join(host, "identity.sh")
			writeFile(t, script, string(identity), 0600)
			env := []string{"PATH=" + host + ":" + os.Getenv("PATH"), "HOME=" + workspace,
				"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null",
				"PACT_GIT_URL=" + server.URL}
			// Initialize once under the entrypoint's strict shell flags, before any
			// key exists. Preserve that environment for the later Git invocation.
			settings := filepath.Join(host, "settings.sh")
			runCommand(t, workspace, env, "bash", "-euc", "source \"$1\"; export -p > \"$2\"", "bash", script, settings)
			runCommand(t, workspace, env, "git", "init")
			commit := "source \"$1\"; git -c user.name=\"$2\" -c user.email=\"$3\" commit --allow-empty -m test"
			cmd := exec.CommandContext(t.Context(), "bash", "-euc", commit, "bash", settings, cfg.Name, cfg.Email)
			cmd.Dir, cmd.Env = workspace, env
			if output, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(output), "cannot load Git config") {
				t.Fatalf("missing key must reject signing: %v, %s", err, output)
			}
			cfg.SSHKey = filepath.Join(host, "not-created-yet")
			runCommand(t, host, nil, "ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", cfg.SSHKey)
			writeConfig(t, path, cfg)
			runCommand(t, workspace, env, "bash", "-euc", commit, "bash", settings, cfg.Name, cfg.Email)
			// Refresh only the public verification material after adding the key.
			env = append(env, "PACT_GIT_EMAIL="+cfg.Email)
			runCommand(t, workspace, env, "bash", "-euc", "source \"$1\"; git verify-commit HEAD", "bash", script)
		})
	}
}
