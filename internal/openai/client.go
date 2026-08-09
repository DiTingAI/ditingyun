// Package openai provides a minimal OpenAI-compatible API client.
// Zero dependencies beyond Go stdlib.
package openai

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"time"
)

type Client struct {
	BaseURL    string
	APIKey     string
	NoAuth     bool // 自建服务不需要 Authorization 头
	HTTPClient *http.Client
}

func New(baseURL, apiKey string) *Client {
	return &Client{
		BaseURL: baseURL,
		APIKey:  apiKey,
		HTTPClient: &http.Client{
			Timeout: 300 * time.Second,
			// 自建服务可能有冷启动 / 连接被关闭的问题，禁用 keep-alive 避免复用死连接
			Transport: &http.Transport{
				MaxIdleConns:        0,
				IdleConnTimeout:     30 * time.Second,
				DisableKeepAlives:   true,
				MaxIdleConnsPerHost: -1,
			},
		},
	}
}

// WithoutAuth returns a copy of the client that skips the Authorization header.
func (c *Client) WithoutAuth() *Client {
	c2 := *c
	c2.NoAuth = true
	return &c2
}

// ── Chat Completions ──

type ChatRequest struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatResponse struct {
	Choices []struct{ Message Message } `json:"choices"`
}

func (c *Client) Chat(req *ChatRequest) (*ChatResponse, error) {
	var resp ChatResponse
	err := c.postJSON("/chat/completions", req, &resp)
	return &resp, err
}

// ── Audio Transcription ──

type TranscribeResponse struct{ Text string `json:"text"` }

func (c *Client) Transcribe(audioPath string, model string) (*TranscribeResponse, error) {
	fileData, err := os.ReadFile(audioPath)
	if err != nil {
		return nil, fmt.Errorf("openai: read audio: %w", err)
	}

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	_ = writer.WriteField("model", model)

	part, _ := writer.CreateFormFile("file", "audio.wav")
	part.Write(fileData)
	writer.Close()

	var resp TranscribeResponse
	err = c.postMultipart("/audio/transcriptions", body, writer.FormDataContentType(), &resp)
	return &resp, err
}

// ── Embeddings ──

type EmbeddingRequest struct {
	Model string `json:"model"`
	Input string `json:"input"`
}

type EmbeddingResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
	Usage struct{ TotalTokens int } `json:"usage"`
}

func (c *Client) Embedding(model, input string) ([]float32, int, error) {
	var resp EmbeddingResponse
	err := c.postJSON("/embeddings", EmbeddingRequest{Model: model, Input: input}, &resp)
	if err != nil {
		return nil, 0, err
	}
	if len(resp.Data) == 0 {
		return nil, 0, fmt.Errorf("openai: empty embedding response")
	}
	return resp.Data[0].Embedding, resp.Usage.TotalTokens, nil
}

// EmbeddingBGE calls a bge-m3 style embedding service (texts field / dense field).
func (c *Client) EmbeddingBGE(model, text string) ([]float32, int, error) {
	type bgeResp struct {
		Data []struct {
			Dense []float32 `json:"dense"`
		} `json:"data"`
		Usage struct{ TotalTokens int } `json:"usage"`
	}
	var resp bgeResp
	err := c.postJSON("/embeddings", map[string]any{"texts": []string{text}}, &resp)
	if err != nil {
		return nil, 0, err
	}
	if len(resp.Data) == 0 {
		return nil, 0, fmt.Errorf("openai: empty embedding response")
	}
	t := resp.Usage.TotalTokens
	if t == 0 {
		t = 1 // bge-m3 不返回 token 计数时默认 1
	}
	return resp.Data[0].Dense, t, nil
}

// ── internal ──

func (c *Client) postJSON(path string, req, resp any) error {
	body, _ := json.Marshal(req)
	return c.do("POST", path, bytes.NewReader(body), "application/json; charset=utf-8", resp)
}

func (c *Client) postMultipart(path string, body io.Reader, ct string, resp any) error {
	return c.do("POST", path, body, ct, resp)
}

func (c *Client) do(method, path string, body io.Reader, ct string, resp any) error {
	url := c.BaseURL + path
	r, _ := http.NewRequest(method, url, body)
	if !c.NoAuth {
		r.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	r.Header.Set("Content-Type", ct)

	hr, err := c.HTTPClient.Do(r)
	if err != nil {
		return fmt.Errorf("openai: %w", err)
	}
	defer hr.Body.Close()

	raw, _ := io.ReadAll(io.LimitReader(hr.Body, 16<<20))
	if hr.StatusCode >= 400 {
		return fmt.Errorf("openai: HTTP %d — %s", hr.StatusCode, string(raw))
	}
	return json.Unmarshal(raw, resp)
}
