package pi

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/alexghr/pact/internal/harness"
	"github.com/alexghr/pact/internal/hostbridge"
	"github.com/alexghr/pact/internal/images"
)

// Real pinned Pi processes, native refresh/persistence/locking, and the shipped
// Unix bridge/relay. Only OAuth and completion HTTP endpoints are synthetic.
func TestLiveOAuthRenewal(t *testing.T) {
	if os.Getenv("PACT_TEST_PI") != "1" || runtime.GOOS != "linux" {
		t.Skip("set PACT_TEST_PI=1 on Linux for native OAuth over the Unix bridge")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	var exchanges, failure atomic.Int32
	var refreshMu sync.Mutex
	expectedRefresh := "initial-refresh"
	var hold atomic.Bool
	rotationStarted, finishRotation := make(chan struct{}), make(chan struct{})
	var finishOnce sync.Once
	finish := func() { finishOnce.Do(func() { close(finishRotation) }) }
	defer finish()
	started, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	oauthEndpoint(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/accounts/oauth/token" || r.Method != http.MethodPost {
			t.Error("unexpected OAuth request")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if err := r.ParseForm(); err != nil || r.Form.Get("grant_type") != "refresh_token" ||
			r.Form.Get("refresh_token") == "" || r.Form.Get("client_id") != "issued-client" {
			t.Error("native refresh lost its host credentials")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		refreshMu.Lock()
		if r.Form.Get("refresh_token") != expectedRefresh {
			refreshMu.Unlock()
			t.Error("a concurrent process reused a rotated refresh token")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		n := exchanges.Add(1)
		code := failure.Load()
		if code == 0 {
			expectedRefresh = fmt.Sprintf("refresh-%d", n)
		}
		refreshMu.Unlock()
		if hold.Swap(false) {
			close(rotationStarted)
			select {
			case <-finishRotation:
			case <-ctx.Done():
				return
			}
		}
		if n == 1 {
			close(started)
			select {
			case <-release:
			case <-ctx.Done():
				return
			}
		}
		if code != 0 {
			w.WriteHeader(int(code))
			fmt.Fprint(w, `{"error":"invalid_grant","details":"upstream-private-secret"}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"access_token":"access-%d","refresh_token":"refresh-%d","expires_in":3600,"scope":"chatgpt.tokens.use.direct"}`, n, n)
	}))

	type providerRequest struct {
		body   []byte
		bearer string
	}
	requests := make(chan providerRequest, 16)
	readRequest := func() providerRequest {
		t.Helper()
		select {
		case request := <-requests:
			return request
		case <-ctx.Done():
			t.Fatal("Pi did not contact the model endpoint")
			return providerRequest{}
		}
	}
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requests <- providerRequest{body: body, bearer: r.Header.Get("Authorization")}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"id\":\"test\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"Acknowledged.\"},\"finish_reason\":null}]}\n\ndata: {\"id\":\"test\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer provider.Close()
	hostDir := t.TempDir()
	writeLogin := func(dir, access, refresh string) {
		t.Helper()
		value := fmt.Sprintf(`{"openai":{"type":"oauth","access":%q,"refresh":%q,"expires":1,"clientId":"issued-client","scopes":["chatgpt.tokens.use.direct"]}}`, access, refresh)
		if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeLogin(hostDir, "old-access", "initial-refresh")

	var clients []*piRPC
	var homes, bridges []string
	var hosts []*HostAuth
	for range 2 {
		host := &HostAuth{dir: hostDir}
		t.Cleanup(host.Close)
		url := authBridge(t, ctx, host)
		bridges = append(bridges, url)
		hosts = append(hosts, host)
		home := t.TempDir()
		homes = append(homes, home)
		writeLogin(home, "old-access", "")
		t.Setenv("PI_CODING_AGENT_DIR", home)
		models := fmt.Sprintf(`{"providers":{"openai":{"baseUrl":%q,"api":"openai-completions","models":[{"id":"pact-oauth-test","name":"OAuth test","reasoning":false,"input":["text"],"contextWindow":8192,"maxTokens":1024}]}}}`, provider.URL)
		if err := os.WriteFile(filepath.Join(home, "models.json"), []byte(models), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(home, "settings.json"), []byte(`{"retry":{"enabled":false}}`), 0600); err != nil {
			t.Fatal(err)
		}
		extension, err := writeExtension(t.TempDir(), harness.Options{}, url+"/harness/binding", url+AuthPath)
		if err != nil {
			t.Fatal(err)
		}
		client := startPiRPC(t, ctx, t.TempDir(), []string{"--mode", "rpc", "--model", "openai/pact-oauth-test", "--extension", extension})
		client.call(t, "get_state", nil)
		clients = append(clients, client)
	}
	var wg sync.WaitGroup
	for _, client := range clients {
		wg.Go(func() {
			if message := client.call(t, "prompt", map[string]any{"message": "The project codename is cobalt-otter."}); message != "" {
				t.Error(message)
			}
		})
	}
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("native host Pi did not request renewal")
	}
	// Another host Pi process uses exactly the same native lock and must not
	// spend the old refresh token while the Pact export is in flight.
	other := exec.CommandContext(ctx, "pi", "auth", "print-bearer-token", "--provider", "openai", "--min-expiry", "10m")
	other.Dir = hostDir
	other.Env = append(os.Environ(), "PI_CODING_AGENT_DIR="+hostDir)
	var otherToken bytes.Buffer
	other.Stdout = &otherToken
	if err := other.Start(); err != nil {
		t.Fatal(err)
	}
	unblock()
	wg.Wait()
	if err := other.Wait(); err != nil || strings.TrimSpace(otherToken.String()) != "access-1" || exchanges.Load() != 1 {
		t.Fatalf("concurrent host Pi did not share the persisted renewal: %v, exchanges=%d", err, exchanges.Load())
	}
	for range clients {
		if request := readRequest(); request.bearer != "Bearer access-1" {
			t.Fatal("running Pi did not use the delivered access token")
		}
	}
	for _, home := range homes {
		data, err := os.ReadFile(filepath.Join(home, "auth.json"))
		if err != nil || bytes.Contains(data, []byte("refresh-1")) || !bytes.Contains(data, []byte("access-1")) {
			t.Fatalf("container-local native state was not access-only: %v", err)
		}
	}
	data, err := os.ReadFile(filepath.Join(hostDir, "auth.json"))
	if err != nil || !bytes.Contains(data, []byte("refresh-1")) {
		t.Fatalf("rotated host refresh token was not persisted: %v", err)
	}

	// Exhaust the validity margin, as after host sleep or delayed delivery.
	// Errors must preserve the process and conversation and never fall back to
	// an empty container-side refresh grant, even when the host is unavailable.
	writeLogin(hostDir, "access-1", "refresh-1")
	writeLogin(homes[0], "access-1", "")
	failure.Store(http.StatusBadRequest)
	message := clients[0].call(t, "prompt", map[string]any{"message": "Continue after host sleep."})
	if !strings.Contains(message, "host Pi /login") || strings.Contains(message, "upstream-private-secret") || exchanges.Load() != 3 {
		t.Fatalf("terminal renewal failure lost safe recovery guidance: %s, exchanges=%d", message, exchanges.Load())
	}
	failure.Store(http.StatusServiceUnavailable)
	before := exchanges.Load()
	message = clients[0].call(t, "prompt", map[string]any{"message": "Try while the host network is unavailable."})
	if !strings.Contains(message, "conversation is preserved") || exchanges.Load()-before != 2 {
		t.Fatalf("transient renewal retries were not bounded: %s, exchanges=%d", message, exchanges.Load()-before)
	}
	failure.Store(0)
	if message := clients[0].call(t, "prompt", map[string]any{"message": "Continue with the original project codename."}); message != "" {
		t.Fatal(message)
	}
	recovered := readRequest()
	if !bytes.Contains(recovered.body, []byte("cobalt-otter")) || !bytes.Contains(recovered.body, []byte("original project codename")) {
		t.Fatal("renewal recovery lost the running conversation")
	}
	if recovered.bearer != fmt.Sprintf("Bearer access-%d", exchanges.Load()) {
		t.Fatal("recovered conversation used a stale access token")
	}
	// Cancel the only bridge waiter after the issuer rotates, but before it
	// delivers the response. Shutdown must let native Pi save the new refresh
	// token; the other live session can then continue without reattachment.
	last := exchanges.Load()
	writeLogin(hostDir, fmt.Sprintf("access-%d", last), fmt.Sprintf("refresh-%d", last))
	hold.Store(true)
	requestCtx, stopWaiting := context.WithCancel(ctx)
	defer stopWaiting()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodPost, bridges[1]+AuthPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	requestDone := make(chan error, 1)
	go func() {
		response, err := http.DefaultClient.Do(request)
		if response != nil {
			response.Body.Close()
		}
		requestDone <- err
	}()
	select {
	case <-rotationStarted:
	case <-ctx.Done():
		t.Fatal("host did not start the cancellable renewal")
	}
	stopWaiting()
	if err := <-requestDone; err == nil {
		t.Fatal("cancelled bridge request completed successfully")
	}
	finish()
	hosts[1].Close()
	data, err = os.ReadFile(filepath.Join(hostDir, "auth.json"))
	if err != nil || !bytes.Contains(data, fmt.Appendf(nil, "refresh-%d", last+1)) {
		t.Fatalf("request cancellation discarded a rotated host credential: %v", err)
	}
	writeLogin(homes[0], "stale-access", "")
	if message := clients[0].call(t, "prompt", map[string]any{"message": "Continue after cancellation of another session's renewal."}); message != "" {
		t.Fatal(message)
	}
	if request := readRequest(); request.bearer != fmt.Sprintf("Bearer access-%d", last+1) || exchanges.Load() != last+1 {
		t.Fatal("surviving session could not reuse the persisted host renewal")
	}

	if err := os.WriteFile(filepath.Join(homes[0], "auth.json"), []byte(`{"openai":{"type":"api_key","key":"local-api-key"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if message := clients[0].call(t, "prompt", map[string]any{"message": "Continue using an API key."}); message != "" {
		t.Fatal(message)
	}
	if request := readRequest(); request.bearer != "Bearer local-api-key" || exchanges.Load() != last+1 {
		t.Fatal("host OAuth integration changed native API-key authentication")
	}

	for _, client := range clients {
		client.close(t)
		if strings.Contains(client.logs.String(), "upstream-private-secret") || strings.Contains(client.logs.String(), "refresh-1") {
			t.Fatal("Pi diagnostics exposed host OAuth secrets")
		}
	}
}

// Route the native client's fixed HTTPS token URL to a minimal local protocol
// endpoint using Node's supported environment proxy and a disposable test CA.
func oauthEndpoint(t *testing.T, handler http.Handler) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	certificate := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Pact OAuth test"},
		DNSNames: []string{"auth.openai.com"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	der, err := x509.CreateCertificate(rand.Reader, certificate, certificate, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	ca := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	endpoint := httptest.NewUnstartedServer(handler)
	endpoint.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}, MinVersion: tls.VersionTLS12}
	endpoint.StartTLS()
	t.Cleanup(endpoint.Close)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect || r.Host != "auth.openai.com:443" {
			t.Error("unexpected host network request")
			w.WriteHeader(http.StatusForbidden)
			return
		}
		upstream, err := net.Dial("tcp", endpoint.Listener.Addr().String())
		if err != nil {
			t.Error(err)
			return
		}
		defer upstream.Close()
		client, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer client.Close()
		_, _ = io.WriteString(client, "HTTP/1.1 200 Connection Established\r\n\r\n")
		go func() { _, _ = io.Copy(upstream, client); upstream.Close() }()
		_, _ = io.Copy(client, upstream)
	}))
	t.Cleanup(proxy.Close)
	for _, name := range []string{"HTTPS_PROXY", "HTTP_PROXY", "https_proxy", "http_proxy"} {
		t.Setenv(name, proxy.URL)
	}
	t.Setenv("NO_PROXY", "127.0.0.1,localhost")
	t.Setenv("no_proxy", "127.0.0.1,localhost")
	t.Setenv("NODE_USE_ENV_PROXY", "1")
	t.Setenv("NODE_EXTRA_CA_CERTS", ca)
}

func authBridge(t *testing.T, ctx context.Context, host *HostAuth) string {
	t.Helper()
	bridge, err := hostbridge.Start([]hostbridge.Server{{Path: AuthPath, Handler: host},
		{Path: "/harness/binding", Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = bridge.Close() })
	script, err := images.GetAgentFile("env/host-relay.sh")
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	command := exec.CommandContext(ctx, "bash", "-e", "-s", "--", bridge.ContainerConfig().ReadOnlyMounts[0].Source, fmt.Sprint(port))
	command.Stdin = strings.NewReader(script + "\nstart_host_relay \"$1\" \"$2\"\necho ready\nwait \"$pact_host_relay_pid\"\n")
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error { return syscall.Kill(-command.Process.Pid, syscall.SIGTERM) }
	command.WaitDelay = time.Second
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = command.Cancel(); _ = command.Wait() })
	if line, err := bufio.NewReader(stdout).ReadString('\n'); err != nil || line != "ready\n" {
		t.Fatalf("host relay did not become ready: %q, %v", line, err)
	}
	return fmt.Sprintf("http://127.0.0.1:%d", port)
}
