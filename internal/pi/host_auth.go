package pi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

const AuthPath = "/harness/openai-auth"

type authExport struct {
	done       chan struct{}
	credential json.RawMessage
	status     int
}

// HostAuth delegates renewal to native host Pi, which shares its credential
// lock with other Pact sessions and host Pi processes. Only access credentials
// cross the bridge. Each request joins the current export, if any.
type HostAuth struct {
	dir    string
	mu     sync.Mutex
	active *authExport
	closed bool
}

func NewHostAuth() (*HostAuth, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return &HostAuth{dir: filepath.Join(home, ".pi", "agent")}, nil
}

func (h *HostAuth) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		http.Error(w, "Host Pi authentication is unavailable", http.StatusServiceUnavailable)
		return
	}
	job := h.active
	if job == nil {
		job = &authExport{done: make(chan struct{})}
		h.active = job
		go h.export(job)
	}
	h.mu.Unlock()

	select {
	case <-r.Context().Done():
		return
	case <-job.done:
	}
	if job.status != http.StatusOK {
		http.Error(w, "Host Pi could not renew OpenAI credentials. Check the host network and run pi auth check --provider openai on the host; reconnect ChatGPT with host Pi /login if needed, then retry here.", job.status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(job.credential)
}

func (h *HostAuth) export(job *authExport) {
	// Do not cancel a refresh: the issuer may already have rotated the only usable refresh token.
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	job.credential, job.status = h.credential(ctx)

	h.mu.Lock()
	h.active = nil
	close(job.done)
	h.mu.Unlock()
}

func (h *HostAuth) credential(ctx context.Context) (json.RawMessage, int) {
	for range 2 {
		command := exec.CommandContext(ctx, "pi", "auth", "print-bearer-token", "--provider", "openai", "--min-expiry", "10m")
		command.Dir = h.dir
		command.Env = append(os.Environ(), "PI_CODING_AGENT_DIR="+h.dir)
		command.WaitDelay = 2 * time.Second
		command.Stderr = io.Discard
		token, err := command.Output()
		if err != nil {
			return nil, http.StatusServiceUnavailable
		}

		// Export completes only after native Pi persists rotated credentials.
		data, err := authSnapshot(filepath.Join(h.dir, "auth.json"))
		if err != nil {
			continue
		}
		var projected map[string]json.RawMessage
		var credential struct {
			Type    string
			Access  string
			Expires int64
		}
		if json.Unmarshal(data, &projected) != nil || json.Unmarshal(projected["openai"], &credential) != nil {
			continue
		}
		if credential.Type == "oauth" && credential.Access != "" &&
			credential.Access == string(bytes.TrimSpace(token)) && credential.Expires > time.Now().Add(5*time.Minute).UnixMilli() {
			return projected["openai"], http.StatusOK
		}
	}
	return nil, http.StatusServiceUnavailable
}

func (h *HostAuth) Close() {
	h.mu.Lock()
	h.closed = true
	job := h.active
	h.mu.Unlock()
	if job != nil {
		<-job.done
	}
}
