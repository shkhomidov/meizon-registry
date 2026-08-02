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
	"sort"

	"go.meizon.cloud/registry/pkg/fwschema"
)

// A framework upgrade must not orphan the consumer's work. A GRC attaches
// evidence, assessments and internal-control links to each requirement; a blind
// re-import throws all of that away. So an upgrade is expressed as a delta keyed
// on requirement CODE — the stable identity, the same one cross-mappings use —
// and the consumer applies only what changed, keeping everything attached to a
// code that did not move.
//
// The delta is derived from two bundles the client has already verified, so it
// needs no signature of its own: it inherits the trust of two independent
// signatures. There is deliberately no distributed delta artifact to keep
// consistent with the bundles it describes.

// VersionDelta is the difference between two versions of one framework.
type VersionDelta struct {
	Framework   string        `json:"framework"`
	FromVersion string        `json:"fromVersion"`
	ToVersion   string        `json:"toVersion"`
	Added       []string      `json:"added"`     // requirement codes new in the target
	Removed     []string      `json:"removed"`   // codes gone from the target
	Modified    []ModifiedReq `json:"modified"`  // codes whose text changed
	Unchanged   int           `json:"unchanged"` // count kept as-is
}

// ModifiedReq names a requirement whose content changed and which fields moved,
// so a reviewer re-checks only what actually differs.
type ModifiedReq struct {
	Ref    string   `json:"ref"`
	Fields []string `json:"fields"` // e.g. ["title","description"]
}

// reqView is the subset of a requirement the delta compares. Codes are identity;
// title and description are the assessable content a change in which warrants
// re-review. Guidance and number are advisory and deliberately excluded — a
// renumbering or a clarified note is not a change to the obligation.
type reqView struct {
	title       string
	description string
}

// DiffBundles computes the delta from oldB to newB. Both must be verified by the
// caller — the delta says nothing about authenticity, only about content. A nil
// old bundle yields an all-added delta, which is the correct description of a
// framework seen for the first time.
func DiffBundles(oldB, newB *fwschema.Framework) VersionDelta {
	d := VersionDelta{
		Framework: newB.ID,
		ToVersion: newB.Version,
		Added:     []string{},
		Removed:   []string{},
		Modified:  []ModifiedReq{},
	}
	if oldB != nil {
		d.FromVersion = oldB.Version
	}

	oldReqs := collectRequirements(oldB)
	newReqs := collectRequirements(newB)

	for code, nv := range newReqs {
		ov, existed := oldReqs[code]
		switch {
		case !existed:
			d.Added = append(d.Added, code)
		case ov != nv:
			d.Modified = append(d.Modified, ModifiedReq{Ref: code, Fields: changedFields(ov, nv)})
		default:
			d.Unchanged++
		}
	}
	for code := range oldReqs {
		if _, stillThere := newReqs[code]; !stillThere {
			d.Removed = append(d.Removed, code)
		}
	}

	// Deterministic ordering so the written delta and its content hash do not
	// wobble between runs over identical input.
	sort.Strings(d.Added)
	sort.Strings(d.Removed)
	sort.Slice(d.Modified, func(i, j int) bool { return d.Modified[i].Ref < d.Modified[j].Ref })

	return d
}

// HasChanges reports whether the delta represents any structural or content
// change — used to decide whether writing a delta file is worthwhile.
func (d VersionDelta) HasChanges() bool {
	return len(d.Added) > 0 || len(d.Removed) > 0 || len(d.Modified) > 0
}

// collectRequirements flattens a bundle to code -> content. A nil bundle yields
// an empty map so a first sight of a framework diffs as all-added. Both v1
// (flat controls) and v2/v3 (categorised requirements) are covered:
// WalkAssessable visits requirements, and legacy top-level controls are folded
// in so a v1 bundle is not silently empty.
func collectRequirements(b *fwschema.Framework) map[string]reqView {
	out := map[string]reqView{}
	if b == nil {
		return out
	}
	b.WalkAssessable(func(_ *fwschema.Category, req *fwschema.Requirement, item *fwschema.Item) {
		// A legacy document nests items under a requirement; the item is the
		// assessable leaf and carries its own code. A modern document has no
		// items, so the requirement itself is the leaf.
		if item != nil {
			out[item.Code] = reqView{title: item.Title, description: item.Description}
			return
		}
		out[req.Code] = reqView{title: req.Title, description: req.Description}
	})
	for i := range b.Controls {
		c := &b.Controls[i]
		if _, seen := out[c.ID]; !seen {
			out[c.ID] = reqView{title: c.Name, description: c.Description}
		}
	}
	return out
}

func changedFields(oldV, newV reqView) []string {
	var f []string
	if oldV.title != newV.title {
		f = append(f, "title")
	}
	if oldV.description != newV.description {
		f = append(f, "description")
	}
	return f
}
