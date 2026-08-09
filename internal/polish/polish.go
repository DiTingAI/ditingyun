// Package polish cleans ASR output via LLM.
package polish

import (
	"fmt"
	"strings"

	"github.com/DiTingAI/ditingyun/internal/openai"
)

const systemPrompt = `你是专业的文字编辑。请对以下语音转写结果进行清理：
1. 删除无意义的语气词和重复碎片（如"嗯"、"额"、"那个那个"）
2. 修正明显的拼音/同音别字，保持专业术语正确
3. 保留原始句意、语序和说话风格
4. 不要添加原文中没有的信息
5. 直接输出清洗后的文本，不要任何解释或标记`

// Polish sends raw transcript to LLM and returns cleaned version.
func Polish(client *openai.Client, model, raw string) (string, error) {
	resp, err := client.Chat(&openai.ChatRequest{
		Model: model,
		Messages: []openai.Message{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: raw},
		},
	})
	if err != nil {
		return "", fmt.Errorf("polish: %w", err)
	}
	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("polish: LLM 返回空结果")
	}
	return strings.TrimSpace(resp.Choices[0].Message.Content), nil
}
