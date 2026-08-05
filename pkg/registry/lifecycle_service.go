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
	"errors"
	"fmt"
	"time"

	"go.gearno.de/kit/pg"
	"go.meizon.cloud/registry/pkg/coredata"
	"go.meizon.cloud/registry/pkg/fwschema"
	"go.meizon.cloud/registry/pkg/gid"
	"go.meizon.cloud/registry/pkg/iam"
)

// ErrSeparationOfDuties is returned when the author of a version attempts to
// approve or publish it themselves.
//
// Publish is gated on the author, not on the approver: requiring a third
// identity to release would deadlock a two-person registry (one auditor, one
// moderator), which is the common deployment. The invariant that matters is
// that nobody ships their own work unreviewed.
var ErrSeparationOfDuties = errors.New("separation of duties: the author cannot approve or publish their own version")

// emitDistributionEvent appends to the consumer-facing change feed inside the
// caller's transaction, so an event can never describe a state change that was
// rolled back — nor a publication go out without an event to announce it.
func (s *Service) emitDistributionEvent(ctx context.Context, tx pg.Tx, kind string, framework coredata.Framework, version coredata.FrameworkVersion, at time.Time) error {
	ev := coredata.DistributionEvent{
		Kind:         kind,
		FrameworkRef: framework.ReferenceID,
		Version:      version.Version,
		ContentHash:  version.ContentHash,
		Region:       framework.Region,
		Public:       framework.Public,
		CreatedAt:    at,
	}
	return ev.Append(ctx, tx, framework.ID.TenantID())
}

// emitVersionArtifactEvent announces an artifact tied to a version (its audit
// template or its translations) on the change feed — but only when the version is
// PUBLISHED. A draft's QA/translations are authoring-side and not distributed;
// once the version is published the artifact becomes fetchable, so the event lets
// a consumer pull it without polling. No-op (nil) for an unpublished version.
func (s *Service) emitVersionArtifactEvent(ctx context.Context, tx pg.Tx, kind string, versionID gid.GID) error {
	scope := s.platformScope()
	var version coredata.FrameworkVersion
	if err := version.LoadByID(ctx, tx, scope, versionID); err != nil {
		return err
	}
	if version.Status != coredata.FrameworkVersionStatusPublished {
		return nil
	}
	var framework coredata.Framework
	if err := framework.LoadByID(ctx, tx, scope, version.FrameworkID); err != nil {
		return err
	}
	return s.emitDistributionEvent(ctx, tx, kind, framework, version, time.Now())
}

// Submit locks a DRAFT for review (DRAFT → IN_REVIEW). The draft must contain at
// least one control and pass schema validation.
func (s *Service) Submit(ctx context.Context, actorID, versionID gid.GID) error {
	return s.db.WithTx(ctx, func(ctx context.Context, tx pg.Tx) error {
		scope := s.platformScope()
		version, framework, err := s.loadVersionAndFramework(ctx, tx, scope, versionID)
		if err != nil {
			return err
		}

		if err := s.authorize(ctx, tx, actorID, iam.ActionFrameworkSubmit, framework.Region, framework.ID); err != nil {
			return err
		}

		if version.Status != coredata.FrameworkVersionStatusDraft {
			return fmt.Errorf("%w: can only submit a DRAFT (current: %s)", ErrInvalidTransition, version.Status)
		}

		bundle, err := s.assembleBundle(ctx, tx, scope, framework, version)
		if err != nil {
			return err
		}
		if err := bundle.Validate(); err != nil {
			return fmt.Errorf("draft fails schema validation: %w", err)
		}

		version.Status = coredata.FrameworkVersionStatusInReview
		version.UpdatedAt = time.Now()
		if err := version.Update(ctx, tx, scope); err != nil {
			return err
		}

		return s.recordAudit(ctx, tx, scope, actorID, "version.submit", version.ID.String(), framework.ReferenceID)
	})
}

