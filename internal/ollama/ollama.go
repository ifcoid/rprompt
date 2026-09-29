// Package ollama adalah klien minimal API native Ollama (/api/chat, /api/tags)
// untuk engine Ollama pada HTTP API rprompt. Request OpenAI diterjemahkan ke
// API native (bukan diteruskan apa adanya) agar urutan pesan bisa dirapikan
// dan reasoning effort bisa dipetakan ke parameter "think".
package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
)

// Message adalah satu pesan pada /api/chat.
type Message struct {
	Role    string   `json:"role"`
	Content string   `json:"content"`
	Images  []string `json:"images,omitempty"` // base64 tanpa prefix "data:"
}

// ChatRequest adalah body /api/chat. Stream selalu diaktifkan oleh Chat.
type ChatRequest struct {
	Model    string         `json:"model"`
	Messages []Message      `json:"messages"`
	Stream   bool           `json:"stream"`
	Think    any            `json:"think,omitempty"` // nil | false | "low" | "medium" | "high"
	Options  map[string]any `json:"options,omitempty"`
}

// Result adalah hasil akhir satu panggilan Chat.
type Result struct {
	Content          string
	Thinking         string
	DoneReason       string // "stop" | "length" | ...
	PromptTokens     int
	CompletionTokens int // termasuk token thinking
}

// APIError adalah error yang dilaporkan Ollama ({"error": "..."}).
type APIError struct {
	StatusCode int
	Message    string
}

func (e *APIError) Error() string { return e.Message }

// Client memanggil server Ollama pada BaseURL.
type Client struct {
	BaseURL string
	HTTP    *http.Client
}

// New membuat Client. baseURL mis. "http://localhost:11434"; akhiran "/v1"
// (kebiasaan klien OpenAI) dibuang karena yang dipakai adalah API native.
func New(baseURL string) *Client {
	u := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	u = strings.TrimSuffix(u, "/v1")
	// Tanpa timeout klien: durasi dibatasi ctx pemanggil (output bisa panjang).
	return &Client{BaseURL: u, HTTP: &http.Client{}}
}

// Chat menjalankan /api/chat (streaming NDJSON). onDelta (boleh nil) dipanggil
// untuk tiap potongan content/thinking yang tidak kosong.
func (c *Client) Chat(ctx context.Context, req ChatRequest, onDelta func(content, thinking string)) (Result, error) {
	req.Stream = true
	resp, err := c.postChat(ctx, req)
	var ae *APIError
	if req.Think != nil && errors.As(err, &ae) && ae.StatusCode == http.StatusBadRequest &&
		strings.Contains(ae.Message, "does not support thinking") {
		// Model tanpa kemampuan thinking menolak "think" sama sekali; effort
		// diabaikan saja agar request tetap jalan.
		req.Think = nil
		resp, err = c.postChat(ctx, req)
	}
	if err != nil {
		return Result{}, err
	}
	defer resp.Body.Close()

	var res Result
	var content, thinking strings.Builder
	dec := json.NewDecoder(resp.Body)
	for {
		var ch struct {
			Message struct {
				Content  string `json:"content"`
				Thinking string `json:"thinking"`
			} `json:"message"`
			Done            bool   `json:"done"`
			DoneReason      string `json:"done_reason"`
			PromptEvalCount int    `json:"prompt_eval_count"`
			EvalCount       int    `json:"eval_count"`
			Error           string `json:"error"`
		}
		if err := dec.Decode(&ch); err != nil {
			if err == io.EOF {
				err = errors.New("stream Ollama berakhir sebelum selesai")
			}
			return res, err
		}
		if ch.Error != "" {
			return res, &APIError{StatusCode: http.StatusInternalServerError, Message: ch.Error}
		}
		if ch.Message.Content != "" || ch.Message.Thinking != "" {
			content.WriteString(ch.Message.Content)
			thinking.WriteString(ch.Message.Thinking)
			if onDelta != nil {
				onDelta(ch.Message.Content, ch.Message.Thinking)
			}
		}
		if ch.Done {
			res.Content = content.String()
			res.Thinking = thinking.String()
			res.DoneReason = ch.DoneReason
			res.PromptTokens = ch.PromptEvalCount
			res.CompletionTokens = ch.EvalCount
			return res, nil
		}
	}
}

