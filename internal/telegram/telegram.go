// Package telegram berisi client minimal untuk Telegram Bot API
// (hanya fitur yang dibutuhkan service ini) dan tipe-tipe update.
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// maxMessageLen adalah batas panjang satu pesan Telegram (4096 karakter).
const maxMessageLen = 4096

// Update merepresentasikan satu update yang dikirim Telegram ke webhook.
type Update struct {
	UpdateID      int64          `json:"update_id"`
	Message       *Message       `json:"message"`
	CallbackQuery *CallbackQuery `json:"callback_query"`
}

// CallbackQuery adalah penekanan tombol inline.
type CallbackQuery struct {
	ID      string   `json:"id"`
	From    *User    `json:"from"`
	Message *Message `json:"message"` // pesan tempat tombol menempel
	Data    string   `json:"data"`    // callback_data tombol yang ditekan
}

// InlineButton adalah satu tombol inline dengan callback_data.
type InlineButton struct {
	Text string
	Data string
}

// Message adalah pesan dari pengguna.
type Message struct {
	MessageID int64       `json:"message_id"`
	From      *User       `json:"from"`
	Chat      Chat        `json:"chat"`
	Text      string      `json:"text"`
	Caption   string      `json:"caption"`
	Photo     []PhotoSize `json:"photo"`
	Document  *Document   `json:"document"`
}

// User adalah pengirim pesan.
type User struct {
	ID        int64  `json:"id"`
	Username  string `json:"username"`
	FirstName string `json:"first_name"`
}

// Chat adalah percakapan tempat pesan berada.
type Chat struct {
	ID int64 `json:"id"`
}

