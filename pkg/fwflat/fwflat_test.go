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

package fwflat

import (
	"strings"
	"testing"
)

// TestWarningsDoNotFailValidation is the regression for a real failure: a
// 106-page OCR'd GOST standard was discarded at the last step because two of
// its parallel process descriptions read identically. Repeated wording is
// normal in some standards, and a heuristic must not throw away a run that
// already cost an OCR pass and dozens of LLM calls.
func TestWarningsDoNotFailValidation(t *testing.T) {
	long := "The process shall be implemented in accordance with the organisation's " +
		"documented procedures and the results shall be recorded and maintained."

	f := &Framework{
		Schema: SchemaMarker, ID: "gost-12207", Name: "GOST R ISO/IEC 12207",
		Requirements: []Requirement{
			{Ref: "7.1.3.3.1.7", Name: "Task A", Description: long},
			{Ref: "7.1.4.3.1.8", Name: "Task B", Description: long},
		},
	}

	if err := f.Validate(ValidateOptions{}); err != nil {
		t.Fatalf("identical descriptions must not fail validation: %v", err)
	}

	w := f.Warnings(ValidateOptions{})
	if len(w) == 0 {
		t.Fatal("the duplicate descriptions must still be reported as a warning")
	}

	// The refs moved out of the sentence and into structure: the reviewer sees
	// them marked on the requirements themselves, which is where a duplicate
	// can actually be judged. Hunting refs out of prose was the old behaviour.
	groups := f.DuplicateDescriptions()
	if len(groups) != 1 {
		t.Fatalf("got %d duplicate group(s), want 1", len(groups))
	}
	if len(groups[0].Refs) != 2 {
		t.Fatalf("group holds %v, want both refs", groups[0].Refs)
	}
	for _, want := range []string{"7.1.3.3.1.7", "7.1.4.3.1.8"} {
		found := false
		for _, ref := range groups[0].Refs {
			if ref == want {
				found = true
			}
		}
		if !found {
			t.Errorf("duplicate group is missing %q: %v", want, groups[0].Refs)
		}
	}
	if !strings.Contains(groups[0].Description, "documented procedures") {
		t.Errorf("the group must carry the shared text so the UI can show WHAT matched, got %q", groups[0].Description)
	}
}

// TestDuplicateDescriptionsIgnoresShortText: "Reserved." and similar repeat
// legitimately everywhere, and marking them would bury the real signal.
func TestDuplicateDescriptionsIgnoresShortText(t *testing.T) {
	f := &Framework{
		Schema: SchemaMarker, ID: "x", Name: "X",
		Requirements: []Requirement{
			{Ref: "1", Name: "One", Description: "Reserved."},
			{Ref: "2", Name: "Two", Description: "Reserved."},
		},
	}
	if groups := f.DuplicateDescriptions(); len(groups) != 0 {
		t.Errorf("short repeated text must not be flagged, got %v", groups)
	}
}

// TestDuplicateDescriptionsGroupsAllMatches: three requirements sharing one
// description is ONE group of three, not three pairs — the reviewer should see
// the set, not a combinatorial list.
func TestDuplicateDescriptionsGroupsAllMatches(t *testing.T) {
	long := "The organisation shall retain records of every review for a period of not " +
		"less than five years and make them available on request."
	f := &Framework{
		Schema: SchemaMarker, ID: "x", Name: "X",
		Requirements: []Requirement{
			{Ref: "a", Name: "A", Description: long},
			{Ref: "b", Name: "B", Description: long},
			{Ref: "c", Name: "C", Description: long},
			{Ref: "d", Name: "D", Description: "something else entirely that is quite long but different from the others here"},
		},
	}
	groups := f.DuplicateDescriptions()
	if len(groups) != 1 {
		t.Fatalf("got %d group(s), want 1", len(groups))
	}
	if len(groups[0].Refs) != 3 {
		t.Errorf("group = %v, want all three matching refs together", groups[0].Refs)
	}
}

// TestStructuralFaultsStillFail: the split must not have softened the checks
// that produce genuinely unusable data.
func TestStructuralFaultsStillFail(t *testing.T) {
	base := func() *Framework {
		return &Framework{
			Schema: SchemaMarker, ID: "x", Name: "X",
			Requirements: []Requirement{{Ref: "1", Name: "One"}},
		}
	}

	dup := base()
	dup.Requirements = append(dup.Requirements, Requirement{Ref: "1", Name: "Duplicate"})
	if err := dup.Validate(ValidateOptions{}); err == nil {
		t.Error("a duplicate requirement ref must still fail")
	}

	dangling := base()
	dangling.Requirements[0].Category = "NOPE"
	if err := dangling.Validate(ValidateOptions{}); err == nil {
		t.Error("an unresolvable category ref must still fail")
	}

	badControl := base()
	badControl.Requirements[0].Controls = []string{"ghost"}
	if err := badControl.Validate(ValidateOptions{}); err == nil {
		t.Error("an unresolvable control ref must still fail")
	}
}

// TestCoverageShortfallIsAWarning: extracting far fewer requirements than the
// document has headings is worth flagging, but it is a heuristic count and must
// not destroy the run either.
func TestCoverageShortfallIsAWarning(t *testing.T) {
	f := &Framework{
		Schema: SchemaMarker, ID: "x", Name: "X",
		Requirements: []Requirement{{Ref: "1", Name: "Only one"}},
	}
	if err := f.Validate(ValidateOptions{ExpectedRequirements: 100}); err != nil {
		t.Fatalf("a coverage shortfall must not fail validation: %v", err)
	}
	if w := f.Warnings(ValidateOptions{ExpectedRequirements: 100}); len(w) == 0 {
		t.Error("a coverage shortfall must be reported as a warning")
	}
}
