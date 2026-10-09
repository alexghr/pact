package gitbroker

import (
	"bytes"
	"context"
	"crypto/rand"
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

func testBroker(t *testing.T, remotes ...string) (*Broker, config.Git, string) {
	t.Helper()
	host, workspace := t.TempDir(), t.TempDir()
	key := filepath.Join(host, "identity")
	runCommand(t, host, nil, "ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", key)
	cfg := config.Git{Name: "Pact Agent", Email: "agent@example.test", SSHKey: key, AllowedRemotes: remotes}
	if len(remotes) > 0 {
		cfg.KnownHosts = filepath.Join(host, "known_hosts")
		writeFile(t, cfg.KnownHosts, "", 0600)
	}
	b, err := New(context.Background(), cfg, workspace)
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
	for _, remotes := range [][]string{nil, {"git@forge.test:team/repo.git", "ssh://git@forge.test:2222/team/other"}} {
		t.Run(fmt.Sprintf("remotes=%d", len(remotes)), func(t *testing.T) {
			b, _, _ := testBroker(t, remotes...)
			server := httptest.NewServer(b)
			defer server.Close()
			env := []string{"PACT_GIT_URL=" + server.URL}
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
			server.Close()
			cmd = exec.CommandContext(t.Context(), bin, "--allowed-remotes")
			cmd.Env = env
			if output, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(output), "cannot reach") {
				t.Fatalf("unavailable broker must fail, not report an empty list: %v, %s", err, output)
			}
		})
	}
}

func testGitWorkflow(t *testing.T, suffix string) {
	otherSuffix := ".git"
	if suffix == ".git" {
		otherSuffix = ""
	}
	b, cfg, workspace := testBroker(t, "ssh://git@forge.test:2222/team/repo"+suffix,
		"ssh://git@forge.test:2222/team/repo"+otherSuffix)
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
		target, err := parseRemote(remoteURL)
		if err != nil {
			t.Fatal(err)
		}
		target.path += suffix
		b.remotes = []remote{target}
		target.path = strings.TrimSuffix(target.path, ".git") + otherSuffix
		b.remotes = append(b.remotes, target)
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
	if got := gitRun(checkout, "log", "-1", "--format=%cn <%ce>"); got != b.identity {
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
	if os.Getenv("PACT_TEST_SSHD") == "1" {
		writeFile(t, cfg.KnownHosts, "", 0600)
		cmd = exec.Command(bash, "-ec", "source \"$1\"; git ls-remote origin", "git", script)
		cmd.Dir, cmd.Env = checkout, env
		if output, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(output), "Host key verification failed") {
			t.Fatalf("unknown SSH host was not rejected: %v, %s", err, output)
		}
	}
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
		if broker, err := New(context.Background(), cfg, workspace); err == nil {
			broker.Close()
			t.Fatal("accepted a private key inside the workspace")
		}
	}
}
