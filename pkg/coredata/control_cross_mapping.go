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

type (
	// ControlCrossMapping relates a control to a control in another framework.
	// Same stub design as RequirementCrossMapping: the code pair is the truth
	// and the resolved id is derived, so a mapping can name a target that is not
	// loaded yet and bind to it later without touching signed content.
	ControlCrossMapping struct {
		ID                       gid.GID   `db:"id"`
		SourceControlID          gid.GID   `db:"source_control_id"`
		SourceFrameworkVersionID gid.GID   `db:"source_framework_version_id"`
		Relation                 string    `db:"relation"`
		TargetControlID          *gid.GID  `db:"target_control_id"`
		TargetFrameworkCode      string    `db:"target_framework_code"`
		TargetFrameworkVersion   string    `db:"target_framework_version"`
		TargetControlCode        string    `db:"target_control_code"`
		IsResolved               bool      `db:"is_resolved"`
		Confidence               float64   `db:"confidence"`
		Rationale                string    `db:"rationale"`
		ReviewState              string    `db:"review_state"`
		Notes                    string    `db:"notes"`
		CreatedAt                time.Time `db:"created_at"`
	}

	ControlCrossMappings []*ControlCrossMapping
)

const controlCrossMappingColumns = `id, source_control_id, source_framework_version_id, relation, target_control_id, target_framework_code, target_framework_version, target_control_code, is_resolved, confidence, rationale, review_state, notes, created_at`

func (m ControlCrossMapping) Insert(ctx context.Context, conn pg.Tx, scope Scoper) error {
	q := `
INSERT INTO control_cross_mappings (
    tenant_id, id, source_control_id, source_framework_version_id, relation,
    target_control_id, target_framework_code, target_framework_version, target_control_code,
    confidence, rationale, review_state, notes, created_at
) VALUES (
    @tenant_id, @id, @source_control_id, @source_framework_version_id, @relation,
    @target_control_id, @target_framework_code, @target_framework_version, @target_control_code,
    @confidence, @rationale, @review_state, @notes, @created_at
);`

	args := pgx.StrictNamedArgs{
		"tenant_id":                   scope.GetTenantID(),
		"id":                          m.ID,
		"source_control_id":           m.SourceControlID,
		"source_framework_version_id": m.SourceFrameworkVersionID,
		"relation":                    m.Relation,
		"target_control_id":           m.TargetControlID,
		"target_framework_code":       m.TargetFrameworkCode,
		"target_framework_version":    m.TargetFrameworkVersion,
		"target_control_code":         m.TargetControlCode,
		"confidence":                  m.Confidence,
		"rationale":                   m.Rationale,
		"review_state":                orCurrent(m.ReviewState),
		"notes":                       m.Notes,
		"created_at":                  m.CreatedAt,
	}

	_, err := conn.Exec(ctx, q, args)
	if err != nil {
		if isUniqueViolation(err, "cxmap_unique") {
			return ErrResourceAlreadyExists
		}
		return err
	}
	return nil
}

// orCurrent defaults an unset review state, mirroring the column default.
func orCurrent(state string) string {
	if state == "" {
		return MappingReviewCurrent
	}
	return state
}

func (m ControlCrossMapping) Delete(ctx context.Context, conn pg.Tx, scope Scoper, id gid.GID) error {
	q := fmt.Sprintf(`DELETE FROM control_cross_mappings WHERE %s AND id = @id;`, scope.SQLFragment())
	args := pgx.StrictNamedArgs{"id": id}
	maps.Copy(args, scope.SQLArguments())
	_, err := conn.Exec(ctx, q, args)
	return err
}

// LoadAllByVersion returns every control mapping authored on a source version.
func (ms *ControlCrossMappings) LoadAllByVersion(ctx context.Context, conn pg.Querier, scope Scoper, versionID gid.GID) error {
	q := fmt.Sprintf(`SELECT %s FROM control_cross_mappings WHERE %s AND source_framework_version_id = @version_id
ORDER BY created_at ASC, target_framework_code ASC, target_control_code ASC, relation ASC, id ASC;`,
		controlCrossMappingColumns, scope.SQLFragment())

	args := pgx.StrictNamedArgs{"version_id": versionID}
	maps.Copy(args, scope.SQLArguments())

	rows, err := conn.Query(ctx, q, args)
	if err != nil {
		return fmt.Errorf("cannot query control mappings: %w", err)
	}
	out, err := pgx.CollectRows(rows, pgx.RowToAddrOfStructByName[ControlCrossMapping])
	if err != nil {
		return fmt.Errorf("cannot collect control mappings: %w", err)
	}
	*ms = out
	return nil
}

