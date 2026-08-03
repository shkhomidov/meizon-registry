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
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go.meizon.cloud/registry/pkg/fwschema"
)

// SyncResult reports what a Sync run produced.
type SyncResult struct {
	Synced          []SyncedFramework // frameworks fetched, verified and written this run
	Skipped         []string          // already held at the current content hash
	Removed         []string          // frameworks deprecated upstream, retired locally
	Mappings        []SyncedMapping   // cross-mapping sets fetched and verified this run
	RemovedMappings []string          // mapping sets deprecated upstream, retired locally
	Failures        []SyncFailure
	Cursor          int64 // the feed position persisted after this run
}

// SyncedFramework is one framework verified and written during a sync.
type SyncedFramework struct {
	ID          string
	Version     string
	ContentHash string
	Units       int // assessable units (requirements for v2/v3, controls for v1)
	SeedPath    string
	// Delta is set when this sync upgraded a framework already held at an older
	// version — it names exactly which requirements changed, so the consumer
	// re-reviews only those and keeps its work on the rest. Nil on a first sync.
	Delta *VersionDelta
}

// SyncFailure records a framework that could not be synced (e.g. verification
// failed). A failure never aborts the whole run — other frameworks still sync.
type SyncFailure struct {
	ID    string
	Error string
}

// syncStateFile is the per-directory record of how far the consumer has synced
// and what it already holds. It is what makes a run incremental: without it,
// every sync re-downloads the whole catalog.
const syncStateFile = ".registry-sync.json"

type syncState struct {
	// Cursor is the change-feed sequence this directory has consumed up to.
	// Zero means "never synced" — the first run falls back to the full catalog.
	Cursor     int64                    `json:"cursor"`
	Frameworks map[string]heldFramework `json:"frameworks"`
}

type heldFramework struct {
	Version     string `json:"version"`
	ContentHash string `json:"contentHash"`
}

// Sync brings outDir up to date with the registry, incrementally.
//
// First run (no cursor): it pulls the full catalog, verifies and writes each
// framework, and records the feed head as the starting cursor. Every run after:
// it drains the change feed from the stored cursor, fetching only what was
// published or deprecated since — a quiet registry transfers nothing.
//
// The cursor advances only after a page has been fully processed and the state
// written, so a crash mid-run re-processes rather than skips. Content hashes are
// compared before fetching, so a re-published-then-reverted framework, or a
// change event for a version already held, costs no download.
func Sync(ctx context.Context, c *Client, region, outDir string) (SyncResult, error) {
	var result SyncResult

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return result, fmt.Errorf("cannot create output dir: %w", err)
	}

	state, err := loadState(outDir)
	if err != nil {
		return result, err
	}
	if state.Frameworks == nil {
		state.Frameworks = map[string]heldFramework{}
	}

	if state.Cursor == 0 {
		// Cold start: nothing held, so walk the catalog once and adopt the feed
		// head as the cursor. From then on the feed drives everything.
		if err := c.syncFromCatalog(ctx, region, outDir, state, &result); err != nil {
			return result, err
		}
	} else {
		if err := c.syncFromChanges(ctx, outDir, state, &result); err != nil {
			return result, err
		}
	}

	result.Cursor = state.Cursor
	return result, saveState(outDir, state)
}

// syncFromCatalog is the cold-start path: no cursor yet, so enumerate the
// catalog, then set the cursor to the current feed head so the next run is
// incremental. Reading the head AFTER the catalog is deliberate — anything
// published during the walk is re-covered by the next changes poll rather than
// missed.
func (c *Client) syncFromCatalog(ctx context.Context, region, outDir string, state *syncState, result *SyncResult) error {
	catalog, err := c.Catalog(ctx, region, nil)
	if err != nil {
		return err
	}

	for _, entry := range catalog {
		c.pull(ctx, outDir, entry.ID, entry.LatestVersion, entry.ContentHash, state, result)
	}

	// The change feed only carries sets published after the cursor, so a cold
	// start must enumerate existing published mapping sets explicitly — the
	// mapping equivalent of walking the framework catalog. An older registry
	// without the endpoint returns an error, which is not fatal: mappings simply
	// arrive later via the feed.
	if entries, merr := c.MappingCatalog(ctx); merr == nil {
		for _, e := range entries {
			c.pullMapping(ctx, outDir, ChangeEvent{
				Framework: e.Source, Version: e.SourceVersion,
				TargetFramework: e.Target, TargetVersion: e.TargetVersion,
			}, state, result)
		}
	}

	// Adopt the head as the starting cursor. If the feed is unavailable (an
	// older registry), leave the cursor at zero so we safely full-sync again
	// next time rather than believing we are caught up.
	feed, err := c.Changes(ctx, 0, 1)
	if err == nil {
		state.Cursor = feed.HeadSeq
	}
	return nil
}

