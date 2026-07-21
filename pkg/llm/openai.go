// Copyright (c) 2026 Meizon Inc.
//
// Permission to use, copy, modify, and/or distribute this software for any
// purpose with or without fee is hereby granted, provided that the above
// copyright notice and this permission notice appear in all copies.
//
// THE SOFTWARE IS PROVIDED "AS IS" AND THE AUTHOR DISCLAIMS ALL WARRANTIES WITH
// REGARD TO THIS SOFTWARE INCLUDING ALL IMPLIED WARRANTIES OF MERCHANTABILITY
// AND FITNESS. IN NO EVENT SHALL THE AUTHOR BE LIABLE FOR ANY SPECIAL, DIRECT,
// INDIRECT, OR CONSEQUENTIAL DAMAGES OR ANY DAMAGES WHATSOEVER RESULTING FROM
// LOSS OF USE, DATA OR PROFITS, WHETHER IN AN ACTION OF CONTRACT, NEGLIGENCE OR
// OTHER TORTIOUS ACTION, ARISING OUT OF OR IN CONNECTION WITH THE USE OR
// PERFORMANCE OF THIS SOFTWARE.

package llm

import (
	"context"
	"fmt"
	"net/http"
)

const openAIDefaultBaseURL = "https://api.openai.com"

type openAIClient struct {
	cfg Config
	hc  *http.Client
}

func newOpenAI(cfg Config, hc *http.Client) *openAIClient {
	if cfg.BaseURL == "" {
		cfg.BaseURL = openAIDefaultBaseURL
	}
	return &openAIClient{cfg: cfg, hc: hc}
}

func (c *openAIClient) Provider() string { return ProviderOpenAI }

// Generate calls POST /v1/chat/completions.
func (c *openAIClient) Generate(ctx context.Context, req Request) (Response, error) {
	body := map[string]any{
		"model": c.cfg.Model,
		"messages": []map[string]string{
			{"role": "system", "content": req.System},
			{"role": "user", "content": req.Prompt},
		},
		"temperature": req.Temperature,
	}
	if req.MaxTokens > 0 {
		body["max_completion_tokens"] = req.MaxTokens
	}

	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}

	if err := postJSON(ctx, c.hc, c.cfg.BaseURL+"/v1/chat/completions", map[string]string{
		"Authorization": "Bearer " + c.cfg.APIKey,
	}, body, &out); err != nil {
		return Response{}, err
	}
	if out.Error != nil {
		return Response{}, fmt.Errorf("openai: %s", out.Error.Message)
	}
	if len(out.Choices) == 0 {
		return Response{}, fmt.Errorf("openai: empty response")
	}

	return Response{
		Text:         out.Choices[0].Message.Content,
		Model:        c.cfg.Model,
		InputTokens:  out.Usage.PromptTokens,
		OutputTokens: out.Usage.CompletionTokens,
	}, nil
}
