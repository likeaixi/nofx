package mcp

import (
	"net/http"
)

const (
	ProviderOpenAI       = "openai"
	DefaultOpenAIBaseURL = "https://api.openai.com/v1"
	// 根据你需要的默认模型改，比如 "gpt-4.1-mini" / "gpt-4.1"
	DefaultOpenAIModel = "gpt-4.1-mini"
)

type OpenAIClient struct {
	*Client
}

// NewOpenAIClient 创建 OpenAI 客户端（向前兼容）
//
// Deprecated: 推荐使用 NewOpenAIClientWithOptions 以获得更好的灵活性
func NewOpenAIClient() AIClient {
	return NewOpenAIClientWithOptions()
}

// NewOpenAIClientWithOptions 创建 OpenAI 客户端（支持选项模式）
//
// 使用示例：
//
//	// 基础用法
//	client := mcp.NewOpenAIClientWithOptions()
//
//	// 自定义配置
//	client := mcp.NewOpenAIClientWithOptions(
//	    mcp.WithAPIKey("sk-xxx"),
//	    mcp.WithLogger(customLogger),
//	    mcp.WithTimeout(60*time.Second),
//	)
func NewOpenAIClientWithOptions(opts ...ClientOption) AIClient {
	// 1. 创建 OpenAI 预设选项
	openaiOpts := []ClientOption{
		WithProvider(ProviderOpenAI),
		WithModel(DefaultOpenAIModel),
		WithBaseURL(DefaultOpenAIBaseURL),
	}

	// 2. 合并用户选项（用户选项优先级更高）
	allOpts := append(openaiOpts, opts...)

	// 3. 创建基础客户端
	baseClient := NewClient(allOpts...).(*Client)

	// 4. 创建 OpenAI 客户端
	oaClient := &OpenAIClient{
		Client: baseClient,
	}

	// 5. 设置 hooks 指向 OpenAIClient（实现动态分派）
	baseClient.hooks = oaClient

	return oaClient
}

// SetAPIKey 设置 OpenAI API Key / BaseURL / Model
//
// customURL 允许你接企业版 / 代理；customModel 用来覆盖默认模型。
func (oaClient *OpenAIClient) SetAPIKey(apiKey string, customURL string, customModel string) {
	oaClient.APIKey = apiKey

	if len(apiKey) > 8 {
		oaClient.logger.Infof("🔧 [MCP] OpenAI API Key: %s...%s", apiKey[:4], apiKey[len(apiKey)-4:])
	}
	if customURL != "" {
		oaClient.BaseURL = customURL
		oaClient.logger.Infof("🔧 [MCP] OpenAI 使用自定义 BaseURL: %s", customURL)
	} else {
		oaClient.logger.Infof("🔧 [MCP] OpenAI 使用默认 BaseURL: %s", oaClient.BaseURL)
	}
	if customModel != "" {
		oaClient.Model = customModel
		oaClient.logger.Infof("🔧 [MCP] OpenAI 使用自定义 Model: %s", customModel)
	} else {
		oaClient.logger.Infof("🔧 [MCP] OpenAI 使用默认 Model: %s", oaClient.Model)
	}
}

// setAuthHeader 设置认证头
//
// 这里沿用底层 Client 的实现（通常是 Authorization: Bearer <APIKey>）
func (oaClient *OpenAIClient) setAuthHeader(reqHeaders http.Header) {
	oaClient.Client.setAuthHeader(reqHeaders)
}
