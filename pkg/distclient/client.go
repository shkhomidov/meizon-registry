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

// Package distclient is the reference consumer of the registry distribution API.
// It is the code path a GRC instance runs to discover, download and verify
// frameworks — online. It pins the registry's ed25519 public keys and verifies
// every bundle before the seed is trusted, so a tampered or unsigned response is
// rejected exactly as it would be for an air-gapped import.
package distclient

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"go.meizon.cloud/registry/pkg/fwschema"
)

// Client talks to a registry's /api/registry/v1 surface with a bearer token and
// a pinned public-key set.
type Client struct {
	baseURL string
	token   string
	keys    map[string]ed25519.PublicKey
	http    *http.Client
}

// New builds a client. baseURL is the registry origin (e.g.
// https://registry.example.com); pinnedKeys maps signing key id to public key.
func New(baseURL, token string, pinnedKeys map[string]ed25519.PublicKey) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		keys:    pinnedKeys,
		http:    &http.Client{Timeout: 30 * time.Second},
	}
}

// CatalogEntry mirrors a distribution catalog row.
type CatalogEntry struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Region        string    `json:"region"`
	License       string    `json:"license"`
	LatestVersion string    `json:"latestVersion"`
	ContentHash   string    `json:"contentHash"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

// Catalog fetches the catalog, optionally filtered by region and a "since"
// timestamp (incremental sync).
func (c *Client) Catalog(ctx context.Context, region string, since *time.Time) ([]CatalogEntry, error) {
	q := url.Values{}
	if region != "" {
		q.Set("region", region)
	}
	if since != nil {
		q.Set("since", since.UTC().Format(time.RFC3339))
	}

	var entries []CatalogEntry
	if err := c.getJSON(ctx, "/api/registry/v1/catalog?"+q.Encode(), &entries); err != nil {
		return nil, err
	}
	return entries, nil
}

// FetchBundle downloads a signed bundle and verifies it against the pinned keys.
// A bundle that is unsigned, signed by an unknown key, or tampered is rejected.
func (c *Client) FetchBundle(ctx context.Context, id, version string) (*fwschema.Framework, error) {
	if version == "" {
		version = "latest"
	}

	var bundle fwschema.Framework
	if err := c.getJSON(ctx, fmt.Sprintf("/api/registry/v1/frameworks/%s/versions/%s", url.PathEscape(id), url.PathEscape(version)), &bundle); err != nil {
		return nil, err
	}

	if err := bundle.Verify(c.keys); err != nil {
		return nil, fmt.Errorf("bundle %s@%s failed verification: %w", id, version, err)
	}

	return &bundle, nil
}

// FetchSeed downloads a bundle, verifies it, and returns the flattened GRC seed
// ready for importFramework. The seed is derived locally from the verified
// bundle rather than trusting the server's /seed endpoint, so the import is only
// ever fed cryptographically verified content.
func (c *Client) FetchSeed(ctx context.Context, id, version string) (fwschema.GRCSeed, error) {
	bundle, err := c.FetchBundle(ctx, id, version)
	if err != nil {
		return fwschema.GRCSeed{}, err
	}
	return bundle.Flatten(), nil
}

func (c *Client) getJSON(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("registry returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("cannot decode response: %w", err)
	}
	return nil
}
