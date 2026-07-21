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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
)

// redactSecrets strips anything that looks like a credential out of text that
// may reach a user or a log — belt and braces behind not putting keys in URLs.
func redactSecrets(text string) string {
	return secretPattern.ReplaceAllString(text, "${1}=REDACTED")
}

// key=… / api_key=… / access_token=… in a URL or message.
var secretPattern = regexp.MustCompile(`(?i)\b(key|api_key|apikey|access_token|token)=[^\s&"']+`)

// postJSON posts a JSON body and decodes the JSON response. Non-2xx responses
// are decoded too when possible (providers put error details in the body), then
// surfaced as an error including the status.
func postJSON(ctx context.Context, hc *http.Client, url string, headers map[string]string, body, out any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("cannot marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := hc.Do(req)
	if err != nil {
		// url.Error embeds the full request URL; redact before it escapes.
		return fmt.Errorf("llm request failed: %s", redactSecrets(err.Error()))
	}
	defer func() { _ = resp.Body.Close() }()

	data, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))

	if err := json.Unmarshal(data, out); err != nil {
		if resp.StatusCode >= 300 {
			return fmt.Errorf("llm provider returned %d: %s", resp.StatusCode,
				redactSecrets(strings.TrimSpace(string(data[:min(len(data), 300)]))))
		}
		return fmt.Errorf("cannot decode llm response: %w", err)
	}
	if resp.StatusCode >= 300 {
		// Decoded error bodies are handled by the callers' Error fields; still
		// guard against providers that return bare non-2xx.
		if strings.TrimSpace(string(data)) == "" {
			return fmt.Errorf("llm provider returned %d", resp.StatusCode)
		}
	}
	return nil
}
