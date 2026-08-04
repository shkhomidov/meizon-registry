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
	"fmt"
	"strings"
)

// Validate checks a template is internally consistent — the guarantees a
// generator must meet and an editor must preserve before the template can drive
// an audit. It is strict on the things that would break a run (a follow-up
// pointing nowhere, an unreadable rule) and quiet on cosmetics.
func (t *Template) Validate() error {
	if t.Schema != SchemaMarker {
		return fmt.Errorf("$schema must be %q, got %q", SchemaMarker, t.Schema)
	}
	if strings.TrimSpace(t.ID) == "" {
		return fmt.Errorf("template id is required")
	}
	if strings.TrimSpace(t.Framework.ID) == "" {
		return fmt.Errorf("framework.id is required")
	}
	if len(t.Sections) == 0 {
		return fmt.Errorf("template has no sections")
	}

	all := t.AllQuestions()
	if len(all) == 0 {
		return fmt.Errorf("template has no questions")
	}

	// Question ids must be unique and non-empty — flow targets address them.
	ids := map[string]bool{}
	for _, q := range all {
		if strings.TrimSpace(q.ID) == "" {
			return fmt.Errorf("a question has an empty id")
		}
		if ids[q.ID] {
			return fmt.Errorf("duplicate question id %q", q.ID)
		}
		ids[q.ID] = true
	}

	// Any question that a follow-up can jump to (askId or skipTo).
	reachableByFollowUp := map[string]bool{}

	scaleKinds := map[string]bool{}
	if t.Scale != nil {
		scaleKinds[t.Scale.Kind] = true
	}

	for si := range t.Sections {
		sec := &t.Sections[si]
		orders := map[int]bool{}
		for qi := range sec.Questions {
			q := &sec.Questions[qi]

			if strings.TrimSpace(q.RequirementRef) == "" {
				return fmt.Errorf("question %q has no requirementRef", q.ID)
			}
			if !KnownTypes[q.Type] {
				return fmt.Errorf("question %q has unknown type %q", q.ID, q.Type)
			}
			if strings.TrimSpace(q.Text) == "" {
				return fmt.Errorf("question %q has empty text", q.ID)
			}
			// Order must be unique within a section — the audit asks by order.
			if orders[q.Order] {
				return fmt.Errorf("section %q has two questions at order %d", sec.Ref, q.Order)
			}
			orders[q.Order] = true

			if err := validateTypeSpecifics(q, scaleKinds); err != nil {
				return err
			}
			if err := validateAssessment(q); err != nil {
				return err
			}
			for _, f := range q.FollowUps {
				if err := validateFollowUp(q, f, ids); err != nil {
					return err
				}
				if f.AskID != "" {
					reachableByFollowUp[f.AskID] = true
				}
			}
		}
	}

	// A conditional question is only reached via a follow-up; if nothing points
	// at it, it is dead and was almost certainly a generation slip.
	for _, q := range all {
		if q.Conditional && !reachableByFollowUp[q.ID] {
			return fmt.Errorf("conditional question %q is unreachable — no follow-up targets it", q.ID)
		}
	}

	if t.VerdictModel != nil {
		if err := validateVerdictModel(t.VerdictModel); err != nil {
			return err
		}
	}
	return nil
}

func validateTypeSpecifics(q *Question, scaleKinds map[string]bool) error {
	switch q.Type {
	case TypeSingleChoice, TypeMultiChoice:
		if len(q.Options) == 0 {
			return fmt.Errorf("choice question %q has no options", q.ID)
		}
		seen := map[string]bool{}
		for _, o := range q.Options {
			if o.Value == "" {
				return fmt.Errorf("question %q has an option with an empty value", q.ID)
			}
			if seen[o.Value] {
				return fmt.Errorf("question %q has duplicate option value %q", q.ID, o.Value)
			}
			seen[o.Value] = true
		}
	case TypeScale:
		if q.ScaleRef != "" && !scaleKinds[q.ScaleRef] {
			return fmt.Errorf("scale question %q references unknown scale %q", q.ID, q.ScaleRef)
		}
	case TypeNumeric:
		if q.Min != nil && q.Max != nil && *q.Min > *q.Max {
			return fmt.Errorf("numeric question %q has min > max", q.ID)
		}
	case TypeAttestation:
		if q.Attestation == nil || strings.TrimSpace(q.Attestation.Statement) == "" {
			return fmt.Errorf("attestation question %q has no statement", q.ID)
		}
	}
	return nil
}

func validateAssessment(q *Question) error {
	if q.Assessment == nil {
		return nil
	}
	checkRules := func(rules []Rule, label string) error {
		for _, r := range rules {
			if err := CheckExpr(r.When); err != nil {
				return fmt.Errorf("question %q %s: bad rule %q: %w", q.ID, label, r.When, err)
			}
			if err := checkVars(r.When); err != nil {
				return fmt.Errorf("question %q %s: %w", q.ID, label, err)
			}
			if !KnownVerdicts[r.Verdict] {
				return fmt.Errorf("question %q %s: unknown verdict %q", q.ID, label, r.Verdict)
			}
		}
		return nil
	}
	if err := checkRules(q.Assessment.Rules, "rule"); err != nil {
		return err
	}
	if err := checkRules(q.Assessment.Thresholds, "threshold"); err != nil {
		return err
	}
	return nil
}

func validateFollowUp(q *Question, f FollowUp, ids map[string]bool) error {
	if err := CheckExpr(f.When); err != nil {
		return fmt.Errorf("question %q follow-up: bad condition %q: %w", q.ID, f.When, err)
	}
	if err := checkVars(f.When); err != nil {
		return fmt.Errorf("question %q follow-up: %w", q.ID, err)
	}
	if (f.AskID == "") == (f.SkipTo == "") {
		return fmt.Errorf("question %q follow-up must set exactly one of askId / skipTo", q.ID)
	}
	target := f.AskID
	if target == "" {
		target = f.SkipTo
	}
	if !ids[target] {
		return fmt.Errorf("question %q follow-up targets unknown question %q", q.ID, target)
	}
	return nil
}

func validateVerdictModel(vm *VerdictModel) error {
	for _, v := range vm.Verdicts {
		if !KnownVerdicts[v] {
			return fmt.Errorf("verdictModel lists unknown verdict %q", v)
		}
	}
	for v := range vm.ScoreOf {
		if !KnownVerdicts[v] {
			return fmt.Errorf("verdictModel.scoreOf keys unknown verdict %q", v)
		}
	}
	return nil
}

// checkVars rejects a reference to an unknown variable — a typo like `anwser`
// would otherwise silently compare against the empty string and never match.
func checkVars(expr string) error {
	vars, err := referencedVars(expr)
	if err != nil {
		return err
	}
	for _, v := range vars {
		if !KnownVar(v) {
			return fmt.Errorf("unknown variable %q (typo?)", v)
		}
	}
	return nil
}
