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

	"go.gearno.de/kit/pg"
	"go.meizon.cloud/registry/pkg/coredata"
	"go.meizon.cloud/registry/pkg/fwqa"
	"go.meizon.cloud/registry/pkg/gid"
)

// QATemplateView is the console-facing payload: the reconstructed template plus
// the DB identifiers the editor needs — the template's GID (which the per-question
// edit endpoints address) and its draft/ready status. The embedded *fwqa.Template
// flattens into the same JSON object, so the client sees one merged shape.
type QATemplateView struct {
	TemplateID string `json:"templateId"`
	Status     string `json:"status"`
	*fwqa.Template
}

// loadQATemplate loads the stored row and reconstructs the template for a
// framework's latest version (draft or published). It is the shared core behind
// the pure-template and console-view readers.
func (s *Service) loadQATemplate(ctx context.Context, frameworkRef string) (*coredata.QATemplate, *fwqa.Template, error) {
	var outRow *coredata.QATemplate
	var outTpl *fwqa.Template
	err := s.db.WithConn(ctx, func(ctx context.Context, conn pg.Querier) error {
		scope := s.platformScope()

		var framework coredata.Framework
		if err := framework.LoadByReferenceID(ctx, conn, scope, frameworkRef); err != nil {
			return err
		}
		// The audit template is bound to the framework's latest version (draft or
		// published) — it is authored alongside the requirements, not gated on a
		// published version.
		versionID, err := s.latestVersionIDConn(ctx, conn, framework.ID)
		if err != nil {
			return err
		}

		var row coredata.QATemplate
		if err := row.LoadTemplateByVersion(ctx, conn, scope, versionID); err != nil {
			return err
		}
		var questions coredata.QAQuestions
		if err := questions.LoadQuestions(ctx, conn, scope, row.ID); err != nil {
			return err
		}
		tpl, err := reconstructTemplate(&row, questions)
		if err != nil {
			return err
		}
		outRow = &row
		outTpl = tpl
		return nil
	})
	return outRow, outTpl, err
}

// QATemplateFor reconstructs the stored audit template for a framework's latest
// version into a fwqa.Template — the shape the preview runner consumes. Returns
// ErrResourceNotFound if none has been generated.
func (s *Service) QATemplateFor(ctx context.Context, frameworkRef string) (*fwqa.Template, error) {
	_, tpl, err := s.loadQATemplate(ctx, frameworkRef)
	return tpl, err
}

// QATemplateViewFor is the console read: the template plus its DB GID and status,
// so the editor can address questions and show the draft/ready state.
func (s *Service) QATemplateViewFor(ctx context.Context, frameworkRef string) (*QATemplateView, error) {
	row, tpl, err := s.loadQATemplate(ctx, frameworkRef)
	if err != nil {
		return nil, err
	}
	return &QATemplateView{TemplateID: row.ID.String(), Status: row.Status, Template: tpl}, nil
}

// QATemplateByID reconstructs a template by its id — used by the edit endpoints
// which address the template directly.
func (s *Service) QATemplateByID(ctx context.Context, templateID gid.GID) (*fwqa.Template, error) {
	var out *fwqa.Template
	err := s.db.WithConn(ctx, func(ctx context.Context, conn pg.Querier) error {
		scope := s.platformScope()
		var row coredata.QATemplate
		if err := row.LoadTemplateByID(ctx, conn, scope, templateID); err != nil {
			return err
		}
		var questions coredata.QAQuestions
		if err := questions.LoadQuestions(ctx, conn, scope, row.ID); err != nil {
			return err
		}
		tpl, err := reconstructTemplate(&row, questions)
		if err != nil {
			return err
		}
		out = tpl
		return nil
	})
	return out, err
}

// reconstructTemplate rebuilds a fwqa.Template from its stored row and question
// rows. Each question's body is unmarshalled, then the structural columns
// (authoritative for identity and order) are stamped over it.
func reconstructTemplate(row *coredata.QATemplate, questions coredata.QAQuestions) (*fwqa.Template, error) {
	tpl := &fwqa.Template{
		Schema:      fwqa.SchemaMarker,
		ID:          row.FrameworkRef + "-audit",
		Framework:   fwqa.FrameworkRef{ID: row.FrameworkRef},
		Title:       row.Title,
		GeneratedBy: row.GeneratedBy,
		Model:       row.Model,
	}
	if len(row.Scale) > 0 {
		_ = json.Unmarshal(row.Scale, &tpl.Scale)
	}
	if len(row.VerdictModel) > 0 {
		_ = json.Unmarshal(row.VerdictModel, &tpl.VerdictModel)
	}
	if len(row.Defaults) > 0 {
		_ = json.Unmarshal(row.Defaults, &tpl.Defaults)
	}

	// Group questions into sections, preserving the ORDER BY section/ord already
	// applied by the query.
	secIdx := map[string]int{}
	for _, q := range questions {
		var question fwqa.Question
		if err := json.Unmarshal(q.Body, &question); err != nil {
			return nil, fmt.Errorf("qa question %s has invalid body: %w", q.ID, err)
		}
		question.ID = q.ID.String()
		question.Order = q.Ord
		question.RequirementRef = q.RequirementRef
		question.ControlRef = q.ControlRef
		question.Type = q.Type

		i, ok := secIdx[q.SectionRef]
		if !ok {
			i = len(tpl.Sections)
			secIdx[q.SectionRef] = i
			tpl.Sections = append(tpl.Sections, fwqa.Section{Ref: q.SectionRef, Name: q.SectionName, Order: q.SectionOrder})
		}
		tpl.Sections[i].Questions = append(tpl.Sections[i].Questions, question)
	}
	return tpl, nil
}