// syncFromChanges drains the feed from the stored cursor, page by page,
// persisting after each page so progress survives a crash.
func (c *Client) syncFromChanges(ctx context.Context, outDir string, state *syncState, result *SyncResult) error {
	for {
		feed, err := c.Changes(ctx, state.Cursor, 0)
		if err != nil {
			return err
		}

		for _, ev := range feed.Events {
			switch ev.Kind {
			case EventPublished:
				c.pull(ctx, outDir, ev.Framework, ev.Version, ev.ContentHash, state, result)
			case EventDeprecated:
				c.retire(outDir, ev.Framework, state, result)
			case EventMappingPublished:
				c.pullMapping(ctx, outDir, ev, state, result)
			case EventMappingDeprecated:
				retireMapping(outDir, ev, result)
			default:
				// Unknown kind from a newer registry: skip rather than misroute
				// it into the framework path, which would report a spurious
				// failure. A cursor still advances past it.
				result.Skipped = append(result.Skipped, ev.Framework)
			}
		}

		// Advance only over events actually processed, and persist before asking
		// for more, so an interruption resumes here rather than past a gap.
		state.Cursor = feed.NextSeq
		if err := saveState(outDir, state); err != nil {
			return err
		}
		if !feed.HasMore {
			return nil
		}
	}
}

// pull fetches, verifies and writes one framework unless the content hash we
// already hold matches — in which case nothing is transferred and it is recorded
// as skipped. The known hash is also sent as If-None-Match, so even a first-time
// fetch of an unchanged version short-circuits at the server.
func (c *Client) pull(ctx context.Context, outDir, id, version, contentHash string, state *syncState, result *SyncResult) {
	held, have := state.Frameworks[id]
	if have && contentHash != "" && held.ContentHash == contentHash {
		result.Skipped = append(result.Skipped, id)
		return
	}

	bundle, notModified, err := c.fetchBundleCond(ctx, id, version, held.ContentHash)
	if err != nil {
		result.Failures = append(result.Failures, SyncFailure{ID: id, Error: err.Error()})
		return
	}
	if notModified {
		result.Skipped = append(result.Skipped, id)
		return
	}

	bundlePath := filepath.Join(outDir, id+".mzfw.json")

	// If we already hold an older version, compute the delta BEFORE overwriting
	// the bundle on disk — the old bundle is the only baseline we have, and the
	// delta is what lets the consumer upgrade without re-reviewing everything.
	var delta *VersionDelta
	if have {
		if old := readHeldBundle(bundlePath); old != nil && old.Version != bundle.Version {
			d := DiffBundles(old, bundle)
			delta = &d
		}
	}

	seedPath := filepath.Join(outDir, id+".seed.json")
	if err := writeJSONFile(seedPath, bundle.Flatten()); err != nil {
		result.Failures = append(result.Failures, SyncFailure{ID: id, Error: err.Error()})
		return
	}
	if err := writeJSONFile(bundlePath, bundle); err != nil {
		result.Failures = append(result.Failures, SyncFailure{ID: id, Error: err.Error()})
		return
	}
	// A delta file is written only when there is a real change to apply, so its
	// presence is a signal, not noise.
	if delta != nil && delta.HasChanges() {
		_ = writeJSONFile(filepath.Join(outDir, id+".delta.json"), delta)
	}

	state.Frameworks[id] = heldFramework{Version: bundle.Version, ContentHash: bundle.Signature.ContentHash}
	result.Synced = append(result.Synced, SyncedFramework{
		ID:          id,
		Version:     bundle.Version,
		ContentHash: bundle.Signature.ContentHash,
		Units:       AssessableCount(bundle),
		SeedPath:    seedPath,
		Delta:       delta,
	})
}

// readHeldBundle loads a previously written bundle for diffing. A missing or
// unreadable file yields nil — the sync then treats the framework as new and
// writes a full seed rather than a delta, which is the safe degradation.
func readHeldBundle(path string) *fwschema.Framework {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var b fwschema.Framework
	if err := json.Unmarshal(data, &b); err != nil {
		return nil
	}
	return &b
}

