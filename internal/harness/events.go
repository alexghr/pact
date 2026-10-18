// Package harness defines the conversation metadata Pact accepts from a harness.
package harness

// Thread contains saved conversation metadata. Timestamps are Unix nanoseconds.
type Thread struct {
	ID        string `json:"id"`
	ParentID  string `json:"parentId,omitempty"`
	Title     string `json:"title,omitempty"`
	CreatedAt int64  `json:"createdAt"`
	LastUsed  int64  `json:"lastUsed"`
	Archived  bool   `json:"archived"`
	Internal  bool   `json:"internal"`
	Missing   bool   `json:"missing"`
}

// Event carries a complete snapshot of the harness's saved conversations.
// Notifications about unsaved conversations do not enter the catalogue.
type Event struct {
	Type    string   `json:"type"`
	Threads []Thread `json:"threads"`
}
