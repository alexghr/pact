// Package harness defines Pact launch requests and backend-neutral bindings.
package harness

// Binding identifies the initial native conversation within a Pact session.
// Only the backend interprets BackendRef; no session ID crosses the bridge.
type Binding struct {
	BackendRef string `json:"backendRef"`
}
