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

// Package fwqa is the schema for an agentic compliance audit questionnaire —
// meizon-qa-template/v1. A template is generated from a published framework's
// requirements: questions are asked in order, each addresses a requirement by
// its stable code (ref), and the agent turns answers into per-requirement
// verdicts using declared rules.
//
// The template is the audit *plan*, not a *run*: it says what to ask and how to
// score, but holds no answers. Binding questions to requirement refs — the same
// identity cross-mappings and version deltas use — is what lets a template
// survive a framework upgrade: only questions for changed refs need regenerating.
package fwqa

// SchemaMarker is the literal "$schema" value of the format.
const SchemaMarker = "meizon-qa-template/v1"

// Question types. The agent renders and scores each differently; a type it does
// not recognise is coerced to free_text at generation time rather than trusted.
const (
	TypeYesNo         = "yes_no"
	TypeYesNoNA       = "yes_no_na"
	TypeYesNoEvidence = "yes_no_evidence"
	TypeScale         = "scale"
	TypeSingleChoice  = "single_choice"
	TypeMultiChoice   = "multi_choice"
	TypeNumeric       = "numeric"
	TypeDate          = "date"
	TypeEvidence      = "evidence"
	TypeAttestation   = "attestation"
	TypeFreeText      = "free_text"
)

// KnownTypes is the closed set of question types. Generation validates the
// model's chosen type against it.
var KnownTypes = map[string]bool{
	TypeYesNo: true, TypeYesNoNA: true, TypeYesNoEvidence: true, TypeScale: true,
	TypeSingleChoice: true, TypeMultiChoice: true, TypeNumeric: true, TypeDate: true,
	TypeEvidence: true, TypeAttestation: true, TypeFreeText: true,
}

// Verdicts an assessment can reach.
const (
	VerdictCompliant     = "compliant"
	VerdictPartial       = "partial"
	VerdictNonCompliant  = "non_compliant"
	VerdictNotApplicable = "not_applicable"
)

// KnownVerdicts is the closed set of verdicts a rule may assign.
var KnownVerdicts = map[string]bool{
	VerdictCompliant: true, VerdictPartial: true, VerdictNonCompliant: true, VerdictNotApplicable: true,
}