// retire drops a deprecated framework's local files and forgets it, so a
// consumer that acts on the seed stops offering a withdrawn framework. The
// deprecation is recorded even if the files were already gone.
func (c *Client) retire(outDir, id string, state *syncState, result *SyncResult) {
	_ = os.Remove(filepath.Join(outDir, id+".seed.json"))
	_ = os.Remove(filepath.Join(outDir, id+".mzfw.json"))
	delete(state.Frameworks, id)
	result.Removed = append(result.Removed, id)
}

// SyncedMapping records a cross-mapping set written this run.
type SyncedMapping struct {
	Source       string
	Target       string
	Path         string
	Requirements int
	Controls     int
	// Resolved is false when the target framework is not held locally — the set
	// is written and verified, but its target refs dangle until that framework
	// is pulled. A dangling set is not an error; it resolves late.
	Resolved bool
}

// mappingFilename is stable and reversible so a later deprecation can find the
// file without holding extra state. Slashes in refs are not expected (codes and
// framework ids do not contain them) but are replaced defensively.
func mappingFilename(source, target string) string {
	safe := func(s string) string { return strings.ReplaceAll(s, "/", "_") }
	return safe(source) + "__" + safe(target) + ".mzmap.json"
}

// pullMapping fetches, verifies and writes a cross-mapping set announced by a
// mapping_published event. Verification is the trust boundary, identical to a
// framework bundle: a set that does not verify is refused and never written.
func (c *Client) pullMapping(ctx context.Context, outDir string, ev ChangeEvent, state *syncState, result *SyncResult) {
	set, err := c.FetchMappingSet(ctx, ev.Framework, ev.Version, ev.TargetFramework, ev.TargetVersion)
	if err != nil {
		result.Failures = append(result.Failures, SyncFailure{
			ID:    ev.Framework + "->" + ev.TargetFramework,
			Error: err.Error(),
		})
		return
	}

	path := filepath.Join(outDir, mappingFilename(ev.Framework, ev.TargetFramework))
	if err := writeJSONFile(path, set); err != nil {
		result.Failures = append(result.Failures, SyncFailure{ID: ev.Framework + "->" + ev.TargetFramework, Error: err.Error()})
		return
	}

	// A set whose target framework is not held locally still writes — its refs
	// resolve once that framework arrives. Resolution is read from the live
	// in-memory state, which reflects a target pulled earlier in this same run
	// (the on-disk copy is only flushed at page boundaries).
	_, targetHeld := state.Frameworks[ev.TargetFramework]
	result.Mappings = append(result.Mappings, SyncedMapping{
		Source: ev.Framework, Target: ev.TargetFramework, Path: path,
		Requirements: len(set.Requirements), Controls: len(set.Controls),
		Resolved: targetHeld,
	})
}

// retireMapping removes a deprecated mapping set's file.
func retireMapping(outDir string, ev ChangeEvent, result *SyncResult) {
	_ = os.Remove(filepath.Join(outDir, mappingFilename(ev.Framework, ev.TargetFramework)))
	result.RemovedMappings = append(result.RemovedMappings, ev.Framework+"->"+ev.TargetFramework)
}

// AssessableCount reports the number of assessable units in a bundle. A v2/v3
// bundle carries requirements under categories and an empty top-level Controls
// slice, so len(bundle.Controls) reported zero for every modern framework —
// which made a successful sync look empty.
func AssessableCount(b *fwschema.Framework) int {
	if len(b.Categories) > 0 {
		n := 0
		b.WalkAssessable(func(_ *fwschema.Category, _ *fwschema.Requirement, _ *fwschema.Item) { n++ })
		return n
	}
	return len(b.Controls)
}

func loadState(outDir string) (*syncState, error) {
	data, err := os.ReadFile(filepath.Join(outDir, syncStateFile))
	if errors.Is(err, os.ErrNotExist) {
		return &syncState{Frameworks: map[string]heldFramework{}}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cannot read sync state: %w", err)
	}
	var st syncState
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, fmt.Errorf("sync state is corrupt (delete %s to re-sync from scratch): %w", syncStateFile, err)
	}
	return &st, nil
}

func saveState(outDir string, st *syncState) error {
	return writeJSONFile(filepath.Join(outDir, syncStateFile), st)
}

func writeJSONFile(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
