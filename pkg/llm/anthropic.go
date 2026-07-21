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

const anthropicDefaultBaseURL = "https://api.anthropic.com"

type anthropicClient struct {
	cfg Config
	hc  *http.Client
}

func newAnthropic(cfg Config, hc *http.Client) *anthropicClient {
	if cfg.BaseURL == "" {
		cfg.BaseURL = anthropicDefaultBaseURL
	}
	return &anthropicClient{cfg: cfg, hc: hc}
}

func (c *anthropicClient) Provider() string { return ProviderAnthropic }

// Generate calls POST /v1/messages.
func (c *anthropicClient) Generate(ctx context.Context, req Request) (Response, error) {
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 4096
	}

	body := map[string]any{
		"model":      c.cfg.Model,
		"max_tokens": maxTokens,
		"system":     req.System,
		"messages": []map[string]string{
			{"role": "user", "content": req.Prompt},
		},
		"temperature": req.Temperature,
	}

	var out struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}

	if err := postJSON(ctx, c.hc, c.cfg.BaseURL+"/v1/messages", map[string]string{
		"x-api-key":         c.cfg.APIKey,
		"anthropic-version": "2023-06-01",
	}, body, &out); err != nil {
		return Response{}, err
	}
	if out.Error != nil {
		return Response{}, fmt.Errorf("anthropic: %s", out.Error.Message)
	}

	text := ""
	for _, block := range out.Content {
		if block.Type == "text" {
			text += block.Text
		}
	}
	if text == "" {
		return Response{}, fmt.Errorf("anthropic: empty response")
	}

	return Response{
		Text:         text,
		Model:        c.cfg.Model,
		InputTokens:  out.Usage.InputTokens,
		OutputTokens: out.Usage.OutputTokens,
	}, nil
}
