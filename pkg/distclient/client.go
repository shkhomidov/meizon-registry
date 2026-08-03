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
	"strconv"
	"strings"
	"time"

	"go.meizon.cloud/registry/pkg/fwmap"
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

// ChangeEvent mirrors one entry of the registry's change feed.
type ChangeEvent struct {
	Seq             int64     `json:"seq"`
	Kind            string    `json:"kind"` // published | deprecated | mapping_published | mapping_deprecated
	Framework       string    `json:"framework"`
	Version         string    `json:"version"`
	ContentHash     string    `json:"contentHash"`
	Region          string    `json:"region"`
	TargetFramework string    `json:"targetFramework"` // mapping_* events only
	TargetVersion   string    `json:"targetVersion"`
	OccurredAt      time.Time `json:"occurredAt"`
}

// ChangeFeed is one page of the feed plus the cursor to resume from.
type ChangeFeed struct {
	Events  []ChangeEvent `json:"events"`
	NextSeq int64         `json:"nextSeq"`
	HeadSeq int64         `json:"headSeq"`
	HasMore bool          `json:"hasMore"`
}

// Change-feed event kinds.
const (
	EventPublished         = "published"
	EventDeprecated        = "deprecated"
	EventMappingPublished  = "mapping_published"
	EventMappingDeprecated = "mapping_deprecated"
)

// MappingCatalogEntry mirrors a row of the mapping-set catalog.
type MappingCatalogEntry struct {
	Source          string    `json:"source"`
	SourceVersion   string    `json:"sourceVersion"`
	Target          string    `json:"target"`
	TargetVersion   string    `json:"targetVersion"`
	RequirementMaps int       `json:"requirementMappings"`
	ControlMaps     int       `json:"controlMappings"`
	ContentHash     string    `json:"contentHash"`
	PublishedAt     time.Time `json:"publishedAt"`
}

// MappingCatalog lists the published mapping sets visible to the token — what a
// cold start enumerates, since the change feed only carries sets published after
// the consumer's cursor.
func (c *Client) MappingCatalog(ctx context.Context) ([]MappingCatalogEntry, error) {
	var entries []MappingCatalogEntry
	if err := c.getJSON(ctx, "/api/registry/v1/mappings", &entries); err != nil {
		return nil, err
	}
	return entries, nil
}

// FetchMappingSet downloads a signed cross-mapping set and verifies it against
// the pinned keys, exactly as a framework bundle is verified — an unsigned,
// wrongly-keyed or tampered set is rejected and never returned. The set is
// addressed by both endpoints, since one source version can map to several
// targets.
func (c *Client) FetchMappingSet(ctx context.Context, source, sourceVer, target, targetVer string) (*fwmap.MappingSet, error) {
	path := fmt.Sprintf("/api/registry/v1/mappings/%s/%s/%s/%s",
		url.PathEscape(source), url.PathEscape(sourceVer),
		url.PathEscape(target), url.PathEscape(targetVer))

	var set fwmap.MappingSet
	if err := c.getJSON(ctx, path, &set); err != nil {
		return nil, err
	}
	if err := set.Verify(c.keys); err != nil {
		return nil, fmt.Errorf("mapping set %s@%s->%s@%s failed verification: %w",
			source, sourceVer, target, targetVer, err)
	}
	return &set, nil
}

// Changes fetches events after the cursor. A limit of 0 lets the server choose.
func (c *Client) Changes(ctx context.Context, since int64, limit int) (ChangeFeed, error) {
	q := url.Values{}
	q.Set("since", strconv.FormatInt(since, 10))
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	var feed ChangeFeed
	if err := c.getJSON(ctx, "/api/registry/v1/changes?"+q.Encode(), &feed); err != nil {
		return ChangeFeed{}, err
	}
	return feed, nil
}

// FetchBundle downloads a signed bundle and verifies it against the pinned keys.
// A bundle that is unsigned, signed by an unknown key, or tampered is rejected.
func (c *Client) FetchBundle(ctx context.Context, id, version string) (*fwschema.Framework, error) {
	bundle, _, err := c.fetchBundleCond(ctx, id, version, "")
	return bundle, err
}

// fetchBundleCond downloads and verifies a bundle, sending If-None-Match when a
// prior content hash is supplied. A 304 returns (nil, true, nil): the caller
// already holds this exact version and nothing was transferred. The ETag the
// server sets is the content hash, quoted.
func (c *Client) fetchBundleCond(ctx context.Context, id, version, heldContentHash string) (*fwschema.Framework, bool, error) {
	if version == "" {
		version = "latest"
	}
	path := fmt.Sprintf("/api/registry/v1/frameworks/%s/versions/%s", url.PathEscape(id), url.PathEscape(version))

	etag := ""
	if heldContentHash != "" {
		etag = `"` + heldContentHash + `"`
	}

	var bundle fwschema.Framework
	notModified, err := c.getJSONCond(ctx, path, etag, &bundle)
	if err != nil {
		return nil, false, err
	}
	if notModified {
		return nil, true, nil
	}

	if err := bundle.Verify(c.keys); err != nil {
		return nil, false, fmt.Errorf("bundle %s@%s failed verification: %w", id, version, err)
	}
	return &bundle, false, nil
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
	_, err := c.getJSONCond(ctx, path, "", out)
	return err
}

// getJSONCond issues a GET, optionally conditional on an ETag. It returns
// notModified=true for a 304 (leaving out untouched), which is a normal
// response to honour, not an error — the previous implementation treated every
// non-200 as failure, so the server's ETag support had no working client.
func (c *Client) getJSONCond(ctx context.Context, path, ifNoneMatch string, out any) (notModified bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	if ifNoneMatch != "" {
		req.Header.Set("If-None-Match", ifNoneMatch)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return false, fmt.Errorf("request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotModified {
		return true, nil
	}

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("registry returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	if err := json.Unmarshal(body, out); err != nil {
		return false, fmt.Errorf("cannot decode response: %w", err)
	}
	return false, nil
}
