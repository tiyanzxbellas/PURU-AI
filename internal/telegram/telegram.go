// Package telegram is a minimal long-polling Telegram Bot API client. It is
// deliberately a hand-rolled HTTP client so the bot keeps single-threaded
// sequential update processing and precise conflict (409) detection, matching
// the previous grammY setup (bot.start) behaviour.
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
	"strconv"
	"strings"
	"time"
)

// BaseURL is the Telegram Bot API base URL. It is a var so tests can point the
// client at an httptest server; production always uses the real endpoint.
var BaseURL = "https://api.telegram.org"

type API struct {
	token   string
	http    *http.Client
	baseURL string
}

func New(token string, hc *http.Client) *API {
	if hc == nil {
		hc = &http.Client{Timeout: 70 * time.Second}
	}
	return &API{token: token, http: hc, baseURL: BaseURL}
}

// TelegramError is a non-OK response from the Bot API.
type TelegramError struct {
	Code    int
	Message string
	Method  string
}

func (e *TelegramError) Error() string {
	return fmt.Sprintf("telegram %s: %d %s", e.Method, e.Code, e.Message)
}

// IsConflict matches the old conflict detection in index.ts.
func (e *TelegramError) IsConflict() bool {
	return e.Code == 409 || strings.Contains(strings.ToLower(e.Message), "conflict")
}

type envelope struct {
	OK          bool            `json:"ok"`
	Result      json.RawMessage `json:"result"`
	Description string          `json:"description"`
	ErrorCode   int             `json:"error_code"`
}

// ---------------------------------------------------------------------------
// Data model (subset needed by the bot)
// ---------------------------------------------------------------------------

type Update struct {
	UpdateID int64    `json:"update_id"`
	Message  *Message `json:"message"`
}

type User struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
}

type Chat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

type Document struct {
	FileID   string `json:"file_id"`
	FileName string `json:"file_name"`
	FileSize int64  `json:"file_size"`
}

