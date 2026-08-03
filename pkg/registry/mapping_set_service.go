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
	"go.meizon.cloud/registry/pkg/fwmap"
	"go.meizon.cloud/registry/pkg/gid"
	"go.meizon.cloud/registry/pkg/iam"
)

// ErrMappingTargetNotPublic is returned when publishing a mapping set whose
// target framework is not publicly distributable. A signed set cannot be
// filtered per consumer, so shipping one that names a non-public framework's
// codes would leak them permanently — signing is irreversible. This is refused
// at the boundary rather than papered over with an override, which would be the
// easy way to defeat it.
var ErrMappingTargetNotPublic = errors.New("mapping target framework is not publicly distributable; publishing would disclose its codes")

// PublishMappingSet assembles, signs and publishes the mapping set from a source
// framework to a target, and announces it on the change feed. It is
// moderator-gated (the same authority that publishes a framework version) and
// refuses a non-public target.
//
// Moderated publish is the whole point: like a framework, a mapping set only
// reaches consumers once someone with publish authority has released it. A
// fuller submit→approve queue can wrap this later; the release gate and the
// signature are what "moderated and verified" require, and both are here.
func (s *Service) PublishMappingSet(ctx context.Context, actorID gid.GID, sourceRef, targetRef, targetVersion string) (gid.GID, error) {
	var id gid.GID
	err := s.db.WithTx(ctx, func(ctx context.Context, tx pg.Tx) error {
		scope := s.platformScope()

		source, sourceVersion, err := s.resolveSource(ctx, tx, scope, sourceRef)
		if err != nil {
			return err
		}
		if err := s.authorize(ctx, tx, actorID, iam.ActionVersionPublish, source.Region, source.ID); err != nil {
			return err
		}

		// No separation-of-duties check here, unlike a framework version publish.
		// That is deliberate, not an oversight: a mapping set is not freehand
		// authored, it is assembled from cross-mappings that were themselves added
		// and reviewed on a DRAFT framework. There is no distinct "author" of the
		// set to hold at arm's length — the publisher is releasing already-moderated
		// content, so the framework SoD invariant does not carry a meaning here.

		// The security gate. Load the target framework and refuse if it is not
		// publicly distributable — a signed set naming its codes could never be
		// unshipped. A target that does not exist locally is allowed (the set is
		// a stub that resolves elsewhere); only a KNOWN non-public target is
		// refused, since that is the case where we would actually leak.
		var target coredata.Framework
		switch err := target.LoadByReferenceID(ctx, tx, coredata.NewNoScope(), targetRef); {
		case err == nil:
			if !target.Public {
				return ErrMappingTargetNotPublic
			}
		case errors.Is(err, coredata.ErrResourceNotFound):
			// Unknown target: not held here, cannot leak from here. Allowed.
		default:
			return err
		}

		// Assemble within the same transaction so the signed content matches
		// exactly what is stored, then sign.
		set, err := s.assembleMappingSetTx(ctx, tx, scope, source, sourceVersion, targetRef, targetVersion)
		if err != nil {
			return err
		}
		if set.IsEmpty() {
			return fmt.Errorf("%w: no mappings from %s to %s to publish", ErrInvalidInput, sourceRef, targetRef)
		}

		now := time.Now()
		set.PublishedBy = actorID.String()
		set.PublishedAt = now.UTC().Format(time.RFC3339)

		keyID, priv, err := s.activeSigningKey(ctx, tx)
		if err != nil {
			return err
		}
		if err := set.Sign(priv, keyID); err != nil {
			return fmt.Errorf("cannot sign mapping set: %w", err)
		}

		// Persist the exact signed JSON so distribution serves it verbatim. The
		// set is signed over its own provenance (including a publish timestamp),
		// which cannot be re-derived from rows byte-for-byte — so it is never
		// re-derived; the stored document is the source of truth.
		document, err := json.Marshal(set)
		if err != nil {
			return fmt.Errorf("cannot encode signed mapping set: %w", err)
		}

		sigBytes := []byte(set.Signature.Value)
		row := coredata.MappingSet{
			ID:                       gid.New(s.cfg.PlatformTenant, coredata.MappingSetEntityType),
			SourceFrameworkVersionID: sourceVersion.ID,
			TargetFrameworkCode:      targetRef,
			TargetFrameworkVersion:   targetVersion,
			Status:                   coredata.MappingSetStatusPublished,
			ContentHash:              set.Signature.ContentHash,
			Signature:                sigBytes,
			Document:                 document,
			KeyID:                    keyID,
			RequirementMappingCount:  len(set.Requirements),
			ControlMappingCount:      len(set.Controls),
			AuthorID:                 actorID.String(),
			PublishedBy:              actorID.String(),
			PublishedAt:              &now,
			CreatedAt:                now,
			UpdatedAt:                now,
		}
		if err := row.Upsert(ctx, tx, scope); err != nil {
			return err
		}
		id = row.ID

		// Announce it. The event names both endpoints so a consumer can tell
		// whether it holds both frameworks before fetching.
		ev := coredata.DistributionEvent{
			Kind:               coredata.DistributionEventMappingPublished,
			FrameworkRef:       source.ReferenceID,
			Version:            sourceVersion.Version,
			ContentHash:        set.Signature.ContentHash,
			Region:             source.Region,
			Public:             source.Public,
			TargetFrameworkRef: targetRef,
			TargetVersion:      targetVersion,
			CreatedAt:          now,
		}
		if err := ev.Append(ctx, tx, source.ID.TenantID()); err != nil {
			return err
		}

		return s.recordAudit(ctx, tx, scope, actorID, "mappingset.publish", row.ID.String(),
			fmt.Sprintf("%s@%s -> %s@%s reqs=%d ctls=%d", source.ReferenceID, sourceVersion.Version,
				targetRef, targetVersion, len(set.Requirements), len(set.Controls)))
	})
	return id, err
}

