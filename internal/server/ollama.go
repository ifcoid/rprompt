package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/awangga/rprompt/internal/ollama"
	"github.com/awangga/rprompt/internal/toolcall"
)

// ollamaPrefix menandai model yang diarahkan ke Ollama: "ollama/<nama>",
// mis. "ollama/qwen3.5:9b". Prefix dibuang sebelum dikirim ke Ollama.
const ollamaPrefix = "ollama/"

// isOllamaModel mengembalikan true bila nama model menunjuk ke engine Ollama.
func isOllamaModel(model string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(model)), ollamaPrefix)
}

// reasoningParam adalah {"effort": "..."} (Responses API / gaya OpenRouter).
type reasoningParam struct {
	Effort string `json:"effort"`
}

// chatToOllama menerjemahkan request Chat Completions ke /api/chat Ollama.
func chatToOllama(req *chatCompletionRequest) (ollama.ChatRequest, error) {
	tools, _, err := requestTools(req.Tools, req.ToolChoice)
	if err != nil {
		return ollama.ChatRequest{}, err
	}
	var msgs []ollama.Message
	names := map[string]string{} // id tool call -> nama tool
	for i, m := range req.Messages {
		text, imgs, err := contentParts(m.Content)
		if err != nil {
			return ollama.ChatRequest{}, fmt.Errorf("messages[%d]: %w", i, err)
		}
		msg := ollama.Message{Role: m.Role, Content: text, Images: imgs}
		for _, c := range m.ToolCalls {
			names[c.ID] = c.Function.Name
			msg.ToolCalls = append(msg.ToolCalls, ollamaCall(c.Function.Name, c.Function.Arguments))
		}
		if m.Role == "tool" {
			if msg.ToolName = names[m.ToolCallID]; msg.ToolName == "" {
				msg.ToolName = m.Name
			}
		}
		msgs = append(msgs, msg)
	}
	effort := req.ReasoningEffort
	if effort == "" && req.Reasoning != nil {
		effort = req.Reasoning.Effort
	}
	maxTokens := req.MaxCompletionTokens
	if maxTokens == nil {
		maxTokens = req.MaxTokens
	}
	return buildOllamaRequest(req.Model, msgs, tools, effort, ollamaOptions(req.Temperature, req.TopP, maxTokens))
}

