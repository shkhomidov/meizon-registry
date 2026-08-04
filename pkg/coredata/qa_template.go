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
	"errors"
	"fmt"
	"maps"
	"time"

	"github.com/jackc/pgx/v5"
	"go.gearno.de/kit/pg"
	"go.meizon.cloud/registry/pkg/gid"
)

const (
	QATemplateStatusDraft = "draft"
	QATemplateStatusReady = "ready"
)

type (
	// QATemplate is the stored audit questionnaire for one framework version. The
	// questions are separate rows (QAQuestion); this row is the template's
	// metadata and template-level config.
	QATemplate struct {
		ID                 gid.GID   `db:"id"`
		FrameworkVersionID gid.GID   `db:"framework_version_id"`
		FrameworkRef       string    `db:"framework_ref"`
		Language           string    `db:"language"` // "" = canonical/source language
		Title              string    `db:"title"`
		Status             string    `db:"status"`
		GeneratedBy        string    `db:"generated_by"`
		Model              string    `db:"model"`
		Scale              []byte    `db:"scale"`
		VerdictModel       []byte    `db:"verdict_model"`
		Defaults           []byte    `db:"defaults"`
		CreatedAt          time.Time `db:"created_at"`
		UpdatedAt          time.Time `db:"updated_at"`
	}

	// QAQuestion is one row of a template. Structural fields (section, order,
	// requirement, type) are columns for querying and ordering; everything else
	// is the Body JSONB, which is the fwqa.Question minus those columns.
	QAQuestion struct {
		ID             gid.GID   `db:"id"`
		TemplateID     gid.GID   `db:"template_id"`
		SectionRef     string    `db:"section_ref"`
		SectionName    string    `db:"section_name"`
		SectionOrder   int       `db:"section_order"`
		Ord            int       `db:"ord"`
		RequirementRef string    `db:"requirement_ref"`
		ControlRef     string    `db:"control_ref"`
		Type           string    `db:"type"`
		Body           []byte    `db:"body"`
		CreatedAt      time.Time `db:"created_at"`
	}

	QAQuestions []*QAQuestion
)

const qaTemplateColumns = `id, framework_version_id, framework_ref, language, title, status, generated_by, model, scale, verdict_model, defaults, created_at, updated_at`
const qaQuestionColumns = `id, template_id, section_ref, section_name, section_order, ord, requirement_ref, control_ref, type, body, created_at`

// UpsertTemplate inserts a template or updates it on the unique framework
// version, returning the row's id (adopted on conflict). Regenerating a template
// re-uses the same row and replaces its questions (see ReplaceQuestions).
func (t *QATemplate) UpsertTemplate(ctx context.Context, conn pg.Tx, scope Scoper) error {
	q := `
INSERT INTO qa_templates (
    tenant_id, id, framework_version_id, framework_ref, language, title, status, generated_by, model,
    scale, verdict_model, defaults, created_at, updated_at
) VALUES (
    @tenant_id, @id, @framework_version_id, @framework_ref, @language, @title, @status, @generated_by, @model,
    @scale, @verdict_model, @defaults, @created_at, @updated_at
)
ON CONFLICT (framework_version_id, language) DO UPDATE SET
    title = EXCLUDED.title, status = EXCLUDED.status, generated_by = EXCLUDED.generated_by,
    model = EXCLUDED.model, scale = EXCLUDED.scale, verdict_model = EXCLUDED.verdict_model,
    defaults = EXCLUDED.defaults, updated_at = EXCLUDED.updated_at
RETURNING id;`

	args := pgx.StrictNamedArgs{
		"tenant_id":            scope.GetTenantID(),
		"id":                   t.ID,
		"framework_version_id": t.FrameworkVersionID,
		"framework_ref":        t.FrameworkRef,
		"language":             t.Language,
		"title":                t.Title,
		"status":               t.Status,
		"generated_by":         t.GeneratedBy,
		"model":                t.Model,
		"scale":                t.Scale,
		"verdict_model":        t.VerdictModel,
		"defaults":             t.Defaults,
		"created_at":           t.CreatedAt,
		"updated_at":           t.UpdatedAt,
	}
	return conn.QueryRow(ctx, q, args).Scan(&t.ID)
}

// LoadTemplateByVersion loads a framework version's template in the given
// language ("" is the canonical/source template).
func (t *QATemplate) LoadTemplateByVersion(ctx context.Context, conn pg.Querier, scope Scoper, versionID gid.GID, language string) error {
	q := fmt.Sprintf(`SELECT %s FROM qa_templates WHERE %s AND framework_version_id = @vid AND language = @lang LIMIT 1;`,
		qaTemplateColumns, scope.SQLFragment())
	args := pgx.StrictNamedArgs{"vid": versionID, "lang": language}
	maps.Copy(args, scope.SQLArguments())

	rows, err := conn.Query(ctx, q, args)
	if err != nil {
		return fmt.Errorf("cannot query qa template: %w", err)
	}
	row, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[QATemplate])
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrResourceNotFound
		}
		return fmt.Errorf("cannot collect qa template: %w", err)
	}
	*t = row
	return nil
}

// LoadTemplateByID loads a template by its id.
func (t *QATemplate) LoadTemplateByID(ctx context.Context, conn pg.Querier, scope Scoper, id gid.GID) error {
	q := fmt.Sprintf(`SELECT %s FROM qa_templates WHERE %s AND id = @id LIMIT 1;`,
		qaTemplateColumns, scope.SQLFragment())
	args := pgx.StrictNamedArgs{"id": id}
	maps.Copy(args, scope.SQLArguments())

	rows, err := conn.Query(ctx, q, args)
	if err != nil {
		return fmt.Errorf("cannot query qa template: %w", err)
	}
	row, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[QATemplate])
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrResourceNotFound
		}
		return fmt.Errorf("cannot collect qa template: %w", err)
	}
	*t = row
	return nil
}

