package mcp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

const (
	ProviderGemini       = "gemini"
	DefaultGeminiBaseURL = "https://generativelanguage.googleapis.com"
	// 可根据需要改成你要用的默认模型
	DefaultGeminiModel = "gemini-2.5-flash"
)

type GeminiClient struct {
	*Client
}

type geminiPart struct {
	Text         string              `json:"text,omitempty"`
	FunctionCall *geminiFunctionCall `json:"functionCall,omitempty"`
	// 如果要支持 tool 结果，还可以加 functionResponse 等
}

type geminiFunctionCall struct {
	Name string                 `json:"name"`
	Args map[string]interface{} `json:"args,omitempty"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

type geminiFunctionDeclaration struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	Parameters  map[string]interface{} `json:"parameters,omitempty"`
}

type geminiTool struct {
	FunctionDeclarations []geminiFunctionDeclaration `json:"functionDeclarations,omitempty"`
}

type geminiGenerationConfig struct {
	Temperature     *float32 `json:"temperature,omitempty"`
	TopP            *float32 `json:"topP,omitempty"`
	MaxOutputTokens *int     `json:"maxOutputTokens,omitempty"`
}

type geminiSystemInstruction struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

type geminiChatRequest struct {
	Contents          []geminiContent          `json:"contents"`
	Tools             []geminiTool             `json:"tools,omitempty"`
	GenerationConfig  *geminiGenerationConfig  `json:"generationConfig,omitempty"`
	SystemInstruction *geminiSystemInstruction `json:"systemInstruction,omitempty"`
}

type geminiUsageMetadata struct {
	PromptTokenCount     int `json:"promptTokenCount"`
	CandidatesTokenCount int `json:"candidatesTokenCount"`
	TotalTokenCount      int `json:"totalTokenCount"`
}

type geminiCandidate struct {
	Content geminiContent `json:"content"`
	// SafetyRatings, finishReason 等可以按需添加
}

type geminiChatResponse struct {
	Candidates    []geminiCandidate    `json:"candidates"`
	UsageMetadata *geminiUsageMetadata `json:"usageMetadata,omitempty"`
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
	//gmClient.Client.setAuthHeader(reqHeaders)
}

func (gmClient *GeminiClient) buildUrl() string {
	return fmt.Sprintf("%s/v1beta/models/%s:generateContent?key=%s", gmClient.BaseURL, gmClient.Model, gmClient.APIKey)
}

func (gmClient *GeminiClient) buildMCPRequestBody(systemPrompt, userPrompt string) map[string]any {
	// 构建 messages 数组
	messages := []geminiContent{}

	system := &geminiSystemInstruction{}
	// 如果有 system prompt，添加 system message
	if systemPrompt != "" {
		system = &geminiSystemInstruction{
			Role: "user", // 或者不写 Role
			Parts: []geminiPart{
				{Text: systemPrompt},
			},
		}
	}
	// 添加 user message
	messages = append(messages, geminiContent{
		Role: "user",
		Parts: []geminiPart{
			{Text: userPrompt},
		},
	})

	// 构建请求体
	requestBody := map[string]interface{}{
		"contents":          messages,
		"systemInstruction": system,
	}
	return requestBody
}

func (gmClient *GeminiClient) parseMCPResponse(body []byte) (string, error) {
	var result geminiChatResponse

	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("解析响应失败: %w", err)
	}

	if len(result.Candidates) == 0 {
		return "", fmt.Errorf("API返回空响应")
	}

	cand := result.Candidates[0]

	// 把所有 text part 拼起来
	var sb strings.Builder
	for _, p := range cand.Content.Parts {
		if p.Text != "" {
			sb.WriteString(p.Text)
		}
	}

	outText := sb.String()

	return outText, nil
}
