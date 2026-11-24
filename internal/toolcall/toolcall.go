// Package toolcall memungkinkan engine CLI (claude, agy, codex) melayani tool
// calling ala OpenAI. CLI itu tidak bisa menerima definisi tool dari luar, jadi
// tool dijelaskan lewat teks, dan permintaan tool dibaca dari teks jawaban.
// Tool dijalankan oleh klien API, bukan oleh CLI: Runner wajib mematikan tool
// bawaan CLI.
package toolcall

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
)

// Runner adalah engine yang bisa menjawab satu giliran tanpa tool bawaan.
type Runner interface {
	RunTools(ctx context.Context, req Request) (Reply, error)
}

// Request adalah satu giliran untuk Runner.
type Request struct {
	System string // instruksi (system klien + aturan tool)
	Prompt string // transkrip percakapan
	Model  string // "" = default CLI
	Effort string // "" = default CLI
}

// Reply adalah jawaban mentah engine.
type Reply struct {
	Text      string
	InTokens  int // 0 bila engine tidak melaporkan
	OutTokens int
}

// Tool adalah definisi satu fungsi.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

// Call adalah satu permintaan tool dari model. Arguments berupa teks JSON; bisa
// tidak valid bila model salah tulis, dan klien yang menolaknya (seperti OpenAI).
type Call struct {
	ID        string
	Name      string
	Arguments string
}

// Message adalah satu pesan percakapan.
type Message struct {
	Role   string // system | user | assistant | tool
	Text   string
	Calls  []Call // assistant
	CallID string // tool
	Name   string // tool
}

// NewID membuat id untuk sebuah Call.
func NewID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "call_" + hex.EncodeToString(b)
}

// Choice adalah tool_choice: "" atau "auto", "none", "required", atau nama tool.
type Choice string

// Build menyusun Request dari percakapan klien. Model & Effort diisi pemanggil.
func Build(msgs []Message, tools []Tool, choice Choice, parallel bool) Request {
	var sys, convo strings.Builder
	names := map[string]string{}
	for _, m := range msgs {
		switch m.Role {
		case "system", "developer":
			if m.Text != "" {
				sys.WriteString(m.Text + "\n\n")
			}
		case "assistant":
			convo.WriteString("<assistant>\n")
			if m.Text != "" {
				convo.WriteString(guard(m.Text, "assistant") + "\n")
			}
			for _, c := range m.Calls {
				names[c.ID] = c.Name
				convo.WriteString(Format(c) + "\n")
			}
			convo.WriteString("</assistant>\n\n")
		case "tool":
			name := m.Name
			if name == "" {
				name = names[m.CallID]
			}
			fmt.Fprintf(&convo, "<tool_result name=%q>\n%s\n</tool_result>\n\n", name, guard(m.Text, "tool_result"))
		default:
			fmt.Fprintf(&convo, "<user>\n%s\n</user>\n\n", guard(m.Text, "user"))
		}
	}
	sys.WriteString(instructions(tools, choice, parallel))
	convo.WriteString("Write the next assistant message only, without the <assistant> tags.")
	return Request{System: sys.String(), Prompt: convo.String()}
}

// guard mencegah isi pesan menutup tag pembungkusnya.
func guard(s, tag string) string {
	return strings.ReplaceAll(s, "</"+tag, "<\\/"+tag)
}

// Format menuliskan Call dalam bentuk yang diminta dari model.
func Format(c Call) string {
	args := strings.TrimSpace(c.Arguments)
	if args == "" {
		args = "{}"
	}
	return fmt.Sprintf("<use_tool name=%q>\n%s\n</use_tool>", c.Name, args)
}

func instructions(tools []Tool, choice Choice, parallel bool) string {
	if len(tools) == 0 || choice == "none" {
		return "You have no tools in this turn. Do not write <use_tool>. Answer in text."
	}
	var b strings.Builder
	b.WriteString(`# Tools

Your only tools are listed below. They are run by the caller, on the caller's machine, not by you. Any other tool, file access, or shell you may have here is disabled and sees a different, empty directory: never use it.

To call a tool, write this block at the start of a line, outside any code fence:
<use_tool name="TOOL_NAME">
{"argument": "value"}
</use_tool>
The body is one JSON object matching the tool's parameters, with newlines in strings written as \n. Text before the block is shown to the user. Write nothing after the last block; the result arrives in the next message as <tool_result>. Never write <tool_result> yourself, and never claim a tool ran before you have its result. If no tool is needed, just answer.
`)
	if !parallel {
		b.WriteString("Call at most one tool per message.\n")
	}
	switch choice {
	case "", "auto":
	case "required":
		b.WriteString("You must call a tool in this message.\n")
	default:
		fmt.Fprintf(&b, "You must call the tool %q in this message.\n", string(choice))
	}
	b.WriteString("\nAvailable tools, one JSON object per line:\n")
	for _, t := range tools {
		j, err := json.Marshal(t)
		if err != nil {
			continue
		}
		b.Write(j)
		b.WriteByte('\n')
	}
	return b.String()
}

