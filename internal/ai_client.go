package internal

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/anhhung04/tmuxai/config"
	"github.com/anhhung04/tmuxai/logger"
	"github.com/sashabaranov/go-openai"
	"google.golang.org/genai"
)

// AiClient represents an AI client for interacting with OpenAI-compatible APIs including Azure OpenAI
type AiClient struct {
	config       *config.Config
	configMgr    *Manager // To access model configuration methods
	geminiClient *genai.Client
	geminiMu     sync.Mutex
}

// Message represents a chat message
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatCompletionRequest represents a request to the chat completion API
type ChatCompletionRequest struct {
	Model    string    `json:"model,omitempty"`
	Messages []Message `json:"messages"`
}

// ChatCompletionChoice represents a choice in the chat completion response
type ChatCompletionChoice struct {
	Index   int     `json:"index"`
	Message Message `json:"message"`
}

// ChatCompletionResponse represents a response from the chat completion API
type ChatCompletionResponse struct {
	ID      string                 `json:"id"`
	Object  string                 `json:"object"`
	Created int64                  `json:"created"`
	Choices []ChatCompletionChoice `json:"choices"`
}

// Responses API Types

// ResponseInput represents the input for the Responses API
type ResponseInput interface{}

// ResponseContent represents content in the Responses API
type ResponseContent struct {
	Type        string        `json:"type"`
	Text        string        `json:"text,omitempty"`
	Annotations []interface{} `json:"annotations,omitempty"`
}

// ResponseOutputItem represents an output item in the Responses API
type ResponseOutputItem struct {
	ID      string            `json:"id"`
	Type    string            `json:"type"`             // "message", "reasoning", "function_call", etc.
	Status  string            `json:"status,omitempty"` // "completed", "in_progress", etc.
	Content []ResponseContent `json:"content,omitempty"`
	Role    string            `json:"role,omitempty"` // "assistant", "user", etc.
	Summary []interface{}     `json:"summary,omitempty"`
}

// ResponseRequest represents a request to the Responses API
type ResponseRequest struct {
	Model              string                 `json:"model"`
	Input              ResponseInput          `json:"input"`
	Instructions       string                 `json:"instructions,omitempty"`
	Tools              []interface{}          `json:"tools,omitempty"`
	PreviousResponseID string                 `json:"previous_response_id,omitempty"`
	Store              bool                   `json:"store,omitempty"`
	Include            []string               `json:"include,omitempty"`
	Text               map[string]interface{} `json:"text,omitempty"` // for structured outputs
}

// Response represents a response from the Responses API
type Response struct {
	ID         string               `json:"id"`
	Object     string               `json:"object"`
	CreatedAt  int64                `json:"created_at"`
	Model      string               `json:"model"`
	Output     []ResponseOutputItem `json:"output"`
	OutputText string               `json:"output_text,omitempty"`
	Error      *ResponseError       `json:"error,omitempty"`
	Usage      *ResponseUsage       `json:"usage,omitempty"`
}

// ResponseError represents an error in the Responses API
type ResponseError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code,omitempty"`
}

// ResponseUsage represents token usage in the Responses API
type ResponseUsage struct {
	InputTokens     int `json:"input_tokens"`
	OutputTokens    int `json:"output_tokens"`
	ReasoningTokens int `json:"reasoning_tokens,omitempty"`
	TotalTokens     int `json:"total_tokens"`
}

// readResponseBody reads the entire response body and returns it.
func (c *AiClient) readResponseBody(resp *http.Response) ([]byte, error) {
	return io.ReadAll(resp.Body)
}

func NewAiClient(cfg *config.Config) *AiClient {
	return &AiClient{
		config: cfg,
	}
}

// SetConfigManager sets the configuration manager for accessing model configurations
func (c *AiClient) SetConfigManager(mgr *Manager) {
	c.configMgr = mgr
}

// determineAPIType determines which API to use based on the model and configuration
func (c *AiClient) determineAPIType(model string) string {
	// Simplified: always use OpenAI compatible client for non‑Gemini providers.
	if c.configMgr != nil {
		if modelConfig, exists := c.configMgr.GetCurrentModelConfig(); exists {
			if modelConfig.Provider == "gemini" {
				return "gemini"
			}
		}
	}
	return "openai"
}

// GetResponseFromChatMessages gets a response from the AI based on chat messages
func (c *AiClient) GetResponseFromChatMessages(ctx context.Context, chatMessages []ChatMessage, model string) (string, error) {
	// Convert chat messages to AI client format
	aiMessages := []Message{}

	for i, msg := range chatMessages {
		var role string

		if i == 0 && !msg.FromUser {
			role = "system"
		} else if msg.FromUser {
			role = "user"
		} else {
			role = "assistant"
		}

		aiMessages = append(aiMessages, Message{
			Role:    role,
			Content: msg.Content,
		})
	}

	logger.Info("Sending %d messages to AI using model: %s", len(aiMessages), model)

	// Determine which API to use
	apiType := c.determineAPIType(model)
	logger.Debug("Using API type: %s for model: %s", apiType, model)

	// Route to appropriate API
	var response string
	var err error

	switch apiType {
	case "responses":
		response, err = c.OpenAIChat(ctx, aiMessages, model)
	case "azure":
		response, err = c.OpenAIChat(ctx, aiMessages, model)
	case "openrouter":
		response, err = c.OpenAIChat(ctx, aiMessages, model)
	case "gemini":
		response, err = c.GeminiGenerateContent(ctx, aiMessages, model)
	default:
		return "", fmt.Errorf("unknown API type: %s", apiType)
	}

	if err != nil {
		return "", err
	}

	return response, nil
}

