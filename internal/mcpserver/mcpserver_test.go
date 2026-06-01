package mcpserver

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/awangga/rprompt/internal/approval"
)

type noopNotifier struct{}

func (noopNotifier) AskApproval(context.Context, int64, string, string, string) (int64, error) {
	return 1, nil
}
func (noopNotifier) Finalize(context.Context, int64, int64, string) {}

// TestApprovalToolExposedAndDenies memverifikasi end-to-end via client MCP:
// tool approval_prompt terdaftar, bisa dipanggil, dan tanpa chat aktif
// mengembalikan keputusan "deny" dalam text content.
func TestApprovalToolExposedAndDenies(t *testing.T) {
	reg := approval.New(noopNotifier{}, time.Second, "behavior")
	// Sengaja tidak Activate → Request langsung menolak tanpa memanggil notifier.

	ts := httptest.NewServer(Handler(reg, "behavior"))
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "v1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:             ts.URL,
		DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatalf("connect MCP: %v", err)
	}
	defer session.Close()

	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	found := false
	for _, tl := range tools.Tools {
		if tl.Name == ToolName {
			found = true
		}
	}
	if !found {
		t.Fatalf("tool %q tidak terdaftar", ToolName)
	}

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: ToolName,
		Arguments: map[string]any{
			"tool_name": "Bash",
			"input":     map[string]any{"command": "ls"},
		},
	})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}
	if len(res.Content) == 0 {
		t.Fatal("hasil tanpa content")
	}
	tc, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("content bukan TextContent: %T", res.Content[0])
	}
	var decision map[string]json.RawMessage
	if err := json.Unmarshal([]byte(tc.Text), &decision); err != nil {
		t.Fatalf("hasil bukan JSON: %v (%s)", err, tc.Text)
	}
	if string(decision["behavior"]) != `"deny"` {
		t.Errorf("tanpa chat aktif harus deny, dapat: %s", tc.Text)
	}
}