// Approve records a moderator's approval. It enforces separation of duties (the
// sole author cannot approve) and promotes the version to APPROVED once the
// quorum of approvals is met.
func (s *Service) Approve(ctx context.Context, actorID, versionID gid.GID, comment string) error {
	return s.db.WithTx(ctx, func(ctx context.Context, tx pg.Tx) error {
		scope := s.platformScope()
		version, framework, err := s.loadVersionAndFramework(ctx, tx, scope, versionID)
		if err != nil {
			return err
		}

		if err := s.authorize(ctx, tx, actorID, iam.ActionVersionApprove, framework.Region, framework.ID); err != nil {
			return err
		}

		if version.Status != coredata.FrameworkVersionStatusInReview {
			return fmt.Errorf("%w: can only approve a version IN_REVIEW (current: %s)", ErrInvalidTransition, version.Status)
		}

		// Separation of duties: the author may never be the approver.
		if actorID == version.AuthorID {
			return ErrSeparationOfDuties
		}

		approval := coredata.Approval{
			ID:                 gid.New(s.cfg.PlatformTenant, coredata.ApprovalEntityType),
			FrameworkVersionID: version.ID,
			ReviewerID:         actorID,
			Decision:           coredata.ApprovalDecisionApproved,
			Comment:            comment,
			CreatedAt:          time.Now(),
		}
		if err := approval.Insert(ctx, tx, scope); err != nil {
			if errors.Is(err, coredata.ErrResourceAlreadyExists) {
				return fmt.Errorf("reviewer has already voted on this version")
			}
			return err
		}

		approved, err := s.countApprovals(ctx, tx, scope, version.ID)
		if err != nil {
			return err
		}

		if approved >= version.Quorum {
			approvedAt := time.Now()
			version.Status = coredata.FrameworkVersionStatusApproved
			// Record the reviewer whose vote met quorum. With a quorum above 1
			// this is the last approver; the full roll call stays in `approvals`.
			version.ApprovedBy = actorID.String()
			version.ApprovedAt = &approvedAt
			version.UpdatedAt = approvedAt
			if err := version.Update(ctx, tx, scope); err != nil {
				return err
			}
		}

		return s.recordAudit(ctx, tx, scope, actorID, "version.approve", version.ID.String(),
			fmt.Sprintf("approvals=%d/%d", approved, version.Quorum))
	})
}

// Reject records a rejection and returns the version to DRAFT for revision.
func (s *Service) Reject(ctx context.Context, actorID, versionID gid.GID, comment string) error {
	return s.db.WithTx(ctx, func(ctx context.Context, tx pg.Tx) error {
		scope := s.platformScope()
		version, framework, err := s.loadVersionAndFramework(ctx, tx, scope, versionID)
		if err != nil {
			return err
		}

		if err := s.authorize(ctx, tx, actorID, iam.ActionVersionReject, framework.Region, framework.ID); err != nil {
			return err
		}

		if version.Status != coredata.FrameworkVersionStatusInReview {
			return fmt.Errorf("%w: can only reject a version IN_REVIEW (current: %s)", ErrInvalidTransition, version.Status)
		}

		approval := coredata.Approval{
			ID:                 gid.New(s.cfg.PlatformTenant, coredata.ApprovalEntityType),
			FrameworkVersionID: version.ID,
			ReviewerID:         actorID,
			Decision:           coredata.ApprovalDecisionRejected,
			Comment:            comment,
			CreatedAt:          time.Now(),
		}
		if err := approval.Insert(ctx, tx, scope); err != nil && !errors.Is(err, coredata.ErrResourceAlreadyExists) {
			return err
		}

		version.Status = coredata.FrameworkVersionStatusDraft
		version.UpdatedAt = time.Now()
		if err := version.Update(ctx, tx, scope); err != nil {
			return err
		}

		return s.recordAudit(ctx, tx, scope, actorID, "version.reject", version.ID.String(), comment)
	})
}

