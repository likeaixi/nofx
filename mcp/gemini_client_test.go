package mcp

import (
	"fmt"
	"testing"
	"time"
)

// ============================================================
// 测试 GeminiClient 创建和配置
// ============================================================

func TestNewGeminiClient_Default(t *testing.T) {
	client := NewGeminiClient()

	if client == nil {
		t.Fatal("client should not be nil")
	}

	// 类型断言检查
	gmClient, ok := client.(*GeminiClient)
	if !ok {
		t.Fatal("client should be *GeminiClient")
	}

	// 验证默认值
	if gmClient.Provider != ProviderGemini {
		t.Errorf("Provider should be '%s', got '%s'", ProviderGemini, gmClient.Provider)
	}

	if gmClient.BaseURL != DefaultGeminiBaseURL {
		t.Errorf("BaseURL should be '%s', got '%s'", DefaultGeminiBaseURL, gmClient.BaseURL)
	}

	if gmClient.Model != DefaultGeminiModel {
		t.Errorf("Model should be '%s', got '%s'", DefaultGeminiModel, gmClient.Model)
	}

	if gmClient.logger == nil {
		t.Error("logger should not be nil")
	}

	if gmClient.httpClient == nil {
		t.Error("httpClient should not be nil")
	}
}

func TestNewGeminiClientWithOptions(t *testing.T) {
	mockLogger := NewMockLogger()
	customModel := "gemini-pro"
	customAPIKey := "sk-custom-key"

	client := NewGeminiClientWithOptions(
		WithLogger(mockLogger),
		WithModel(customModel),
		WithAPIKey(customAPIKey),
		WithMaxTokens(4000),
	)

	gmClient := client.(*GeminiClient)

	// 验证自定义选项被应用
	if gmClient.logger != mockLogger {
		t.Error("logger should be set from option")
	}

	if gmClient.Model != customModel {
		t.Error("Model should be set from option")
	}

	if gmClient.APIKey != customAPIKey {
		t.Error("APIKey should be set from option")
	}

	if gmClient.MaxTokens != 4000 {
		t.Error("MaxTokens should be 4000")
	}

	// 验证 Gemini 默认值仍然保留
	if gmClient.Provider != ProviderGemini {
		t.Errorf("Provider should still be '%s'", ProviderGemini)
	}

	if gmClient.BaseURL != DefaultGeminiBaseURL {
		t.Errorf("BaseURL should still be '%s'", DefaultGeminiBaseURL)
	}
}

// ============================================================
// 测试 SetAPIKey
// ============================================================

func TestGeminiClient_SetAPIKey(t *testing.T) {
	mockLogger := NewMockLogger()
	client := NewGeminiClientWithOptions(
		WithLogger(mockLogger),
	)

	gmClient := client.(*GeminiClient)

	// 测试设置 API Key（默认 URL 和 Model）
	gmClient.SetAPIKey("sk-test-key-12345678", "", "")

	if gmClient.APIKey != "sk-test-key-12345678" {
		t.Errorf("APIKey should be 'sk-test-key-12345678', got '%s'", gmClient.APIKey)
	}

	// 验证日志记录
	logs := mockLogger.GetLogsByLevel("INFO")
	if len(logs) == 0 {
		t.Error("should have logged API key setting")
	}

	// 验证 BaseURL 和 Model 保持默认
	if gmClient.BaseURL != DefaultGeminiBaseURL {
		t.Error("BaseURL should remain default")
	}

	if gmClient.Model != DefaultGeminiModel {
		t.Error("Model should remain default")
	}
}

func TestGeminiClient_SetAPIKey_WithCustomURL(t *testing.T) {
	mockLogger := NewMockLogger()
	client := NewGeminiClientWithOptions(
		WithLogger(mockLogger),
	)

	gmClient := client.(*GeminiClient)

	customURL := "https://custom.api.com/v1"
	gmClient.SetAPIKey("sk-test-key-12345678", customURL, "")

	if gmClient.BaseURL != customURL {
		t.Errorf("BaseURL should be '%s', got '%s'", customURL, gmClient.BaseURL)
	}

	// 验证日志记录
	logs := mockLogger.GetLogsByLevel("INFO")
	hasCustomURLLog := false
	for _, log := range logs {
		if log.Format == "🔧 [MCP] Gemini 使用自定义 BaseURL: %s" {
			hasCustomURLLog = true
			break
		}
	}

	if !hasCustomURLLog {
		t.Error("should have logged custom BaseURL")
	}
}

