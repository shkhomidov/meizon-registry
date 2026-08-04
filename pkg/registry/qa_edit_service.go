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
	"context"
	"encoding/json"
	"fmt"
	"time"

	"go.gearno.de/kit/pg"
	"go.meizon.cloud/registry/pkg/coredata"
	"go.meizon.cloud/registry/pkg/fwqa"
	"go.meizon.cloud/registry/pkg/gid"
)

// UpdateQAQuestion replaces one question's editable content. The submitted
// question is validated in the context of its template so a bad rule or a
// dangling follow-up cannot be saved.
func (s *Service) UpdateQAQuestion(ctx context.Context, actorID, templateID, questionID gid.GID, q fwqa.Question) error {
	// Load the template, swap in the edited question, and validate the whole —
	// an edit that breaks a follow-up target elsewhere must be caught.
	tpl, err := s.QATemplateByID(ctx, templateID)
	if err != nil {
		return err
	}
	q.ID = questionID.String()
	if !replaceInTemplate(tpl, q) {
		return fmt.Errorf("%w: question %s not in template", ErrInvalidInput, questionID)
	}
	if err := tpl.Validate(); err != nil {
		return fmt.Errorf("%w: edit would break the template: %v", ErrInvalidInput, err)
	}

	body, err := json.Marshal(q)
	if err != nil {
		return err
	}
	return s.db.WithTx(ctx, func(ctx context.Context, tx pg.Tx) error {
		return coredata.QAQuestion{
			ID: questionID, TemplateID: templateID,
			RequirementRef: q.RequirementRef, ControlRef: q.ControlRef, Type: q.Type, Body: body,
		}.UpdateQuestion(ctx, tx, s.platformScope())
	})
}

// ReorderQAQuestions applies a new order to questions within their sections.
func (s *Service) ReorderQAQuestions(ctx context.Context, actorID, templateID gid.GID, order map[string]int) error {
	return s.db.WithTx(ctx, func(ctx context.Context, tx pg.Tx) error {
		scope := s.platformScope()
		for idStr, ord := range order {
			id, err := gid.ParseGID(idStr)
			if err != nil {
				return fmt.Errorf("%w: bad question id %q", ErrInvalidInput, idStr)
			}
			if err := (coredata.QAQuestion{}).SetQuestionOrder(ctx, tx, scope, templateID, id, ord); err != nil {
				return err
			}
		}
		return nil
	})
}

// DeleteQAQuestion removes a question. The remaining template must still
// validate — deleting a follow-up target would orphan a conditional question.
func (s *Service) DeleteQAQuestion(ctx context.Context, actorID, templateID, questionID gid.GID) error {
	tpl, err := s.QATemplateByID(ctx, templateID)
	if err != nil {
		return err
	}
	removeFromTemplate(tpl, questionID.String())
	if len(tpl.AllQuestions()) > 0 {
		if err := tpl.Validate(); err != nil {
			return fmt.Errorf("%w: deleting this question would break the template: %v", ErrInvalidInput, err)
		}
	}
	return s.db.WithTx(ctx, func(ctx context.Context, tx pg.Tx) error {
		return coredata.QAQuestion{}.DeleteQuestion(ctx, tx, s.platformScope(), templateID, questionID)
	})
}

// SetQATemplateStatus flips a template between draft and ready.
func (s *Service) SetQATemplateStatus(ctx context.Context, actorID, templateID gid.GID, status string) error {
	if status != coredata.QATemplateStatusDraft && status != coredata.QATemplateStatusReady {
		return fmt.Errorf("%w: unknown status %q", ErrInvalidInput, status)
	}
	return s.db.WithTx(ctx, func(ctx context.Context, tx pg.Tx) error {
		return coredata.QATemplate{}.SetStatus(ctx, tx, s.platformScope(), templateID, status, time.Now())
	})
}

// QAEvalResult is the outcome of evaluating one answered question — what the
// chat preview and a real session both render.
type QAEvalResult struct {
	Verdict   string   `json:"verdict,omitempty"`
	Score     *float64 `json:"score,omitempty"`
	FollowUps []string `json:"followUps,omitempty"`
	SkipTo    string   `json:"skipTo,omitempty"`
}

// EvaluateQAAnswer runs one answered question through the shared fwqa runner.
// This is the single evaluator — the preview calls it rather than reimplementing
// the `when` grammar in the browser.
func (s *Service) EvaluateQAAnswer(ctx context.Context, frameworkRef, questionID string, answer fwqa.Answer) (QAEvalResult, error) {
	tpl, err := s.QATemplateFor(ctx, frameworkRef)
	if err != nil {
		return QAEvalResult{}, err
	}
	step, err := tpl.Step(questionID, answer)
	if err != nil {
		return QAEvalResult{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	return QAEvalResult{Verdict: step.Verdict, Score: step.Score, FollowUps: step.FollowUps, SkipTo: step.SkipTo}, nil
}

// --- template mutation helpers (operate on the reconstructed fwqa.Template) ---

func replaceInTemplate(t *fwqa.Template, q fwqa.Question) bool {
	for si := range t.Sections {
		for qi := range t.Sections[si].Questions {
			if t.Sections[si].Questions[qi].ID == q.ID {
				// Preserve order; the edit form does not move a question.
				q.Order = t.Sections[si].Questions[qi].Order
				t.Sections[si].Questions[qi] = q
				return true
			}
		}
	}
	return false
}

func removeFromTemplate(t *fwqa.Template, id string) {
	for si := range t.Sections {
		kept := t.Sections[si].Questions[:0]
		for _, q := range t.Sections[si].Questions {
			if q.ID != id {
				kept = append(kept, q)
			}
		}
		t.Sections[si].Questions = kept
	}
}
