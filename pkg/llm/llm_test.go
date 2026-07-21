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
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Each provider is exercised against a mock of its real wire format: the tests
// assert the request shape (auth header, endpoint, payload) and response parsing.

func TestOpenAIWireFormat(t *testing.T) {
	var got map[string]any
	var auth, path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		path = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &got)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"ok\":true}"}}],"usage":{"prompt_tokens":10,"completion_tokens":5}}`))
	}))
	defer srv.Close()

	c, _ := New(Config{Provider: ProviderOpenAI, APIKey: "sk-test", Model: "gpt-4o", BaseURL: srv.URL})
	resp, err := c.Generate(context.Background(), Request{System: "sys", Prompt: "hi", MaxTokens: 100})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	if auth != "Bearer sk-test" || path != "/v1/chat/completions" {
		t.Fatalf("bad request: auth=%q path=%q", auth, path)
	}
	msgs := got["messages"].([]any)
	if len(msgs) != 2 || msgs[0].(map[string]any)["role"] != "system" {
		t.Fatalf("bad messages: %+v", msgs)
	}
	if resp.Text != `{"ok":true}` || resp.InputTokens != 10 || resp.OutputTokens != 5 {
		t.Fatalf("bad response: %+v", resp)
	}
}

func TestAnthropicWireFormat(t *testing.T) {
	var apiKey, version, path string
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiKey = r.Header.Get("x-api-key")
		version = r.Header.Get("anthropic-version")
		path = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &got)
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"{\"ok\":true}"}],"usage":{"input_tokens":7,"output_tokens":3}}`))
	}))
	defer srv.Close()

	c, _ := New(Config{Provider: ProviderAnthropic, APIKey: "ak-test", Model: "claude-sonnet-5", BaseURL: srv.URL})
	resp, err := c.Generate(context.Background(), Request{System: "sys", Prompt: "hi"})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	if apiKey != "ak-test" || version == "" || path != "/v1/messages" {
		t.Fatalf("bad request: key=%q version=%q path=%q", apiKey, version, path)
	}
	if got["system"] != "sys" || got["max_tokens"] == nil {
		t.Fatalf("bad payload: %+v", got)
	}
	if resp.Text != `{"ok":true}` || resp.InputTokens != 7 {
		t.Fatalf("bad response: %+v", resp)
	}
}

func TestGeminiWireFormat(t *testing.T) {
	var path, rawQuery, apiKeyHeader string
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		rawQuery = r.URL.RawQuery
		apiKeyHeader = r.Header.Get("x-goog-api-key")
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &got)
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"{\"ok\":true}"}]}}],"usageMetadata":{"promptTokenCount":4,"candidatesTokenCount":2}}`))
	}))
	defer srv.Close()

	c, _ := New(Config{Provider: ProviderGemini, APIKey: "gk-test", Model: "gemini-2.5-pro", BaseURL: srv.URL})
	resp, err := c.Generate(context.Background(), Request{System: "sys", Prompt: "hi"})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	if !strings.Contains(path, "/v1beta/models/gemini-2.5-pro:generateContent") {
		t.Fatalf("bad request path: %q", path)
	}
	// The key must travel in a header — a URL leaks into transport errors and
	// access logs.
	if strings.Contains(rawQuery, "gk-test") {
		t.Fatalf("API key leaked into the query string: %q", rawQuery)
	}
	if apiKeyHeader != "gk-test" {
		t.Fatalf("x-goog-api-key header = %q, want the key", apiKeyHeader)
	}
	if got["systemInstruction"] == nil || got["contents"] == nil {
		t.Fatalf("bad payload: %+v", got)
	}
	if resp.Text != `{"ok":true}` || resp.OutputTokens != 2 {
		t.Fatalf("bad response: %+v", resp)
	}
}

// TestGeminiEmptyTextDiagnostics: a 2xx reply with no text must explain WHY.
// The 2.5 "thinking" models burn maxOutputTokens on reasoning and come back with
// finishReason=MAX_TOKENS and zero parts — the old code just said
// "empty response", which told the operator nothing actionable.
func TestGeminiEmptyTextDiagnostics(t *testing.T) {
	cases := []struct {
		name, body, wantSubstr string
	}{
		{
			name:       "thinking budget exhausted",
			body:       `{"candidates":[{"content":{"parts":[]},"finishReason":"MAX_TOKENS"}],"usageMetadata":{"thoughtsTokenCount":1024}}`,
			wantSubstr: "Max output tokens",
		},
		{
			name:       "prompt blocked",
			body:       `{"candidates":[],"promptFeedback":{"blockReason":"SAFETY"}}`,
			wantSubstr: "blocked (SAFETY)",
		},
		{
			name:       "other finish reason",
			body:       `{"candidates":[{"content":{"parts":[]},"finishReason":"RECITATION"}]}`,
			wantSubstr: "finishReason=RECITATION",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			c, _ := New(Config{Provider: ProviderGemini, APIKey: "gk", Model: "gemini-2.5-pro", BaseURL: srv.URL})
			_, err := c.Generate(context.Background(), Request{Prompt: "hi"})
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tc.wantSubstr) {
				t.Fatalf("error %q does not mention %q", err, tc.wantSubstr)
			}
		})
	}
}

// TestGeminiCustomEndpointHint: a stale Base URL from another provider answers
// 200 with a body this client cannot read. The error must name the endpoint
// instead of blaming Gemini.
func TestGeminiCustomEndpointHint(t *testing.T) {
	// An OpenAI-shaped reply — exactly what a leftover mock returns.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"hi"}}]}`))
	}))
	defer srv.Close()

	c, _ := New(Config{Provider: ProviderGemini, APIKey: "gk", Model: "gemini-2.5-pro", BaseURL: srv.URL})
	_, err := c.Generate(context.Background(), Request{Prompt: "hi"})
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "Base URL") || !strings.Contains(err.Error(), srv.URL) {
		t.Fatalf("error should name the custom endpoint, got: %v", err)
	}
}

