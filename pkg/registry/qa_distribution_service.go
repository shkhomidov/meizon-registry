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
	"errors"

	"go.gearno.de/kit/pg"
	"go.meizon.cloud/registry/pkg/coredata"
	"go.meizon.cloud/registry/pkg/fwqa"
	"go.meizon.cloud/registry/pkg/gid"
)

// QATemplateDistribution serves a published version's audit template over the
// distribution API. It enforces the same copyright + region gate as the bundle
// (via resolveDistributable) and only serves a template marked `ready` — a draft
// is authoring-in-progress and never distributed. language selects a translation
// and falls back to the canonical template when that language is not translated.
// Returns ErrResourceNotFound when the version has no ready template.
func (s *Service) QATemplateDistribution(ctx context.Context, tc TokenContext, referenceID, version, language string) (*fwqa.Template, error) {
	var out *fwqa.Template
	err := s.db.WithConn(ctx, func(ctx context.Context, conn pg.Querier) error {
		framework, ver, err := s.resolveDistributable(ctx, conn, tc, referenceID, version)
		if err != nil {
			return err
		}
		scope := coredata.NewScopeFromObjectID(framework.ID)

		var row coredata.QATemplate
		err = row.LoadTemplateByVersion(ctx, conn, scope, ver.ID, language)
		if errors.Is(err, coredata.ErrResourceNotFound) && language != "" {
			err = row.LoadTemplateByVersion(ctx, conn, scope, ver.ID, "")
		}
		if err != nil {
			return err
		}
		if row.Status != coredata.QATemplateStatusReady {
			// Only a reviewed template is distributed; a draft looks like "none".
			return coredata.ErrResourceNotFound
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
		return s.recordDownload(ctx, conn, tc, ver, coredata.DownloadFormatQA)
	})
	return out, err
}

// VersionLanguages is the WithConn wrapper around DistributableLanguages for the
// framework metadata handler.
func (s *Service) VersionLanguages(ctx context.Context, framework coredata.Framework, versionID gid.GID) ([]string, error) {
	var out []string
	err := s.db.WithConn(ctx, func(ctx context.Context, conn pg.Querier) error {
		langs, err := s.DistributableLanguages(ctx, conn, framework, versionID)
		out = langs
		return err
	})
	return out, err
}

// DistributableLanguages lists the languages a published version is available in
// over the distribution API — the source language plus every stored translation.
// Used for discovery so a consumer knows which ?lang values to pull.
func (s *Service) DistributableLanguages(ctx context.Context, conn pg.Querier, framework coredata.Framework, versionID gid.GID) ([]string, error) {
	rows, err := (coredata.FrameworkTranslations{}).LanguagesOfVersion(ctx, conn, coredata.NewScopeFromObjectID(framework.ID), versionID)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	out := []string{}
	add := func(l string) {
		if l != "" && !seen[l] {
			seen[l] = true
			out = append(out, l)
		}
	}
	add(framework.SourceLanguage)
	for _, r := range rows {
		add(r.Language)
	}
	return out, nil
}