// assembleMappingSetTx is the in-transaction assembly used by publish, so the
// bytes signed are exactly the bytes derived from the rows in the same tx.
func (s *Service) assembleMappingSetTx(ctx context.Context, conn pg.Querier, scope coredata.Scoper, source coredata.Framework, sourceVersion coredata.FrameworkVersion, targetRef, targetVersion string) (*fwmap.MappingSet, error) {
	out := &fwmap.MappingSet{
		Schema: fwmap.SchemaMarker,
		Source: fwmap.Endpoint{Framework: source.ReferenceID, Version: sourceVersion.Version},
		Target: fwmap.Endpoint{Framework: targetRef, Version: targetVersion},
	}

	var reqMappings coredata.RequirementCrossMappings
	if err := reqMappings.LoadAllByVersion(ctx, conn, scope, sourceVersion.ID); err != nil {
		return nil, err
	}
	// Index requirement ids to codes once, rather than reloading every
	// requirement per mapping (an N+1 the control path below already avoids).
	var requirements coredata.Requirements
	if err := requirements.LoadAllByVersion(ctx, conn, scope, sourceVersion.ID); err != nil {
		return nil, err
	}
	reqCodeByID := map[gid.GID]string{}
	for _, r := range requirements {
		reqCodeByID[r.ID] = r.Code
	}
	for _, m := range reqMappings {
		if m.TargetFrameworkCode != targetRef {
			continue
		}
		out.Requirements = append(out.Requirements, fwmap.Mapping{
			SourceRef: reqCodeByID[m.SourceRequirementID], TargetRef: m.TargetRequirementCode,
			Relation: m.Relation, Notes: m.Notes,
		})
	}

	var ctlMappings coredata.ControlCrossMappings
	if err := ctlMappings.LoadAllByVersion(ctx, conn, scope, sourceVersion.ID); err != nil {
		return nil, err
	}
	var library coredata.ControlLibraryEntries
	if err := library.LoadAllByFramework(ctx, conn, scope, source.ID); err != nil {
		return nil, err
	}
	codeByID := map[gid.GID]string{}
	for _, c := range library {
		codeByID[c.ID] = c.Code
	}
	for _, m := range ctlMappings {
		if m.TargetFrameworkCode != targetRef {
			continue
		}
		out.Controls = append(out.Controls, fwmap.Mapping{
			SourceRef: codeByID[m.SourceControlID], TargetRef: m.TargetControlCode, Relation: m.Relation, Notes: m.Notes,
		})
	}
	return out, nil
}

// resolveSource loads a framework by reference and its latest version.
func (s *Service) resolveSource(ctx context.Context, conn pg.Querier, scope coredata.Scoper, ref string) (coredata.Framework, coredata.FrameworkVersion, error) {
	var framework coredata.Framework
	if err := framework.LoadByReferenceID(ctx, conn, scope, ref); err != nil {
		return framework, coredata.FrameworkVersion{}, err
	}
	versionID, err := s.latestVersionIDConn(ctx, conn, framework.ID)
	if err != nil {
		return framework, coredata.FrameworkVersion{}, err
	}
	var version coredata.FrameworkVersion
	if err := version.LoadByID(ctx, conn, scope, versionID); err != nil {
		return framework, version, err
	}
	return framework, version, nil
}
