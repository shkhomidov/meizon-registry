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
	"crypto/ed25519"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"go.meizon.cloud/registry/pkg/fwmap"
	"go.meizon.cloud/registry/pkg/fwschema"
)

// fakeRegistry is a minimal, in-memory stand-in for the distribution API: it
// serves signed bundles, a catalog, and a change feed, so the client's sync
// logic can be exercised end to end without a database or the real server.
type fakeRegistry struct {
	priv     ed25519.PrivateKey
	keyID    string
	bundles  map[string]*fwschema.Framework // id -> latest signed bundle
	mappings map[string]*fwmap.MappingSet   // "src__tgt" -> signed set
	events   []ChangeEvent
	bundleGE int // count of bundle GETs, to prove skips avoid transfer
}

func newFakeRegistry(t *testing.T) *fakeRegistry {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	return &fakeRegistry{
		priv:     priv,
		keyID:    "test-key",
		bundles:  map[string]*fwschema.Framework{},
		mappings: map[string]*fwmap.MappingSet{},
	}
}

// publishMapping signs a one-mapping set and appends a mapping_published event,
// naming both endpoints the way the registry would.
func (fr *fakeRegistry) publishMapping(t *testing.T, source, sourceVer, target, targetVer string) {
	t.Helper()
	set := &fwmap.MappingSet{
		Schema: fwmap.SchemaMarker,
		Source: fwmap.Endpoint{Framework: source, Version: sourceVer},
		Target: fwmap.Endpoint{Framework: target, Version: targetVer},
		Requirements: []fwmap.Mapping{
			{SourceRef: "r1", TargetRef: "x1", Relation: "equivalent"},
		},
	}
	if err := set.Sign(fr.priv, fr.keyID); err != nil {
		t.Fatalf("sign mapping: %v", err)
	}
	fr.mappings[source+"__"+target] = set
	fr.events = append(fr.events, ChangeEvent{
		Seq: int64(len(fr.events) + 1), Kind: EventMappingPublished,
		Framework: source, Version: sourceVer,
		TargetFramework: target, TargetVersion: targetVer, Region: "EU",
	})
}

func (fr *fakeRegistry) keys() map[string]ed25519.PublicKey {
	return map[string]ed25519.PublicKey{fr.keyID: fr.priv.Public().(ed25519.PublicKey)}
}

// publish signs a one-requirement framework and appends a published event, the
// way the registry does on Publish.
func (fr *fakeRegistry) publish(t *testing.T, id, version, reqText string) {
	t.Helper()
	b := &fwschema.Framework{
		SchemaVersion: fwschema.SchemaVersion3,
		ID:            id,
		Name:          id,
		Version:       version,
		Status:        "PUBLISHED",
		Region:        "EU",
		License:       "public-domain",
		Categories: []fwschema.Category{{
			Code: "c1", Name: "Cat",
			Requirements: []fwschema.Requirement{{Code: "r1", Title: reqText, Description: reqText}},
		}},
	}
	if err := b.Sign(fr.priv, fr.keyID); err != nil {
		t.Fatalf("sign: %v", err)
	}
	fr.bundles[id] = b
	fr.events = append(fr.events, ChangeEvent{
		Seq: int64(len(fr.events) + 1), Kind: EventPublished,
		Framework: id, Version: version, ContentHash: b.Signature.ContentHash, Region: "EU",
	})
}

func (fr *fakeRegistry) deprecate(id string) {
	fr.events = append(fr.events, ChangeEvent{
		Seq: int64(len(fr.events) + 1), Kind: EventDeprecated, Framework: id, Region: "EU",
	})
	delete(fr.bundles, id)
}

