package llm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/mrYush/hint/internal/config"
	"github.com/mrYush/hint/internal/context"
)

// Request represents a request to the LLM API
type Request struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
}

// Message represents a message in OpenAI API format
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Response represents a response from the LLM API
type Response struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// AskLLM sends a request to the LLM and returns the response
func AskLLM(cfg *config.Config, ctx *context.DirectoryContext, question string) (string, error) {
	// Creating a system message with context
	systemPrompt := fmt.Sprintf(
		"You are a helpful assistant aiding a developer with their project. "+
		"Current directory: %s\n"+
		"Files in directory: %s\n\n"+
		"Answer the developer's question with this context in mind.",
		ctx.CurrentDir,
		strings.Join(ctx.Files, ", "),
	)
	
	// Creating the request
	reqBody := Request{
		Model: cfg.Model,
		Messages: []Message{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: question},
		},
	}
	
	// Serializing the request
	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("error serializing request: %w", err)
	}
	
	// Sending the request
	url := fmt.Sprintf("%s/chat/completions", cfg.APIURL)
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return "", fmt.Errorf("error creating HTTP request: %w", err)
	}
	
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", cfg.APIKey))
	
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("error executing HTTP request: %w", err)
	}
	defer resp.Body.Close()
	
	// Reading the response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("error reading response: %w", err)
	}
	
	// Deserializing the response
	var result Response
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("error deserializing response: %w", err)
	}
	
	// Checking for errors
	if result.Error != nil && result.Error.Message != "" {
		return "", fmt.Errorf("API error: %s", result.Error.Message)
	}
	
	// Checking for response presence
	if len(result.Choices) == 0 {
		return "", fmt.Errorf("received empty response from API")
	}
	
	return result.Choices[0].Message.Content, nil
} 