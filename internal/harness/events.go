// Package harness defines Pact launch requests and backend-neutral bindings.
package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Binding identifies the initial native conversation within a Pact session.
// Only the backend interprets BackendRef; no session ID crosses the bridge.
type Binding struct {
	BackendRef string `json:"backendRef"`
}

// PublishBinding reports an opaque native reference to the host-selected session.
func PublishBinding(ctx context.Context, endpoint, ref string) error {
	data, err := json.Marshal(Binding{BackendRef: ref})
	if err != nil {
		return err
	}
	client := &http.Client{
		Timeout:       10 * time.Second,
		Transport:     &http.Transport{DisableKeepAlives: true},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	defer client.CloseIdleConnections()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("publish Pact backend binding: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK && res.StatusCode != http.StatusNoContent {
		return fmt.Errorf("publish Pact backend binding: %s", res.Status)
	}
	_, err = io.Copy(io.Discard, io.LimitReader(res.Body, 4096))
	return err
}
