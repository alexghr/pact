package approval

import "testing"

func TestSessionGrants(t *testing.T) {
	request := Request{
		Hostname: "work-server",
		Session:  Session{ID: 42, Key: "session-key", ExecutionID: 1},
		Call:     ToolCall{Server: "forgejo", Tool: "create_issue"},
	}
	exact, group := make(grants), make(grants)
	if exact.allows(request) {
		t.Fatal("a request without a grant was approved")
	}
	exact.remember(request, false)
	group.remember(request, true)

	for _, tt := range []struct {
		name, hostname, session, server, tool string
		exact, group                          bool
	}{
		{"resumed session", "work-server", "session-key", "forgejo", "create_issue", true, true},
		{"another tool", "work-server", "session-key", "forgejo", "delete_issue", false, true},
		{"another hostname", "other-host", "session-key", "forgejo", "create_issue", false, false},
		{"another session", "work-server", "other-key", "forgejo", "create_issue", false, false},
		{"another server", "work-server", "session-key", "other-server", "create_issue", false, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			call := Request{
				Hostname: tt.hostname,
				Session:  Session{ID: 42, Key: tt.session, ExecutionID: 2},
				Call:     ToolCall{Server: tt.server, Tool: tt.tool},
			}
			if got := exact.allows(call); got != tt.exact {
				t.Errorf("exact tool grant: got %v, want %v", got, tt.exact)
			}
			if got := group.allows(call); got != tt.group {
				t.Errorf("server grant: got %v, want %v", got, tt.group)
			}
		})
	}
}
