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
	"fmt"
	"time"

	"go.gearno.de/kit/pg"
	"go.meizon.cloud/registry/pkg/coredata"
	"go.meizon.cloud/registry/pkg/gid"
	"go.meizon.cloud/registry/pkg/iam"
)

// The review side of auto-mapping: what the auditor sees and decides.

type (
	// ProposalView is one proposed mapping awaiting a decision.
	ProposalView struct {
		ID         gid.GID `json:"id"`
		NodeKind   string  `json:"nodeKind"`
		SourceRef  string  `json:"sourceRef"`
		SourceName string  `json:"sourceName,omitempty"`
		Target     string  `json:"target"`
		TargetRef  string  `json:"targetRef"`
		TargetName string  `json:"targetName,omitempty"`
		Relation   string  `json:"relation"`
		Confidence float64 `json:"confidence"`
		Rationale  string  `json:"rationale,omitempty"`
		Status     string  `json:"status"`
	}

	// MappingRunView reports what a previous auto-map pass covered, so the UI
	// can distinguish "never run" from "considered, no match".
	MappingRunView struct {
		Target          string    `json:"target"`
		TargetVersion   string    `json:"targetVersion,omitempty"`
		NodeKind        string    `json:"nodeKind"`
		Adjudicated     int       `json:"adjudicated"`
		PairsConsidered int       `json:"pairsConsidered"`
		Proposed        int       `json:"proposed"`
		Model           string    `json:"model,omitempty"`
		CompletedAt     time.Time `json:"completedAt"`
	}

	// MappingReview is the whole review surface for one framework.
	MappingReview struct {
		Proposals []ProposalView   `json:"proposals"`
		Runs      []MappingRunView `json:"runs"`
		// Conflicts flags accepted control mappings whose requirement-level
		// mapping disagrees. Surfaced, never auto-resolved.
		Conflicts []string `json:"conflicts"`
	}
)

// MappingReviewOf returns the proposals and run history for a framework.
func (s *Service) MappingReviewOf(ctx context.Context, ref, status string) (MappingReview, error) {
	out := MappingReview{Proposals: []ProposalView{}, Runs: []MappingRunView{}, Conflicts: []string{}}

	// Node names make the review readable; a bare pair of refs is not something
	// an auditor can judge.
	doc, err := s.ExportFlatIn(ctx, ref, CanonicalLanguage)
	if err != nil {
		return out, err
	}
	names := map[string]string{}
	for _, r := range doc.Requirements {
		names[coredata.MappingNodeRequirement+"\x00"+r.Ref] = r.Name
	}
	for _, c := range doc.Controls {
		names[coredata.MappingNodeControl+"\x00"+c.Ref] = c.Name
	}

	err = s.db.WithConn(ctx, func(ctx context.Context, conn pg.Querier) error {
		scope := s.platformScope()

		var framework coredata.Framework
		if err := framework.LoadByReferenceID(ctx, conn, scope, ref); err != nil {
			return err
		}
		versionID, err := s.latestVersionIDConn(ctx, conn, framework.ID)
		if err != nil {
			return err
		}

		var proposals coredata.MappingProposals
		if err := proposals.LoadAllByVersion(ctx, conn, scope, versionID, status); err != nil {
			return err
		}
		targetNames := map[string]map[string]string{}
		for _, p := range proposals {
			if _, ok := targetNames[p.TargetFrameworkCode]; !ok {
				targetNames[p.TargetFrameworkCode] = s.nodeNames(ctx, p.TargetFrameworkCode)
			}
			out.Proposals = append(out.Proposals, ProposalView{
				ID: p.ID, NodeKind: p.NodeKind,
				SourceRef:  p.SourceRef,
				SourceName: names[p.NodeKind+"\x00"+p.SourceRef],
				Target:     p.TargetFrameworkCode,
				TargetRef:  p.TargetRef,
				TargetName: targetNames[p.TargetFrameworkCode][p.NodeKind+"\x00"+p.TargetRef],
				Relation:   p.Relation, Confidence: p.Confidence,
				Rationale: p.Rationale, Status: p.Status,
			})
		}

		var runs coredata.MappingRuns
		if err := runs.LoadAllByVersion(ctx, conn, scope, versionID); err != nil {
			return err
		}
		for _, r := range runs {
			out.Runs = append(out.Runs, MappingRunView{
				Target: r.TargetFrameworkCode, TargetVersion: r.TargetFrameworkVersion,
				NodeKind: r.NodeKind, Adjudicated: len(r.AdjudicatedRefs),
				PairsConsidered: r.PairsConsidered, Proposed: r.Proposed,
				Model: r.Model, CompletedAt: r.CompletedAt,
			})
		}
		return nil
	})
	return out, err
}