func TestProviderErrorsSurface(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		_, _ = w.Write([]byte(`{"error":{"message":"invalid api key"}}`))
	}))
	defer srv.Close()

	c, _ := New(Config{Provider: ProviderOpenAI, APIKey: "bad", BaseURL: srv.URL})
	if _, err := c.Generate(context.Background(), Request{Prompt: "hi"}); err == nil || !strings.Contains(err.Error(), "invalid api key") {
		t.Fatalf("expected provider error to surface, got: %v", err)
	}
}

// TestSecretsRedacted: even if a credential reaches an error string, it must be
// scrubbed before it can surface in the console or a log.
// TestTemperatureAlwaysSent: extraction is transcription, so every provider must
// receive an explicit temperature rather than inheriting a creative default.
func TestTemperatureAlwaysSent(t *testing.T) {
	for _, tc := range []struct {
		provider, reply string
		read            func(map[string]any) (any, bool)
	}{
		{ProviderOpenAI, `{"choices":[{"message":{"content":"x"}}]}`,
			func(m map[string]any) (any, bool) { v, ok := m["temperature"]; return v, ok }},
		{ProviderAnthropic, `{"content":[{"type":"text","text":"x"}]}`,
			func(m map[string]any) (any, bool) { v, ok := m["temperature"]; return v, ok }},
		{ProviderGemini, `{"candidates":[{"content":{"parts":[{"text":"x"}]}}]}`,
			func(m map[string]any) (any, bool) {
				g, ok := m["generationConfig"].(map[string]any)
				if !ok {
					return nil, false
				}
				v, ok := g["temperature"]
				return v, ok
			}},
	} {
		t.Run(tc.provider, func(t *testing.T) {
			var got map[string]any
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				_ = json.Unmarshal(body, &got)
				_, _ = w.Write([]byte(tc.reply))
			}))
			defer srv.Close()

			c, _ := New(Config{Provider: tc.provider, APIKey: "k", BaseURL: srv.URL})
			if _, err := c.Generate(context.Background(), Request{Prompt: "hi", Temperature: 0}); err != nil {
				t.Fatalf("generate: %v", err)
			}
			v, ok := tc.read(got)
			if !ok {
				t.Fatalf("%s did not send a temperature: %+v", tc.provider, got)
			}
			if f, _ := v.(float64); f != 0 {
				t.Fatalf("%s temperature = %v, want 0", tc.provider, v)
			}
		})
	}
}

func TestSecretsRedacted(t *testing.T) {
	in := `Post "https://x/v1beta/models/m:generateContent?key=AQ.Ab8RN6SUPERSECRET": context deadline exceeded`
	got := redactSecrets(in)
	if strings.Contains(got, "AQ.Ab8RN6SUPERSECRET") {
		t.Fatalf("secret survived redaction: %s", got)
	}
	if !strings.Contains(got, "key=REDACTED") {
		t.Fatalf("expected key=REDACTED, got: %s", got)
	}
}

func TestDefaults(t *testing.T) {
	if !IsValidProvider("openai") || !IsValidProvider("anthropic") || !IsValidProvider("gemini") {
		t.Fatal("expected all three providers valid")
	}
	if IsValidProvider("fake") {
		t.Fatal("fake must not be operator-selectable")
	}
	for _, p := range []string{ProviderOpenAI, ProviderAnthropic, ProviderGemini} {
		if DefaultModel(p) == "" {
			t.Fatalf("no default model for %s", p)
		}
	}
}