// Publish signs an APPROVED version and makes it immutable and distributable
// (APPROVED → PUBLISHED). The framework's public-catalog visibility is set from
// its license.
func (s *Service) Publish(ctx context.Context, actorID, versionID gid.GID) error {
	return s.db.WithTx(ctx, func(ctx context.Context, tx pg.Tx) error {
		scope := s.platformScope()
		version, framework, err := s.loadVersionAndFramework(ctx, tx, scope, versionID)
		if err != nil {
			return err
		}

		if err := s.authorize(ctx, tx, actorID, iam.ActionVersionPublish, framework.Region, framework.ID); err != nil {
			return err
		}

		if version.Status != coredata.FrameworkVersionStatusApproved {
			return fmt.Errorf("%w: can only publish an APPROVED version (current: %s)", ErrInvalidTransition, version.Status)
		}

		// Separation of duties, same rule as Approve: publishing is the act that
		// makes a version real to every consuming GRC, so the author must not be
		// the one to perform it. Without this, an author holding the moderator
		// role could get a colleague's approval and then ship it themselves.
		if actorID == version.AuthorID {
			return ErrSeparationOfDuties
		}

		publishedAt := time.Now()
		version.Status = coredata.FrameworkVersionStatusPublished
		version.PublishedAt = &publishedAt
		version.PublishedBy = actorID.String()
		// Pin the exchange schema before assembling, so what gets signed and
		// what we record as having been signed cannot disagree. Assembly reads
		// this field, so setting it afterwards would sign one shape and claim
		// another — a mismatch that surfaces only at the consumer.
		//
		// A version reaching publish for the first time has no signature to
		// protect, so it is always signed under v3: its cross-mappings are
		// distributed as separate signed mapping sets rather than baked into the
		// bundle, which is what lets them be updated and re-published without
		// re-signing the framework. We force v3 rather than only defaulting an
		// empty value because a draft inherits the framework_versions table's
		// legacy '2.0' column default on insert — that value predates v3 and must
		// not pin new publishes to the old inline-mapping format. Existing v2
		// publishes keep their signed schema; only IN_REVIEW/APPROVED drafts
		// reach here, and none of them has been signed yet.
		version.BundleSchema = fwschema.SchemaVersion3

		bundle, err := s.assembleBundle(ctx, tx, scope, framework, version)
		if err != nil {
			return err
		}
		if err := bundle.Validate(); err != nil {
			return fmt.Errorf("version fails schema validation: %w", err)
		}

		keyID, priv, err := s.activeSigningKey(ctx, tx)
		if err != nil {
			return err
		}
		if err := bundle.Sign(priv, keyID); err != nil {
			return fmt.Errorf("cannot sign version: %w", err)
		}

		sigJSON, err := json.Marshal(bundle.Signature)
		if err != nil {
			return fmt.Errorf("cannot encode signature: %w", err)
		}

		version.Signature = sigJSON
		version.ContentHash = bundle.Signature.ContentHash
		version.KeyID = keyID
		version.UpdatedAt = publishedAt
		if err := version.Update(ctx, tx, scope); err != nil {
			return err
		}

		// Copyright gate: only public-domain / statutory frameworks become
		// visible in the public catalog. Proprietary ones stay owner-only.
		framework.Public = bundle.License.IsPublicDistributable()
		framework.UpdatedAt = publishedAt
		if err := framework.Update(ctx, tx, scope); err != nil {
			return err
		}

		// Cross-mapping resolution (derived state; never touches signed bytes):
		// this version's own stubs, plus every stub elsewhere pointing at this
		// framework's code.
		resolved, err := s.resolveAfterPublish(ctx, tx, framework, version)
		if err != nil {
			return fmt.Errorf("cannot resolve mapping stubs: %w", err)
		}
		if resolved > 0 {
			if err := s.recordAudit(ctx, tx, scope, actorID, "mapping.resolve", version.ID.String(),
				fmt.Sprintf("resolved=%d on publish of %s@%s", resolved, framework.ReferenceID, version.Version)); err != nil {
				return err
			}
		}

		// Announce to consumers. Emitted after framework.Public is settled from
		// the license, because the event carries the visibility that decides who
		// is allowed to see it.
		if err := s.emitDistributionEvent(ctx, tx, coredata.DistributionEventPublished, framework, version, publishedAt); err != nil {
			return err
		}

		// Publishing a framework distributes its audit too. Any audit template on
		// this version — canonical and every translation — is marked ready in the
		// same commit, so an operator does not have to confirm "ready" separately
		// after already deciding to publish. If at least one existed, announce it
		// so a consumer can pull it right after the framework.
		readied, err := (coredata.QATemplate{}).SetStatusByVersion(ctx, tx, scope, version.ID, coredata.QATemplateStatusReady, publishedAt)
		if err != nil {
			return err
		}
		if readied > 0 {
			if err := s.emitDistributionEvent(ctx, tx, coredata.DistributionEventQAPublished, framework, version, publishedAt); err != nil {
				return err
			}
		}

		return s.recordAudit(ctx, tx, scope, actorID, "version.publish", version.ID.String(),
			fmt.Sprintf("%s@%s hash=%s", framework.ReferenceID, version.Version, version.ContentHash))
	})
}

// Deprecate supersedes a PUBLISHED version (PUBLISHED → DEPRECATED).
func (s *Service) Deprecate(ctx context.Context, actorID, versionID gid.GID) error {
	return s.db.WithTx(ctx, func(ctx context.Context, tx pg.Tx) error {
		scope := s.platformScope()
		version, framework, err := s.loadVersionAndFramework(ctx, tx, scope, versionID)
		if err != nil {
			return err
		}

		if err := s.authorize(ctx, tx, actorID, iam.ActionVersionDeprecate, framework.Region, framework.ID); err != nil {
			return err
		}

		if version.Status != coredata.FrameworkVersionStatusPublished {
			return fmt.Errorf("%w: can only deprecate a PUBLISHED version (current: %s)", ErrInvalidTransition, version.Status)
		}

		deprecatedAt := time.Now()
		version.Status = coredata.FrameworkVersionStatusDeprecated
		version.UpdatedAt = deprecatedAt
		if err := version.Update(ctx, tx, scope); err != nil {
			return err
		}

		// The retirement signal. A consumer that already imported this version
		// has no other way to learn it was withdrawn — the catalog simply stops
		// listing it, which is indistinguishable from a network hiccup.
		if err := s.emitDistributionEvent(ctx, tx, coredata.DistributionEventDeprecated, framework, version, deprecatedAt); err != nil {
			return err
		}

		return s.recordAudit(ctx, tx, scope, actorID, "version.deprecate", version.ID.String(), framework.ReferenceID)
	})
}

func (s *Service) countApprovals(ctx context.Context, conn pg.Querier, scope coredata.Scoper, versionID gid.GID) (int, error) {
	var approvals coredata.Approvals
	if err := approvals.LoadAllByVersion(ctx, conn, scope, versionID); err != nil {
		return 0, err
	}
	n := 0
	for _, a := range approvals {
		if a.Decision == coredata.ApprovalDecisionApproved {
			n++
		}
	}
	return n, nil
}
