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

// Proposal node kinds and decision states.
const (
	MappingNodeRequirement = "requirement"
	MappingNodeControl     = "control"

	ProposalPending  = "pending"
	ProposalAccepted = "accepted"
	ProposalRejected = "rejected"
)

// Mapping review states. A mapping is `current` until the framework it points
// at changes underneath it.
const (
	MappingReviewCurrent     = "current"
	MappingReviewNeedsReview = "needs_review"
	MappingReviewOrphaned    = "orphaned"
)

type (
	// MappingProposal is a model-proposed cross-mapping awaiting an auditor.
	// Nothing here is authoritative until accepted; a rejected row is kept so a
	// later run does not re-propose what a human already turned down.
	MappingProposal struct {
		ID                       gid.GID   `db:"id"`
		SourceFrameworkVersionID gid.GID   `db:"source_framework_version_id"`
		NodeKind                 string    `db:"node_kind"`
		SourceRef                string    `db:"source_ref"`
		TargetFrameworkCode      string    `db:"target_framework_code"`
		TargetFrameworkVersion   string    `db:"target_framework_version"`
		TargetRef                string    `db:"target_ref"`
		Relation                 string    `db:"relation"`
		Confidence               float64   `db:"confidence"`
		Rationale                string    `db:"rationale"`
		Status                   string    `db:"status"`
		DecidedBy                string    `db:"decided_by"`
		CreatedAt                time.Time `db:"created_at"`
	}

	MappingProposals []*MappingProposal
)

const mappingProposalColumns = `id, source_framework_version_id, node_kind, source_ref, target_framework_code, target_framework_version, target_ref, relation, confidence, rationale, status, decided_by, created_at`

// Insert records a proposal. A repeat of the same triple is ignored rather than
// erroring: re-proposing an identical pair is normal, and the stored decision
// (including a rejection) must win over a fresh suggestion.
func (p MappingProposal) Insert(ctx context.Context, conn pg.Tx, scope Scoper) error {
	q := `
INSERT INTO mapping_proposals (
    tenant_id, id, source_framework_version_id, node_kind, source_ref,
    target_framework_code, target_framework_version, target_ref, relation,
    confidence, rationale, status, decided_by, created_at
) VALUES (
    @tenant_id, @id, @source_framework_version_id, @node_kind, @source_ref,
    @target_framework_code, @target_framework_version, @target_ref, @relation,
    @confidence, @rationale, @status, @decided_by, @created_at
)
ON CONFLICT ON CONSTRAINT proposals_unique DO NOTHING;`

	args := pgx.StrictNamedArgs{
		"tenant_id":                   scope.GetTenantID(),
		"id":                          p.ID,
		"source_framework_version_id": p.SourceFrameworkVersionID,
		"node_kind":                   p.NodeKind,
		"source_ref":                  p.SourceRef,
		"target_framework_code":       p.TargetFrameworkCode,
		"target_framework_version":    p.TargetFrameworkVersion,
		"target_ref":                  p.TargetRef,
		"relation":                    p.Relation,
		"confidence":                  p.Confidence,
		"rationale":                   p.Rationale,
		"status":                      p.Status,
		"decided_by":                  p.DecidedBy,
		"created_at":                  p.CreatedAt,
	}

	_, err := conn.Exec(ctx, q, args)
	return err
}

// SetStatus records an auditor's decision.
func (p MappingProposal) SetStatus(ctx context.Context, conn pg.Tx, scope Scoper, id gid.GID, status, decidedBy string) error {
	q := fmt.Sprintf(`UPDATE mapping_proposals SET status = @status, decided_by = @decided_by WHERE %s AND id = @id;`, scope.SQLFragment())
	args := pgx.StrictNamedArgs{"id": id, "status": status, "decided_by": decidedBy}
	maps.Copy(args, scope.SQLArguments())
	_, err := conn.Exec(ctx, q, args)
	return err
}

// LoadByID reads one proposal.
func (p *MappingProposal) LoadByID(ctx context.Context, conn pg.Querier, scope Scoper, id gid.GID) error {
	q := fmt.Sprintf(`SELECT %s FROM mapping_proposals WHERE %s AND id = @id LIMIT 1;`, mappingProposalColumns, scope.SQLFragment())
	args := pgx.StrictNamedArgs{"id": id}
	maps.Copy(args, scope.SQLArguments())

	rows, err := conn.Query(ctx, q, args)
	if err != nil {
		return fmt.Errorf("cannot query proposal: %w", err)
	}
	out, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[MappingProposal])
	if err != nil {
		return ErrResourceNotFound
	}
	*p = out
	return nil
}

// LoadAllByVersion returns a version's proposals. Pending first, then LEAST
// confident first: an auditor's attention belongs on the doubtful ones, and a
// list sorted the other way trains people to rubber-stamp the top of it.
func (ps *MappingProposals) LoadAllByVersion(ctx context.Context, conn pg.Querier, scope Scoper, versionID gid.GID, status string) error {
	q := fmt.Sprintf(`SELECT %s FROM mapping_proposals
WHERE %s AND source_framework_version_id = @version_id AND (@status = '' OR status = @status)
ORDER BY (status = 'pending') DESC, confidence ASC, source_ref ASC;`, mappingProposalColumns, scope.SQLFragment())

	args := pgx.StrictNamedArgs{"version_id": versionID, "status": status}
	maps.Copy(args, scope.SQLArguments())

	rows, err := conn.Query(ctx, q, args)
	if err != nil {
		return fmt.Errorf("cannot query proposals: %w", err)
	}
	out, err := pgx.CollectRows(rows, pgx.RowToAddrOfStructByName[MappingProposal])
	if err != nil {
		return fmt.Errorf("cannot collect proposals: %w", err)
	}
	*ps = out
	return nil
}

