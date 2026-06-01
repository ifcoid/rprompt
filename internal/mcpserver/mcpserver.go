// Package mcpserver mengekspos satu tool MCP, "approval_prompt", yang dipakai
// Claude Code via --permission-prompt-tool. Saat Claude hendak memakai sebuah
// tool, ia memanggil tool ini; kita teruskan ke approval.Registry yang meminta
// persetujuan pengguna lewat Telegram, lalu mengembalikan keputusannya.
package mcpserver

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/awangga/rprompt/internal/approval"
)

// ToolName adalah nama tool tanpa namespace. Setelah server didaftarkan dengan
// nama "rprompt", nama lengkapnya menjadi mcp__rprompt__approval_prompt.
const ToolName = "approval_prompt"

// ServerName adalah key server pada --mcp-config; menentukan namespace tool.
const ServerName = "rprompt"

// Handler membangun http.Handler MCP (Streamable HTTP) yang memakai registry
// untuk meminta persetujuan, dan resultFormat untuk membentuk balasannya.
//
// Argumen tool diterima sebagai map[string]any (bukan struct ber-skema ketat)
// karena: (a) kontrak field-nya kurang terdokumentasi, dan (b) skema yang
// di-generate dari struct akan menolak object pada field bertipe json.RawMessage
// serta field tak dikenal (session_id, cwd, dst.). map[string]any menerima
// payload apa pun.
func Handler(reg *approval.Registry, resultFormat string) http.Handler {
	server := mcp.NewServer(&mcp.Implementation{Name: ServerName, Version: "v1.0.0"}, nil)

	mcp.AddTool(server, &mcp.Tool{
		Name:        ToolName,
		Description: "Meminta persetujuan pengguna sebelum sebuah tool dijalankan.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args map[string]any) (*mcp.CallToolResult, any, error) {
		toolName, _ := args["tool_name"].(string)
		input := inputJSON(args)
		allow := reg.Request(ctx, toolName, input)
		text := approval.Decision(resultFormat, allow, input, "Ditolak oleh pengguna lewat Telegram.")
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: text}},
		}, nil, nil
	})

	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return server
	}, nil)
}

// inputJSON mengambil argumen tool yang akan disetujui dari "input" atau
// "tool_input" (mana pun yang ada) dan mengembalikannya sebagai JSON mentah.
func inputJSON(args map[string]any) json.RawMessage {
	v, ok := args["input"]
	if !ok || v == nil {
		v = args["tool_input"]
	}
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return b
}
