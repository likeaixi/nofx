package mcp

import (
	"net/http"
)

const (
	ProviderGemini       = "gemini"
	DefaultGeminiBaseURL = "https://generativelanguage.googleapis.com/v1beta"
	// 可根据需要改成你要用的默认模型
	DefaultGeminiModel = "gemini-2.0-flash"
)

type GeminiClient struct {
	*Client
}

// NewGeminiClient 创建 Gemini 客户端（向前兼容）
//
// Deprecated: 推荐使用 NewGeminiClientWithOptions 以获得更好的灵活性
func NewGeminiClient() AIClient {
	return NewGeminiClientWithOptions()
}

// NewGeminiClientWithOptions 创建 Gemini 客户端（支持选项模式）
//
// 使用示例：
//
//	// 基础用法
//	client := mcp.NewGeminiClientWithOptions()
//
//	// 自定义配置
//	client := mcp.NewGeminiClientWithOptions(
//	    mcp.WithAPIKey("sk-xxx"),
//	    mcp.WithLogger(customLogger),
//	    mcp.WithTimeout(60*time.Second),
//	)
func NewGeminiClientWithOptions(opts ...ClientOption) AIClient {
	// 1. 创建 Gemini 预设选项
	geminiOpts := []ClientOption{
		WithProvider(ProviderGemini),
		WithModel(DefaultGeminiModel),
		WithBaseURL(DefaultGeminiBaseURL),
	}

	// 2. 合并用户选项（用户选项优先级更高）
	allOpts := append(geminiOpts, opts...)

	// 3. 创建基础客户端
	baseClient := NewClient(allOpts...).(*Client)

	// 4. 创建 Gemini 客户端
	gmClient := &GeminiClient{
		Client: baseClient,
	}

	// 5. 设置 hooks 指向 GeminiClient（实现动态分派）
	baseClient.hooks = gmClient

	return gmClient
}

func (gmClient *GeminiClient) SetAPIKey(apiKey string, customURL string, customModel string) {
	gmClient.APIKey = apiKey

	if len(apiKey) > 8 {
		gmClient.logger.Infof("🔧 [MCP] Gemini API Key: %s...%s", apiKey[:4], apiKey[len(apiKey)-4:])
	}
	if customURL != "" {
		gmClient.BaseURL = customURL
		gmClient.logger.Infof("🔧 [MCP] Gemini 使用自定义 BaseURL: %s", customURL)
	} else {
		gmClient.logger.Infof("🔧 [MCP] Gemini 使用默认 BaseURL: %s", gmClient.BaseURL)
	}
	if customModel != "" {
		gmClient.Model = customModel
		gmClient.logger.Infof("🔧 [MCP] Gemini 使用自定义 Model: %s", customModel)
	} else {
		gmClient.logger.Infof("🔧 [MCP] Gemini 使用默认 Model: %s", gmClient.Model)
	}
}

func (gmClient *GeminiClient) setAuthHeader(reqHeaders http.Header) {
	// 使用 OpenAI 兼容端点时，仍然是 Authorization: Bearer <API_KEY>
	gmClient.Client.setAuthHeader(reqHeaders)
}