// OpenAIChat sends a chat completion request using the official OpenAI Go SDK.
func (c *AiClient) OpenAIChat(ctx context.Context, messages []Message, model string) (string, error) {
	// Resolve configuration for the selected model.
	var apiKey, baseURL string
	if c.configMgr != nil {
		if mc, ok := c.configMgr.GetCurrentModelConfig(); ok {
			if mc.Provider == "openai" || mc.Provider == "azure" || mc.Provider == "openrouter" {
				apiKey = mc.APIKey
				baseURL = mc.BaseURL
			}
		}
	}
	if apiKey == "" {
		apiKey = c.config.OpenAI.APIKey
	}
	if baseURL == "" {
		baseURL = c.config.OpenAI.BaseURL
	}
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	cfg := openai.DefaultConfig(apiKey)
	cfg.BaseURL = strings.TrimSuffix(baseURL, "/")
	client := openai.NewClientWithConfig(cfg)

	// Convert internal Message structs to OpenAI SDK message format.
	var oaMsgs []openai.ChatCompletionMessage
	for _, m := range messages {
		role := openai.ChatMessageRoleAssistant
		if m.Role == "user" {
			role = openai.ChatMessageRoleUser
		} else if m.Role == "system" {
			role = openai.ChatMessageRoleSystem
		}
		oaMsgs = append(oaMsgs, openai.ChatCompletionMessage{Role: role, Content: m.Content})
	}

	req := openai.ChatCompletionRequest{Model: model, Messages: oaMsgs}
	resp, err := client.CreateChatCompletion(ctx, req)
	if err != nil {
		logger.Error("OpenAI request failed: %v", err)
		return "", fmt.Errorf("openai request error: %w", err)
	}
	if len(resp.Choices) == 0 {
		logger.Error("OpenAI returned no choices")
		return "", fmt.Errorf("no completion choices returned (model: %s)", model)
	}
	content := resp.Choices[0].Message.Content
	logger.Debug("Received OpenAI response (%d characters)", len(content))
	return content, nil
}

// ChatCompletion sends a chat completion request using the official OpenAI SDK.
// It mirrors the previous custom implementation but now relies on the SDK for reliability.
func (c *AiClient) ChatCompletion(ctx context.Context, req ChatCompletionRequest) (ChatCompletionResponse, error) {
	// Resolve API key and base URL the same way OpenAIChat does.
	var apiKey, baseURL string
	if c.configMgr != nil {
		if mc, ok := c.configMgr.GetCurrentModelConfig(); ok {
			if mc.Provider == "openai" || mc.Provider == "azure" || mc.Provider == "openrouter" {
				apiKey = mc.APIKey
				baseURL = mc.BaseURL
			}
		}
	}
	if apiKey == "" {
		apiKey = c.config.OpenAI.APIKey
	}
	if baseURL == "" {
		baseURL = c.config.OpenAI.BaseURL
	}
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	cfg := openai.DefaultConfig(apiKey)
	cfg.BaseURL = strings.TrimSuffix(baseURL, "/")
	client := openai.NewClientWithConfig(cfg)

	// Convert request messages to SDK format.
	oaMsgs := make([]openai.ChatCompletionMessage, len(req.Messages))
	for i, m := range req.Messages {
		role := openai.ChatMessageRoleAssistant
		if m.Role == "user" {
			role = openai.ChatMessageRoleUser
		} else if m.Role == "system" {
			role = openai.ChatMessageRoleSystem
		}
		oaMsgs[i] = openai.ChatCompletionMessage{Role: role, Content: m.Content}
	}

	oaReq := openai.ChatCompletionRequest{Model: req.Model, Messages: oaMsgs}
	resp, err := client.CreateChatCompletion(ctx, oaReq)
	if err != nil {
		logger.Error("OpenAI request failed: %v", err)
		return ChatCompletionResponse{}, fmt.Errorf("openai request error: %w", err)
	}
	if len(resp.Choices) == 0 {
		return ChatCompletionResponse{}, fmt.Errorf("no completion choices returned (model: %s)", req.Model)
	}

	choice := resp.Choices[0]
	cResp := ChatCompletionResponse{
		ID:      resp.ID,
		Object:  resp.Object,
		Created: resp.Created,
		Choices: []ChatCompletionChoice{{
			Index:   choice.Index,
			Message: Message{Role: string(choice.Message.Role), Content: choice.Message.Content},
		}},
	}
	return cResp, nil
}

