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
	// RequirementCrossMapping relates a source requirement to a requirement in
	// another framework. Either TargetRequirementID is set (resolved) or the
	// code pair is set (stub); the signed bundle content stores only the codes,
	// so resolution never changes signed bytes. IsResolved is a generated column.
	RequirementCrossMapping struct {
		ID                       gid.GID   `db:"id"`
		SourceRequirementID      gid.GID   `db:"source_requirement_id"`
		SourceFrameworkVersionID gid.GID   `db:"source_framework_version_id"`
		Relation                 string    `db:"relation"`
		TargetRequirementID      *gid.GID  `db:"target_requirement_id"`
		TargetFrameworkCode      string    `db:"target_framework_code"`
		TargetFrameworkVersion   string    `db:"target_framework_version"`
		TargetRequirementCode    string    `db:"target_requirement_code"`
		IsResolved               bool      `db:"is_resolved"`
		Confidence               float64   `db:"confidence"`
		Rationale                string    `db:"rationale"`
		ReviewState              string    `db:"review_state"`
		Notes                    string    `db:"notes"`
		CreatedAt                time.Time `db:"created_at"`
	}

	RequirementCrossMappings []*RequirementCrossMapping
)

const requirementCrossMappingColumns = `id, source_requirement_id, source_framework_version_id, relation, target_requirement_id, target_framework_code, target_framework_version, target_requirement_code, is_resolved, confidence, rationale, review_state, notes, created_at`

func (m RequirementCrossMapping) Insert(ctx context.Context, conn pg.Tx, scope Scoper) error {
	q := `
INSERT INTO requirement_cross_mappings (
    tenant_id, id, source_requirement_id, source_framework_version_id, relation,
    target_requirement_id, target_framework_code, target_framework_version, target_requirement_code,
    confidence, rationale, review_state, notes, created_at
) VALUES (
    @tenant_id, @id, @source_requirement_id, @source_framework_version_id, @relation,
    @target_requirement_id, @target_framework_code, @target_framework_version, @target_requirement_code,
    @confidence, @rationale, @review_state, @notes, @created_at
);`

	args := pgx.StrictNamedArgs{
		"tenant_id":                   scope.GetTenantID(),
		"id":                          m.ID,
		"source_requirement_id":       m.SourceRequirementID,
		"source_framework_version_id": m.SourceFrameworkVersionID,
		"relation":                    m.Relation,
		"target_requirement_id":       m.TargetRequirementID,
		"target_framework_code":       m.TargetFrameworkCode,
		"target_framework_version":    m.TargetFrameworkVersion,
		"target_requirement_code":     m.TargetRequirementCode,
		"confidence":                  m.Confidence,
		"rationale":                   m.Rationale,
		"review_state":                orCurrent(m.ReviewState),
		"notes":                       m.Notes,
		"created_at":                  m.CreatedAt,
	}

	_, err := conn.Exec(ctx, q, args)
	if err != nil {
		if isUniqueViolation(err, "rxmap_unique") {
			return ErrResourceAlreadyExists
		}
		return err
	}
	return nil
}

func (m RequirementCrossMapping) Delete(ctx context.Context, conn pg.Tx, scope Scoper, id gid.GID) error {
	q := fmt.Sprintf(`DELETE FROM requirement_cross_mappings WHERE %s AND id = @id;`, scope.SQLFragment())
	args := pgx.StrictNamedArgs{"id": id}
	maps.Copy(args, scope.SQLArguments())
	_, err := conn.Exec(ctx, q, args)
	return err
}

