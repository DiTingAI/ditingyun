// Package api handles HTTP endpoints, wiring all pipeline modules together.
package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"github.com/DiTingAI/ditingyun/internal/config"
	"github.com/DiTingAI/ditingyun/internal/index"
	"github.com/DiTingAI/ditingyun/internal/ingest"
	"github.com/DiTingAI/ditingyun/internal/openai"
	"github.com/DiTingAI/ditingyun/internal/polish"
	"github.com/DiTingAI/ditingyun/internal/retrieve"
	"github.com/DiTingAI/ditingyun/internal/transcribe"
)

const version = "0.1.0-dev"

// Handler bundles all dependencies.
type Handler struct {
	Cfg *config.Config

	ChatClient    *openai.Client
	WhisperClient *openai.Client
	EmbedFn       index.EmbedFunc // 向量化闭包（屏蔽服务差异）

	Store *index.Store

	uploadMu sync.Mutex
}

func New(cfg *config.Config, chatClient, whisperClient, embedClient *openai.Client, store *index.Store) *Handler {
	model := cfg.EmbeddingModel
	embedFn := func(text string) ([]float32, error) {
		v, _, err := embedClient.EmbeddingBGE(model, text)
		return v, err
	}
	return &Handler{
		Cfg:           cfg,
		ChatClient:    chatClient,
		WhisperClient: whisperClient,
		EmbedFn:       embedFn,
		Store:         store,
	}
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", h.Healthz)
	mux.HandleFunc("GET /api/v1/info", h.Info)
	mux.HandleFunc("POST /api/v1/upload", h.Upload)
	mux.HandleFunc("POST /api/v1/search", h.Search)
	mux.HandleFunc("POST /api/v1/qa", h.QA)
}

func (h *Handler) Healthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) Info(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"name":    "ditingyun",
		"version": version,
	})
}

func (h *Handler) Upload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 2<<30)
	if err := r.ParseMultipartForm(2 << 30); err != nil {
		writeError(w, http.StatusBadRequest, "解析上传文件失败: "+err.Error())
		return
	}

	fh, ok := r.MultipartForm.File["file"]
	if !ok || len(fh) == 0 {
		writeError(w, http.StatusBadRequest, "缺少 file 字段")
		return
	}
	fileHeader := fh[0]

	h.uploadMu.Lock()
	defer h.uploadMu.Unlock()

	taskLabel := sanitizeFilename(fileHeader.Filename)
	start := time.Now()

	// Step 1: 保存 + ffmpeg 抽音频
	log.Printf("[%s] ▶ Step 1/4 保存 + ffmpeg 抽音频...", taskLabel)
	stored, err := ingest.SaveAndExtract(h.Cfg, fileHeader)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "音频提取失败: "+err.Error())
		return
	}
	taskLabel = sanitizeFilename(stored.Name)
	log.Printf("[%s] ✅ Step 1/4 完成 (%.2fs) → %s", taskLabel, time.Since(start).Seconds(), stored.AudioPath)

	// Step 2: Whisper 转写
	stepStart := time.Now()
	log.Printf("[%s] ▶ Step 2/4 Whisper 转写中（模型: %s）...", taskLabel, h.Cfg.WhisperModel)
	raw, err := transcribe.Transcribe(h.WhisperClient, h.Cfg.WhisperModel, stored.AudioPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "语音转写失败: "+err.Error())
		return
	}
	log.Printf("[%s] ✅ Step 2/4 完成 (%.2fs) → %d 字", taskLabel, time.Since(stepStart).Seconds(), len([]rune(raw)))

	// Step 3: LLM 清洗
	stepStart = time.Now()
	log.Printf("[%s] ▶ Step 3/4 LLM 清洗中（模型: %s）...", taskLabel, h.Cfg.LLMModel)
	polished, err := polish.Polish(h.ChatClient, h.Cfg.LLMModel, raw)
	if err != nil {
		log.Printf("[%s] ⚠ Step 3/4 清洗失败（用原文）: %v", taskLabel, err)
		polished = raw
	} else {
		log.Printf("[%s] ✅ Step 3/4 完成 (%.2fs)", taskLabel, time.Since(stepStart).Seconds())
	}

	// Step 4: 切片 + 向量化 + 落库
	stepStart = time.Now()
	taskID := fmt.Sprintf("%s_%d", taskLabel, time.Now().Unix())
	log.Printf("[%s] ▶ Step 4/4 向量化 + 索引构建中（模型: %s）...", taskLabel, h.Cfg.EmbeddingModel)
	n, err := h.Store.IndexTask(h.EmbedFn, taskID, polished)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "索引构建失败: "+err.Error())
		return
	}
	log.Printf("[%s] ✅ Step 4/4 完成 (%.2fs) → %d chunks", taskLabel, time.Since(stepStart).Seconds(), n)
	log.Printf("[%s] 🎉 全链路完成，总耗时 %.2fs", taskLabel, time.Since(start).Seconds())

	writeJSON(w, http.StatusOK, map[string]any{
		"task_id":     taskID,
		"filename":    stored.Name,
		"transcript":  raw,
		"polished":    polished,
		"chunk_count": n,
	})
}

func (h *Handler) Search(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Query string `json:"query"`
		TopK  int    `json:"topK"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "JSON 解析失败: "+err.Error())
		return
	}
	if req.Query == "" {
		writeError(w, http.StatusBadRequest, "query 不能为空")
		return
	}
	if req.TopK <= 0 {
		req.TopK = 5
	}

	results, err := retrieve.Search(h.Store, h.EmbedFn, req.Query, req.TopK)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "检索失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"query": req.Query, "results": results})
}

func (h *Handler) QA(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Query string `json:"query"`
		TopK  int    `json:"topK"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "JSON 解析失败: "+err.Error())
		return
	}
	if req.Query == "" {
		writeError(w, http.StatusBadRequest, "query 不能为空")
		return
	}
	if req.TopK <= 0 {
		req.TopK = 5
	}

	answer, sources, err := retrieve.QA(h.Store, h.EmbedFn, h.ChatClient, h.Cfg.LLMModel, req.Query, req.TopK)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "问答失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"query": req.Query, "answer": answer, "sources": sources})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func sanitizeFilename(name string) string {
	ext := filepath.Ext(name)
	base := name[:len(name)-len(ext)]
	base = filepath.Base(base)
	if base == "" {
		return "task"
	}
	for i := range base {
		c := base[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' {
			continue
		}
		base = base[:i] + "_" + base[i+1:]
	}
	return base
}