func TestGeminiClient_SetAPIKey_WithCustomModel(t *testing.T) {
	mockLogger := NewMockLogger()
	client := NewGeminiClientWithOptions(
		WithLogger(mockLogger),
	)

	gmClient := client.(*GeminiClient)

	customModel := "gemini-ultra"
	gmClient.SetAPIKey("sk-test-key-12345678", "", customModel)

	if gmClient.Model != customModel {
		t.Errorf("Model should be '%s', got '%s'", customModel, gmClient.Model)
	}

	// 验证日志记录
	logs := mockLogger.GetLogsByLevel("INFO")
	hasCustomModelLog := false
	for _, log := range logs {
		if log.Format == "🔧 [MCP] Gemini 使用自定义 Model: %s" {
			hasCustomModelLog = true
			break
		}
	}

	if !hasCustomModelLog {
		t.Error("should have logged custom Model")
	}
}

// ============================================================
// 测试集成功能
// ============================================================

func TestGeminiClient_CallWithMessages_Success(t *testing.T) {
	mockHTTP := NewMockHTTPClient()
	mockHTTP.SetSuccessResponse("Gemini AI response")
	mockLogger := NewMockLogger()

	client := NewGeminiClientWithOptions(
		WithHTTPClient(mockHTTP.ToHTTPClient()),
		WithLogger(mockLogger),
		WithAPIKey("sk-test-key"),
	)

	result, err := client.CallWithMessages("system prompt", "user prompt")
	fmt.Printf("result %v", result)
	if err != nil {
		t.Fatalf("should not error: %v", err)
	}

	if result != "Gemini AI response" {
		t.Errorf("expected 'Gemini AI response', got '%s'", result)
	}

	// 验证请求
	requests := mockHTTP.GetRequests()
	if len(requests) != 1 {
		t.Fatalf("expected 1 request, got %d", len(requests))
	}

	req := requests[0]

	// 验证 URL（OpenAI 兼容 ChatCompletions）
	expectedURL := DefaultGeminiBaseURL + "/chat/completions"
	if req.URL.String() != expectedURL {
		t.Errorf("expected URL '%s', got '%s'", expectedURL, req.URL.String())
	}

	// 验证 Authorization header
	authHeader := req.Header.Get("Authorization")
	if authHeader != "Bearer sk-test-key" {
		t.Errorf("expected 'Bearer sk-test-key', got '%s'", authHeader)
	}

	// 验证 Content-Type
	if req.Header.Get("Content-Type") != "application/json" {
		t.Error("Content-Type should be application/json")
	}
}

func TestGeminiClient_Timeout(t *testing.T) {
	client := NewGeminiClientWithOptions(
		WithTimeout(30 * time.Second),
	)

	gmClient := client.(*GeminiClient)

	if gmClient.httpClient.Timeout != 30*time.Second {
		t.Errorf("expected timeout 30s, got %v", gmClient.httpClient.Timeout)
	}

	// 测试 SetTimeout
	client.SetTimeout(60 * time.Second)

	if gmClient.httpClient.Timeout != 60*time.Second {
		t.Errorf("expected timeout 60s after SetTimeout, got %v", gmClient.httpClient.Timeout)
	}
}

// ============================================================
// 测试 hooks 机制
// ============================================================

func TestGeminiClient_HooksIntegration(t *testing.T) {
	client := NewGeminiClientWithOptions()
	gmClient := client.(*GeminiClient)

	// 验证 hooks 指向 gmClient 自己（实现多态）
	if gmClient.hooks != gmClient {
		t.Error("hooks should point to gmClient for polymorphism")
	}

	// 验证 buildUrl 使用 Gemini 配置（OpenAI 兼容路径）
	url := gmClient.buildUrl()
	expectedURL := DefaultGeminiBaseURL + "/chat/completions"
	if url != expectedURL {
		t.Errorf("expected URL '%s', got '%s'", expectedURL, url)
	}
}
