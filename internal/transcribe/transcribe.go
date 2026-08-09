// Package transcribe handles ASR via OpenAI-compatible Whisper API.
package transcribe

import (
	"fmt"

	"github.com/DiTingAI/ditingyun/internal/openai"
)

// Transcribe calls the Whisper-compatible endpoint and returns raw text.
func Transcribe(client *openai.Client, model, audioPath string) (string, error) {
	resp, err := client.Transcribe(audioPath, model)
	if err != nil {
		return "", fmt.Errorf("transcribe: %w", err)
	}
	return resp.Text, nil
}
