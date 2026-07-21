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

// Package fwflat is the flat "meizon-framework/v2" exchange format produced by
// the document-ingestion pipeline (see docs/DOCUMENT-TO-FRAMEWORK-V2-GUIDE.md).
//
// It is deliberately FLAT: categories, requirements and controls are top-level
// collections addressed by natural `ref` keys, one framework per file, with no
// cross-framework mappings (those are joined in the database after import).
// This is not the registry's internal nested schema — pkg/fwschema — which the
// database and signed bundles use; conversion happens at accept time.
package fwflat

import (
	"fmt"
	"sort"
	"strings"
)

// SchemaMarker is the literal "$schema" value of the format.
const SchemaMarker = "meizon-framework/v2"

// Framework categories (the standard's subject area), per the guide's Step 1.
var FrameworkCategories = []string{
	"cybersecurity", "data_protection", "payment_security",
	"healthcare_privacy", "financial_resilience", "privacy", "other",
}

// ControlCategories classify a suggested implementation control (Step 3).
var ControlCategories = []string{
	"Policy", "Access", "Logging", "Encryption", "Network", "Endpoint",
	"Vulnerability", "Incident", "Vendor", "HR", "Physical", "Governance",
	"Monitoring", "Backup", "Other",
}

type (
	// Framework is one complete flat exchange file.
	Framework struct {
		Schema   string `json:"$schema"`
		ID       string `json:"id"`
		Name     string `json:"name"`
		Version  string `json:"version,omitempty"`
		Category string `json:"category,omitempty"`
		// Language is set on an exported translation. Absent means the canonical
		// record; a translation never differs from it structurally, only in text.
		Language     string        `json:"language,omitempty"`
		Regions      []string      `json:"regions,omitempty"`
		Categories   []Category    `json:"categories,omitempty"`
		Requirements []Requirement `json:"requirements"`
		Controls     []Control     `json:"controls,omitempty"`
	}

	// Category is a top-level grouping addressed by ref.
	Category struct {
		Ref  string `json:"ref"`
		Name string `json:"name"`
	}

	// Requirement is an individual, testable obligation. Category is the ref of
	// its group (empty when the standard is flat); Controls are refs into
	// Framework.Controls.
	Requirement struct {
		Ref         string   `json:"ref"`
		Category    string   `json:"category,omitempty"`
		Name        string   `json:"name"`
		Description string   `json:"description,omitempty"`
		Controls    []string `json:"controls,omitempty"`
	}

	// Control is a suggested, reusable implementation.
	Control struct {
		Ref         string `json:"ref"`
		Name        string `json:"name"`
		Description string `json:"description,omitempty"`
		Category    string `json:"category,omitempty"`
	}
)

// Counts summarises a framework for progress and QA reporting.
type Counts struct {
	Categories   int `json:"categories"`
	Requirements int `json:"requirements"`
	Controls     int `json:"controls"`
	Links        int `json:"links"`
}

// Count returns the collection sizes.
func (f *Framework) Count() Counts {
	c := Counts{
		Categories:   len(f.Categories),
		Requirements: len(f.Requirements),
		Controls:     len(f.Controls),
	}
	for _, r := range f.Requirements {
		c.Links += len(r.Controls)
	}
	return c
}

// ValidateOptions carries the checks that need context beyond the file itself.
type ValidateOptions struct {
	// ExpectedRequirements is the number of numbered headings found in the
	// source document. When > 0, gate 8 rejects a result that misses more than
	// 10% of them — the signal that a chunk silently returned nothing.
	ExpectedRequirements int
}

// Validate runs the guide's Step 4 gate. It returns the first failure; the
// caller surfaces it as "the generated framework is not valid: …".
func (f *Framework) Validate(opts ValidateOptions) error {
	// 1 — shape
	if f.Schema != SchemaMarker {
		return fmt.Errorf("$schema must be %q, got %q", SchemaMarker, f.Schema)
	}
	if strings.TrimSpace(f.ID) == "" {
		return fmt.Errorf("id is required")
	}
	if strings.TrimSpace(f.Name) == "" {
		return fmt.Errorf("name is required")
	}
	if f.Category != "" && !contains(FrameworkCategories, f.Category) {
		return fmt.Errorf("category %q is not one of %s", f.Category, strings.Join(FrameworkCategories, "|"))
	}

	// 2 — requirements non-empty, refs unique
	if len(f.Requirements) == 0 {
		return fmt.Errorf("no requirements were extracted")
	}
	seenReq := map[string]bool{}
	for _, r := range f.Requirements {
		if strings.TrimSpace(r.Ref) == "" {
			return fmt.Errorf("a requirement has an empty ref")
		}
		if seenReq[r.Ref] {
			return fmt.Errorf("duplicate requirement ref %q", r.Ref)
		}
		seenReq[r.Ref] = true
		if strings.TrimSpace(r.Name) == "" {
			return fmt.Errorf("requirement %q has no name", r.Ref)
		}
	}

	// 3 — every requirement.category resolves
	catRefs := map[string]bool{}
	for _, c := range f.Categories {
		if strings.TrimSpace(c.Ref) == "" {
			return fmt.Errorf("a category has an empty ref")
		}
		if catRefs[c.Ref] {
			return fmt.Errorf("duplicate category ref %q", c.Ref)
		}
		catRefs[c.Ref] = true
	}
	for _, r := range f.Requirements {
		if r.Category != "" && !catRefs[r.Category] {
			return fmt.Errorf("requirement %q references unknown category %q", r.Ref, r.Category)
		}
	}

	// 4 + 6 — control refs resolve; control categories are in the enum
	ctrlRefs := map[string]bool{}
	for _, c := range f.Controls {
		if strings.TrimSpace(c.Ref) == "" {
			return fmt.Errorf("a control has an empty ref")
		}
		if ctrlRefs[c.Ref] {
			return fmt.Errorf("duplicate control ref %q", c.Ref)
		}
		ctrlRefs[c.Ref] = true
		if c.Category != "" && !contains(ControlCategories, c.Category) {
			return fmt.Errorf("control %q has category %q, not one of %s", c.Ref, c.Category, strings.Join(ControlCategories, "|"))
		}
	}
	for _, r := range f.Requirements {
		for _, ref := range r.Controls {
			if !ctrlRefs[ref] {
				return fmt.Errorf("requirement %q references unknown control %q", r.Ref, ref)
			}
		}
	}

	// 5 — every requirement has at least one control (Step 3 guarantees this)
	if len(f.Controls) > 0 {
		for _, r := range f.Requirements {
			if len(r.Controls) == 0 {
				return fmt.Errorf("requirement %q has no control", r.Ref)
			}
		}
	}

	// Gates 7 and 8 are quality SMELLS, not structural faults — see Warnings.
	return nil
}