// PhotoSize adalah satu varian resolusi sebuah foto.
type PhotoSize struct {
	FileID   string `json:"file_id"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
	FileSize int64  `json:"file_size"`
}

// Document adalah berkas non-foto.
type Document struct {
	FileID   string `json:"file_id"`
	FileName string `json:"file_name"`
	MimeType string `json:"mime_type"`
	FileSize int64  `json:"file_size"`
}

// Client adalah HTTP client untuk Telegram Bot API.
type Client struct {
	token string
	http  *http.Client
}

// New membuat client baru dengan token bot.
func New(token string) *Client {
	return &Client{
		token: token,
		http:  &http.Client{Timeout: 60 * time.Second},
	}
}

func (c *Client) apiURL(method string) string {
	return fmt.Sprintf("https://api.telegram.org/bot%s/%s", c.token, method)
}

// apiResponse adalah amplop respons standar Telegram.
type apiResponse struct {
	OK          bool            `json:"ok"`
	Description string          `json:"description"`
	Result      json.RawMessage `json:"result"`
}

// Send mengirim satu pesan teks (diasumsikan <= batas) dan mengembalikan
// message_id-nya. Dipakai untuk pesan yang akan diedit (streaming).
func (c *Client) Send(ctx context.Context, chatID int64, text string) (int64, error) {
	var out struct {
		MessageID int64 `json:"message_id"`
	}
	err := c.callText(ctx, "sendMessage", map[string]any{
		"chat_id": chatID,
		"text":    nonEmpty(text),
	}, &out)
	return out.MessageID, err
}

// EditMessageText mengubah teks pesan yang sudah dikirim. Galat "message is not
// modified" diperlakukan sebagai sukses.
func (c *Client) EditMessageText(ctx context.Context, chatID, messageID int64, text string) error {
	err := c.callText(ctx, "editMessageText", map[string]any{
		"chat_id":    chatID,
		"message_id": messageID,
		"text":       nonEmpty(text),
	}, nil)
	if err != nil && strings.Contains(err.Error(), "not modified") {
		return nil
	}
	return err
}

// SendMessage mengirim teks ke chat, memecah otomatis bila melebihi batas.
func (c *Client) SendMessage(ctx context.Context, chatID int64, text string) error {
	for _, chunk := range splitText(text, maxMessageLen) {
		if _, err := c.Send(ctx, chatID, chunk); err != nil {
			return err
		}
	}
	return nil
}

// SendButtons mengirim pesan teks dengan keyboard inline (baris tombol) dan
// mengembalikan message_id-nya.
func (c *Client) SendButtons(ctx context.Context, chatID int64, text string, rows [][]InlineButton) (int64, error) {
	var out struct {
		MessageID int64 `json:"message_id"`
	}
	err := c.callText(ctx, "sendMessage", map[string]any{
		"chat_id":      chatID,
		"text":         nonEmpty(text),
		"reply_markup": inlineKeyboard(rows),
	}, &out)
	return out.MessageID, err
}

// AnswerCallbackQuery wajib dipanggil untuk menutup status "loading" tombol.
func (c *Client) AnswerCallbackQuery(ctx context.Context, callbackID, text string) error {
	return c.callJSON(ctx, "answerCallbackQuery", map[string]any{
		"callback_query_id": callbackID,
		"text":              text,
	}, nil)
}

// EditTextClearButtons mengubah teks pesan sekaligus menghapus keyboard inline.
func (c *Client) EditTextClearButtons(ctx context.Context, chatID, messageID int64, text string) error {
	err := c.callText(ctx, "editMessageText", map[string]any{
		"chat_id":      chatID,
		"message_id":   messageID,
		"text":         nonEmpty(text),
		"reply_markup": map[string]any{"inline_keyboard": [][]any{}},
	}, nil)
	if err != nil && strings.Contains(err.Error(), "not modified") {
		return nil
	}
	return err
}

// inlineKeyboard membangun struktur reply_markup Telegram dari baris tombol.
func inlineKeyboard(rows [][]InlineButton) map[string]any {
	kb := make([][]map[string]string, 0, len(rows))
	for _, row := range rows {
		cells := make([]map[string]string, 0, len(row))
		for _, b := range row {
			cells = append(cells, map[string]string{"text": b.Text, "callback_data": b.Data})
		}
		kb = append(kb, cells)
	}
	return map[string]any{"inline_keyboard": kb}
}

// SendChatAction menampilkan indikator seperti "typing..." pada chat.
func (c *Client) SendChatAction(ctx context.Context, chatID int64, action string) error {
	return c.callJSON(ctx, "sendChatAction", map[string]any{
		"chat_id": chatID,
		"action":  action,
	}, nil)
}

// GetUpdates menarik update via long polling. offset = update_id berikutnya yang
// diharapkan; timeoutSeconds = lama Telegram menahan koneksi bila belum ada
// update. Dipakai pada mode polling (alternatif webhook, tanpa URL publik).
func (c *Client) GetUpdates(ctx context.Context, offset int64, timeoutSeconds int) ([]Update, error) {
	var out []Update
	err := c.callJSON(ctx, "getUpdates", map[string]any{
		"offset":          offset,
		"timeout":         timeoutSeconds,
		"allowed_updates": []string{"message", "callback_query"},
	}, &out)
	return out, err
}

// SetWebhook mendaftarkan URL webhook beserta secret_token ke Telegram.
func (c *Client) SetWebhook(ctx context.Context, webhookURL, secret string) error {
	form := url.Values{}
	form.Set("url", webhookURL)
	if secret != "" {
		form.Set("secret_token", secret)
	}
	form.Set("allowed_updates", `["message","callback_query"]`)
	form.Set("drop_pending_updates", "true")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.apiURL("setWebhook"),
		strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return c.do(req, nil)
}

// DeleteWebhook menghapus webhook yang terdaftar.
func (c *Client) DeleteWebhook(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.apiURL("deleteWebhook"), nil)
	if err != nil {
		return err
	}
	return c.do(req, nil)
}

// GetFile mengembalikan file_path internal Telegram untuk sebuah file_id.
func (c *Client) GetFile(ctx context.Context, fileID string) (string, error) {
	var out struct {
		FilePath string `json:"file_path"`
	}
	if err := c.callJSON(ctx, "getFile", map[string]any{"file_id": fileID}, &out); err != nil {
		return "", err
	}
	return out.FilePath, nil
}

// DownloadFile mengunduh berkas (berdasarkan file_id) ke direktori dir.
// Bila name kosong, nama diambil dari file_path Telegram. Mengembalikan path
// absolut berkas yang tersimpan.
func (c *Client) DownloadFile(ctx context.Context, fileID, dir, name string) (string, error) {
	filePath, err := c.GetFile(ctx, fileID)
	if err != nil {
		return "", err
	}
	if name == "" {
		name = filepath.Base(filePath)
	}
	if name == "" || name == "." {
		name = "file"
	}

	dlURL := fmt.Sprintf("https://api.telegram.org/file/bot%s/%s", c.token, filePath)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, dlURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unduh berkas: status %s", resp.Status)
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	dest := filepath.Join(dir, name)
	f, err := os.Create(dest)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := io.Copy(f, resp.Body); err != nil {
		return "", err
	}
	abs, err := filepath.Abs(dest)
	if err != nil {
		return dest, nil
	}
	return abs, nil
}

// SendFile mengirim berkas lokal ke chat. Berkas gambar berukuran wajar
// dikirim sebagai foto; selebihnya sebagai dokumen.
func (c *Client) SendFile(ctx context.Context, chatID int64, path, caption string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	const photoLimit = 10 << 20 // 10 MB: batas aman pengiriman sebagai foto
	if isImageExt(path) && info.Size() <= photoLimit {
		return c.uploadFile(ctx, "sendPhoto", "photo", chatID, path, caption)
	}
	return c.uploadFile(ctx, "sendDocument", "document", chatID, path, caption)
}

func (c *Client) uploadFile(ctx context.Context, method, field string, chatID int64, path, caption string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("chat_id", strconv.FormatInt(chatID, 10))
	if caption != "" {
		_ = mw.WriteField("caption", caption)
	}
	part, err := mw.CreateFormFile(field, filepath.Base(path))
	if err != nil {
		return err
	}
	if _, err := io.Copy(part, f); err != nil {
		return err
	}
	if err := mw.Close(); err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.apiURL(method), &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return c.do(req, nil)
}

// callJSON mengirim payload JSON ke method dan mengisi out dengan field result.
func (c *Client) callJSON(ctx context.Context, method string, payload map[string]any, out any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.apiURL(method), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	return c.do(req, out)
}

// callText memanggil method yang berisi field "text": teks dikonversi ke HTML
// (parse_mode=HTML) agar format markdown Claude ter-render rapi di Telegram.
// Bila Telegram menolak karena entitas HTML tak valid atau teks kepanjangan
// (markup bisa memperpanjang teks), pesan dikirim ulang sebagai teks polos agar
// tetap sampai ke pengguna.
func (c *Client) callText(ctx context.Context, method string, payload map[string]any, out any) error {
	raw, _ := payload["text"].(string)
	payload["text"] = mdToHTML(raw)
	payload["parse_mode"] = "HTML"
	err := c.callJSON(ctx, method, payload, out)
	if err != nil && isFormatError(err) {
		delete(payload, "parse_mode")
		payload["text"] = raw
		return c.callJSON(ctx, method, payload, out)
	}
	return err
}

// isFormatError benar bila galat berasal dari parse_mode HTML (entitas tak valid
// atau pesan terlalu panjang setelah markup) sehingga layak dikirim ulang polos.
func isFormatError(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "parse entities") || strings.Contains(msg, "too long")
}

func (c *Client) do(req *http.Request, out any) error {
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	var ar apiResponse
	if err := json.Unmarshal(data, &ar); err != nil {
		return fmt.Errorf("telegram %s: respons tak terbaca: %s", resp.Status, strings.TrimSpace(string(data)))
	}
	if !ar.OK {
		return fmt.Errorf("telegram %s: %s", method(req), strings.TrimSpace(ar.Description))
	}
	if out != nil && len(ar.Result) > 0 {
		return json.Unmarshal(ar.Result, out)
	}
	return nil
}

func method(req *http.Request) string {
	return filepath.Base(req.URL.Path)
}

func nonEmpty(s string) string {
	if strings.TrimSpace(s) == "" {
		return "…"
	}
	return s
}

func isImageExt(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp":
		return true
	}
	return false
}

// splitText memecah teks menjadi potongan <= limit karakter (rune), sebisa
// mungkin pada batas baris, tanpa memotong karakter multibyte di tengah.
func splitText(text string, limit int) []string {
	if strings.TrimSpace(text) == "" {
		return []string{"(kosong)"}
	}
	var chunks []string
	runes := []rune(text)
	for len(runes) > limit {
		window := string(runes[:limit])
		cut := strings.LastIndex(window, "\n")
		if cut <= 0 {
			chunks = append(chunks, window)
			runes = runes[limit:]
			continue
		}
		head := []rune(window[:cut])
		chunks = append(chunks, string(head))
		runes = runes[len(head)+1:] // +1 untuk membuang '\n'
	}
	if len(runes) > 0 {
		chunks = append(chunks, string(runes))
	}
	return chunks
}
