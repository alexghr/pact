package harness

// Options describes Pact's requested agent run before native translation.
type Options struct {
	Model                     string            `json:"model"`
	Effort                    string            `json:"effort,omitempty"`
	Prompt                    string            `json:"prompt,omitempty"`
	BackendRef                string            `json:"backendRef,omitempty"`
	MCP                       map[string]string `json:"mcp,omitempty"`
	MCPApprovalTimeoutSeconds int               `json:"mcpApprovalTimeoutSeconds,omitempty"`
}
