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
	"go.meizon.cloud/registry/pkg/fwschema"
	"go.meizon.cloud/registry/pkg/gid"
)

// ErrNotDistributable is returned when a framework or version cannot be served
// to the caller (not published, or gated by the copyright rule).
var ErrNotDistributable = errors.New("framework version is not distributable")

// CatalogEntry is a single row of the distribution catalog.
type CatalogEntry struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Region        string    `json:"region"`
	License       string    `json:"license"`
	LatestVersion string    `json:"latestVersion"`
	ContentHash   string    `json:"contentHash"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

// ChangeEvent is one entry of the consumer-facing change feed.
type ChangeEvent struct {
	Seq         int64  `json:"seq"`
	Kind        string `json:"kind"` // published | deprecated | mapping_published | mapping_deprecated
	Framework   string `json:"framework"`
	Version     string `json:"version"`
	ContentHash string `json:"contentHash,omitempty"`
	Region      string `json:"region,omitempty"`
	// TargetFramework/TargetVersion are set only on mapping_* events: the other
	// framework the mapping set connects to, so a consumer can tell whether it
	// holds both ends before fetching.
	TargetFramework string    `json:"targetFramework,omitempty"`
	TargetVersion   string    `json:"targetVersion,omitempty"`
	OccurredAt      time.Time `json:"occurredAt"`
}

// ChangeFeed is a page of the feed plus the cursor to resume from.
type ChangeFeed struct {
	Events []ChangeEvent `json:"events"`
	// NextSeq is where the consumer should resume. It advances only over
	// events actually returned, so a client that persists it after a
	// successful import can never skip one.
	NextSeq int64 `json:"nextSeq"`
	// HeadSeq is the newest sequence visible to this token, so a consumer can
	// report how far behind it is without draining the feed.
	HeadSeq int64 `json:"headSeq"`
	HasMore bool  `json:"hasMore"`
}

// Change-feed paging bounds. The default is small enough that a consumer
// importing synchronously stays responsive; the max caps a single query.
const (
	changesDefaultLimit = 100
	changesMaxLimit     = 500
)

// Changes returns events after the cursor that this token may see.
//
// This supersedes the catalog's `?since=<timestamp>` filter, which could not
// represent a retirement and was unsafe as a cursor: an event committed at T but
// made visible after the consumer read T would be lost with no way to detect it.
func (s *Service) Changes(ctx context.Context, tc TokenContext, since int64, limit int) (ChangeFeed, error) {
	if since < 0 {
		since = 0
	}
	switch {
	case limit <= 0:
		limit = changesDefaultLimit
	case limit > changesMaxLimit:
		limit = changesMaxLimit
	}

	feed := ChangeFeed{Events: []ChangeEvent{}, NextSeq: since}
	err := s.db.WithConn(ctx, func(ctx context.Context, conn pg.Querier) error {
		head, err := coredata.HeadSeq(ctx, conn, tc.OwnerTenant, tc.Regions)
		if err != nil {
			return err
		}
		feed.HeadSeq = head

		// Ask for one more than requested: if it comes back, there is another
		// page. Cheaper and race-free compared to a separate COUNT.
		var events coredata.DistributionEvents
		if err := events.LoadSince(ctx, conn, tc.OwnerTenant, tc.Regions, since, limit+1); err != nil {
			return err
		}

		feed.HasMore = len(events) > limit
		if feed.HasMore {
			events = events[:limit]
		}

		for _, e := range events {
			feed.Events = append(feed.Events, ChangeEvent{
				Seq: e.Seq, Kind: e.Kind, Framework: e.FrameworkRef,
				Version: e.Version, ContentHash: e.ContentHash, Region: e.Region,
				TargetFramework: e.TargetFrameworkRef, TargetVersion: e.TargetVersion,
				OccurredAt: e.CreatedAt,
			})
			feed.NextSeq = e.Seq
		}
		return nil
	})
	return feed, err
}

// TokenContext is the resolved identity of a distribution consumer.
type TokenContext struct {
	TokenID     gid.GID
	OwnerTenant gid.TenantID
	Regions     []string
}

// AuthenticateToken resolves a bearer token to its distribution context.
func (s *Service) AuthenticateToken(ctx context.Context, plaintext string) (TokenContext, error) {
	var tc TokenContext
	err := s.db.WithConn(ctx, func(ctx context.Context, conn pg.Querier) error {
		token, err := s.resolveToken(ctx, conn, plaintext)
		if err != nil {
			return err
		}
		tc = TokenContext{
			TokenID:     token.ID,
			OwnerTenant: token.ID.TenantID(),
			Regions:     token.RegionList(),
		}
		return nil
	})
	return tc, err
}

// Catalog returns the published frameworks visible to the token, filtered by an
// optional region and a "since" timestamp.
func (s *Service) Catalog(ctx context.Context, tc TokenContext, region string, since *time.Time) ([]CatalogEntry, error) {
	var out []CatalogEntry
	err := s.db.WithConn(ctx, func(ctx context.Context, conn pg.Querier) error {
		regions := tc.Regions
		if region != "" {
			regions = intersect(regions, []string{region})
			if len(tc.Regions) == 0 {
				regions = []string{region}
			}
			if len(regions) == 0 {
				return nil // requested region outside token scope
			}
		}

		var frameworks coredata.Frameworks
		if err := frameworks.LoadCatalog(ctx, conn, tc.OwnerTenant, regions); err != nil {
			return err
		}

		for _, f := range frameworks {
			var latest coredata.FrameworkVersion
			if err := latest.LoadLatestPublished(ctx, conn, f.ID); err != nil {
				if errors.Is(err, coredata.ErrResourceNotFound) {
					continue // no published version yet
				}
				return err
			}

			if since != nil && latest.PublishedAt != nil && !latest.PublishedAt.After(*since) {
				continue
			}

			out = append(out, CatalogEntry{
				ID:            f.ReferenceID,
				Name:          f.Name,
				Region:        f.Region,
				License:       f.License,
				LatestVersion: latest.Version,
				ContentHash:   latest.ContentHash,
				UpdatedAt:     derefTime(latest.PublishedAt),
			})
		}
		return nil
	})
	return out, err
}

// Bundle returns the signed rich-schema bundle for a published version and
// records the download.
func (s *Service) Bundle(ctx context.Context, tc TokenContext, referenceID, version string) (*fwschema.Framework, error) {
	var bundle *fwschema.Framework
	err := s.db.WithConn(ctx, func(ctx context.Context, conn pg.Querier) error {
		framework, ver, err := s.resolveDistributable(ctx, conn, tc, referenceID, version)
		if err != nil {
			return err
		}

		b, err := s.assembleBundle(ctx, conn, coredata.NewScopeFromObjectID(framework.ID), framework, ver)
		if err != nil {
			return err
		}

		if len(ver.Signature) > 0 {
			var sig fwschema.Signature
			if err := jsonUnmarshal(ver.Signature, &sig); err != nil {
				return fmt.Errorf("cannot decode stored signature: %w", err)
			}
			b.Signature = &sig
		}

		bundle = b
		return s.recordDownload(ctx, conn, tc, ver, coredata.DownloadFormatBundle)
	})
	return bundle, err
}

// Seed returns the flat GRC seed for a published version and records the
// download.
func (s *Service) Seed(ctx context.Context, tc TokenContext, referenceID, version string) (fwschema.GRCSeed, error) {
	var seed fwschema.GRCSeed
	err := s.db.WithConn(ctx, func(ctx context.Context, conn pg.Querier) error {
		framework, ver, err := s.resolveDistributable(ctx, conn, tc, referenceID, version)
		if err != nil {
			return err
		}

		b, err := s.assembleBundle(ctx, conn, coredata.NewScopeFromObjectID(framework.ID), framework, ver)
		if err != nil {
			return err
		}

		seed = b.Flatten()
		return s.recordDownload(ctx, conn, tc, ver, coredata.DownloadFormatSeed)
	})
	return seed, err
}

// resolveDistributable loads a framework/version pair, enforcing the copyright
// gate (proprietary frameworks only for the owning tenant), the region scope of
// the token, and that the version is PUBLISHED.
func (s *Service) resolveDistributable(ctx context.Context, conn pg.Querier, tc TokenContext, referenceID, version string) (coredata.Framework, coredata.FrameworkVersion, error) {
	var framework coredata.Framework
	if err := framework.LoadByReferenceID(ctx, conn, coredata.NewNoScope(), referenceID); err != nil {
		return framework, coredata.FrameworkVersion{}, err
	}

	// Copyright gate: non-public frameworks are visible only to the owning tenant.
	if !framework.Public && framework.ID.TenantID() != tc.OwnerTenant {
		return framework, coredata.FrameworkVersion{}, ErrNotDistributable
	}

	// Region scope: an empty token region set means all regions.
	if len(tc.Regions) > 0 && !contains(tc.Regions, framework.Region) {
		return framework, coredata.FrameworkVersion{}, ErrNotDistributable
	}

	var ver coredata.FrameworkVersion
	scope := coredata.NewScopeFromObjectID(framework.ID)
	if version == "" || version == "latest" {
		if err := ver.LoadLatestPublished(ctx, conn, framework.ID); err != nil {
			return framework, ver, err
		}
	} else {
		if err := ver.LoadByFrameworkAndVersion(ctx, conn, scope, framework.ID, version); err != nil {
			return framework, ver, err
		}
		if ver.Status != coredata.FrameworkVersionStatusPublished {
			return framework, ver, ErrNotDistributable
		}
	}

	return framework, ver, nil
}

func (s *Service) recordDownload(ctx context.Context, conn pg.Querier, tc TokenContext, ver coredata.FrameworkVersion, format string) error {
	scope := coredata.NewScopeFromObjectID(ver.ID)
	dl := coredata.Download{
		ID:                 gid.New(ver.ID.TenantID(), coredata.DownloadEntityType),
		FrameworkVersionID: ver.ID,
		TokenID:            tc.TokenID.String(),
		Format:             format,
		CreatedAt:          time.Now(),
	}
	return dl.Insert(ctx, conn, scope)
}

// Versions lists the versions of a framework visible to the token.
func (s *Service) Versions(ctx context.Context, tc TokenContext, referenceID string) (coredata.Framework, coredata.FrameworkVersions, error) {
	var framework coredata.Framework
	var versions coredata.FrameworkVersions
	err := s.db.WithConn(ctx, func(ctx context.Context, conn pg.Querier) error {
		if err := framework.LoadByReferenceID(ctx, conn, coredata.NewNoScope(), referenceID); err != nil {
			return err
		}
		if !framework.Public && framework.ID.TenantID() != tc.OwnerTenant {
			return ErrNotDistributable
		}
		// Region scope, same rule as resolveDistributable. Without it a token
		// scoped to one region could enumerate the versions and content hashes
		// of a framework in another — metadata it can never fetch the body of.
		if len(tc.Regions) > 0 && !contains(tc.Regions, framework.Region) {
			return ErrNotDistributable
		}
		return versions.LoadAllByFramework(ctx, conn, coredata.NewScopeFromObjectID(framework.ID), framework.ID)
	})
	return framework, versions, err
}