// Response sends a request to the OpenAI Responses API (legacy wrapper).
func (c *AiClient) Response(ctx context.Context, messages []Message, model string) (string, error) {
	// For backward compatibility, delegate to OpenAIChat which now uses the official SDK.
	return c.OpenAIChat(ctx, messages, model)
}

// getOrCreateGeminiClient creates or returns the cached Gemini client
func (c *AiClient) getOrCreateGeminiClient(ctx context.Context, apiKey string) (*genai.Client, error) {
	c.geminiMu.Lock()
	defer c.geminiMu.Unlock()

	if c.geminiClient != nil {
		return c.geminiClient, nil
	}

	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create Gemini client: %w", err)
	}

	c.geminiClient = client
	return client, nil
}

// GeminiGenerateContent sends a request to the Gemini API using the go-genai SDK
func (c *AiClient) GeminiGenerateContent(ctx context.Context, messages []Message, model string) (string, error) {
	if len(messages) == 0 {
		return "", fmt.Errorf("no messages provided")
	}

	// Get API key from model configuration
	var apiKey string
	if c.configMgr != nil {
		if modelConfig, exists := c.configMgr.GetCurrentModelConfig(); exists && modelConfig.Provider == "gemini" {
			apiKey = modelConfig.APIKey
		}
	}

	if apiKey == "" {
		return "", fmt.Errorf("gemini API key not configured")
	}

	// Get or create Gemini client
	client, err := c.getOrCreateGeminiClient(ctx, apiKey)
	if err != nil {
		return "", err
	}

	// Convert messages to Gemini format
	var systemInstruction *genai.Content
	var contents []*genai.Content

	for _, msg := range messages {
		if msg.Role == "system" {
			// System instruction is handled separately in Gemini
			systemInstruction = &genai.Content{
				Parts: []*genai.Part{{Text: msg.Content}},
			}
			continue
		}

		// Map roles: user -> user, assistant -> model
		role := msg.Role
		if role == "assistant" {
			role = "model"
		}

		contents = append(contents, &genai.Content{
			Role:  role,
			Parts: []*genai.Part{{Text: msg.Content}},
		})
	}

	if len(contents) == 0 {
		return "", fmt.Errorf("no user/assistant messages to send")
	}

	// Build generation config
	config := &genai.GenerateContentConfig{}
	if systemInstruction != nil {
		config.SystemInstruction = systemInstruction
	}

	logger.Debug("Sending Gemini API request with model: %s, %d messages", model, len(contents))

	// Call the Gemini API
	result, err := client.Models.GenerateContent(ctx, model, contents, config)
	if err != nil {
		if ctx.Err() == context.Canceled {
			return "", fmt.Errorf("request canceled: %w", ctx.Err())
		}
		logger.Error("Failed to generate content with Gemini: %v", err)
		return "", fmt.Errorf("gemini API error: %w", err)
	}

	// Extract text from response
	responseText := result.Text()
	if responseText == "" {
		// Try to extract from candidates directly
		if len(result.Candidates) > 0 && result.Candidates[0].Content != nil {
			for _, part := range result.Candidates[0].Content.Parts {
				if part.Text != "" {
					responseText = part.Text
					break
				}
			}
		}
	}

	if responseText == "" {
		logger.Error("No response text returned from Gemini")
		return "", fmt.Errorf("no response content returned from Gemini (model: %s)", model)
	}

	logger.Debug("Received Gemini response (%d characters)", len(responseText))
	return responseText, nil
}

func debugChatMessages(chatMessages []ChatMessage, response string) {

	timestamp := time.Now().Format("20060102-150405")
	configDir, _ := config.GetConfigDir()

	debugDir := fmt.Sprintf("%s/debug", configDir)
	if _, err := os.Stat(debugDir); os.IsNotExist(err) {
		_ = os.Mkdir(debugDir, 0755)
	}

	debugFileName := fmt.Sprintf("%s/debug-%s.txt", debugDir, timestamp)

	file, err := os.Create(debugFileName)
	if err != nil {
		logger.Error("Failed to create debug file: %v", err)
		return
	}
	defer func() { _ = file.Close() }()

	_, _ = file.WriteString("==================    SENT CHAT MESSAGES ==================\n\n")

	for i, msg := range chatMessages {
		role := "assistant"
		if msg.FromUser {
			role = "user"
		}
		if i == 0 && !msg.FromUser {
			role = "system"
		}
		timeStr := msg.Timestamp.Format(time.RFC3339)

		_, _ = fmt.Fprintf(file, "Message %d: Role=%s, Time=%s\n", i+1, role, timeStr)
		_, _ = fmt.Fprintf(file, "Content:\n%s\n\n", msg.Content)
	}

	_, _ = file.WriteString("==================    RECEIVED RESPONSE ==================\n\n")
	_, _ = file.WriteString(response)
	_, _ = file.WriteString("\n\n==================    END DEBUG ==================\n")
}
