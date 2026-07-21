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

package distclient

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// SyncResult reports what a Sync run produced.
type SyncResult struct {
	Synced   []SyncedFramework
	Skipped  []string
	Failures []SyncFailure
}

// SyncedFramework is one framework verified and written during a sync.
type SyncedFramework struct {
	ID          string
	Version     string
	ContentHash string
	Controls    int
	SeedPath    string
}

// SyncFailure records a framework that could not be synced (e.g. verification
// failed). A failure never aborts the whole run — other frameworks still sync.
type SyncFailure struct {
	ID    string
	Error string
}

// Sync discovers every framework in the catalog (optionally region-filtered),
// downloads and verifies each latest version, and writes the verified flat seed
// (and signed bundle) into outDir. This is the reference implementation of the
// GRC-side online sync: verify-before-import, and never silently accept an
// unverifiable bundle.
func Sync(ctx context.Context, c *Client, region, outDir string) (SyncResult, error) {
	var result SyncResult

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return result, fmt.Errorf("cannot create output dir: %w", err)
	}

	catalog, err := c.Catalog(ctx, region, nil)
	if err != nil {
		return result, err
	}

	for _, entry := range catalog {
		bundle, err := c.FetchBundle(ctx, entry.ID, entry.LatestVersion)
		if err != nil {
			result.Failures = append(result.Failures, SyncFailure{ID: entry.ID, Error: err.Error()})
			continue
		}

		seed := bundle.Flatten()
		seedPath := filepath.Join(outDir, entry.ID+".seed.json")
		if err := writeJSONFile(seedPath, seed); err != nil {
			result.Failures = append(result.Failures, SyncFailure{ID: entry.ID, Error: err.Error()})
			continue
		}
		_ = writeJSONFile(filepath.Join(outDir, entry.ID+".mzfw.json"), bundle)

		result.Synced = append(result.Synced, SyncedFramework{
			ID:          entry.ID,
			Version:     bundle.Version,
			ContentHash: entry.ContentHash,
			Controls:    len(bundle.Controls),
			SeedPath:    seedPath,
		})
	}

	return result, nil
}

func writeJSONFile(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
