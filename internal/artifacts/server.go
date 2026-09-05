package artifacts

import (
	"context"
	_ "embed"
	"fmt"

	"github.com/alexghr/pact/internal/state"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

//go:embed instructions.md
var instructions string

func NewServer(store *state.Store, pactSessionID int64) *mcp.Server {
	server := mcp.NewServer(
		&mcp.Implementation{Name: "pact-artifacts", Version: "0.1.0"},
		&mcp.ServerOptions{
			Instructions: fmt.Sprintf(
				"This broker is bound to Pact session %d.\n\n%s",
				pactSessionID, instructions,
			),
		},
	)
	service := NewService(store, pactSessionID)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "create_artifact",
		Title:       "Create artifact",
		Description: "Create an empty shared artifact attributed to the current Pact session. Search for existing project knowledge first. Include project and topic identifiers in its name or description, then add files with write_artifact_file.",
		Annotations: closedWorldMutation(false),
	}, toolHandler(service.CreateArtifact))
	mcp.AddTool(server, &mcp.Tool{
		Name:        "search_artifacts",
		Title:       "Search artifacts",
		Description: "Find shared project knowledge before investigating or updating it. Search names, descriptions, and file paths across sessions; file contents are not searched. The optional creating-session filter is not a project filter.",
		Annotations: closedWorldReadOnly(),
	}, toolHandler(service.SearchArtifacts))
	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_artifact",
		Title:       "Get artifact",
		Description: "Get artifact metadata and the complete file manifest. File contents are returned by read_artifact_file.",
		Annotations: closedWorldReadOnly(),
	}, toolHandler(service.GetArtifact))
	mcp.AddTool(server, &mcp.Tool{
		Name:        "update_artifact",
		Title:       "Update artifact",
		Description: "Change an artifact's name or description. This is globally writable and does not change its creating session.",
		Annotations: closedWorldMutation(true),
	}, toolHandler(service.UpdateArtifact))
	mcp.AddTool(server, &mcp.Tool{
		Name:        "read_artifact_file",
		Title:       "Read artifact file",
		Description: "Read a byte range from a globally visible artifact file. Text is returned as UTF-8 and other data as base64.",
		Annotations: closedWorldReadOnly(),
	}, toolHandler(service.ReadFile))
	mcp.AddTool(server, &mcp.Tool{
		Name:        "write_artifact_file",
		Title:       "Write artifact file",
		Description: fmt.Sprintf("Create or completely replace one artifact file, up to %d bytes. Use expected_version=0 for creation or the current version for replacement.", state.MaxArtifactFileBytes),
		Annotations: closedWorldMutation(true),
	}, toolHandler(service.WriteFile))
	mcp.AddTool(server, &mcp.Tool{
		Name:        "edit_artifact_file",
		Title:       "Edit artifact file",
		Description: "Replace exact text in a UTF-8 artifact file without resending the full file. The edit fails safely on concurrent changes.",
		Annotations: closedWorldMutation(true),
	}, toolHandler(service.EditFile))

	return server
}

// Keep MCP request/result plumbing out of the artifact service.
func toolHandler[Input, Output any](handle func(context.Context, Input) (Output, error)) mcp.ToolHandlerFor[Input, Output] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, input Input) (*mcp.CallToolResult, Output, error) {
		output, err := handle(ctx, input)
		return nil, output, err
	}
}

func closedWorldReadOnly() *mcp.ToolAnnotations {
	closed := false
	return &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: &closed}
}

func closedWorldMutation(destructive bool) *mcp.ToolAnnotations {
	closed := false
	return &mcp.ToolAnnotations{DestructiveHint: &destructive, OpenWorldHint: &closed}
}