// ResolveOwnStubs binds this version's unresolved control stubs to controls of
// the target framework. Control library rows are framework-scoped rather than
// version-scoped, so the join goes through frameworks, not framework_versions.
func (ms ControlCrossMappings) ResolveOwnStubs(ctx context.Context, conn pg.Querier, scope Scoper, sourceVersionID gid.GID) (int, error) {
	q := fmt.Sprintf(`
UPDATE control_cross_mappings x
SET    target_control_id = cl.id
FROM   control_library cl
JOIN   frameworks f ON f.id = cl.framework_id
WHERE  x.%s
  AND  x.source_framework_version_id = @source_version_id
  AND  x.target_control_id IS NULL
  AND  x.target_framework_code = f.reference_id
  AND  x.target_control_code = cl.code;`, scope.SQLFragment())

	args := pgx.StrictNamedArgs{"source_version_id": sourceVersionID}
	maps.Copy(args, scope.SQLArguments())

	tag, err := conn.Exec(ctx, q, args)
	if err != nil {
		return 0, fmt.Errorf("cannot resolve control stubs: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// MarkReviewState flags mappings whose source refs need re-review after the
// framework they point at changed underneath them.
func (ms ControlCrossMappings) MarkReviewState(ctx context.Context, conn pg.Tx, scope Scoper, versionID gid.GID, refs []string, state string) error {
	if len(refs) == 0 {
		return nil
	}
	q := fmt.Sprintf(`UPDATE control_cross_mappings SET review_state = @state
WHERE %s AND source_framework_version_id = @version_id AND target_control_code = ANY(@refs::text[]);`, scope.SQLFragment())

	args := pgx.StrictNamedArgs{"version_id": versionID, "refs": refs, "state": state}
	maps.Copy(args, scope.SQLArguments())
	_, err := conn.Exec(ctx, q, args)
	return err
}

// Coverage returns per-relation counts of a version's control mappings,
// optionally for one target framework. Mirrors the requirement-level report so
// the console can show both halves of a framework's coverage.
func (ms ControlCrossMappings) Coverage(ctx context.Context, conn pg.Querier, scope Scoper, sourceVersionID gid.GID, targetCode string) ([]CoverageRow, error) {
	q := fmt.Sprintf(`
SELECT relation, COUNT(*)::int AS total, COUNT(target_control_id)::int AS resolved
FROM control_cross_mappings
WHERE %s
  AND source_framework_version_id = @version_id
  AND (@target_code = '' OR target_framework_code = @target_code)
GROUP BY relation
ORDER BY relation;`, scope.SQLFragment())

	args := pgx.StrictNamedArgs{"version_id": sourceVersionID, "target_code": targetCode}
	maps.Copy(args, scope.SQLArguments())

	rows, err := conn.Query(ctx, q, args)
	if err != nil {
		return nil, fmt.Errorf("cannot query control coverage: %w", err)
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByName[CoverageRow])
	if err != nil {
		return nil, fmt.Errorf("cannot collect control coverage: %w", err)
	}
	return out, nil
}

// Update changes an auditor-editable field of a control mapping.
func (m ControlCrossMapping) Update(ctx context.Context, conn pg.Tx, scope Scoper, id gid.GID, relation, notes string) error {
	q := fmt.Sprintf(`UPDATE control_cross_mappings
SET relation = @relation, notes = @notes, review_state = 'current'
WHERE %s AND id = @id;`, scope.SQLFragment())

	args := pgx.StrictNamedArgs{"id": id, "relation": relation, "notes": notes}
	maps.Copy(args, scope.SQLArguments())
	_, err := conn.Exec(ctx, q, args)
	return err
}
