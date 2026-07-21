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

// Package llm is a small multi-provider text-generation client used by the
// AI-assisted authoring pipeline. Three providers are built in — OpenAI (chat
// completions), Anthropic (messages) and Google Gemini (generateContent) — all
// over plain HTTP so the surface stays uniform and dependency-light. Every
// provider accepts a base-URL override (Azure OpenAI, proxies, gateways, test
// mocks). The LLM only ever *proposes* content; acceptance is a human action in
// the service layer.
package llm

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// Provider names.
const (
	ProviderOpenAI    = "openai"
	ProviderAnthropic = "anthropic"
	ProviderGemini    = "gemini"
	ProviderFake      = "fake" // deterministic, for tests
)

// IsValidProvider reports whether name is a configurable provider (the fake
// provider is test-only and not offered in settings).
func IsValidProvider(name string) bool {
	switch name {
	case ProviderOpenAI, ProviderAnthropic, ProviderGemini:
		return true
	default:
		return false
	}
}

// DefaultProvider is what a fresh install is configured with. Gemini leads
// because gemini-2.5-pro carries the largest context window of the three, which
// is what ingesting a full compliance standard needs.
const DefaultProvider = ProviderGemini

// DefaultModel suggests a sensible default model per provider (operators can
// set any model string in settings). Each is that provider's most capable
// long-context model.
func DefaultModel(provider string) string {
	switch provider {
	case ProviderOpenAI:
		return "gpt-4o"
	case ProviderAnthropic:
		return "claude-sonnet-5"
	case ProviderGemini:
		return "gemini-2.5-pro"
	default:
		return ""
	}
}

// DefaultMaxTokens is the per-request output budget a provider gets when the
// operator has not set one. It is deliberately generous: the extract step emits
// a large JSON object, and "thinking" models spend part of the budget reasoning
// before producing any text at all.
func DefaultMaxTokens(provider string) int {
	switch provider {
	case ProviderGemini:
		return 32768
	case ProviderAnthropic:
		return 16384
	case ProviderOpenAI:
		return 8192
	default:
		return 8192
	}
}

// ContextRunes is a rough usable input size (in runes) per provider, used to
// size document chunks. Values are conservative fractions of the real context
// windows, leaving room for the system prompt and the response.
func ContextRunes(provider string) int {
	switch provider {
	case ProviderGemini:
		return 480_000 // gemini-2.5-pro: ~1M-token window
	case ProviderAnthropic:
		return 320_000
	case ProviderOpenAI:
		return 160_000
	default:
		return 48_000
	}
}

// Request is a single-turn generation request.
type Request struct {
	System    string
	Prompt    string
	MaxTokens int
	// Temperature is always sent. The zero value (0) is deliberate: every step
	// of the ingestion pipeline wants deterministic, reproducible extraction,
	// and provider defaults are typically ~1.0.
	Temperature float64
}

// Response carries the generated text and token usage.
type Response struct {
	Text         string
	Model        string
	InputTokens  int
	OutputTokens int
}

// Client generates text from a prompt.
type Client interface {
	// Provider returns the provider name.
	Provider() string
	// Generate performs one generation call.
	Generate(ctx context.Context, req Request) (Response, error)
}

// Config selects and configures a provider client.
type Config struct {
	Provider string
	APIKey   string
	Model    string
	// BaseURL overrides the provider endpoint (optional).
	BaseURL string
	// Timeout for the HTTP call (default 10m — thinking models on large chunks).
	Timeout time.Duration
}

// New builds a client for the configured provider.
func New(cfg Config) (Client, error) {
	if cfg.Model == "" {
		cfg.Model = DefaultModel(cfg.Provider)
	}
	if cfg.Timeout <= 0 {
		// A "thinking" model reasoning over a large chunk and emitting tens of
		// thousands of output tokens routinely runs for minutes; 90s produced
		// spurious "context deadline exceeded" failures mid-extraction.
		cfg.Timeout = 10 * time.Minute
	}
	hc := &http.Client{Timeout: cfg.Timeout}

	switch cfg.Provider {
	case ProviderOpenAI:
		return newOpenAI(cfg, hc), nil
	case ProviderAnthropic:
		return newAnthropic(cfg, hc), nil
	case ProviderGemini:
		return newGemini(cfg, hc), nil
	case ProviderFake:
		return &Fake{}, nil
	default:
		return nil, fmt.Errorf("unknown llm provider %q", cfg.Provider)
	}
}
