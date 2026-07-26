package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/adharshmk96/dbprompter/internal/store"
)

// callOpenAICompatible speaks the OpenAI chat-completions wire format, which
// covers OpenAI itself plus local runtimes (Ollama http://localhost:11434/v1,
// LM Studio http://localhost:1234/v1) and most hosted gateways.
func callOpenAICompatible(ctx context.Context, prov store.Provider, system, user string) (string, error) {
	base := strings.TrimSuffix(prov.BaseURL, "/")
	if base == "" {
		base = "https://api.openai.com/v1"
	}

	payload := map[string]any{
		"model": prov.Model,
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": user},
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if prov.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+prov.APIKey)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("request to %s failed: %w", base, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("provider returned %s: %.300s", resp.Status, string(data))
	}

	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return "", fmt.Errorf("decode provider response: %w", err)
	}
	if len(parsed.Choices) == 0 || parsed.Choices[0].Message.Content == "" {
		return "", fmt.Errorf("provider returned no content")
	}
	return parsed.Choices[0].Message.Content, nil
}
