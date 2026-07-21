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
	"net/url"
)

const geminiDefaultBaseURL = "https://generativelanguage.googleapis.com"

type geminiClient struct {
	cfg Config
	hc  *http.Client
}

func newGemini(cfg Config, hc *http.Client) *geminiClient {
	if cfg.BaseURL == "" {
		cfg.BaseURL = geminiDefaultBaseURL
	}
	return &geminiClient{cfg: cfg, hc: hc}
}

func (c *geminiClient) Provider() string { return ProviderGemini }

// endpointHint names a non-default base URL in errors. A stale override (say,
// an OpenAI-shaped mock left in Settings) answers 200 with a body this client
// cannot read, which otherwise looks like an unexplained empty reply.
func (c *geminiClient) endpointHint() string {
	if c.cfg.BaseURL == "" || c.cfg.BaseURL == geminiDefaultBaseURL {
		return ""
	}
	return fmt.Sprintf(
		" — requests are going to the custom Base URL %q, not Google. Clear Base URL in Settings to use the real Gemini endpoint",
		c.cfg.BaseURL)
}

// Generate calls POST /v1beta/models/{model}:generateContent.
func (c *geminiClient) Generate(ctx context.Context, req Request) (Response, error) {
	body := map[string]any{
		"contents": []map[string]any{
			{"role": "user", "parts": []map[string]string{{"text": req.Prompt}}},
		},
	}
	if req.System != "" {
		body["systemInstruction"] = map[string]any{"parts": []map[string]string{{"text": req.System}}}
	}
	generation := map[string]any{"temperature": req.Temperature}
	if req.MaxTokens > 0 {
		generation["maxOutputTokens"] = req.MaxTokens
	}
	body["generationConfig"] = generation

	var out struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
			FinishReason string `json:"finishReason"`
		} `json:"candidates"`
		PromptFeedback struct {
			BlockReason string `json:"blockReason"`
		} `json:"promptFeedback"`
		UsageMetadata struct {
			PromptTokenCount     int `json:"promptTokenCount"`
			CandidatesTokenCount int `json:"candidatesTokenCount"`
			ThoughtsTokenCount   int `json:"thoughtsTokenCount"`
		} `json:"usageMetadata"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}

	// The key goes in a header, never the query string: a URL ends up verbatim in
	// timeout/transport error messages, request logs and proxy access logs, which
	// would leak the credential to anyone reading them.
	endpoint := fmt.Sprintf("%s/v1beta/models/%s:generateContent",
		c.cfg.BaseURL, url.PathEscape(c.cfg.Model))

	if err := postJSON(ctx, c.hc, endpoint, map[string]string{
		"x-goog-api-key": c.cfg.APIKey,
	}, body, &out); err != nil {
		return Response{}, err
	}
	if out.Error != nil {
		return Response{}, fmt.Errorf("gemini: %s", out.Error.Message)
	}
	if len(out.Candidates) == 0 {
		if r := out.PromptFeedback.BlockReason; r != "" {
			return Response{}, fmt.Errorf("gemini: the prompt was blocked (%s)%s", r, c.endpointHint())
		}
		return Response{}, fmt.Errorf("gemini: the model returned no candidates%s", c.endpointHint())
	}

	text := ""
	for _, p := range out.Candidates[0].Content.Parts {
		text += p.Text
	}

	if text == "" {
		// The 2.5 "thinking" models spend maxOutputTokens on internal reasoning
		// first; too small a budget is consumed entirely and the reply comes back
		// with no text at all. Say so, instead of a bare "empty response".
		reason := out.Candidates[0].FinishReason
		if reason == "MAX_TOKENS" {
			return Response{}, fmt.Errorf(
				"gemini: the model hit the output limit before returning any text (finishReason=MAX_TOKENS, %d thinking tokens used). "+
					"Raise \"Max output tokens\" in Settings — thinking models such as gemini-2.5-* need noticeably more headroom",
				out.UsageMetadata.ThoughtsTokenCount)
		}
		if reason != "" && reason != "STOP" {
			return Response{}, fmt.Errorf("gemini: the model returned no text (finishReason=%s)", reason)
		}
		return Response{}, fmt.Errorf("gemini: the model returned no text")
	}

	return Response{
		Text:         text,
		Model:        c.cfg.Model,
		InputTokens:  out.UsageMetadata.PromptTokenCount,
		OutputTokens: out.UsageMetadata.CandidatesTokenCount,
	}, nil
}
