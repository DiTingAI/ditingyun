package config

import "os"

type Config struct {
	Port    string
	DataDir string

	// Chat / LLM（清洗、问答）
	ChatURL string
	APIKey  string

	// ASR 语音转写（独立服务）
	WhisperURL string

	// Embedding 向量化（独立服务）
	EmbedURL string

	// 模型名称
	WhisperModel   string
	LLMModel       string
	EmbeddingModel string
}

func Load() *Config {
	return &Config{
		Port:     env("PORT", "8080"),
		DataDir:  env("DATA_DIR", "./data"),

		ChatURL:    env("CHAT_BASE_URL", "https://api.openai.com/v1"),
		APIKey:     env("OPENAI_API_KEY", ""),
		WhisperURL: env("WHISPER_BASE_URL", "https://api.openai.com/v1"),
		EmbedURL:   env("EMBED_BASE_URL", "https://api.openai.com/v1"),

		WhisperModel:   env("WHISPER_MODEL", "whisper-1"),
		LLMModel:       env("LLM_MODEL", "gpt-4o-mini"),
		EmbeddingModel: env("EMBEDDING_MODEL", "bge-m3"),
	}
}

func (c *Config) Validate() error {
	if c.APIKey == "" {
		return wrapErr("OPENAI_API_KEY 未设置，请修改 .env 文件")
	}
	return os.MkdirAll(c.DataDir, 0o755)
}

func wrapErr(msg string) error { return configError(msg) }

type configError string

func (e configError) Error() string { return "config: " + string(e) }

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
