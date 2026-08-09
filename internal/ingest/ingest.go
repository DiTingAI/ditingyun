// Package ingest handles file uploads and audio extraction via ffmpeg.
package ingest

import (
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/DiTingAI/ditingyun/internal/config"
)

// StoredFile holds the result of a processed upload.
type StoredFile struct {
	Name      string // original filename
	AudioPath string // path to extracted audio (wav)
}

// SaveAndExtract saves an uploaded multipart file to disk and extracts audio via ffmpeg.
// Returns the stored file info or an error.
func SaveAndExtract(cfg *config.Config, fileHeader *multipart.FileHeader) (*StoredFile, error) {
	name := sanitize(fileHeader.Filename)
	videoPath := filepath.Join(cfg.DataDir, "uploads", name)
	if err := os.MkdirAll(filepath.Dir(videoPath), 0o755); err != nil {
		return nil, fmt.Errorf("ingest: mkdir: %w", err)
	}

	src, err := fileHeader.Open()
	if err != nil {
		return nil, fmt.Errorf("ingest: open upload: %w", err)
	}
	defer src.Close()

	dst, err := os.Create(videoPath)
	if err != nil {
		return nil, fmt.Errorf("ingest: create file: %w", err)
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		return nil, fmt.Errorf("ingest: write file: %w", err)
	}

	ext := strings.ToLower(filepath.Ext(name))
	audioPath := videoPath + "_audio.wav"

	if ext == ".wav" {
		// 已是 WAV 格式，直接使用（跳过 ffmpeg）
		return &StoredFile{Name: name, AudioPath: videoPath}, nil
	}

	// 非 WAV 格式（视频/其他音频）→ ffmpeg 提取音频
	cmd := exec.Command("ffmpeg",
		"-y", "-i", videoPath,
		"-vn",                     // 丢弃视频流
		"-acodec", "pcm_s16le",    // 16-bit PCM
		"-ar", "16000",            // 16 kHz 采样率
		"-ac", "1",                // 单声道
		audioPath,
	)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ingest: ffmpeg: %w\n%s", err, stderr.String())
	}

	return &StoredFile{Name: name, AudioPath: audioPath}, nil
}

func sanitize(name string) string {
	s := filepath.Base(name)
	s = strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r == 0 {
			return -1
		}
		return r
	}, s)
	if s == "" || s == "." {
		s = "unnamed"
	}
	return s
}
