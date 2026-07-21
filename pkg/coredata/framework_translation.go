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

package coredata

import (
	"context"
	"fmt"
	"maps"
	"time"

	"github.com/jackc/pgx/v5"
	"go.gearno.de/kit/pg"
	"go.meizon.cloud/registry/pkg/gid"
)

// Translation node kinds. A requirement and a control can share a ref string,
// so the kind is part of the key.
const (
	TranslationNodeFramework   = "framework"
	TranslationNodeCategory    = "category"
	TranslationNodeRequirement = "requirement"
	TranslationNodeControl     = "control"
)

type (
	// FrameworkTranslation is one node's text in one language. It is an overlay
	// on the canonical record: it carries text and nothing structural, and is
	// addressed by ref so that inserting or reordering nodes cannot misalign it.
	FrameworkTranslation struct {
		ID                 gid.GID   `db:"id"`
		FrameworkVersionID gid.GID   `db:"framework_version_id"`
		Language           string    `db:"language"`
		NodeKind           string    `db:"node_kind"`
		NodeRef            string    `db:"node_ref"`
		Name               string    `db:"name"`
		Description        string    `db:"description"`
		Origin             string    `db:"origin"`
		CreatedAt          time.Time `db:"created_at"`
	}

	FrameworkTranslations []*FrameworkTranslation
)

const frameworkTranslationColumns = `id, framework_version_id, language, node_kind, node_ref, name, description, origin, created_at`

// Upsert writes one node's translation, replacing any previous text for the
// same (version, language, kind, ref).
func (t FrameworkTranslation) Upsert(ctx context.Context, conn pg.Tx, scope Scoper) error {
	q := `
INSERT INTO framework_translations (
    tenant_id, id, framework_version_id, language, node_kind, node_ref, name, description, origin, created_at
) VALUES (
    @tenant_id, @id, @framework_version_id, @language, @node_kind, @node_ref, @name, @description, @origin, @created_at
)
ON CONFLICT ON CONSTRAINT translations_unique DO UPDATE SET
  name = EXCLUDED.name,
  description = EXCLUDED.description,
  origin = EXCLUDED.origin,
  created_at = EXCLUDED.created_at;`

	args := pgx.StrictNamedArgs{
		"tenant_id":            scope.GetTenantID(),
		"id":                   t.ID,
		"framework_version_id": t.FrameworkVersionID,
		"language":             t.Language,
		"node_kind":            t.NodeKind,
		"node_ref":             t.NodeRef,
		"name":                 t.Name,
		"description":          t.Description,
		"origin":               orManual(t.Origin),
		"created_at":           t.CreatedAt,
	}

	_, err := conn.Exec(ctx, q, args)
	return err
}

// LoadAllByVersionAndLanguage returns every translated node of a version in one
// language.
func (ts *FrameworkTranslations) LoadAllByVersionAndLanguage(ctx context.Context, conn pg.Querier, scope Scoper, versionID gid.GID, language string) error {
	q := fmt.Sprintf(`SELECT %s FROM framework_translations WHERE %s AND framework_version_id = @version_id AND language = @language ORDER BY node_kind, node_ref;`,
		frameworkTranslationColumns, scope.SQLFragment())

	args := pgx.StrictNamedArgs{"version_id": versionID, "language": language}
	maps.Copy(args, scope.SQLArguments())

	rows, err := conn.Query(ctx, q, args)
	if err != nil {
		return fmt.Errorf("cannot query translations: %w", err)
	}

	out, err := pgx.CollectRows(rows, pgx.RowToAddrOfStructByName[FrameworkTranslation])
	if err != nil {
		return fmt.Errorf("cannot collect translations: %w", err)
	}

	*ts = out
	return nil
}

// LanguageRow reports one available language and how many nodes it covers.
// Served verbatim by the console API, hence the json tags.
type LanguageRow struct {
	Language string `db:"language" json:"language"`
	Nodes    int    `db:"nodes" json:"nodes"`
}

// LanguagesOfVersion lists the languages a version has been translated into.
func (ts FrameworkTranslations) LanguagesOfVersion(ctx context.Context, conn pg.Querier, scope Scoper, versionID gid.GID) ([]LanguageRow, error) {
	q := fmt.Sprintf(`SELECT language, COUNT(*)::int AS nodes FROM framework_translations
WHERE %s AND framework_version_id = @version_id GROUP BY language ORDER BY language;`, scope.SQLFragment())

	args := pgx.StrictNamedArgs{"version_id": versionID}
	maps.Copy(args, scope.SQLArguments())

	rows, err := conn.Query(ctx, q, args)
	if err != nil {
		return nil, fmt.Errorf("cannot query languages: %w", err)
	}

	out, err := pgx.CollectRows(rows, pgx.RowToStructByName[LanguageRow])
	if err != nil {
		return nil, fmt.Errorf("cannot collect languages: %w", err)
	}
	return out, nil
}

// DeleteByVersionAndLanguage removes a whole language overlay.
func (t FrameworkTranslation) DeleteByVersionAndLanguage(ctx context.Context, conn pg.Tx, scope Scoper, versionID gid.GID, language string) error {
	q := fmt.Sprintf(`DELETE FROM framework_translations WHERE %s AND framework_version_id = @version_id AND language = @language;`, scope.SQLFragment())
	args := pgx.StrictNamedArgs{"version_id": versionID, "language": language}
	maps.Copy(args, scope.SQLArguments())
	_, err := conn.Exec(ctx, q, args)
	return err
}
