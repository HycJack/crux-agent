// Package openrouter implements image generation via OpenRouter.
package openrouter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	core "github.com/hycjack/crux-ai/core"
)

const defaultOpenRouterBaseURL = "https://openrouter.ai/api/v1"

// OpenRouterProvider implements image generation via OpenRouter.
type OpenRouterProvider struct{}

// NewOpenRouter creates a new OpenRouter images provider.
func NewOpenRouter() *OpenRouterProvider {
	return &OpenRouterProvider{}
}

func (p *OpenRouterProvider) GenerateImages(model core.ImagesModel, c core.Context, opts core.ImageOptions) (*core.AssistantImages, error) {
	apiKey := core.ResolveAPIKey(model.Provider, opts.APIKey)
	if apiKey == "" {
		return nil, fmt.Errorf("openrouter-images: no API key provided")
	}

	baseURL := core.ResolveBaseURL(core.Model{
		BaseURL: model.BaseURL,
	}, defaultOpenRouterBaseURL)

	// Build request
	messages := []map[string]any{}
	if c.SystemPrompt != "" {
		messages = append(messages, map[string]any{
			"role":    "system",
			"content": c.SystemPrompt,
		})
	}

	for _, msg := range c.Messages {
		switch m := msg.(type) {
		case core.UserMessage:
			messages = append(messages, map[string]any{
				"role":    "user",
				"content": stringifyContent(m.Content),
			})
		case core.AssistantMessage:
			var parts []string
			for _, b := range m.Content {
				if tc, ok := b.(core.TextContent); ok {
					parts = append(parts, tc.Text)
				}
			}
			if len(parts) > 0 {
				messages = append(messages, map[string]any{
					"role":    "assistant",
					"content": strings.Join(parts, "\n"),
				})
			}
		}
	}

	body := map[string]any{
		"model":      model.ID,
		"messages":   messages,
		"modalities": []string{"image"},
	}

	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	url := baseURL + "/chat/completions"

	req, err := http.NewRequestWithContext(context.Background(), "POST", url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	for k, v := range model.Headers {
		req.Header.Set(k, v)
	}
	for k, v := range opts.Headers {
		req.Header.Set(k, v)
	}

	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("openrouter-images: API error %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var result struct {
		Choices []struct {
			Message struct {
				Images []struct {
					URL string `json:"url"`
				} `json:"images"`
			} `json:"message"`
		} `json:"choices"`
		Usage *struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	images := core.AssistantImages{
		API:        "openrouter-images",
		Provider:   model.Provider,
		Model:      model.ID,
		StopReason: core.StopStop,
		Timestamp:  time.Now(),
	}

	if len(result.Choices) > 0 {
		for _, img := range result.Choices[0].Message.Images {
			// Parse data: URLs
			data := img.URL
			mimeType := "image/png"

			if strings.HasPrefix(data, "data:") {
				parts := strings.SplitN(data, ",", 2)
				if len(parts) == 2 {
					headerParts := strings.SplitN(parts[0], ";", 2)
					if len(headerParts) > 0 {
						mimeType = strings.TrimPrefix(headerParts[0], "data:")
					}
					data = parts[1]
				}
			}

			images.Output = append(images.Output, core.ImageData{
				Data:     data,
				MimeType: mimeType,
			})
		}
	}

	if result.Usage != nil {
		images.Usage = &core.Usage{
			Input:       result.Usage.PromptTokens,
			Output:      result.Usage.CompletionTokens,
			TotalTokens: result.Usage.TotalTokens,
		}
	}

	return &images, nil
}

// stringifyContent converts UserMessage.Content to a string suitable for API.
func stringifyContent(content any) string {
	switch c := content.(type) {
	case string:
		return c
	case []core.ContentBlock:
		var parts []string
		for _, b := range c {
			if tc, ok := b.(core.TextContent); ok {
				parts = append(parts, tc.Text)
			}
		}
		return strings.Join(parts, "\n")
	default:
		return fmt.Sprintf("%v", content)
	}
}
