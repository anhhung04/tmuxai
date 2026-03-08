package internal

import (
	"context"
	"fmt"
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

// chatMessageRole returns the role string for a chat message given its index.
func chatMessageRole(msg ChatMessage, index int) string {
	if index == 0 && !msg.FromUser {
		return "system"
	}
	if msg.FromUser {
		return "user"
	}
	return "assistant"
}

// GetResponseFromChatMessages gets a response from the AI based on chat messages
func (c *AiClient) GetResponseFromChatMessages(ctx context.Context, chatMessages []ChatMessage, model string) (string, error) {
	aiMessages := make([]Message, len(chatMessages))
	for i, msg := range chatMessages {
		aiMessages[i] = Message{Role: chatMessageRole(msg, i), Content: msg.Content}
	}

	logger.Info("Sending %d messages to AI using model: %s", len(aiMessages), model)

	// Determine which API to use
	apiType := c.determineAPIType(model)
	logger.Debug("Using API type: %s for model: %s", apiType, model)

	// Route to appropriate API
	var response string
	var err error

	switch apiType {
	case "openai":
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

// newOpenAIClient builds an OpenAI SDK client from the active model configuration.
func (c *AiClient) newOpenAIClient() *openai.Client {
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
	return openai.NewClientWithConfig(cfg)
}

// toOpenAIMessages converts internal Message structs to the OpenAI SDK format.
func toOpenAIMessages(messages []Message) []openai.ChatCompletionMessage {
	oaMsgs := make([]openai.ChatCompletionMessage, len(messages))
	for i, m := range messages {
		role := openai.ChatMessageRoleAssistant
		if m.Role == "user" {
			role = openai.ChatMessageRoleUser
		} else if m.Role == "system" {
			role = openai.ChatMessageRoleSystem
		}
		oaMsgs[i] = openai.ChatCompletionMessage{Role: role, Content: m.Content}
	}
	return oaMsgs
}

// OpenAIChat sends a chat completion request using the official OpenAI Go SDK.
func (c *AiClient) OpenAIChat(ctx context.Context, messages []Message, model string) (string, error) {
	client := c.newOpenAIClient()
	req := openai.ChatCompletionRequest{Model: model, Messages: toOpenAIMessages(messages)}
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
		role := chatMessageRole(msg, i)
		timeStr := msg.Timestamp.Format(time.RFC3339)

		_, _ = fmt.Fprintf(file, "Message %d: Role=%s, Time=%s\n", i+1, role, timeStr)
		_, _ = fmt.Fprintf(file, "Content:\n%s\n\n", msg.Content)
	}

	_, _ = file.WriteString("==================    RECEIVED RESPONSE ==================\n\n")
	_, _ = file.WriteString(response)
	_, _ = file.WriteString("\n\n==================    END DEBUG ==================\n")
}