// DecidedRefs returns the (source_ref, target_ref) pairs already decided for a
// target framework, so a re-run does not re-propose them.
func (ps MappingProposals) DecidedRefs(ctx context.Context, conn pg.Querier, scope Scoper, versionID gid.GID, targetCode string) (map[string]bool, error) {
	q := fmt.Sprintf(`SELECT source_ref, target_ref FROM mapping_proposals
WHERE %s AND source_framework_version_id = @version_id AND target_framework_code = @target_code;`, scope.SQLFragment())

	args := pgx.StrictNamedArgs{"version_id": versionID, "target_code": targetCode}
	maps.Copy(args, scope.SQLArguments())

	rows, err := conn.Query(ctx, q, args)
	if err != nil {
		return nil, fmt.Errorf("cannot query decided refs: %w", err)
	}
	defer rows.Close()

	out := map[string]bool{}
	for rows.Next() {
		var src, tgt string
		if err := rows.Scan(&src, &tgt); err != nil {
			return nil, err
		}
		out[src+"\x00"+tgt] = true
	}
	return out, rows.Err()
}

// MappingRun is the ledger entry for one (source version → target framework)
// auto-mapping pass.
type MappingRun struct {
	ID                       gid.GID   `db:"id"`
	SourceFrameworkVersionID gid.GID   `db:"source_framework_version_id"`
	TargetFrameworkCode      string    `db:"target_framework_code"`
	TargetFrameworkVersion   string    `db:"target_framework_version"`
	NodeKind                 string    `db:"node_kind"`
	AdjudicatedRefs          []string  `db:"adjudicated_refs"`
	PairsConsidered          int       `db:"pairs_considered"`
	Proposed                 int       `db:"proposed"`
	Model                    string    `db:"model"`
	CompletedAt              time.Time `db:"completed_at"`
}

type MappingRuns []*MappingRun

const mappingRunColumns = `id, source_framework_version_id, target_framework_code, target_framework_version, node_kind, adjudicated_refs, pairs_considered, proposed, model, completed_at`

// Upsert records a completed run, unioning the adjudicated refs with whatever a
// previous run already covered.
func (r MappingRun) Upsert(ctx context.Context, conn pg.Tx, scope Scoper) error {
	q := `
INSERT INTO mapping_runs (
    tenant_id, id, source_framework_version_id, target_framework_code, target_framework_version,
    node_kind, adjudicated_refs, pairs_considered, proposed, model, completed_at
) VALUES (
    @tenant_id, @id, @source_framework_version_id, @target_framework_code, @target_framework_version,
    @node_kind, @adjudicated_refs, @pairs_considered, @proposed, @model, @completed_at
)
ON CONFLICT ON CONSTRAINT mapping_runs_unique DO UPDATE SET
  adjudicated_refs = ARRAY(SELECT DISTINCT unnest(mapping_runs.adjudicated_refs || EXCLUDED.adjudicated_refs)),
  pairs_considered = mapping_runs.pairs_considered + EXCLUDED.pairs_considered,
  proposed = mapping_runs.proposed + EXCLUDED.proposed,
  model = EXCLUDED.model,
  completed_at = EXCLUDED.completed_at;`

	args := pgx.StrictNamedArgs{
		"tenant_id":                   scope.GetTenantID(),
		"id":                          r.ID,
		"source_framework_version_id": r.SourceFrameworkVersionID,
		"target_framework_code":       r.TargetFrameworkCode,
		"target_framework_version":    r.TargetFrameworkVersion,
		"node_kind":                   r.NodeKind,
		"adjudicated_refs":            orEmpty(r.AdjudicatedRefs),
		"pairs_considered":            r.PairsConsidered,
		"proposed":                    r.Proposed,
		"model":                       r.Model,
		"completed_at":                r.CompletedAt,
	}

	_, err := conn.Exec(ctx, q, args)
	return err
}

// LoadAllByVersion returns every recorded run for a source version.
func (rs *MappingRuns) LoadAllByVersion(ctx context.Context, conn pg.Querier, scope Scoper, versionID gid.GID) error {
	q := fmt.Sprintf(`SELECT %s FROM mapping_runs WHERE %s AND source_framework_version_id = @version_id ORDER BY target_framework_code;`,
		mappingRunColumns, scope.SQLFragment())

	args := pgx.StrictNamedArgs{"version_id": versionID}
	maps.Copy(args, scope.SQLArguments())

	rows, err := conn.Query(ctx, q, args)
	if err != nil {
		return fmt.Errorf("cannot query mapping runs: %w", err)
	}
	out, err := pgx.CollectRows(rows, pgx.RowToAddrOfStructByName[MappingRun])
	if err != nil {
		return fmt.Errorf("cannot collect mapping runs: %w", err)
	}
	*rs = out
	return nil
}
