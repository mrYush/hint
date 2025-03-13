package llm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/yourusername/hint/internal/config"
	"github.com/yourusername/hint/internal/context"
)

// Request представляет запрос к API LLM
type Request struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
}

// Message представляет сообщение в формате OpenAI API
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Response представляет ответ от API LLM
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

// AskLLM отправляет запрос к LLM и возвращает ответ
func AskLLM(cfg *config.Config, ctx *context.DirectoryContext, question string) (string, error) {
	// Формирование системного сообщения с контекстом
	systemPrompt := fmt.Sprintf(
		"Вы - полезный ассистент, который помогает разработчику с проектом. "+
		"Текущая директория: %s\n"+
		"Файлы в директории: %s\n\n"+
		"Ответьте на вопрос разработчика, учитывая данный контекст.",
		ctx.CurrentDir,
		strings.Join(ctx.Files, ", "),
	)
	
	// Формирование запроса
	reqBody := Request{
		Model: cfg.Model,
		Messages: []Message{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: question},
		},
	}
	
	// Сериализация запроса
	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("ошибка сериализации запроса: %w", err)
	}
	
	// Отправка запроса
	url := fmt.Sprintf("%s/chat/completions", cfg.APIURL)
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return "", fmt.Errorf("ошибка создания HTTP-запроса: %w", err)
	}
	
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", cfg.APIKey))
	
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("ошибка выполнения HTTP-запроса: %w", err)
	}
	defer resp.Body.Close()
	
	// Чтение ответа
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("ошибка чтения ответа: %w", err)
	}
	
	// Десериализация ответа
	var result Response
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("ошибка десериализации ответа: %w", err)
	}
	
	// Проверка на ошибки
	if result.Error != nil && result.Error.Message != "" {
		return "", fmt.Errorf("ошибка API: %s", result.Error.Message)
	}
	
	// Проверка на наличие ответа
	if len(result.Choices) == 0 {
		return "", fmt.Errorf("получен пустой ответ от API")
	}
	
	return result.Choices[0].Message.Content, nil
} 