// postChat mengirim request dan mengembalikan response 200, atau *APIError.
func (c *Client) postChat(ctx context.Context, req ChatRequest) (*http.Response, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	hr.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(hr)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		return nil, readAPIError(resp)
	}
	return resp, nil
}

// Models mengembalikan nama model lokal (/api/tags) yang bisa dipakai chat;
// model khusus embedding dilewati.
func (c *Client) Models(ctx context.Context) ([]string, error) {
	hr, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/api/tags", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(hr)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, readAPIError(resp)
	}
	var tags struct {
		Models []struct {
			Name         string   `json:"name"`
			Capabilities []string `json:"capabilities"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tags); err != nil {
		return nil, err
	}
	var names []string
	for _, m := range tags.Models {
		// Ollama lama tidak mengirim capabilities: anggap bisa chat.
		if len(m.Capabilities) > 0 && !slices.Contains(m.Capabilities, "completion") {
			continue
		}
		names = append(names, m.Name)
	}
	return names, nil
}

func readAPIError(resp *http.Response) error {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	var e struct {
		Error string `json:"error"`
	}
	msg := strings.TrimSpace(string(b))
	if json.Unmarshal(b, &e) == nil && e.Error != "" {
		msg = e.Error
	}
	if msg == "" {
		msg = resp.Status
	}
	return &APIError{StatusCode: resp.StatusCode, Message: msg}
}

// ThinkForEffort memetakan reasoning effort ala OpenAI ke parameter "think":
// "" -> nil (ikut default model), "none" -> false (thinking mati),
// minimal/low -> "low", medium -> "medium", high/xhigh -> "high".
func ThinkForEffort(effort string) (any, error) {
	switch strings.ToLower(strings.TrimSpace(effort)) {
	case "":
		return nil, nil
	case "none":
		return false, nil
	case "minimal", "low":
		return "low", nil
	case "medium":
		return "medium", nil
	case "high", "xhigh":
		return "high", nil
	}
	return nil, fmt.Errorf("reasoning effort %q tidak dikenal (none|minimal|low|medium|high|xhigh)", effort)
}

// Normalize merapikan urutan pesan agar aman untuk template model yang ketat.
// Sebagian template hanya menaruh prompt jawaban bila pesan terakhir adalah
// user, atau mewajibkan system di awal dan user/assistant berselang-seling;
// urutan lain (mis. user lalu system) membuat prompt rusak atau error.
//   - Semua pesan system/developer digabung (sesuai urutan) jadi SATU pesan
//     system di awal.
//   - Role lain selain user/assistant (mis. tool) diperlakukan sebagai user.
//   - Pesan tanpa teks & gambar dibuang.
//   - Pesan berurutan dengan role sama digabung, sehingga user/assistant
//     selalu berselang-seling.
//   - Bila tidak ada pesan user/assistant sama sekali, system dikirim sebagai
//     user (model butuh giliran untuk dijawab).
func Normalize(msgs []Message) []Message {
	var systems []string
	var convo []Message
	for _, m := range msgs {
		role := strings.ToLower(strings.TrimSpace(m.Role))
		text := strings.TrimSpace(m.Content)
		if text == "" && len(m.Images) == 0 {
			continue
		}
		switch role {
		case "system", "developer":
			if text != "" {
				systems = append(systems, text)
			}
			continue
		case "user", "assistant":
		default:
			role = "user"
		}
		if n := len(convo); n > 0 && convo[n-1].Role == role {
			prev := &convo[n-1]
			prev.Content = joinText(prev.Content, text)
			prev.Images = append(prev.Images, m.Images...)
			continue
		}
		convo = append(convo, Message{Role: role, Content: text, Images: slices.Clone(m.Images)})
	}

	if len(systems) == 0 {
		return convo
	}
	sys := Message{Role: "system", Content: strings.Join(systems, "\n\n")}
	if len(convo) == 0 {
		sys.Role = "user"
	}
	return append([]Message{sys}, convo...)
}

func joinText(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	}
	return a + "\n\n" + b
}
