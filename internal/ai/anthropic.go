package ai

import (
	"context"
	"fmt"
	"strings"

	"github.com/adharshmk96/dbprompter/internal/store"
	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

func callAnthropic(ctx context.Context, prov store.Provider, system, user string) (string, error) {
	opts := []option.RequestOption{option.WithAPIKey(prov.APIKey)}
	if prov.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(prov.BaseURL))
	}
	client := anthropic.NewClient(opts...)

	model := prov.Model
	if model == "" {
		model = "claude-opus-5"
	}

	resp, err := client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.Model(model),
		MaxTokens: 4096,
		System:    []anthropic.TextBlockParam{{Text: system}},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(user)),
		},
	})
	if err != nil {
		return "", fmt.Errorf("anthropic: %w", err)
	}
	if resp.StopReason == anthropic.StopReasonRefusal {
		return "", fmt.Errorf("anthropic: the model declined this request")
	}

	var b strings.Builder
	for _, block := range resp.Content {
		if t, ok := block.AsAny().(anthropic.TextBlock); ok {
			b.WriteString(t.Text)
		}
	}
	if b.Len() == 0 {
		return "", fmt.Errorf("anthropic: empty response")
	}
	return b.String(), nil
}
