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

// QATemplateFor reconstructs the stored audit template for a framework's latest
// published version into a fwqa.Template — the shape the console and the preview
// runner consume. Returns ErrResourceNotFound if none has been generated.
func (s *Service) QATemplateFor(ctx context.Context, frameworkRef string) (*fwqa.Template, error) {
	var out *fwqa.Template
	err := s.db.WithConn(ctx, func(ctx context.Context, conn pg.Querier) error {
		scope := s.platformScope()

		var framework coredata.Framework
		if err := framework.LoadByReferenceID(ctx, conn, scope, frameworkRef); err != nil {
			return err
		}
		var pub coredata.FrameworkVersion
		if err := pub.LoadLatestPublished(ctx, conn, framework.ID); err != nil {
			return err
		}

		var row coredata.QATemplate
		if err := row.LoadTemplateByVersion(ctx, conn, scope, pub.ID); err != nil {
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
