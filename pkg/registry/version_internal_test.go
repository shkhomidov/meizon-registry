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

package registry

import (
	"testing"

	"go.meizon.cloud/registry/pkg/fwflat"
)

// TestDiffFlatAgainstBaseline classifies a regenerated flat framework against
// the framework's current latest version.
func TestDiffFlatAgainstBaseline(t *testing.T) {
	baseline := []StructureCategory{{
		Code: "AC", Name: "Access Control",
		Requirements: []StructureRequirement{
			{Code: "AC-1", Title: "Policy", Description: "old"},
			{Code: "AC-9", Title: "Dropped", Description: "gone"},
		},
	}}

	doc := &fwflat.Framework{Requirements: []fwflat.Requirement{
		{Ref: "AC-1", Name: "Policy", Description: "new wording"}, // modified
		{Ref: "AC-2", Name: "Brand new"},                          // added
	}}

	diff := diffFlatAgainstBaseline(baseline, doc)
	for key, want := range map[string]string{
		"req:AC-1": ChangeModified,
		"req:AC-2": ChangeAdded,
		"req:AC-9": ChangeRemoved,
	} {
		if diff[key] != want {
			t.Errorf("diff[%q] = %q, want %q", key, diff[key], want)
		}
	}
}
