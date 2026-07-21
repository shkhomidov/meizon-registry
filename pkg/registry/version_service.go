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
	"strings"
	"time"

	"go.gearno.de/kit/pg"
	"go.meizon.cloud/registry/pkg/coredata"
	"go.meizon.cloud/registry/pkg/fwflat"
	"go.meizon.cloud/registry/pkg/gid"
	"go.meizon.cloud/registry/pkg/iam"
)

// Change kinds for a next-version diff.
const (
	ChangeAdded     = "added"
	ChangeModified  = "modified"
	ChangeRemoved   = "removed"
	ChangeUnchanged = "unchanged"
)

// NextVersionProposal is a generated framework diffed against the framework's
// current latest version. Diff keys use the same node-key convention as
// provenance ("cat:<code>", "req:<code>", "item:<code>"); values are one of the
// Change* kinds. Removed keys refer to baseline nodes absent from the new doc.
type NextVersionProposal struct {
	GeneratedFramework
	BaselineVersion string            `json:"baselineVersion"`
	Diff            map[string]string `json:"diff"`
}

// AcceptNextVersion imports a reviewed next-version document as a new DRAFT
// version (origin=ai) on the existing framework, then follows the normal
// lifecycle. The framework's region governs authorization.
func (s *Service) AcceptNextVersion(ctx context.Context, actorID gid.GID, ref, newVersion string, flat *fwflat.Framework) (CreateFrameworkResult, error) {
	flat.Normalize()
	if err := flat.Validate(fwflat.ValidateOptions{}); err != nil {
		return CreateFrameworkResult{}, fmt.Errorf("the framework is not valid: %w", err)
	}
	doc, err := fromFlat(flat)
	if err != nil {
		return CreateFrameworkResult{}, err
	}
	if strings.TrimSpace(newVersion) == "" {
		return CreateFrameworkResult{}, fmt.Errorf("a new version identifier is required")
	}

	var out CreateFrameworkResult
	err = s.db.WithTx(ctx, func(ctx context.Context, tx pg.Tx) error {
		scope := s.platformScope()

		var framework coredata.Framework
		if err := framework.LoadByReferenceID(ctx, tx, scope, ref); err != nil {
			return err
		}

		if err := s.authorize(ctx, tx, actorID, iam.ActionFrameworkCreate, framework.Region, gid.Nil); err != nil {
			return err
		}

		var versions coredata.FrameworkVersions
		if err := versions.LoadAllByFramework(ctx, tx, scope, framework.ID); err != nil {
			return err
		}
		for _, v := range versions {
			if v.Version == newVersion {
				return fmt.Errorf("version %q already exists for %q", newVersion, ref)
			}
		}

		now := time.Now()
		versionID, err := s.createVersionTx(ctx, tx, framework.ID, actorID, doc, newVersion, "ai", now)
		if err != nil {
			return err
		}

		out = CreateFrameworkResult{FrameworkID: framework.ID, VersionID: versionID, Version: newVersion}
		return s.recordAudit(ctx, tx, scope, actorID, "framework.version_generate_accept", framework.ID.String(),
			fmt.Sprintf("%s@%s origin=ai", framework.ReferenceID, newVersion))
	})
	if err == nil {
		// A new version replaces the content the old translation described, so
		// the canonical record has to be rebuilt for it.
		s.EnsureCanonicalTranslation(ctx, actorID, ref)
	}
	return out, err
}

// diffFlatAgainstBaseline classifies each requirement of a newly generated flat
// framework against the framework's current latest version, by ref.
func diffFlatAgainstBaseline(baseline []StructureCategory, doc *fwflat.Framework) map[string]string {
	base := map[string]string{}
	for _, c := range baseline {
		for _, r := range c.Requirements {
			base["req:"+r.Code] = r.Title + "\x00" + r.Description
		}
	}

	diff := map[string]string{}
	seen := map[string]bool{}
	for _, r := range doc.Requirements {
		text := r.Name + "\x00" + r.Description
		key, prev, ok := "req:"+r.Ref, "", false
		if v, hit := base[key]; hit {
			prev, ok = v, true
		}
		seen[key] = true
		seen["item:"+r.Ref] = true
		switch {
		case !ok:
			diff[key] = ChangeAdded
		case prev != text:
			diff[key] = ChangeModified
		default:
			diff[key] = ChangeUnchanged
		}
	}
	for key := range base {
		if !seen[key] {
			diff[key] = ChangeRemoved
		}
	}
	return diff
}
