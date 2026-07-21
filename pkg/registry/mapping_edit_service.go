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
	"fmt"

	"go.gearno.de/kit/pg"
	"go.meizon.cloud/registry/pkg/coredata"
	"go.meizon.cloud/registry/pkg/fwflat"
	"go.meizon.cloud/registry/pkg/fwschema"
	"go.meizon.cloud/registry/pkg/gid"
	"go.meizon.cloud/registry/pkg/iam"
)

// MappingRow is one saved mapping between two frameworks, with both ends named
// so an auditor can judge it without cross-referencing codes by hand.
type MappingRow struct {
	ID         gid.GID `json:"id"`
	NodeKind   string  `json:"nodeKind"` // requirement | control
	SourceRef  string  `json:"sourceRef"`
	SourceName string  `json:"sourceName,omitempty"`
	TargetRef  string  `json:"targetRef"`
	TargetName string  `json:"targetName,omitempty"`
	Relation   string  `json:"relation"`
	Confidence float64 `json:"confidence,omitempty"`
	Rationale  string  `json:"rationale,omitempty"`
	Notes      string  `json:"notes,omitempty"`
	Resolved   bool    `json:"resolved"`
	// ReviewState is `needs_review` when the target framework published a new
	// version that changed the requirement this points at.
	ReviewState string `json:"reviewState,omitempty"`
}

// MappingsBetween lists the saved mappings from one framework to another, at
// both levels. Names are read in the canonical language, the same basis the
// mapping was decided on.
func (s *Service) MappingsBetween(ctx context.Context, sourceRef, targetRef string) ([]MappingRow, error) {
	out := []MappingRow{}

	sourceDoc, err := s.ExportFlatIn(ctx, sourceRef, CanonicalLanguage)
	if err != nil {
		return nil, err
	}
	sourceReq, sourceCtl := nameIndex(sourceDoc)

	// A target that is not loaded here is normal — the mapping is a stub until
	// that framework arrives — so missing names are left blank, not an error.
	targetReq, targetCtl := map[string]string{}, map[string]string{}
	if targetRef != "" {
		if targetDoc, terr := s.ExportFlatIn(ctx, targetRef, CanonicalLanguage); terr == nil {
			targetReq, targetCtl = nameIndex(targetDoc)
		}
	}

	err = s.db.WithConn(ctx, func(ctx context.Context, conn pg.Querier) error {
		scope := s.platformScope()

		var framework coredata.Framework
		if err := framework.LoadByReferenceID(ctx, conn, scope, sourceRef); err != nil {
			return err
		}
		versionID, err := s.latestVersionIDConn(ctx, conn, framework.ID)
		if err != nil {
			return err
		}

		var reqMappings coredata.RequirementCrossMappings
		if err := reqMappings.LoadAllByVersion(ctx, conn, scope, versionID); err != nil {
			return err
		}
		for _, m := range reqMappings {
			if targetRef != "" && m.TargetFrameworkCode != targetRef {
				continue
			}
			code, err := s.requirementCodeByID(ctx, conn, scope, versionID, m.SourceRequirementID)
			if err != nil {
				return err
			}
			out = append(out, MappingRow{
				ID: m.ID, NodeKind: coredata.MappingNodeRequirement,
				SourceRef: code, SourceName: sourceReq[code],
				TargetRef: m.TargetRequirementCode, TargetName: targetReq[m.TargetRequirementCode],
				Relation: m.Relation, Confidence: m.Confidence, Rationale: m.Rationale,
				Notes: m.Notes, Resolved: m.IsResolved, ReviewState: m.ReviewState,
			})
		}

		var ctlMappings coredata.ControlCrossMappings
		if err := ctlMappings.LoadAllByVersion(ctx, conn, scope, versionID); err != nil {
			return err
		}
		var library coredata.ControlLibraryEntries
		if err := library.LoadAllByFramework(ctx, conn, scope, framework.ID); err != nil {
			return err
		}
		codeByID := map[gid.GID]string{}
		for _, c := range library {
			codeByID[c.ID] = c.Code
		}
		for _, m := range ctlMappings {
			if targetRef != "" && m.TargetFrameworkCode != targetRef {
				continue
			}
			code := codeByID[m.SourceControlID]
			out = append(out, MappingRow{
				ID: m.ID, NodeKind: coredata.MappingNodeControl,
				SourceRef: code, SourceName: sourceCtl[code],
				TargetRef: m.TargetControlCode, TargetName: targetCtl[m.TargetControlCode],
				Relation: m.Relation, Confidence: m.Confidence, Rationale: m.Rationale,
				Notes: m.Notes, Resolved: m.IsResolved, ReviewState: m.ReviewState,
			})
		}
		return nil
	})
	return out, err
}

