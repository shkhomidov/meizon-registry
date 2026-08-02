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

// Mapping-set statuses mirror a framework version's, so the moderation gate is
// the same shape for both.
const (
	MappingSetStatusDraft      = "DRAFT"
	MappingSetStatusPublished  = "PUBLISHED"
	MappingSetStatusDeprecated = "DEPRECATED"
)

type (
	// MappingSet is the stored record of a signed cross-mapping artifact: which
	// source framework version maps to which target framework version, its
	// signature, and moderation provenance. The mappings themselves are not
	// stored here — they are read from requirement_cross_mappings /
	// control_cross_mappings at assembly time, so this row is metadata about a
	// signed set, not a second copy of the mappings.
	MappingSet struct {
		ID                       gid.GID `db:"id"`
		SourceFrameworkVersionID gid.GID `db:"source_framework_version_id"`
		TargetFrameworkCode      string  `db:"target_framework_code"`
		TargetFrameworkVersion   string  `db:"target_framework_version"`
		Status                   string  `db:"status"`
		ContentHash              string  `db:"content_hash"`
		Signature                []byte  `db:"signature"`
		// Document is the exact signed JSON, served verbatim. It is the source of
		// truth for distribution — the set is never re-derived, so the bytes a
		// consumer verifies are always the bytes that were signed.
		Document                []byte     `db:"document"`
		KeyID                   string     `db:"key_id"`
		RequirementMappingCount int        `db:"requirement_mapping_count"`
		ControlMappingCount     int        `db:"control_mapping_count"`
		AuthorID                string     `db:"author_id"`
		ApprovedBy              string     `db:"approved_by"`
		ApprovedAt              *time.Time `db:"approved_at"`
		PublishedBy             string     `db:"published_by"`
		PublishedAt             *time.Time `db:"published_at"`
		CreatedAt               time.Time  `db:"created_at"`
		UpdatedAt               time.Time  `db:"updated_at"`
	}

	MappingSets []*MappingSet
)

const mappingSetColumns = `id, source_framework_version_id, target_framework_code, target_framework_version, status, content_hash, signature, document, key_id, requirement_mapping_count, control_mapping_count, author_id, approved_by, approved_at, published_by, published_at, created_at, updated_at`

// Upsert inserts a mapping set or updates it in place on the unique
// (source version, target framework@version) key. Re-assembling a set for the
// same pair — the normal case when mappings are edited before publication —
// replaces the draft rather than colliding.
func (m *MappingSet) Upsert(ctx context.Context, conn pg.Tx, scope Scoper) error {
	q := `
INSERT INTO mapping_sets (
    tenant_id, id, source_framework_version_id, target_framework_code, target_framework_version,
    status, content_hash, signature, document, key_id, requirement_mapping_count, control_mapping_count,
    author_id, approved_by, approved_at, published_by, published_at, created_at, updated_at
) VALUES (
    @tenant_id, @id, @source_framework_version_id, @target_framework_code, @target_framework_version,
    @status, @content_hash, @signature, @document, @key_id, @requirement_mapping_count, @control_mapping_count,
    @author_id, @approved_by, @approved_at, @published_by, @published_at, @created_at, @updated_at
)
ON CONFLICT (source_framework_version_id, target_framework_code, target_framework_version)
DO UPDATE SET
    status = EXCLUDED.status, content_hash = EXCLUDED.content_hash, signature = EXCLUDED.signature,
    document = EXCLUDED.document, key_id = EXCLUDED.key_id, requirement_mapping_count = EXCLUDED.requirement_mapping_count,
    control_mapping_count = EXCLUDED.control_mapping_count, approved_by = EXCLUDED.approved_by,
    approved_at = EXCLUDED.approved_at, published_by = EXCLUDED.published_by,
    published_at = EXCLUDED.published_at, updated_at = EXCLUDED.updated_at
RETURNING id;`

	args := pgx.StrictNamedArgs{
		"tenant_id":                   scope.GetTenantID(),
		"id":                          m.ID,
		"source_framework_version_id": m.SourceFrameworkVersionID,
		"target_framework_code":       m.TargetFrameworkCode,
		"target_framework_version":    m.TargetFrameworkVersion,
		"status":                      m.Status,
		"content_hash":                m.ContentHash,
		"signature":                   m.Signature,
		"document":                    m.Document,
		"key_id":                      m.KeyID,
		"requirement_mapping_count":   m.RequirementMappingCount,
		"control_mapping_count":       m.ControlMappingCount,
		"author_id":                   m.AuthorID,
		"approved_by":                 m.ApprovedBy,
		"approved_at":                 m.ApprovedAt,
		"published_by":                m.PublishedBy,
		"published_at":                m.PublishedAt,
		"created_at":                  m.CreatedAt,
		"updated_at":                  m.UpdatedAt,
	}
	// The returned id is the existing row's on conflict; adopt it so a caller
	// holding a freshly-minted id updates the right row afterwards.
	return conn.QueryRow(ctx, q, args).Scan(&m.ID)
}

// LoadByPair loads the set for one (source version → target framework@version).
func (m *MappingSet) LoadByPair(ctx context.Context, conn pg.Querier, scope Scoper, sourceVersionID gid.GID, targetCode, targetVersion string) error {
	q := fmt.Sprintf(`SELECT %s FROM mapping_sets
WHERE %s AND source_framework_version_id = @sv AND target_framework_code = @tc AND target_framework_version = @tv
LIMIT 1;`, mappingSetColumns, scope.SQLFragment())

	args := pgx.StrictNamedArgs{"sv": sourceVersionID, "tc": targetCode, "tv": targetVersion}
	maps.Copy(args, scope.SQLArguments())

	rows, err := conn.Query(ctx, q, args)
	if err != nil {
		return fmt.Errorf("cannot query mapping set: %w", err)
	}
	set, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[MappingSet])
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrResourceNotFound
		}
		return fmt.Errorf("cannot collect mapping set: %w", err)
	}
	*m = set
	return nil
}

// LoadPublishedBySource returns every published set authored on a source
// version — the catalog of what mappings ship for that framework.
func (ms *MappingSets) LoadPublishedBySource(ctx context.Context, conn pg.Querier, scope Scoper, sourceVersionID gid.GID) error {
	q := fmt.Sprintf(`SELECT %s FROM mapping_sets
WHERE %s AND source_framework_version_id = @sv AND status = @status
ORDER BY target_framework_code, target_framework_version;`, mappingSetColumns, scope.SQLFragment())

	args := pgx.StrictNamedArgs{"sv": sourceVersionID, "status": MappingSetStatusPublished}
	maps.Copy(args, scope.SQLArguments())

	rows, err := conn.Query(ctx, q, args)
	if err != nil {
		return fmt.Errorf("cannot query mapping sets: %w", err)
	}
	out, err := pgx.CollectRows(rows, pgx.RowToAddrOfStructByName[MappingSet])
	if err != nil {
		return fmt.Errorf("cannot collect mapping sets: %w", err)
	}
	*ms = out
	return nil
}
