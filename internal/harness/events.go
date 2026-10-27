// Package harness defines the backend-neutral identity payload sent to Pact.
package harness

// Binding identifies the initial native conversation within a Pact session.
// Only the backend interprets BackendRef; no session ID crosses the bridge.
type Binding struct {
	BackendRef string `json:"backendRef"`
}