// nameIndex builds ref → name lookups for a framework's requirements and controls.
func nameIndex(doc *fwflat.Framework) (map[string]string, map[string]string) {
	reqs, ctls := map[string]string{}, map[string]string{}
	for _, r := range doc.Requirements {
		reqs[r.Ref] = r.Name
	}
	for _, c := range doc.Controls {
		ctls[c.Ref] = c.Name
	}
	return reqs, ctls
}

func (s *Service) requirementCodeByID(ctx context.Context, conn pg.Querier, scope coredata.Scoper, versionID, id gid.GID) (string, error) {
	var requirements coredata.Requirements
	if err := requirements.LoadAllByVersion(ctx, conn, scope, versionID); err != nil {
		return "", err
	}
	for _, r := range requirements {
		if r.ID == id {
			return r.Code, nil
		}
	}
	return "", nil
}

// UpdateMapping edits a saved mapping's relation and notes.
//
// The TARGET is deliberately not editable: pointing a mapping somewhere else is
// a different assertion about the standard, and silently rewriting the row
// would leave the original claim with no trace. Delete and re-map instead.
func (s *Service) UpdateMapping(ctx context.Context, actorID gid.GID, ref, nodeKind string, id gid.GID, relation, notes string) error {
	if !fwschema.MappingRelation(relation).IsValid() {
		return fmt.Errorf("%w: unknown relation %q (equivalent|partial|superset|subset)", ErrInvalidInput, relation)
	}
	return s.db.WithTx(ctx, func(ctx context.Context, tx pg.Tx) error {
		if _, err := s.mappingEditScope(ctx, tx, actorID, ref); err != nil {
			return err
		}

		scope := s.platformScope()
		switch nodeKind {
		case coredata.MappingNodeControl:
			if err := (coredata.ControlCrossMapping{}).Update(ctx, tx, scope, id, relation, notes); err != nil {
				return err
			}
		default:
			if err := (coredata.RequirementCrossMapping{}).Update(ctx, tx, scope, id, relation, notes); err != nil {
				return err
			}
		}
		return s.recordAudit(ctx, tx, scope, actorID, "mapping.update", ref,
			fmt.Sprintf("kind=%s id=%s relation=%s", nodeKind, id, relation))
	})
}

// DeleteMapping removes a saved mapping of either kind.
func (s *Service) DeleteMapping(ctx context.Context, actorID gid.GID, ref, nodeKind string, id gid.GID) error {
	return s.db.WithTx(ctx, func(ctx context.Context, tx pg.Tx) error {
		if _, err := s.mappingEditScope(ctx, tx, actorID, ref); err != nil {
			return err
		}
		scope := s.platformScope()
		switch nodeKind {
		case coredata.MappingNodeControl:
			if err := (coredata.ControlCrossMapping{}).Delete(ctx, tx, scope, id); err != nil {
				return err
			}
		default:
			if err := (coredata.RequirementCrossMapping{}).Delete(ctx, tx, scope, id); err != nil {
				return err
			}
		}
		return s.recordAudit(ctx, tx, scope, actorID, "mapping.delete", ref,
			fmt.Sprintf("kind=%s id=%s", nodeKind, id))
	})
}

// mappingEditScope authorizes a mapping edit and returns the version it applies
// to. DRAFT-only: a published version's mapping codes are inside its signed
// bundle, so editing one would invalidate the signature that vouches for it.
func (s *Service) mappingEditScope(ctx context.Context, tx pg.Tx, actorID gid.GID, ref string) (gid.GID, error) {
	scope := s.platformScope()

	var framework coredata.Framework
	if err := framework.LoadByReferenceID(ctx, tx, scope, ref); err != nil {
		return gid.Nil, err
	}
	versionID, err := s.latestVersionIDTx(ctx, tx, framework.ID)
	if err != nil {
		return gid.Nil, err
	}
	if _, _, err := s.requireDraft(ctx, tx, actorID, versionID, iam.ActionControlEdit); err != nil {
		return gid.Nil, err
	}
	return versionID, nil
}
