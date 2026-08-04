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

	"go.meizon.cloud/registry/pkg/coredata"
	"go.meizon.cloud/registry/pkg/fwflat"
	"go.meizon.cloud/registry/pkg/fwqa"
)

// oneReqDoc builds a minimal flat framework with a single category and the given
// requirements under it — enough for assembleQATemplate.
func oneReqDoc(reqs ...fwflat.Requirement) *fwflat.Framework {
	return &fwflat.Framework{
		ID: "iso-27001", Version: "2022", Name: "ISO/IEC 27001",
		Categories:   []fwflat.Category{{Ref: "A.5", Name: "Organizational controls"}},
		Requirements: reqs,
	}
}

func assemble(t *testing.T, doc *fwflat.Framework, byReq map[string][]qaGenQuestion) *fwqa.Template {
	t.Helper()
	catName := map[string]string{}
	catOrder := map[string]int{}
	for i, c := range doc.Categories {
		catName[c.Ref] = c.Name
		catOrder[c.Ref] = i + 1
	}
	return (&Service{}).assembleQATemplate("iso-27001", doc, byReq, catName, catOrder, &coredata.LLMSetting{Model: "test-model"})
}

func findQuestion(tpl *fwqa.Template, id string) *fwqa.Question {
	for _, q := range tpl.AllQuestions() {
		if q.ID == id {
			return q
		}
	}
	return nil
}

// TestAssembleWiresFollowUpsToConditional is the core of the branching-audit
// feature: a question's index-based branch resolves to a sibling's deterministic
// id, the target keeps its conditional flag, and the whole template validates.
func TestAssembleWiresFollowUpsToConditional(t *testing.T) {
	doc := oneReqDoc(fwflat.Requirement{Ref: "A.5.1", Category: "A.5", Name: "Policies"})
	byReq := map[string][]qaGenQuestion{
		"A.5.1": {
			{
				Text: "Is there an approved information security policy?",
				Type: fwqa.TypeYesNoEvidence, Weight: 3,
				FollowUps: []qaGenFollowUp{{When: "answer == 'no'", Ask: 2}},
			},
			{
				Text: "What is the target date and owner to put an approved policy in place?",
				Type: fwqa.TypeFreeText, Conditional: true,
			},
		},
	}

	tpl := assemble(t, doc, byReq)
	if err := tpl.Validate(); err != nil {
		t.Fatalf("assembled template must validate, got: %v", err)
	}

	q1 := findQuestion(tpl, "q-A.5.1-1")
	if q1 == nil || len(q1.FollowUps) != 1 {
		t.Fatalf("q1 should carry exactly one follow-up, got %+v", q1)
	}
	if q1.FollowUps[0].AskID != "q-A.5.1-2" {
		t.Fatalf("follow-up askId = %q, want q-A.5.1-2", q1.FollowUps[0].AskID)
	}
	if q1.FollowUps[0].When != "answer == 'no'" {
		t.Fatalf("follow-up when mangled: %q", q1.FollowUps[0].When)
	}
	q2 := findQuestion(tpl, "q-A.5.1-2")
	if q2 == nil || !q2.Conditional {
		t.Fatalf("q2 should stay conditional (it is targeted), got %+v", q2)
	}
}

// TestAssembleDropsInvalidFollowUps: a self-loop, an out-of-range target, and an
// unparseable condition must each be dropped rather than sink the template.
func TestAssembleDropsInvalidFollowUps(t *testing.T) {
	doc := oneReqDoc(fwflat.Requirement{Ref: "A.5.1", Category: "A.5", Name: "Policies"})
	byReq := map[string][]qaGenQuestion{
		"A.5.1": {
			{
				Text: "Anchor question?", Type: fwqa.TypeYesNo,
				FollowUps: []qaGenFollowUp{
					{When: "answer == 'no'", Ask: 1},      // self-loop -> drop
					{When: "answer == 'no'", Ask: 9},      // out of range -> drop
					{When: "totally not an expr", Ask: 2}, // bad expr -> drop
				},
			},
			{Text: "A plain second question.", Type: fwqa.TypeFreeText},
		},
	}

	tpl := assemble(t, doc, byReq)
	if err := tpl.Validate(); err != nil {
		t.Fatalf("template must validate after dropping bad follow-ups, got: %v", err)
	}
	if q1 := findQuestion(tpl, "q-A.5.1-1"); q1 == nil || len(q1.FollowUps) != 0 {
		t.Fatalf("all three follow-ups should have been dropped, got %+v", q1)
	}
}

// TestAssembleDemotesOrphanConditional: a conditional question that nothing
// branches to would be unreachable (Validate rejects that), so it is demoted to a
// normal question instead of failing the run.
func TestAssembleDemotesOrphanConditional(t *testing.T) {
	doc := oneReqDoc(fwflat.Requirement{Ref: "A.5.1", Category: "A.5", Name: "Policies"})
	byReq := map[string][]qaGenQuestion{
		"A.5.1": {
			{Text: "First question, no branch.", Type: fwqa.TypeYesNo},
			{Text: "Marked conditional but nothing points here.", Type: fwqa.TypeFreeText, Conditional: true},
		},
	}

	tpl := assemble(t, doc, byReq)
	if err := tpl.Validate(); err != nil {
		t.Fatalf("orphan conditional should be demoted, not fail validation, got: %v", err)
	}
	if q2 := findQuestion(tpl, "q-A.5.1-2"); q2 == nil || q2.Conditional {
		t.Fatalf("orphan conditional should have been demoted, got %+v", q2)
	}
}

// TestSynthesizedQuestionValidates: the last-resort baseline for an empty
// requirement is itself a valid, scoreable question.
func TestSynthesizedQuestionValidates(t *testing.T) {
	req := fwflat.Requirement{Ref: "A.5.9", Category: "A.5", Name: "Inventory of assets"}
	doc := oneReqDoc(req)
	byReq := map[string][]qaGenQuestion{"A.5.9": {synthesizedQuestion(req)}}

	tpl := assemble(t, doc, byReq)
	if err := tpl.Validate(); err != nil {
		t.Fatalf("synthesized baseline must validate, got: %v", err)
	}
	q := findQuestion(tpl, "q-A.5.9-1")
	if q == nil || q.Type != fwqa.TypeYesNoEvidence || q.Assessment == nil {
		t.Fatalf("synthesized question malformed: %+v", q)
	}
}
