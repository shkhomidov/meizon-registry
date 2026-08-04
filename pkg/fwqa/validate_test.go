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

package fwqa

import (
	"strings"
	"testing"
)

// validTemplate returns a small but complete template that must pass Validate.
// Negative tests mutate a copy of it.
func validTemplate() *Template {
	return &Template{
		Schema:    SchemaMarker,
		ID:        "iso-audit",
		Framework: FrameworkRef{ID: "iso-27001", Version: "2022", Language: "en"},
		Title:     "ISO 27001 Audit",
		Scale:     &Scale{Kind: "maturity", Levels: []ScaleLevel{{0, "Absent"}, {3, "Defined"}}},
		VerdictModel: &VerdictModel{
			Verdicts: []string{VerdictCompliant, VerdictPartial, VerdictNonCompliant, VerdictNotApplicable},
			ScoreOf: map[string]*float64{
				VerdictCompliant: f64(1.0), VerdictPartial: f64(0.5),
				VerdictNonCompliant: f64(0.0), VerdictNotApplicable: nil,
			},
		},
		Sections: []Section{
			{
				Ref: "A.5", Name: "Organizational", Order: 1,
				Questions: []Question{
					{
						ID: "q1", Order: 1, RequirementRef: "A.5.1", Type: TypeYesNoEvidence,
						Text: "Is there an approved policy?", Weight: 3,
						Assessment: &Assessment{
							Rules: []Rule{
								{When: "answer == 'yes' && evidence.count >= 1", Verdict: VerdictCompliant},
								{When: "answer == 'no'", Verdict: VerdictNonCompliant},
							},
						},
						FollowUps: []FollowUp{{When: "answer == 'no'", AskID: "q2"}},
					},
					{
						ID: "q2", Order: 2, RequirementRef: "A.5.1", Type: TypeFreeText,
						Text: "Target date and owner?", Conditional: true,
					},
					{
						ID: "q3", Order: 3, RequirementRef: "A.5.10", Type: TypeSingleChoice,
						Text:    "How communicated?",
						Options: []Option{{Value: "annual", Label: "Annually"}, {Value: "none", Label: "Not"}},
						Assessment: &Assessment{Rules: []Rule{
							{When: "answer in ['annual']", Verdict: VerdictCompliant},
						}},
					},
				},
			},
		},
	}
}

func TestValidateAcceptsGoodTemplate(t *testing.T) {
	if err := validTemplate().Validate(); err != nil {
		t.Fatalf("a valid template was rejected: %v", err)
	}
}

func TestValidateRejects(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Template)
		wantSub string
	}{
		{"wrong schema", func(t *Template) { t.Schema = "nope" }, "$schema"},
		{"no id", func(t *Template) { t.ID = "" }, "id is required"},
		{"no framework", func(t *Template) { t.Framework.ID = "" }, "framework.id"},
		{"no sections", func(t *Template) { t.Sections = nil }, "no sections"},
		{"empty question id", func(t *Template) { t.Sections[0].Questions[0].ID = "" }, "empty id"},
		{"duplicate id", func(t *Template) { t.Sections[0].Questions[1].ID = "q1" }, "duplicate question id"},
		{"missing requirementRef", func(t *Template) { t.Sections[0].Questions[0].RequirementRef = "" }, "requirementRef"},
		{"unknown type", func(t *Template) { t.Sections[0].Questions[0].Type = "mystery" }, "unknown type"},
		{"empty text", func(t *Template) { t.Sections[0].Questions[0].Text = "" }, "empty text"},
		{"duplicate order", func(t *Template) { t.Sections[0].Questions[1].Order = 1 }, "order"},
		{"dangling askId", func(t *Template) { t.Sections[0].Questions[0].FollowUps[0].AskID = "ghost" }, "unknown question"},
		{"follow-up both targets", func(t *Template) {
			t.Sections[0].Questions[0].FollowUps[0].SkipTo = "q3"
		}, "exactly one"},
		{"orphaned conditional", func(t *Template) {
			// remove the only follow-up that reaches the conditional q2
			t.Sections[0].Questions[0].FollowUps = nil
		}, "unreachable"},
		{"choice without options", func(t *Template) { t.Sections[0].Questions[2].Options = nil }, "no options"},
		{"unknown verdict", func(t *Template) {
			t.Sections[0].Questions[0].Assessment.Rules[0].Verdict = "maybe"
		}, "unknown verdict"},
		{"bad rule expression", func(t *Template) {
			t.Sections[0].Questions[0].Assessment.Rules[0].When = "answer =="
		}, "bad rule"},
		{"typo'd variable in rule", func(t *Template) {
			t.Sections[0].Questions[0].Assessment.Rules[0].When = "anwser == 'yes'"
		}, "unknown variable"},
		{"bad follow-up expression", func(t *Template) {
			t.Sections[0].Questions[0].FollowUps[0].When = "&& broken"
		}, "bad condition"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tpl := validTemplate()
			tc.mutate(tpl)
			err := tpl.Validate()
			if err == nil {
				t.Fatalf("expected rejection for %q, got nil", tc.name)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("error %q does not mention %q", err.Error(), tc.wantSub)
			}
		})
	}
}

func TestComputeCounts(t *testing.T) {
	c := validTemplate().ComputeCounts()
	if c.Sections != 1 {
		t.Errorf("sections = %d, want 1", c.Sections)
	}
	if c.Questions != 3 {
		t.Errorf("questions = %d, want 3", c.Questions)
	}
	if c.Requirements != 2 { // A.5.1 and A.5.10
		t.Errorf("requirements = %d, want 2", c.Requirements)
	}
	if c.Conditional != 1 { // q2
		t.Errorf("conditional = %d, want 1", c.Conditional)
	}
}

func f64(v float64) *float64 { return &v }
