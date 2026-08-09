// Package retrieve provides semantic search and RAG Q&A on top of the index.
package retrieve

import (
	"fmt"
	"strings"

	"github.com/DiTingAI/ditingyun/internal/index"
	"github.com/DiTingAI/ditingyun/internal/openai"
)

const ragPrompt = `你是一个知识库问答助手，请**只基于以下上下文**回答用户的问题。
如果上下文中找不到答案，请如实说"当前知识库中未找到相关信息"。
回答时引用相关来源片段的编号。

上下文：
%s

问题：%s`

func Search(store *index.Store, embedFn index.EmbedFunc, query string, topK int) ([]index.SearchResult, error) {
	return store.Search(embedFn, query, topK)
}

func QA(store *index.Store, embedFn index.EmbedFunc, chatClient *openai.Client, llmModel, query string, topK int) (string, []string, error) {
	results, err := store.Search(embedFn, query, topK)
	if err != nil {
		return "", nil, fmt.Errorf("retrieve: search: %w", err)
	}

	var contexts []string
	for _, r := range results {
		contexts = append(contexts, r.Content)
	}
	sources := make([]string, len(results))
	for i, r := range results {
		sources[i] = r.ID
	}

	if len(contexts) == 0 {
		return "当前知识库中未找到与问题相关的信息。", sources, nil
	}

	ctx := buildContext(contexts)
	prompt := fmt.Sprintf(ragPrompt, ctx, query)

	resp, err := chatClient.Chat(&openai.ChatRequest{
		Model: llmModel,
		Messages: []openai.Message{
			{Role: "user", Content: prompt},
		},
	})
	if err != nil {
		return "", nil, fmt.Errorf("retrieve: qa: %w", err)
	}
	if len(resp.Choices) == 0 {
		return "生成回答失败，请重试。", sources, nil
	}
	return strings.TrimSpace(resp.Choices[0].Message.Content), sources, nil
}

func buildContext(contexts []string) string {
	var b strings.Builder
	for i, c := range contexts {
		b.WriteString(fmt.Sprintf("[%d] %s\n\n", i+1, c))
	}
	return b.String()
}