// responsesToOllama menerjemahkan request Responses API ke /api/chat Ollama.
// "instructions" menjadi pesan system; item "reasoning" (hasil giliran
// sebelumnya yang dikirim ulang klien) dilewati.
func responsesToOllama(req *responsesRequest) (ollama.ChatRequest, error) {
	tools, _, err := requestTools(req.chatTools(), req.ToolChoice)
	if err != nil {
		return ollama.ChatRequest{}, err
	}
	var msgs []ollama.Message
	names := map[string]string{} // call_id -> nama tool
	if req.Instructions != "" {
		msgs = append(msgs, ollama.Message{Role: "system", Content: req.Instructions})
	}
	var s string
	if json.Unmarshal(req.Input, &s) == nil {
		msgs = append(msgs, ollama.Message{Role: "user", Content: s})
	} else {
		var items []struct {
			Type    string          `json:"type"`
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`

			CallID    string          `json:"call_id"`   // function_call & function_call_output
			Name      string          `json:"name"`      // function_call
			Arguments string          `json:"arguments"` // function_call
			Output    json.RawMessage `json:"output"`    // function_call_output
		}
		if err := json.Unmarshal(req.Input, &items); err != nil {
			return ollama.ChatRequest{}, errors.New("'input' harus string atau array item")
		}
		for i, it := range items {
			switch it.Type {
			case "", "message":
			case "reasoning":
				continue
			case "function_call":
				names[it.CallID] = it.Name
				call := ollamaCall(it.Name, it.Arguments)
				// Beberapa call dalam satu giliran dikirim sebagai item terpisah.
				if n := len(msgs); n > 0 && msgs[n-1].Role == "assistant" {
					msgs[n-1].ToolCalls = append(msgs[n-1].ToolCalls, call)
				} else {
					msgs = append(msgs, ollama.Message{Role: "assistant", ToolCalls: []ollama.ToolCall{call}})
				}
				continue
			case "function_call_output":
				text, _, err := contentParts(it.Output)
				if err != nil {
					text = string(it.Output)
				}
				msgs = append(msgs, ollama.Message{Role: "tool", Content: text, ToolName: names[it.CallID]})
				continue
			default:
				return ollama.ChatRequest{}, fmt.Errorf("input[%d]: item bertipe %q belum didukung", i, it.Type)
			}
			text, imgs, err := contentParts(it.Content)
			if err != nil {
				return ollama.ChatRequest{}, fmt.Errorf("input[%d]: %w", i, err)
			}
			msgs = append(msgs, ollama.Message{Role: it.Role, Content: text, Images: imgs})
		}
	}
	var effort string
	if req.Reasoning != nil {
		effort = req.Reasoning.Effort
	}
	return buildOllamaRequest(req.Model, msgs, tools, effort, ollamaOptions(req.Temperature, req.TopP, req.MaxOutputTokens))
}

// buildOllamaRequest merapikan urutan pesan (ollama.Normalize) dan memetakan
// effort ke "think".
func buildOllamaRequest(model string, msgs []ollama.Message, tools []toolcall.Tool, effort string, opts map[string]any) (ollama.ChatRequest, error) {
	name := strings.TrimSpace(model)[len(ollamaPrefix):]
	if name == "" {
		return ollama.ChatRequest{}, errors.New("nama model Ollama kosong (format: ollama/<nama>)")
	}
	think, err := ollama.ThinkForEffort(effort)
	if err != nil {
		return ollama.ChatRequest{}, err
	}
	msgs = ollama.Normalize(msgs)
	if len(msgs) == 0 {
		return ollama.ChatRequest{}, errors.New("pesan kosong atau tanpa teks")
	}
	return ollama.ChatRequest{Model: name, Messages: msgs, Think: think, Options: opts, Tools: ollamaTools(tools)}, nil
}

// ollamaOptions memetakan parameter sampling OpenAI ke "options" Ollama.
func ollamaOptions(temperature, topP *float64, maxTokens *int) map[string]any {
	opts := map[string]any{}
	if temperature != nil {
		opts["temperature"] = *temperature
	}
	if topP != nil {
		opts["top_p"] = *topP
	}
	if maxTokens != nil && *maxTokens > 0 {
		opts["num_predict"] = *maxTokens
	}
	if len(opts) == 0 {
		return nil
	}
	return opts
}

// contentParts mengambil teks & gambar dari content yang berupa string atau
// array bagian: text/input_text/output_text dan image_url/input_image (data
// URL base64). Teks antar-bagian dipisah baris baru.
func contentParts(raw json.RawMessage) (string, []string, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil, nil
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s, nil, nil
	}
	var parts []struct {
		Type     string          `json:"type"`
		Text     string          `json:"text"`
		ImageURL json.RawMessage `json:"image_url"`
	}
	if err := json.Unmarshal(raw, &parts); err != nil {
		return "", nil, errors.New("'content' harus string atau array bagian")
	}
	var texts, imgs []string
	for _, p := range parts {
		switch p.Type {
		case "text", "input_text", "output_text":
			if p.Text != "" {
				texts = append(texts, p.Text)
			}
		case "image_url", "input_image":
			img, err := imageBase64(p.ImageURL)
			if err != nil {
				return "", nil, err
			}
			imgs = append(imgs, img)
		case "refusal":
		default:
			return "", nil, fmt.Errorf("bagian konten %q belum didukung", p.Type)
		}
	}
	return strings.Join(texts, "\n"), imgs, nil
}

// imageBase64 mengambil data base64 dari image_url, baik string (Responses)
// maupun {"url": ...} (Chat Completions).
func imageBase64(raw json.RawMessage) (string, error) {
	var url string
	if json.Unmarshal(raw, &url) != nil {
		var obj struct {
			URL string `json:"url"`
		}
		_ = json.Unmarshal(raw, &obj)
		url = obj.URL
	}
	const marker = ";base64,"
	i := strings.Index(url, marker)
	if !strings.HasPrefix(url, "data:") || i < 0 {
		return "", errors.New("gambar harus berupa data URL base64 (URL http/file_id belum didukung)")
	}
	return url[i+len(marker):], nil
}

// writeOllamaError membalas error Ollama: 4xx diteruskan (mis. 404 model tidak
// ada), selain itu 502.
func writeOllamaError(w http.ResponseWriter, err error) {
	var ae *ollama.APIError
	if errors.As(err, &ae) && ae.StatusCode >= 400 && ae.StatusCode < 500 {
		writeOpenAIError(w, ae.StatusCode, "Ollama error: "+ae.Message, "invalid_request_error")
		return
	}
	writeOpenAIError(w, http.StatusBadGateway, "Ollama error: "+err.Error(), "api_error")
}

// finishReason memetakan done_reason Ollama ke finish_reason OpenAI.
func finishReason(doneReason string) string {
	if doneReason == "length" {
		return "length"
	}
	return "stop"
}