// nodeNames best-effort loads a target framework's node names for display. A
// target that is not loaded locally simply shows refs.
func (s *Service) nodeNames(ctx context.Context, ref string) map[string]string {
	out := map[string]string{}
	doc, err := s.ExportFlatIn(ctx, ref, CanonicalLanguage)
	if err != nil {
		return out
	}
	for _, r := range doc.Requirements {
		out[coredata.MappingNodeRequirement+"\x00"+r.Ref] = r.Name
	}
	for _, c := range doc.Controls {
		out[coredata.MappingNodeControl+"\x00"+c.Ref] = c.Name
	}
	return out
}

// AcceptMappingProposals turns accepted proposals into real mappings.
//
// The mapping is written as a code-addressed stub exactly like a hand-authored
// one, so it resolves through the existing publish-time machinery. A proposal
// whose source node no longer exists is skipped rather than failing the batch —
// the framework may have been edited since the run.
func (s *Service) AcceptMappingProposals(ctx context.Context, actorID gid.GID, ref string, ids []gid.GID) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	applied := 0
	err := s.db.WithTx(ctx, func(ctx context.Context, tx pg.Tx) error {
		scope := s.platformScope()

		var framework coredata.Framework
		if err := framework.LoadByReferenceID(ctx, tx, scope, ref); err != nil {
			return err
		}
		versionID, err := s.latestVersionIDTx(ctx, tx, framework.ID)
		if err != nil {
			return err
		}
		if _, _, err := s.requireDraft(ctx, tx, actorID, versionID, iam.ActionControlEdit); err != nil {
			return err
		}

		var requirements coredata.Requirements
		if err := requirements.LoadAllByVersion(ctx, tx, scope, versionID); err != nil {
			return err
		}
		reqByCode := map[string]gid.GID{}
		for _, r := range requirements {
			reqByCode[r.Code] = r.ID
		}

		var controls coredata.ControlLibraryEntries
		if err := controls.LoadAllByFramework(ctx, tx, scope, framework.ID); err != nil {
			return err
		}
		ctrlByCode := map[string]gid.GID{}
		for _, c := range controls {
			ctrlByCode[c.Code] = c.ID
		}

		now := time.Now()
		for _, id := range ids {
			var p coredata.MappingProposal
			if err := p.LoadByID(ctx, tx, scope, id); err != nil {
				return err
			}
			if p.Status == coredata.ProposalAccepted {
				continue
			}

			switch p.NodeKind {
			case coredata.MappingNodeRequirement:
				sourceID, ok := reqByCode[p.SourceRef]
				if !ok {
					continue // the requirement was edited away since the run
				}
				m := coredata.RequirementCrossMapping{
					ID:                       gid.New(s.cfg.PlatformTenant, coredata.RequirementCrossMappingEntityType),
					SourceRequirementID:      sourceID,
					SourceFrameworkVersionID: versionID,
					Relation:                 p.Relation,
					TargetFrameworkCode:      p.TargetFrameworkCode,
					TargetFrameworkVersion:   p.TargetFrameworkVersion,
					TargetRequirementCode:    p.TargetRef,
					Confidence:               p.Confidence,
					Rationale:                p.Rationale,
					ReviewState:              coredata.MappingReviewCurrent,
					CreatedAt:                now,
				}
				if err := m.Insert(ctx, tx, scope); err != nil && !errors.Is(err, coredata.ErrResourceAlreadyExists) {
					return fmt.Errorf("mapping %s→%s: %w", p.SourceRef, p.TargetRef, err)
				}
			case coredata.MappingNodeControl:
				sourceID, ok := ctrlByCode[p.SourceRef]
				if !ok {
					continue
				}
				m := coredata.ControlCrossMapping{
					ID:                       gid.New(s.cfg.PlatformTenant, coredata.ControlCrossMappingEntityType),
					SourceControlID:          sourceID,
					SourceFrameworkVersionID: versionID,
					Relation:                 p.Relation,
					TargetFrameworkCode:      p.TargetFrameworkCode,
					TargetFrameworkVersion:   p.TargetFrameworkVersion,
					TargetControlCode:        p.TargetRef,
					Confidence:               p.Confidence,
					Rationale:                p.Rationale,
					ReviewState:              coredata.MappingReviewCurrent,
					CreatedAt:                now,
				}
				if err := m.Insert(ctx, tx, scope); err != nil && !errors.Is(err, coredata.ErrResourceAlreadyExists) {
					return fmt.Errorf("control mapping %s→%s: %w", p.SourceRef, p.TargetRef, err)
				}
			default:
				continue
			}

			if err := (coredata.MappingProposal{}).SetStatus(ctx, tx, scope, id, coredata.ProposalAccepted, actorID.String()); err != nil {
				return err
			}
			applied++
		}

		// Bind anything whose target is already published.
		if _, err := (coredata.RequirementCrossMappings{}).ResolveOwnStubs(ctx, tx, scope, versionID); err != nil {
			return err
		}
		if _, err := (coredata.ControlCrossMappings{}).ResolveOwnStubs(ctx, tx, scope, versionID); err != nil {
			return err
		}

		return s.recordAudit(ctx, tx, scope, actorID, "mapping.accept_proposals", ref,
			fmt.Sprintf("accepted=%d", applied))
	})
	return applied, err
}