// dupThreshold: only descriptions this long are worth comparing. Short ones
// ("Reserved.", "Not applicable.") repeat legitimately all the time.
const dupThreshold = 80

// DuplicateGroup is a set of requirement refs that share the same description.
// Returned as structure rather than prose so the reviewer can be shown WHICH
// requirements are affected, on the requirements themselves, instead of having
// to hunt for refs listed in a sentence.
type DuplicateGroup struct {
	Refs        []string `json:"refs"`
	Description string   `json:"description,omitempty"`
}

// DuplicateDescriptions groups requirements whose long descriptions match
// exactly. Duplicates are permitted — some standards genuinely repeat wording —
// so this reports them for marking, never for rejection.
func (f *Framework) DuplicateDescriptions() []DuplicateGroup {
	byDesc := map[string][]string{}
	order := []string{}
	for _, r := range f.Requirements {
		d := strings.TrimSpace(r.Description)
		if len(d) < dupThreshold {
			continue
		}
		if _, seen := byDesc[d]; !seen {
			order = append(order, d)
		}
		byDesc[d] = append(byDesc[d], r.Ref)
	}

	var out []DuplicateGroup
	for _, d := range order {
		if len(byDesc[d]) < 2 {
			continue
		}
		out = append(out, DuplicateGroup{Refs: byDesc[d], Description: truncateRunes(d, 160)})
	}
	return out
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// Warnings returns advisory quality findings: things worth a human's attention
// that are not, on their own, evidence of a broken document.
//
// These used to fail generation outright. That was the wrong trade: both checks
// are heuristics, and failing on them discarded an entire run — including the
// OCR and every LLM call already paid for — over a judgement a reviewer is
// better placed to make. A real standard can legitimately repeat wording
// verbatim across sections (GOST R ISO/IEC 12207, whose parallel process
// descriptions triggered this, is exactly that shape).
//
// Structural faults — unresolvable refs, duplicate refs, a requirement with no
// control — still fail in Validate, because those produce data the rest of the
// system cannot use.
func (f *Framework) Warnings(opts ValidateOptions) []string {
	var out []string

	// 7 — repeated long descriptions. Reported as a count only; the affected
	// requirements are marked individually in the review editor, which is where
	// a reviewer can actually judge them.
	if groups := f.DuplicateDescriptions(); len(groups) > 0 {
		affected := 0
		for _, g := range groups {
			affected += len(g.Refs)
		}
		out = append(out, fmt.Sprintf(
			"%d requirement(s) in %d group(s) share an identical description — marked below. "+
				"Repeated wording is normal in some standards; check they are genuinely distinct obligations.",
			affected, len(groups)))
	}

	// 8 — coverage against the document's own numbered headings.
	if opts.ExpectedRequirements > 0 {
		got, want := len(f.Requirements), opts.ExpectedRequirements
		if float64(got) < float64(want)*0.9 {
			out = append(out, fmt.Sprintf(
				"only %d requirements were extracted but the document has about %d numbered headings — check whether a section was skipped",
				got, want))
		}
	}

	return out
}

// Normalize fills defaults and sorts nothing (document order is meaningful); it
// is applied before validation so a model omitting "$schema" still passes.
func (f *Framework) Normalize() {
	if f.Schema == "" {
		f.Schema = SchemaMarker
	}
	if len(f.Regions) == 0 {
		f.Regions = []string{"GLOBAL"}
	}
	for i := range f.Requirements {
		f.Requirements[i].Controls = dedupeStrings(f.Requirements[i].Controls)
	}
}

// PrimaryRegion is the region the registry stores on the framework row; the
// exchange format allows several, the database column holds one.
func (f *Framework) PrimaryRegion() string {
	if len(f.Regions) > 0 && strings.TrimSpace(f.Regions[0]) != "" {
		return f.Regions[0]
	}
	return "GLOBAL"
}

// ControlsByRef indexes the control list.
func (f *Framework) ControlsByRef() map[string]Control {
	out := make(map[string]Control, len(f.Controls))
	for _, c := range f.Controls {
		out[c.Ref] = c
	}
	return out
}

// SortedControlRefs returns a stable ref list, for prompting later batches.
func (f *Framework) SortedControlRefs() []string {
	refs := make([]string, 0, len(f.Controls))
	for _, c := range f.Controls {
		refs = append(refs, c.Ref)
	}
	sort.Strings(refs)
	return refs
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func dedupeStrings(in []string) []string {
	if len(in) == 0 {
		return in
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, v := range in {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}
