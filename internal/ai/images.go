package ai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// visionTimeout bounds one vision-model call.
const visionTimeout = 120 * time.Second

// geminiRequest/… mirror the Google Gemini generateContent wire format used by
// Gemini-style gateways (contents → parts → text / inlineData).
type geminiRequest struct {
	Contents []geminiContent `json:"contents"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text       *string     `json:"text,omitempty"`
	InlineData *geminiData `json:"inlineData,omitempty"`
}

type geminiData struct {
	MIMEType string `json:"mimeType"`
	Data     string `json:"data"`
}

type geminiResponse struct {
	Candidates []struct {
		Content struct {
			Parts []struct {
				Text string `json:"text"`
			} `json:"parts"`
		} `json:"content"`
	} `json:"candidates"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// DescribeImage sends one image + a prompt to a Gemini-style vision endpoint
// and returns the model's answer text. It is used by the app's upload pipeline
// (summarising a user photo) and by the CLI -image probe.
func DescribeImage(ctx context.Context, hc *http.Client, endpoint, prompt string, image []byte, mime string) (string, error) {
	if strings.TrimSpace(endpoint) == "" {
		return "", fmt.Errorf("model visi tidak dikonfigurasi (VISION_MODEL_URL kosong)")
	}
	if len(image) == 0 {
		return "", fmt.Errorf("data gambar kosong")
	}
	if mime == "" {
		mime = sniffImageMIME(image)
	}
	promptText := prompt
	reqBody := geminiRequest{Contents: []geminiContent{{
		Role: "user",
		Parts: []geminiPart{
			{Text: &promptText},
			{InlineData: &geminiData{MIMEType: mime, Data: base64.StdEncoding.EncodeToString(image)}},
		},
	}}}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("gagal menyusun request model visi: %v", err)
	}

	ctx, cancel := context.WithTimeout(ctx, visionTimeout)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("request model visi tidak valid: %v", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if hc == nil {
		return "", fmt.Errorf("http client tidak tersedia")
	}
	resp, err := hc.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("gagal menghubungi model visi: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", fmt.Errorf("gagal membaca jawaban model visi: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		msg := strings.TrimSpace(string(body))
		if len(msg) > 400 {
			msg = msg[:400]
		}
		return "", fmt.Errorf("model visi: HTTP %d: %s", resp.StatusCode, msg)
	}

	var gr geminiResponse
	if err := json.Unmarshal(body, &gr); err != nil {
		return "", fmt.Errorf("jawaban model visi tidak valid: %v", err)
	}
	if gr.Error != nil && gr.Error.Message != "" {
		return "", fmt.Errorf("model visi: %s", gr.Error.Message)
	}
	if len(gr.Candidates) == 0 || len(gr.Candidates[0].Content.Parts) == 0 {
		return "", fmt.Errorf("model visi: tidak ada kandidat jawaban (model tidak mendukung gambar?)")
	}
	return strings.TrimSpace(gr.Candidates[0].Content.Parts[0].Text), nil
}

// IsImageContent reports whether data looks like a supported image
// (png/jpeg/gif/webp) based on its magic bytes.
func IsImageContent(data []byte) bool {
	return sniffImageMIME(data) != "application/octet-stream"
}

// sniffImageMIME guesses an image content type from magic bytes.
func sniffImageMIME(data []byte) string {
	switch {
	case len(data) >= 8 && bytes.Equal(data[:8], []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}):
		return "image/png"
	case len(data) >= 3 && bytes.Equal(data[:3], []byte{0xFF, 0xD8, 0xFF}):
		return "image/jpeg"
	case len(data) >= 4 && bytes.Equal(data[:4], []byte{'G', 'I', 'F', '8'}):
		return "image/gif"
	case len(data) >= 12 && bytes.Equal(data[:4], []byte{'R', 'I', 'F', 'F'}) && bytes.Equal(data[8:12], []byte{'W', 'E', 'B', 'P'}):
		return "image/webp"
	default:
		return "application/octet-stream"
	}
}