// PhotoSize is one entry of a photo message: Telegram sends the same image in
// several resolutions; the largest one is what the bot downloads and analyses.
type PhotoSize struct {
	FileID   string `json:"file_id"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
	FileSize int64  `json:"file_size"`
}

type Message struct {
	MessageID int64       `json:"message_id"`
	From      *User       `json:"from"`
	Chat      *Chat       `json:"chat"`
	Text      string      `json:"text"`
	Caption   string      `json:"caption"`
	Document  *Document   `json:"document"`
	Photo     []PhotoSize `json:"photo"`
}

// File is the getFile result (used to download documents).
type File struct {
	FileID   string `json:"file_id"`
	FilePath string `json:"file_path"`
}

// ---------------------------------------------------------------------------
// Low-level calls
// ---------------------------------------------------------------------------

type uploadFile struct {
	field    string
	filename string
	data     []byte
}

func (a *API) do(ctx context.Context, methodName string, params url.Values, up *uploadFile) (json.RawMessage, error) {
	u := a.baseURL + "/bot" + a.token + "/" + methodName

	var req *http.Request
	var err error
	if up == nil {
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, u, strings.NewReader(params.Encode()))
		if err == nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
	} else {
		buf := &bytes.Buffer{}
		w := multipart.NewWriter(buf)
		for k, vs := range params {
			for _, v := range vs {
				_ = w.WriteField(k, v)
			}
		}
		fw, err2 := w.CreateFormFile(up.field, up.filename)
		if err2 != nil {
			return nil, err2
		}
		_, _ = fw.Write(up.data)
		_ = w.Close()
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, u, buf)
		if err == nil {
			req.Header.Set("Content-Type", w.FormDataContentType())
		}
	}
	if err != nil {
		return nil, err
	}

	resp, err := a.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var out envelope
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("telegram %s: invalid response: %s", methodName, truncate(string(body), 300))
	}
	if !out.OK {
		return nil, &TelegramError{Code: out.ErrorCode, Message: out.Description, Method: methodName}
	}
	return out.Result, nil
}

func params(m map[string]any) url.Values {
	v := url.Values{}
	for k, val := range m {
		v.Set(k, toParam(val))
	}
	return v
}

func toParam(v any) string {
	switch t := v.(type) {
	case string:
		return sanitizeText(t)
	case int:
		return strconv.Itoa(t)
	case int64:
		return strconv.FormatInt(t, 10)
	case bool:
		if t {
			return "true"
		}
		return "false"
	default:
		b, _ := json.Marshal(t)
		return string(b)
	}
}

// sanitizeText replaces invalid UTF-8 byte sequences (which can leak in from
// model output or scraped content) with U+FFFD so Telegram never rejects the
// request with "400 text must be encoded in UTF-8".
func sanitizeText(s string) string {
	return strings.ToValidUTF8(s, "\uFFFD")
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// ---------------------------------------------------------------------------
// Bot API methods used by the app
// ---------------------------------------------------------------------------

func (a *API) GetMe(ctx context.Context) (*User, error) {
	raw, err := a.do(ctx, "getMe", url.Values{}, nil)
	if err != nil {
		return nil, err
	}
	var u User
	if err := json.Unmarshal(raw, &u); err != nil {
		return nil, err
	}
	return &u, nil
}

func (a *API) DeleteWebhook(ctx context.Context, dropPending bool) error {
	v := url.Values{}
	if dropPending {
		v.Set("drop_pending_updates", "true")
	}
	_, err := a.do(ctx, "deleteWebhook", v, nil)
	return err
}

// GetUpdates long-polls (timeout seconds). Multiple updates can be returned;
// the caller processes them sequentially.
func (a *API) GetUpdates(ctx context.Context, offset int64, timeout int) ([]Update, error) {
	v := url.Values{}
	v.Set("offset", strconv.FormatInt(offset, 10))
	v.Set("timeout", strconv.Itoa(timeout))
	v.Set("allowed_updates", `["message"]`)
	raw, err := a.do(ctx, "getUpdates", v, nil)
	if err != nil {
		return nil, err
	}
	var updates []Update
	if err := json.Unmarshal(raw, &updates); err != nil {
		return nil, err
	}
	return updates, nil
}

func (a *API) SendMessage(ctx context.Context, chatID int64, text string, opts map[string]any) (json.RawMessage, error) {
	v := params(opts)
	v.Set("chat_id", strconv.FormatInt(chatID, 10))
	v.Set("text", sanitizeText(text))
	return a.do(ctx, "sendMessage", v, nil)
}

func (a *API) EditMessageText(ctx context.Context, chatID int64, messageID int64, text string, opts map[string]any) (json.RawMessage, error) {
	v := params(opts)
	v.Set("chat_id", strconv.FormatInt(chatID, 10))
	v.Set("message_id", strconv.FormatInt(messageID, 10))
	v.Set("text", sanitizeText(text))
	return a.do(ctx, "editMessageText", v, nil)
}

func (a *API) DeleteMessage(ctx context.Context, chatID int64, messageID int64) error {
	v := url.Values{}
	v.Set("chat_id", strconv.FormatInt(chatID, 10))
	v.Set("message_id", strconv.FormatInt(messageID, 10))
	_, err := a.do(ctx, "deleteMessage", v, nil)
	return err
}

func (a *API) SendFile(ctx context.Context, chatID int64, filename string, data []byte, method string, opts map[string]any) (json.RawMessage, error) {
	v := params(opts)
	v.Set("chat_id", strconv.FormatInt(chatID, 10))
	up := &uploadFile{field: methodField(method), filename: filename, data: data}
	return a.do(ctx, method, v, up)
}

func methodField(method string) string {
	switch method {
	case "sendDocument":
		return "document"
	case "sendAudio":
		return "audio"
	case "sendVideo":
		return "video"
	case "sendPhoto":
		return "photo"
	default:
		return "document"
	}
}

func (a *API) GetFile(ctx context.Context, fileID string) (*File, error) {
	v := url.Values{}
	v.Set("file_id", fileID)
	raw, err := a.do(ctx, "getFile", v, nil)
	if err != nil {
		return nil, err
	}
	var f File
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, err
	}
	return &f, nil
}

func (a *API) DownloadFile(ctx context.Context, filePath string) ([]byte, error) {
	u := a.baseURL + "/file/bot" + a.token + "/" + filePath
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := a.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("telegram download: HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}