func (fr *fakeRegistry) server() *httptest.Server {
	mux := http.NewServeMux()

	mux.HandleFunc("/api/registry/v1/catalog", func(w http.ResponseWriter, r *http.Request) {
		var out []CatalogEntry
		for id, b := range fr.bundles {
			out = append(out, CatalogEntry{ID: id, Name: id, Region: "EU", LatestVersion: b.Version, ContentHash: b.Signature.ContentHash})
		}
		_ = json.NewEncoder(w).Encode(out)
	})

	mux.HandleFunc("/api/registry/v1/changes", func(w http.ResponseWriter, r *http.Request) {
		since, _ := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
		feed := ChangeFeed{Events: []ChangeEvent{}, NextSeq: since}
		if len(fr.events) > 0 {
			feed.HeadSeq = fr.events[len(fr.events)-1].Seq
		}
		for _, e := range fr.events {
			if e.Seq > since {
				feed.Events = append(feed.Events, e)
				feed.NextSeq = e.Seq
			}
		}
		_ = json.NewEncoder(w).Encode(feed)
	})

	mux.HandleFunc("/api/registry/v1/mappings", func(w http.ResponseWriter, r *http.Request) {
		var out []MappingCatalogEntry
		for key, set := range fr.mappings {
			_ = key
			out = append(out, MappingCatalogEntry{
				Source: set.Source.Framework, SourceVersion: set.Source.Version,
				Target: set.Target.Framework, TargetVersion: set.Target.Version,
				RequirementMaps: len(set.Requirements), ControlMaps: len(set.Controls),
			})
		}
		_ = json.NewEncoder(w).Encode(out)
	})

	mux.HandleFunc("/api/registry/v1/mappings/", func(w http.ResponseWriter, r *http.Request) {
		// .../mappings/{source}/{sourceVer}/{target}/{targetVer}
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/registry/v1/mappings/"), "/")
		if len(parts) != 4 {
			http.Error(w, "bad path", http.StatusBadRequest)
			return
		}
		set, ok := fr.mappings[parts[0]+"__"+parts[2]]
		if !ok {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(set)
	})

	mux.HandleFunc("/api/registry/v1/frameworks/", func(w http.ResponseWriter, r *http.Request) {
		// .../frameworks/{id}/versions/{version}
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/registry/v1/frameworks/"), "/")
		id := parts[0]
		b, ok := fr.bundles[id]
		if !ok {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		etag := `"` + b.Signature.ContentHash + `"`
		w.Header().Set("ETag", etag)
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		fr.bundleGE++
		_ = json.NewEncoder(w).Encode(b)
	})

	return httptest.NewServer(mux)
}

func heldIDs(t *testing.T, dir string) map[string]heldFramework {
	t.Helper()
	st, err := loadState(dir)
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	return st.Frameworks
}

// TestSyncColdThenIncremental is the whole point: a first sync pulls everything,
// a second with no upstream change pulls nothing, and a third transfers only
// what was published in between.
func TestSyncColdThenIncremental(t *testing.T) {
	fr := newFakeRegistry(t)
	fr.publish(t, "iso-27001", "2022", "Access control")
	fr.publish(t, "gost-r", "2014", "Audit")

	srv := fr.server()
	defer srv.Close()
	c := New(srv.URL, "tok", fr.keys())
	dir := t.TempDir()

	// Cold start: both frameworks fetched.
	r1, err := Sync(context.Background(), c, "", dir)
	if err != nil {
		t.Fatalf("cold sync: %v", err)
	}
	if len(r1.Synced) != 2 {
		t.Fatalf("cold start should sync 2, got %d", len(r1.Synced))
	}
	if len(heldIDs(t, dir)) != 2 {
		t.Fatalf("state should hold 2 frameworks")
	}
	// A v3 bundle has one requirement; the count must not be zero.
	for _, s := range r1.Synced {
		if s.Units != 1 {
			t.Fatalf("expected 1 unit for %s, got %d (the controls=0 bug)", s.ID, s.Units)
		}
	}

	// Quiet re-run: the cursor is caught up, so nothing transfers.
	before := fr.bundleGE
	r2, err := Sync(context.Background(), c, "", dir)
	if err != nil {
		t.Fatalf("quiet sync: %v", err)
	}
	if len(r2.Synced) != 0 {
		t.Fatalf("a quiet re-run must sync nothing, synced %d", len(r2.Synced))
	}
	if fr.bundleGE != before {
		t.Fatalf("a quiet re-run must not fetch any bundle (fetched %d)", fr.bundleGE-before)
	}

	// Publish a third framework; only it should transfer.
	fr.publish(t, "nist", "r2", "Config management")
	r3, err := Sync(context.Background(), c, "", dir)
	if err != nil {
		t.Fatalf("incremental sync: %v", err)
	}
	if len(r3.Synced) != 1 || r3.Synced[0].ID != "nist" {
		t.Fatalf("incremental sync should pull only nist, got %+v", r3.Synced)
	}
	if _, ok := heldIDs(t, dir)["nist"]; !ok {
		t.Fatalf("nist should now be held")
	}
}

// TestSyncAppliesDeprecation: a deprecation event retires the local files and
// forgets the framework, so a consumer stops offering a withdrawn standard.
func TestSyncAppliesDeprecation(t *testing.T) {
	fr := newFakeRegistry(t)
	fr.publish(t, "temp-std", "1.0", "Temporary")

	srv := fr.server()
	defer srv.Close()
	c := New(srv.URL, "tok", fr.keys())
	dir := t.TempDir()

	if _, err := Sync(context.Background(), c, "", dir); err != nil {
		t.Fatalf("initial sync: %v", err)
	}
	seed := filepath.Join(dir, "temp-std.seed.json")
	if _, err := os.Stat(seed); err != nil {
		t.Fatalf("seed should exist after sync: %v", err)
	}

	fr.deprecate("temp-std")
	r, err := Sync(context.Background(), c, "", dir)
	if err != nil {
		t.Fatalf("sync after deprecate: %v", err)
	}
	if len(r.Removed) != 1 || r.Removed[0] != "temp-std" {
		t.Fatalf("expected temp-std retired, got %+v", r.Removed)
	}
	if _, err := os.Stat(seed); !os.IsNotExist(err) {
		t.Fatalf("seed of a deprecated framework should be gone")
	}
	if _, ok := heldIDs(t, dir)["temp-std"]; ok {
		t.Fatalf("deprecated framework should not remain in state")
	}
}

// TestSyncRejectsTamperedBundle: verification is the trust boundary, so a bundle
// whose bytes were altered after signing must be refused and not written.
func TestSyncRejectsTamperedBundle(t *testing.T) {
	fr := newFakeRegistry(t)
	fr.publish(t, "iso-27001", "2022", "Access control")
	// Tamper: change a requirement title after signing, leaving the signature
	// in place. The content hash no longer matches.
	fr.bundles["iso-27001"].Categories[0].Requirements[0].Title = "Altered"

	srv := fr.server()
	defer srv.Close()
	c := New(srv.URL, "tok", fr.keys())
	dir := t.TempDir()

	r, err := Sync(context.Background(), c, "", dir)
	if err != nil {
		t.Fatalf("sync should not hard-error on one bad bundle: %v", err)
	}
	if len(r.Synced) != 0 {
		t.Fatalf("a tampered bundle must not be synced")
	}
	if len(r.Failures) != 1 {
		t.Fatalf("expected one verification failure, got %d", len(r.Failures))
	}
	if _, err := os.Stat(filepath.Join(dir, "iso-27001.seed.json")); !os.IsNotExist(err) {
		t.Fatalf("a tampered bundle must never be written to disk")
	}
}

// TestSyncResumesFromPersistedCursor: a fresh client pointed at the same dir
// must continue from the stored cursor, not re-download everything — the
// property that makes sync survive process restarts.
func TestSyncResumesFromPersistedCursor(t *testing.T) {
	fr := newFakeRegistry(t)
	fr.publish(t, "a", "1", "one")

	srv := fr.server()
	defer srv.Close()
	dir := t.TempDir()

	c1 := New(srv.URL, "tok", fr.keys())
	if _, err := Sync(context.Background(), c1, "", dir); err != nil {
		t.Fatalf("first sync: %v", err)
	}

	// A brand new client (as if the process restarted) over the same dir.
	before := fr.bundleGE
	c2 := New(srv.URL, "tok", fr.keys())
	r, err := Sync(context.Background(), c2, "", dir)
	if err != nil {
		t.Fatalf("resumed sync: %v", err)
	}
	if len(r.Synced) != 0 || fr.bundleGE != before {
		t.Fatalf("a resumed sync with no upstream change must transfer nothing")
	}
}

// TestSyncPullsAndVerifiesMappingSet: a mapping_published event drives a fetch,
// verification and write of the .mzmap.json — the consumer half of getting
// cross-mappings across the wire.
func TestSyncPullsAndVerifiesMappingSet(t *testing.T) {
	fr := newFakeRegistry(t)
	fr.publish(t, "iso-27001", "2022", "Access control")
	fr.publish(t, "nist-csf", "2.0", "Govern")

	srv := fr.server()
	defer srv.Close()
	c := New(srv.URL, "tok", fr.keys())
	dir := t.TempDir()

	// Cold sync brings in both frameworks and sets the cursor.
	if _, err := Sync(context.Background(), c, "", dir); err != nil {
		t.Fatalf("cold sync: %v", err)
	}

	// A mapping set connecting the two is published.
	fr.publishMapping(t, "iso-27001", "2022", "nist-csf", "2.0")

	r, err := Sync(context.Background(), c, "", dir)
	if err != nil {
		t.Fatalf("mapping sync: %v", err)
	}
	if len(r.Mappings) != 1 {
		t.Fatalf("expected 1 mapping set synced, got %d (failures: %+v)", len(r.Mappings), r.Failures)
	}
	m := r.Mappings[0]
	if m.Source != "iso-27001" || m.Target != "nist-csf" {
		t.Fatalf("mapping endpoints wrong: %s -> %s", m.Source, m.Target)
	}
	if !m.Resolved {
		t.Fatalf("both frameworks are held, so the set should be marked resolved")
	}
	if _, err := os.Stat(filepath.Join(dir, "iso-27001__nist-csf.mzmap.json")); err != nil {
		t.Fatalf("mapping file not written: %v", err)
	}
}

// TestSyncMappingSetDanglesWhenTargetAbsent: a set whose target framework is not
// held is still fetched, verified and written — it resolves when the target
// arrives. A dangling reference is not an error.
func TestSyncMappingSetDanglesWhenTargetAbsent(t *testing.T) {
	fr := newFakeRegistry(t)
	fr.publish(t, "iso-27001", "2022", "Access control")

	srv := fr.server()
	defer srv.Close()
	c := New(srv.URL, "tok", fr.keys())
	dir := t.TempDir()

	if _, err := Sync(context.Background(), c, "", dir); err != nil {
		t.Fatalf("cold sync: %v", err)
	}
	// Map to a framework we do NOT hold.
	fr.publishMapping(t, "iso-27001", "2022", "unheld-fw", "1.0")

	r, err := Sync(context.Background(), c, "", dir)
	if err != nil {
		t.Fatalf("mapping sync: %v", err)
	}
	if len(r.Mappings) != 1 {
		t.Fatalf("expected the set to be synced despite the missing target, got %d", len(r.Mappings))
	}
	if r.Mappings[0].Resolved {
		t.Fatalf("a set whose target is not held must be marked unresolved")
	}
	if len(r.Failures) != 0 {
		t.Fatalf("a dangling set is not a failure, got %+v", r.Failures)
	}
}

// TestSyncRejectsTamperedMappingSet: verification is the trust boundary for a
// mapping set exactly as for a bundle — a tampered set is refused and not written.
func TestSyncRejectsTamperedMappingSet(t *testing.T) {
	fr := newFakeRegistry(t)
	fr.publish(t, "a", "1", "x")
	fr.publish(t, "b", "1", "y")

	srv := fr.server()
	defer srv.Close()
	c := New(srv.URL, "tok", fr.keys())
	dir := t.TempDir()
	if _, err := Sync(context.Background(), c, "", dir); err != nil {
		t.Fatalf("cold sync: %v", err)
	}

	fr.publishMapping(t, "a", "1", "b", "1")
	// Tamper after signing.
	fr.mappings["a__b"].Requirements[0].TargetRef = "altered"

	r, err := Sync(context.Background(), c, "", dir)
	if err != nil {
		t.Fatalf("sync should not hard-error: %v", err)
	}
	if len(r.Mappings) != 0 {
		t.Fatalf("a tampered mapping set must not be synced")
	}
	if len(r.Failures) != 1 {
		t.Fatalf("expected one verification failure, got %d", len(r.Failures))
	}
	if _, err := os.Stat(filepath.Join(dir, "a__b.mzmap.json")); !os.IsNotExist(err) {
		t.Fatalf("a tampered mapping set must never be written")
	}
}

// TestSyncColdStartEnumeratesMappingSets: a mapping set published BEFORE a
// consumer's first sync is not on the feed after the cursor, so cold start must
// enumerate it via the catalog — otherwise a fresh consumer would miss every
// pre-existing mapping.
func TestSyncColdStartEnumeratesMappingSets(t *testing.T) {
	fr := newFakeRegistry(t)
	fr.publish(t, "iso-27001", "2022", "Access control")
	fr.publish(t, "nist-csf", "2.0", "Govern")
	// Published before this consumer ever syncs.
	fr.publishMapping(t, "iso-27001", "2022", "nist-csf", "2.0")

	srv := fr.server()
	defer srv.Close()
	c := New(srv.URL, "tok", fr.keys())
	dir := t.TempDir()

	r, err := Sync(context.Background(), c, "", dir)
	if err != nil {
		t.Fatalf("cold sync: %v", err)
	}
	if len(r.Mappings) != 1 {
		t.Fatalf("cold start should enumerate the pre-existing mapping set, got %d (failures %+v)", len(r.Mappings), r.Failures)
	}
	if _, err := os.Stat(filepath.Join(dir, "iso-27001__nist-csf.mzmap.json")); err != nil {
		t.Fatalf("mapping file not written on cold start: %v", err)
	}
}