type (
	// Template is a full audit questionnaire for one framework version.
	Template struct {
		Schema      string       `json:"$schema"`
		ID          string       `json:"id"`
		Framework   FrameworkRef `json:"framework"`
		Title       string       `json:"title"`
		Description string       `json:"description,omitempty"`

		GeneratedBy string `json:"generatedBy,omitempty"` // ai | human
		Model       string `json:"model,omitempty"`
		GeneratedAt string `json:"generatedAt,omitempty"`

		Scale        *Scale        `json:"scale,omitempty"`
		VerdictModel *VerdictModel `json:"verdictModel,omitempty"`
		Defaults     *Defaults     `json:"defaults,omitempty"`

		Sections []Section `json:"sections"`
		Meta     *Meta     `json:"meta,omitempty"`
	}

	// FrameworkRef binds a template to the framework version it audits.
	FrameworkRef struct {
		ID       string `json:"id"`
		Version  string `json:"version,omitempty"`
		Language string `json:"language,omitempty"`
	}

	// Section groups questions under a framework category.
	Section struct {
		Ref       string     `json:"ref"` // category ref in the framework
		Name      string     `json:"name"`
		Order     int        `json:"order"`
		Questions []Question `json:"questions"`
	}

	// Question is one audit prompt bound to a requirement by ref.
	Question struct {
		ID             string `json:"id"`
		Order          int    `json:"order"`
		RequirementRef string `json:"requirementRef"`
		ControlRef     string `json:"controlRef,omitempty"`

		Text   string `json:"text"`
		Intent string `json:"intent,omitempty"`
		Type   string `json:"type"`

		Required    bool `json:"required,omitempty"`
		Weight      int  `json:"weight,omitempty"`
		Conditional bool `json:"conditional,omitempty"` // reached only via a follow-up

		ExpectedEvidence []string `json:"expectedEvidence,omitempty"`

		// Type-specific fields (all omitempty; only those meaningful for the
		// question's type are set).
		Options       []Option     `json:"options,omitempty"`       // choice types
		MinSelections int          `json:"minSelections,omitempty"` // multi_choice
		Unit          string       `json:"unit,omitempty"`          // numeric
		Min           *float64     `json:"min,omitempty"`           // numeric
		Max           *float64     `json:"max,omitempty"`           // numeric
		EvidenceTypes []string     `json:"evidenceTypes,omitempty"` // evidence
		MinEvidence   int          `json:"minEvidence,omitempty"`   // evidence
		Attestation   *Attestation `json:"attestation,omitempty"`   // attestation
		ScaleRef      string       `json:"scaleRef,omitempty"`      // scale
		Placeholder   string       `json:"placeholder,omitempty"`   // free_text
		MaxLength     int          `json:"maxLength,omitempty"`     // free_text

		Assessment *Assessment `json:"assessment,omitempty"`
		FollowUps  []FollowUp  `json:"followUps,omitempty"`
	}

	// Option is a choice for single/multi choice questions.
	Option struct {
		Value string `json:"value"`
		Label string `json:"label"`
	}

	// Attestation is a signed-statement question's contract.
	Attestation struct {
		Statement        string `json:"statement"`
		RequireSignatory bool   `json:"requireSignatory,omitempty"`
	}

	// Assessment declares how the agent turns an answer into a verdict.
	Assessment struct {
		Criteria   string      `json:"criteria,omitempty"`
		Rules      []Rule      `json:"rules,omitempty"`      // ordered; first match wins
		Thresholds []Rule      `json:"thresholds,omitempty"` // numeric/date; same shape
		Rubric     []RubricRow `json:"rubric,omitempty"`     // scale descriptors
	}

	// Rule maps a `when` expression to a verdict. First matching rule wins.
	Rule struct {
		When    string `json:"when"`
		Verdict string `json:"verdict"`
	}

	// RubricRow describes a maturity level for a scale question.
	RubricRow struct {
		Level      int    `json:"level"`
		Descriptor string `json:"descriptor"`
	}

	// FollowUp inserts a conditional question, or skips ahead, when its `when`
	// holds. Exactly one of AskID / SkipTo is set.
	FollowUp struct {
		When   string `json:"when"`
		AskID  string `json:"askId,omitempty"`
		SkipTo string `json:"skipTo,omitempty"`
	}

	// Scale defines a rating scale (e.g. maturity 0..5).
	Scale struct {
		Kind   string       `json:"kind"`
		Levels []ScaleLevel `json:"levels"`
	}

	ScaleLevel struct {
		Value int    `json:"value"`
		Label string `json:"label"`
	}

	// VerdictModel makes scoring deterministic: the agent decides verdicts, the
	// arithmetic is declared here so any run of the same answers scores alike.
	VerdictModel struct {
		Verdicts            []string            `json:"verdicts"`
		ScoreOf             map[string]*float64 `json:"scoreOf"` // verdict -> score; null = excluded
		RequirementRollup   string              `json:"requirementRollup,omitempty"`
		NotApplicablePolicy string              `json:"notApplicablePolicy,omitempty"` // exclude | zero
	}

	// Defaults apply to questions that do not override them.
	Defaults struct {
		Required           bool     `json:"required,omitempty"`
		Weight             int      `json:"weight,omitempty"`
		AllowNotApplicable bool     `json:"allowNotApplicable,omitempty"`
		EvidenceTypes      []string `json:"evidenceTypes,omitempty"`
	}

	// Meta is advisory counts, recomputable from the body.
	Meta struct {
		Counts        Counts   `json:"counts"`
		QuestionTypes []string `json:"questionTypes,omitempty"`
	}

	Counts struct {
		Sections     int `json:"sections"`
		Requirements int `json:"requirements"`
		Questions    int `json:"questions"`
		Conditional  int `json:"conditional"`
	}
)

// AllQuestions flattens every question across sections in section-then-question
// order. Convenience for validation and for a runner that walks the template.
func (t *Template) AllQuestions() []*Question {
	var out []*Question
	for si := range t.Sections {
		for qi := range t.Sections[si].Questions {
			out = append(out, &t.Sections[si].Questions[qi])
		}
	}
	return out
}

// QuestionByID returns the question with the given id, or nil.
func (t *Template) QuestionByID(id string) *Question {
	for _, q := range t.AllQuestions() {
		if q.ID == id {
			return q
		}
	}
	return nil
}

// ComputeCounts recomputes Meta.Counts from the body.
func (t *Template) ComputeCounts() Counts {
	c := Counts{Sections: len(t.Sections)}
	refs := map[string]bool{}
	for _, q := range t.AllQuestions() {
		c.Questions++
		if q.Conditional {
			c.Conditional++
		}
		if q.RequirementRef != "" {
			refs[q.RequirementRef] = true
		}
	}
	c.Requirements = len(refs)
	return c
}
