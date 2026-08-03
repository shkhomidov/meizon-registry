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
	"os"
	"path/filepath"
	"testing"

	"go.meizon.cloud/registry/pkg/fwschema"
)

// bundleWith builds an unsigned v3 bundle from code->{title,description} triples,
// for the pure diff tests. reqs is a flat list of [code, title, description].
func bundleWith(id, version string, reqs [][3]string) *fwschema.Framework {
	cat := fwschema.Category{Code: "c1", Name: "Cat"}
	for _, r := range reqs {
		cat.Requirements = append(cat.Requirements, fwschema.Requirement{
			Code: r[0], Title: r[1], Description: r[2],
		})
	}
	return &fwschema.Framework{
		SchemaVersion: fwschema.SchemaVersion3,
		ID:            id, Name: id, Version: version, Status: "PUBLISHED",
		Categories: []fwschema.Category{cat},
	}
}

func TestDiffBundles(t *testing.T) {
	old := bundleWith("iso", "2022", [][3]string{
		{"A.1", "Access control", "Restrict access."},
		{"A.2", "Logging", "Keep logs."},
		{"A.3", "Backups", "Take backups."},
	})
	// A.1 unchanged; A.2 description changed; A.3 removed; A.4 added.
	newB := bundleWith("iso", "2023", [][3]string{
		{"A.1", "Access control", "Restrict access."},
		{"A.2", "Logging", "Keep logs for 12 months."},
		{"A.4", "Encryption", "Encrypt data at rest."},
	})

	d := DiffBundles(old, newB)

	if d.FromVersion != "2022" || d.ToVersion != "2023" {
		t.Fatalf("versions wrong: %s -> %s", d.FromVersion, d.ToVersion)
	}
	if len(d.Added) != 1 || d.Added[0] != "A.4" {
		t.Fatalf("added = %v, want [A.4]", d.Added)
	}
	if len(d.Removed) != 1 || d.Removed[0] != "A.3" {
		t.Fatalf("removed = %v, want [A.3]", d.Removed)
	}
	if len(d.Modified) != 1 || d.Modified[0].Ref != "A.2" {
		t.Fatalf("modified = %v, want [A.2]", d.Modified)
	}
	if len(d.Modified[0].Fields) != 1 || d.Modified[0].Fields[0] != "description" {
		t.Fatalf("A.2 should be modified in description only, got %v", d.Modified[0].Fields)
	}
	if d.Unchanged != 1 {
		t.Fatalf("unchanged = %d, want 1 (A.1)", d.Unchanged)
	}
	if !d.HasChanges() {
		t.Fatal("HasChanges should be true")
	}
}

// TestDiffRenameIsAddPlusRemove pins the identity rule: a requirement code is
// its identity, so renaming a code is a removal and an addition, never a
// modification. Treating it as a modify would silently rebind the consumer's
// evidence to a different obligation.
func TestDiffRenameIsAddPlusRemove(t *testing.T) {
	old := bundleWith("f", "1", [][3]string{{"OLD.1", "Thing", "Do the thing."}})
	newB := bundleWith("f", "2", [][3]string{{"NEW.1", "Thing", "Do the thing."}})

	d := DiffBundles(old, newB)
	if len(d.Added) != 1 || d.Added[0] != "NEW.1" {
		t.Fatalf("added = %v, want [NEW.1]", d.Added)
	}
	if len(d.Removed) != 1 || d.Removed[0] != "OLD.1" {
		t.Fatalf("removed = %v, want [OLD.1]", d.Removed)
	}
	if len(d.Modified) != 0 {
		t.Fatalf("a rename must not be a modify, got %v", d.Modified)
	}
}