// RejectMappingProposals records a rejection. Rejected rows are kept, not
// deleted: they are what stops the next run re-proposing what a human already
// turned down.
func (s *Service) RejectMappingProposals(ctx context.Context, actorID gid.GID, ref string, ids []gid.GID) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	rejected := 0
	err := s.db.WithTx(ctx, func(ctx context.Context, tx pg.Tx) error {
		scope := s.platformScope()

		var framework coredata.Framework
		if err := framework.LoadByReferenceID(ctx, tx, scope, ref); err != nil {
			return err
		}
		versionID, err := s.latestVersionIDTx(ctx, tx, framework.ID)
		if err != nil {
			return err
		}
		if _, _, err := s.requireDraft(ctx, tx, actorID, versionID, iam.ActionControlEdit); err != nil {
			return err
		}
		for _, id := range ids {
			if err := (coredata.MappingProposal{}).SetStatus(ctx, tx, scope, id, coredata.ProposalRejected, actorID.String()); err != nil {
				return err
			}
			rejected++
		}
		return s.recordAudit(ctx, tx, scope, actorID, "mapping.reject_proposals", ref,
			fmt.Sprintf("rejected=%d", rejected))
	})
	return rejected, err
}

// latestVersionIDTx is latestVersionIDConn inside a transaction.
func (s *Service) latestVersionIDTx(ctx context.Context, tx pg.Tx, frameworkID gid.GID) (gid.GID, error) {
	var versions coredata.FrameworkVersions
	if err := versions.LoadAllByFramework(ctx, tx, s.platformScope(), frameworkID); err != nil {
		return gid.Nil, err
	}
	if len(versions) == 0 {
		return gid.Nil, coredata.ErrResourceNotFound
	}
	return versions[0].ID, nil
}