// SetStatus updates a template's status and timestamp.
func (t QATemplate) SetStatus(ctx context.Context, conn pg.Tx, scope Scoper, id gid.GID, status string, now time.Time) error {
	q := fmt.Sprintf(`UPDATE qa_templates SET status = @status, updated_at = @now WHERE %s AND id = @id;`, scope.SQLFragment())
	args := pgx.StrictNamedArgs{"id": id, "status": status, "now": now}
	maps.Copy(args, scope.SQLArguments())
	_, err := conn.Exec(ctx, q, args)
	return err
}

// ReplaceQuestions swaps all of a template's questions for a new set in one
// transaction — the regeneration path. Deleting then inserting under the same
// tx means a reader never sees a half-replaced template.
func (t QATemplate) ReplaceQuestions(ctx context.Context, conn pg.Tx, scope Scoper, templateID gid.GID, questions QAQuestions) error {
	del := fmt.Sprintf(`DELETE FROM qa_questions WHERE %s AND template_id = @tid;`, scope.SQLFragment())
	dargs := pgx.StrictNamedArgs{"tid": templateID}
	maps.Copy(dargs, scope.SQLArguments())
	if _, err := conn.Exec(ctx, del, dargs); err != nil {
		return fmt.Errorf("cannot clear qa questions: %w", err)
	}
	for _, q := range questions {
		if err := q.insert(ctx, conn, scope); err != nil {
			return err
		}
	}
	return nil
}

func (q QAQuestion) insert(ctx context.Context, conn pg.Tx, scope Scoper) error {
	sql := `
INSERT INTO qa_questions (
    tenant_id, id, template_id, section_ref, section_name, section_order, ord,
    requirement_ref, control_ref, type, body, created_at
) VALUES (
    @tenant_id, @id, @template_id, @section_ref, @section_name, @section_order, @ord,
    @requirement_ref, @control_ref, @type, @body, @created_at
);`
	args := pgx.StrictNamedArgs{
		"tenant_id":       scope.GetTenantID(),
		"id":              q.ID,
		"template_id":     q.TemplateID,
		"section_ref":     q.SectionRef,
		"section_name":    q.SectionName,
		"section_order":   q.SectionOrder,
		"ord":             q.Ord,
		"requirement_ref": q.RequirementRef,
		"control_ref":     q.ControlRef,
		"type":            q.Type,
		"body":            q.Body,
		"created_at":      q.CreatedAt,
	}
	_, err := conn.Exec(ctx, sql, args)
	return err
}

// LoadQuestions returns a template's questions in section-then-order sequence.
func (qs *QAQuestions) LoadQuestions(ctx context.Context, conn pg.Querier, scope Scoper, templateID gid.GID) error {
	q := fmt.Sprintf(`SELECT %s FROM qa_questions WHERE %s AND template_id = @tid
ORDER BY section_order ASC, ord ASC, id ASC;`, qaQuestionColumns, scope.SQLFragment())
	args := pgx.StrictNamedArgs{"tid": templateID}
	maps.Copy(args, scope.SQLArguments())

	rows, err := conn.Query(ctx, q, args)
	if err != nil {
		return fmt.Errorf("cannot query qa questions: %w", err)
	}
	out, err := pgx.CollectRows(rows, pgx.RowToAddrOfStructByName[QAQuestion])
	if err != nil {
		return fmt.Errorf("cannot collect qa questions: %w", err)
	}
	*qs = out
	return nil
}

// UpdateQuestion replaces one question's editable fields (body + structural
// columns). Used by the console editor.
func (q QAQuestion) UpdateQuestion(ctx context.Context, conn pg.Tx, scope Scoper) error {
	sql := fmt.Sprintf(`UPDATE qa_questions
SET requirement_ref = @requirement_ref, control_ref = @control_ref, type = @type, body = @body
WHERE %s AND template_id = @template_id AND id = @id;`, scope.SQLFragment())
	args := pgx.StrictNamedArgs{
		"template_id":     q.TemplateID,
		"id":              q.ID,
		"requirement_ref": q.RequirementRef,
		"control_ref":     q.ControlRef,
		"type":            q.Type,
		"body":            q.Body,
	}
	maps.Copy(args, scope.SQLArguments())
	_, err := conn.Exec(ctx, sql, args)
	return err
}

// SetQuestionOrder updates one question's order within its section — the reorder
// primitive.
func (q QAQuestion) SetQuestionOrder(ctx context.Context, conn pg.Tx, scope Scoper, templateID, id gid.GID, ord int) error {
	sql := fmt.Sprintf(`UPDATE qa_questions SET ord = @ord WHERE %s AND template_id = @tid AND id = @id;`, scope.SQLFragment())
	args := pgx.StrictNamedArgs{"tid": templateID, "id": id, "ord": ord}
	maps.Copy(args, scope.SQLArguments())
	_, err := conn.Exec(ctx, sql, args)
	return err
}

// DeleteQuestion removes one question from a template.
func (q QAQuestion) DeleteQuestion(ctx context.Context, conn pg.Tx, scope Scoper, templateID, id gid.GID) error {
	sql := fmt.Sprintf(`DELETE FROM qa_questions WHERE %s AND template_id = @tid AND id = @id;`, scope.SQLFragment())
	args := pgx.StrictNamedArgs{"tid": templateID, "id": id}
	maps.Copy(args, scope.SQLArguments())
	_, err := conn.Exec(ctx, sql, args)
	return err
}