// TestDiffFirstSightIsAllAdded: a nil baseline (never held) is an all-added
// delta, the correct description of a framework seen for the first time.
func TestDiffFirstSightIsAllAdded(t *testing.T) {
	newB := bundleWith("f", "1", [][3]string{{"A.1", "x", "y"}, {"A.2", "p", "q"}})
	d := DiffBundles(nil, newB)
	if len(d.Added) != 2 || d.Unchanged != 0 || len(d.Removed) != 0 {
		t.Fatalf("first sight should be all-added: %+v", d)
	}
}

// TestDiffTitleAndDescription: both fields changed are both reported, so a
// reviewer knows the whole requirement moved, not just one line.
func TestDiffTitleAndDescription(t *testing.T) {
	old := bundleWith("f", "1", [][3]string{{"A.1", "Old title", "Old body"}})
	newB := bundleWith("f", "2", [][3]string{{"A.1", "New title", "New body"}})
	d := DiffBundles(old, newB)
	if len(d.Modified) != 1 || len(d.Modified[0].Fields) != 2 {
		t.Fatalf("expected both fields changed, got %+v", d.Modified)
	}
}

// TestSyncUpgradeWritesDelta is the feature end to end: hold a framework, publish
// a revised version, sync, and assert the sync produced a delta naming exactly
// the changed requirement — and a .delta.json file on disk.
func TestSyncUpgradeWritesDelta(t *testing.T) {
	fr := newFakeRegistry(t)
	fr.publish(t, "iso-27001", "2022", "Access control")

	srv := fr.server()
	defer srv.Close()
	c := New(srv.URL, "tok", fr.keys())
	dir := t.TempDir()

	if _, err := Sync(context.Background(), c, "", dir); err != nil {
		t.Fatalf("initial sync: %v", err)
	}

	// Publish a new version with the single requirement's text changed. The
	// fakeRegistry uses one requirement "r1"; publishing again at a new version
	// with different text is a modify of r1.
	fr.publish(t, "iso-27001", "2023", "Access control, revised")

	r, err := Sync(context.Background(), c, "", dir)
	if err != nil {
		t.Fatalf("upgrade sync: %v", err)
	}
	if len(r.Synced) != 1 {
		t.Fatalf("expected 1 upgraded framework, got %d", len(r.Synced))
	}
	d := r.Synced[0].Delta
	if d == nil {
		t.Fatal("an upgrade must carry a delta")
	}
	if d.FromVersion != "2022" || d.ToVersion != "2023" {
		t.Fatalf("delta versions wrong: %s->%s", d.FromVersion, d.ToVersion)
	}
	if len(d.Modified) != 1 || d.Modified[0].Ref != "r1" {
		t.Fatalf("expected r1 modified, got %+v", d.Modified)
	}

	// The delta file must be on disk for the consumer's importer to read.
	deltaPath := filepath.Join(dir, "iso-27001.delta.json")
	data, err := os.ReadFile(deltaPath)
	if err != nil {
		t.Fatalf("delta file not written: %v", err)
	}
	var onDisk VersionDelta
	if err := json.Unmarshal(data, &onDisk); err != nil {
		t.Fatalf("delta file is not valid JSON: %v", err)
	}
	if onDisk.ToVersion != "2023" {
		t.Fatalf("delta file has wrong target version: %s", onDisk.ToVersion)
	}
}

// TestSyncFirstVersionWritesNoDelta: the initial sync of a framework has no
// baseline, so no delta file should appear — its presence must mean "an upgrade
// happened", nothing weaker.
func TestSyncFirstVersionWritesNoDelta(t *testing.T) {
	fr := newFakeRegistry(t)
	fr.publish(t, "new-fw", "1.0", "First")

	srv := fr.server()
	defer srv.Close()
	c := New(srv.URL, "tok", fr.keys())
	dir := t.TempDir()

	r, err := Sync(context.Background(), c, "", dir)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if r.Synced[0].Delta != nil {
		t.Fatalf("first sync must not produce a delta")
	}
	if _, err := os.Stat(filepath.Join(dir, "new-fw.delta.json")); !os.IsNotExist(err) {
		t.Fatalf("no delta file should exist after a first sync")
	}
}