// LoadAllByVersion returns every mapping authored on a source version.
func (ms *RequirementCrossMappings) LoadAllByVersion(ctx context.Context, conn pg.Querier, scope Scoper, versionID gid.GID) error {
	// Fully deterministic order: signed bundle content is assembled from these
	// rows, so the order must be stable across re-assemblies (created_at alone
	// ties for rows inserted in one import).
	q := fmt.Sprintf(`SELECT %s FROM requirement_cross_mappings WHERE %s AND source_framework_version_id = @version_id
ORDER BY created_at ASC, target_framework_code ASC, target_requirement_code ASC, relation ASC, id ASC;`,
		requirementCrossMappingColumns, scope.SQLFragment())

	args := pgx.StrictNamedArgs{"version_id": versionID}
	maps.Copy(args, scope.SQLArguments())

	rows, err := conn.Query(ctx, q, args)
	if err != nil {
		return fmt.Errorf("cannot query mappings: %w", err)
	}

	mappings, err := pgx.CollectRows(rows, pgx.RowToAddrOfStructByName[RequirementCrossMapping])
	if err != nil {
		return fmt.Errorf("cannot collect mappings: %w", err)
	}

	*ms = mappings
	return nil
}

// ResolveOwnStubs links this version's unresolved stubs to requirements of the LATEST
// published version of each target framework (honoring a pinned version).
// Returns the number of stubs resolved. Adapted from the template's
// resolve_cross_mapping_stubs().
func (ms RequirementCrossMappings) ResolveOwnStubs(ctx context.Context, conn pg.Querier, scope Scoper, sourceVersionID gid.GID) (int, error) {
	q := fmt.Sprintf(`
UPDATE requirement_cross_mappings x
SET    target_requirement_id = r.id
FROM   requirements r
JOIN   framework_versions fv ON fv.id = r.framework_version_id AND fv.status = @published
JOIN   frameworks f          ON f.id = fv.framework_id
WHERE  x.%s
  AND  x.source_framework_version_id = @source_version_id
  AND  x.target_requirement_id IS NULL
  AND  x.target_framework_code = f.reference_id
  AND  (x.target_framework_version = '' OR x.target_framework_version = fv.version)
  AND  x.target_requirement_code = r.code
  AND  fv.id = (
        SELECT v2.id FROM framework_versions v2
        WHERE v2.framework_id = f.id AND v2.status = @published
          AND (x.target_framework_version = '' OR v2.version = x.target_framework_version)
        ORDER BY v2.published_at DESC LIMIT 1
  );`, scope.SQLFragment())

	args := pgx.StrictNamedArgs{
		"source_version_id": sourceVersionID,
		"published":         FrameworkVersionStatusPublished,
	}
	maps.Copy(args, scope.SQLArguments())

	tag, err := conn.Exec(ctx, q, args)
	if err != nil {
		return 0, fmt.Errorf("cannot resolve own stubs: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// ResolveStubsTargeting links every unresolved stub (from any framework) that
// points at frameworkCode to requirements of the just-published version. Honors
// version pinning. Returns the number of stubs resolved.
func (ms RequirementCrossMappings) ResolveStubsTargeting(ctx context.Context, conn pg.Querier, scope Scoper, frameworkCode, version string, versionID gid.GID) (int, error) {
	q := fmt.Sprintf(`
UPDATE requirement_cross_mappings x
SET    target_requirement_id = r.id
FROM   requirements r
WHERE  x.%s
  AND  r.framework_version_id = @version_id
  AND  x.target_requirement_id IS NULL
  AND  x.target_framework_code = @code
  AND  (x.target_framework_version = '' OR x.target_framework_version = @version)
  AND  x.target_requirement_code = r.code;`, scope.SQLFragment())

	args := pgx.StrictNamedArgs{
		"version_id": versionID,
		"code":       frameworkCode,
		"version":    version,
	}
	maps.Copy(args, scope.SQLArguments())

	tag, err := conn.Exec(ctx, q, args)
	if err != nil {
		return 0, fmt.Errorf("cannot resolve stubs targeting %s: %w", frameworkCode, err)
	}
	return int(tag.RowsAffected()), nil
}

// CoverageRow aggregates mappings per relation for a source version / target.
// It is served verbatim by the console API, hence the json tags.
type CoverageRow struct {
	Relation string `db:"relation" json:"relation"`
	Total    int    `db:"total" json:"total"`
	Resolved int    `db:"resolved" json:"resolved"`
}

// Coverage returns per-relation mapping counts for a source version, optionally
// filtered to one target framework code.
func (ms RequirementCrossMappings) Coverage(ctx context.Context, conn pg.Querier, scope Scoper, sourceVersionID gid.GID, targetCode string) ([]CoverageRow, error) {
	q := fmt.Sprintf(`
SELECT relation, COUNT(*)::int AS total, COUNT(target_requirement_id)::int AS resolved
FROM requirement_cross_mappings
WHERE %s
  AND source_framework_version_id = @version_id
  AND (@target_code = '' OR target_framework_code = @target_code)
GROUP BY relation
ORDER BY relation;`, scope.SQLFragment())

	args := pgx.StrictNamedArgs{"version_id": sourceVersionID, "target_code": targetCode}
	maps.Copy(args, scope.SQLArguments())

	rows, err := conn.Query(ctx, q, args)
	if err != nil {
		return nil, fmt.Errorf("cannot query coverage: %w", err)
	}

	out, err := pgx.CollectRows(rows, pgx.RowToStructByName[CoverageRow])
	if err != nil {
		return nil, fmt.Errorf("cannot collect coverage: %w", err)
	}
	return out, nil
}

// UnresolvedSummaryRow groups unresolved stubs by their target framework.
// It is served verbatim by the console API, hence the json tags.
type UnresolvedSummaryRow struct {
	TargetFrameworkCode    string `db:"target_framework_code" json:"targetFrameworkCode"`
	TargetFrameworkVersion string `db:"target_framework_version" json:"targetFrameworkVersion"`
	Count                  int    `db:"count" json:"count"`
}

// UnresolvedSummary groups all unresolved stubs by target framework, for the
// admin remediation view.
func (ms RequirementCrossMappings) UnresolvedSummary(ctx context.Context, conn pg.Querier, scope Scoper) ([]UnresolvedSummaryRow, error) {
	q := fmt.Sprintf(`
SELECT target_framework_code, target_framework_version, COUNT(*)::int AS count
FROM requirement_cross_mappings
WHERE %s AND target_requirement_id IS NULL
GROUP BY target_framework_code, target_framework_version
ORDER BY count DESC;`, scope.SQLFragment())

	rows, err := conn.Query(ctx, q, scope.SQLArguments())
	if err != nil {
		return nil, fmt.Errorf("cannot query unresolved summary: %w", err)
	}

	out, err := pgx.CollectRows(rows, pgx.RowToStructByName[UnresolvedSummaryRow])
	if err != nil {
		return nil, fmt.Errorf("cannot collect unresolved summary: %w", err)
	}
	return out, nil
}

// MarkReviewState flags mappings that point at refs which changed in the target
// framework's new version. Nothing is deleted: a mapping whose target vanished
// is surfaced as orphaned for an auditor to judge, because auto-deleting it
// would quietly shrink a published coverage claim.
func (ms RequirementCrossMappings) MarkReviewState(ctx context.Context, conn pg.Tx, scope Scoper, targetCode string, refs []string, state string) error {
	if len(refs) == 0 {
		return nil
	}
	q := fmt.Sprintf(`UPDATE requirement_cross_mappings SET review_state = @state
WHERE %s AND target_framework_code = @target_code AND target_requirement_code = ANY(@refs::text[]);`, scope.SQLFragment())

	args := pgx.StrictNamedArgs{"target_code": targetCode, "refs": refs, "state": state}
	maps.Copy(args, scope.SQLArguments())
	_, err := conn.Exec(ctx, q, args)
	return err
}

// Update changes an auditor-editable field of a mapping. The target is not
// editable: re-pointing a mapping is a different assertion, so it is a delete
// and a re-create rather than a silent edit of an existing row.
func (m RequirementCrossMapping) Update(ctx context.Context, conn pg.Tx, scope Scoper, id gid.GID, relation, notes string) error {
	q := fmt.Sprintf(`UPDATE requirement_cross_mappings
SET relation = @relation, notes = @notes, review_state = 'current'
WHERE %s AND id = @id;`, scope.SQLFragment())

	args := pgx.StrictNamedArgs{"id": id, "relation": relation, "notes": notes}
	maps.Copy(args, scope.SQLArguments())
	_, err := conn.Exec(ctx, q, args)
	return err
}
