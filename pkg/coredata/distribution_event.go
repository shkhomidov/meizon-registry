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
	"time"

	"github.com/jackc/pgx/v5"
	"go.gearno.de/kit/pg"
	"go.meizon.cloud/registry/pkg/gid"
)

// Event kinds. A consumer acts on these directly: `published` means fetch and
// import, `deprecated` means stop trusting what you already hold. The mapping_*
// kinds carry the same meaning for a cross-mapping set — its target framework is
// named in the target_* columns so a consumer can tell whether it holds both
// ends before fetching.
const (
	DistributionEventPublished         = "published"
	DistributionEventDeprecated        = "deprecated"
	DistributionEventMappingPublished  = "mapping_published"
	DistributionEventMappingDeprecated = "mapping_deprecated"
)

// distributionSeqLockKey is the advisory-lock key guarding seq assignment. Any
// fixed value works as long as every writer agrees on it; it is namespaced away
// from other advisory locks by being an otherwise unused constant.
const distributionSeqLockKey int64 = 0x6D7A_6664_0001 // "mzfd" + 1

type (
	// DistributionEvent is one entry in the append-only feed consumers read to
	// discover what changed. Visibility (region, public) is captured at emission
	// so the feed filters identically to /catalog even if the framework row is
	// edited afterwards.
	DistributionEvent struct {
		Seq          int64  `db:"seq"`
		Kind         string `db:"kind"`
		FrameworkRef string `db:"framework_ref"`
		Version      string `db:"version"`
		ContentHash  string `db:"content_hash"`
		Region       string `db:"region"`
		Public       bool   `db:"public"`
		// TargetFrameworkRef/TargetVersion are set only on mapping_* events: the
		// other framework a mapping set connects to, so a consumer can decide
		// whether it holds both ends. Empty for framework events.
		TargetFrameworkRef string    `db:"target_framework_ref"`
		TargetVersion      string    `db:"target_version"`
		CreatedAt          time.Time `db:"created_at"`
	}

	DistributionEvents []*DistributionEvent
)

const distributionEventColumns = `seq, kind, framework_ref, version, content_hash, region, public, target_framework_ref, target_version, created_at`

// Append writes one event, assigning seq under an advisory lock held to commit.
//
// The lock is what makes the cursor safe. Without it, two concurrent publishes
// could take seq 101 and 102 and commit in the opposite order, so a consumer
// that read as far as 102 would never see 101 — a gap it cannot detect, since a
// cursor only knows where it got to, not what it skipped. Holding the lock for
// the remainder of the transaction means seq order is commit order, and "give
// me everything after N" is therefore complete.
//
// Must be called inside the same transaction as the state change it describes,
// so the event and the publication commit or roll back together.
func (e *DistributionEvent) Append(ctx context.Context, tx pg.Tx, tenantID gid.TenantID) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(@key);`,
		pgx.StrictNamedArgs{"key": distributionSeqLockKey}); err != nil {
		return fmt.Errorf("cannot take distribution sequence lock: %w", err)
	}

	q := `
INSERT INTO distribution_events (
    tenant_id, seq, kind, framework_ref, version, content_hash, region, public,
    target_framework_ref, target_version, created_at
) VALUES (
    @tenant_id,
    (SELECT COALESCE(MAX(seq), 0) + 1 FROM distribution_events),
    @kind, @framework_ref, @version, @content_hash, @region, @public,
    @target_framework_ref, @target_version, @created_at
)
RETURNING seq;`

	args := pgx.StrictNamedArgs{
		"tenant_id":            tenantID,
		"kind":                 e.Kind,
		"framework_ref":        e.FrameworkRef,
		"version":              e.Version,
		"content_hash":         e.ContentHash,
		"region":               e.Region,
		"public":               e.Public,
		"target_framework_ref": e.TargetFrameworkRef,
		"target_version":       e.TargetVersion,
		"created_at":           e.CreatedAt,
	}

	if err := tx.QueryRow(ctx, q, args).Scan(&e.Seq); err != nil {
		return fmt.Errorf("cannot append distribution event: %w", err)
	}
	return nil
}

// LoadSince returns up to limit events after the cursor, visible to the caller.
//
// Visibility mirrors LoadCatalog exactly: non-public frameworks only for the
// owning tenant, and an empty region set means all regions. A consumer must
// never learn from the feed that a framework it cannot pull exists.
func (es *DistributionEvents) LoadSince(ctx context.Context, conn pg.Querier, ownerTenantID gid.TenantID, regions []string, since int64, limit int) error {
	q := `
SELECT ` + distributionEventColumns + `
FROM distribution_events
WHERE seq > @since
  AND (public = TRUE OR tenant_id = @owner_tenant_id)
  AND (@all_regions OR region = ANY(@regions::text[]))
ORDER BY seq ASC
LIMIT @limit;`

	args := pgx.StrictNamedArgs{
		"since":           since,
		"owner_tenant_id": ownerTenantID,
		"all_regions":     len(regions) == 0,
		"regions":         regions,
		"limit":           limit,
	}

	rows, err := conn.Query(ctx, q, args)
	if err != nil {
		return fmt.Errorf("cannot query distribution events: %w", err)
	}

	events, err := pgx.CollectRows(rows, pgx.RowToAddrOfStructByName[DistributionEvent])
	if err != nil {
		return fmt.Errorf("cannot collect distribution events: %w", err)
	}

	*es = events
	return nil
}

// HeadSeq returns the highest sequence number visible to the caller, so a
// consumer can report how far behind it is without draining the feed.
func HeadSeq(ctx context.Context, conn pg.Querier, ownerTenantID gid.TenantID, regions []string) (int64, error) {
	q := `
SELECT COALESCE(MAX(seq), 0)
FROM distribution_events
WHERE (public = TRUE OR tenant_id = @owner_tenant_id)
  AND (@all_regions OR region = ANY(@regions::text[]));`

	args := pgx.StrictNamedArgs{
		"owner_tenant_id": ownerTenantID,
		"all_regions":     len(regions) == 0,
		"regions":         regions,
	}

	var head int64
	if err := conn.QueryRow(ctx, q, args).Scan(&head); err != nil {
		return 0, fmt.Errorf("cannot read distribution head: %w", err)
	}
	return head, nil
}