var (
	openRe  = regexp.MustCompile(`^[ \t]*<use_tool\s+name=["']([^"'<>\s]+)["']\s*>`)
	fenceRe = regexp.MustCompile("^ {0,3}(```+|~~~+)")
)

const closeTag = "</use_tool>"

// Parse memisahkan teks jawaban dari permintaan tool di dalamnya. Blok hanya
// dikenali di awal baris, di luar blok kode, dan bila nama toolnya dikenal;
// selain itu dibiarkan sebagai teks. Setelah blok pertama hanya blok lain yang
// dibaca: sisanya dibuang karena model bisa mengarang hasil tool.
func Parse(text string, tools []Tool) (string, []Call) {
	known := map[string]bool{}
	for _, t := range tools {
		known[t.Name] = true
	}
	fence := ""
	for pos := 0; pos < len(text); {
		end := strings.IndexByte(text[pos:], '\n')
		line := text[pos:]
		if end >= 0 {
			line = text[pos : pos+end]
		}
		if f := fenceRe.FindStringSubmatch(line); f != nil {
			switch {
			case fence == "":
				fence = f[1]
			case strings.HasPrefix(f[1], fence) && strings.TrimSpace(line) == f[1]:
				fence = ""
			}
		} else if fence == "" {
			if c, n, ok := parseOne(text[pos:], known); ok {
				calls := []Call{c}
				rest := text[pos+n:]
				for {
					trimmed := strings.TrimLeft(rest, " \t\r\n")
					c, n, ok := parseOne(trimmed, known)
					if !ok {
						return strings.TrimSpace(text[:pos]), calls
					}
					calls = append(calls, c)
					rest = trimmed[n:]
				}
			}
		}
		if end < 0 {
			break
		}
		pos += end + 1
	}
	return strings.TrimSpace(text), nil
}

// parseOne membaca satu blok di awal s dan mengembalikan panjangnya.
func parseOne(s string, known map[string]bool) (Call, int, bool) {
	m := openRe.FindStringSubmatchIndex(s)
	if m == nil || !known[s[m[2]:m[3]]] {
		return Call{}, 0, false
	}
	c := Call{ID: NewID(), Name: s[m[2]:m[3]]}
	body := s[m[1]:]
	// JSON dibaca lebih dulu: isi argumen boleh memuat teks penutup tag.
	dec := json.NewDecoder(strings.NewReader(body))
	var raw json.RawMessage
	if err := dec.Decode(&raw); err == nil && bytes.HasPrefix(raw, []byte("{")) {
		n := int(dec.InputOffset())
		rest := body[n:]
		skip := len(rest) - len(strings.TrimLeft(rest, " \t\r\n"))
		if strings.HasPrefix(rest[skip:], closeTag) {
			n += skip + len(closeTag)
		}
		c.Arguments = compact(raw)
		return c, m[1] + n, true
	}
	// Tanpa penutup berarti terpotong: jangan dianggap permintaan tool.
	i := strings.Index(body, closeTag)
	if i < 0 {
		return Call{}, 0, false
	}
	c.Arguments = strings.TrimSpace(body[:i])
	if c.Arguments == "" {
		c.Arguments = "{}"
	}
	return c, m[1] + i + len(closeTag), true
}

func compact(raw []byte) string {
	var b bytes.Buffer
	if json.Compact(&b, raw) != nil {
		return string(raw)
	}
	return b.String()
}

// TempDir membuat folder kosong sebagai tempat kerja CLI, agar instruksi proyek
// (CLAUDE.md, AGENTS.md) dan file di WORK_DIR tidak terbaca. Pemanggil wajib
// menjalankan cleanup.
func TempDir() (dir string, cleanup func(), err error) {
	dir, err = os.MkdirTemp("", "rprompt-tool-")
	if err != nil {
		return "", nil, err
	}
	return dir, func() { _ = os.RemoveAll(dir) }, nil
}
