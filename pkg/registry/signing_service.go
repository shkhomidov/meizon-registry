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
	"crypto/ed25519"
	"errors"
	"fmt"
	"time"

	"go.gearno.de/kit/pg"
	"go.meizon.cloud/registry/pkg/coredata"
	"go.meizon.cloud/registry/pkg/crypto/cipher"
	"go.meizon.cloud/registry/pkg/fwschema"
	"go.meizon.cloud/registry/pkg/gid"
	"go.meizon.cloud/registry/pkg/iam"
)

// GenerateSigningKey creates a new active ed25519 signing key, deactivating any
// previously active key. Superadmin only. The private half is stored encrypted.
func (s *Service) GenerateSigningKey(ctx context.Context, actorID gid.GID, keyID string) error {
	if keyID == "" {
		return fmt.Errorf("key id is required")
	}

	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		return fmt.Errorf("cannot generate ed25519 key: %w", err)
	}

	encrypted, err := cipher.Encrypt(priv, s.cfg.EncryptionKey)
	if err != nil {
		return fmt.Errorf("cannot encrypt private key: %w", err)
	}

	return s.db.WithTx(ctx, func(ctx context.Context, tx pg.Tx) error {
		if err := s.authorize(ctx, tx, actorID, iam.ActionKeyManage, "", gid.Nil); err != nil {
			return err
		}

		scope := s.platformScope()

		var active coredata.SigningKey
		if err := active.LoadActive(ctx, tx, scope); err == nil {
			if err := active.Deactivate(ctx, tx, scope, time.Now()); err != nil {
				return fmt.Errorf("cannot rotate previous key: %w", err)
			}
		} else if !errors.Is(err, coredata.ErrResourceNotFound) {
			return err
		}

		key := coredata.SigningKey{
			ID:                  gid.New(s.cfg.PlatformTenant, coredata.SigningKeyEntityType),
			KeyID:               keyID,
			PublicKey:           pub,
			EncryptedPrivateKey: encrypted,
			Active:              true,
			CreatedAt:           time.Now(),
		}
		if err := key.Insert(ctx, tx, scope); err != nil {
			return fmt.Errorf("cannot store signing key: %w", err)
		}

		return s.recordAudit(ctx, tx, scope, actorID, "key.generate", key.ID.String(), keyID)
	})
}

// activeSigningKey returns the current active key id and decrypted private key.
func (s *Service) activeSigningKey(ctx context.Context, conn pg.Querier) (string, ed25519.PrivateKey, error) {
	var key coredata.SigningKey
	if err := key.LoadActive(ctx, conn, s.platformScope()); err != nil {
		return "", nil, fmt.Errorf("no active signing key: %w", err)
	}

	priv, err := cipher.Decrypt(key.EncryptedPrivateKey, s.cfg.EncryptionKey)
	if err != nil {
		return "", nil, fmt.Errorf("cannot decrypt signing key: %w", err)
	}

	return key.KeyID, ed25519.PrivateKey(priv), nil
}

// VerificationKeys returns every signing key's public half, indexed by key id,
// for verifying bundles.
func (s *Service) VerificationKeys(ctx context.Context) (map[string]ed25519.PublicKey, error) {
	out := map[string]ed25519.PublicKey{}
	err := s.db.WithConn(ctx, func(ctx context.Context, conn pg.Querier) error {
		var keys coredata.SigningKeys
		if err := keys.LoadAllPublic(ctx, conn, s.platformScope()); err != nil {
			return err
		}
		for _, k := range keys {
			out[k.KeyID] = ed25519.PublicKey(k.PublicKey)
		}
		return nil
	})
	return out, err
}

// assembleBundle builds the exchange document for a version (without a
// signature): a structured v2 bundle when the version has requirements,
// otherwise the legacy flat v1 bundle from controls.
func (s *Service) assembleBundle(ctx context.Context, conn pg.Querier, scope coredata.Scoper, framework coredata.Framework, version coredata.FrameworkVersion) (*fwschema.Framework, error) {
	requirementCount, err := (&coredata.Requirements{}).CountByVersion(ctx, conn, scope, version.ID)
	if err != nil {
		return nil, err
	}
	if requirementCount > 0 {
		return s.buildBundleV2(ctx, conn, scope, framework, version)
	}

	var controls coredata.Controls
	if err := controls.LoadAllByVersion(ctx, conn, scope, version.ID); err != nil {
		return nil, fmt.Errorf("cannot load controls: %w", err)
	}
	return buildBundle(framework, version, controls), nil
}

// buildBundleV2 assembles the v2 exchange document from the version's stored
// structure and code-based mappings: categories, their requirements, and the
// mappings each requirement declares. Resolution state is deliberately
// excluded — signed content addresses targets by code only, which is what lets
// a stub resolve later without changing the signature.
func (s *Service) buildBundleV2(ctx context.Context, conn pg.Querier, scope coredata.Scoper, framework coredata.Framework, version coredata.FrameworkVersion) (*fwschema.Framework, error) {
	var categories coredata.RequirementCategories
	var requirements coredata.Requirements
	var mappings coredata.RequirementCrossMappings

	if err := categories.LoadAllByVersion(ctx, conn, scope, version.ID); err != nil {
		return nil, err
	}
	if err := requirements.LoadAllByVersion(ctx, conn, scope, version.ID); err != nil {
		return nil, err
	}
	if err := mappings.LoadAllByVersion(ctx, conn, scope, version.ID); err != nil {
		return nil, err
	}

	mapsByRequirement := map[gid.GID][]fwschema.ItemMapping{}
	for _, m := range mappings {
		mapsByRequirement[m.SourceRequirementID] = append(mapsByRequirement[m.SourceRequirementID], fwschema.ItemMapping{
			Relation:  fwschema.MappingRelation(m.Relation),
			Framework: m.TargetFrameworkCode,
			Version:   m.TargetFrameworkVersion,
			Item:      m.TargetRequirementCode,
			Notes:     m.Notes,
		})
	}

	reqsByCategory := map[gid.GID][]fwschema.Requirement{}
	for _, r := range requirements {
		reqsByCategory[r.CategoryID] = append(reqsByCategory[r.CategoryID], fwschema.Requirement{
			Code:                   r.Code,
			Number:                 r.Number,
			Title:                  r.Title,
			Description:            r.Description,
			ItemType:               fwschema.ItemType(r.ItemType),
			LegalCitation:          r.LegalCitation,
			ValidationApproaches:   r.ValidationApproaches,
			EffectiveFrom:          r.EffectiveFrom,
			RetiredAt:              r.RetiredAt,
			Guidance:               r.Guidance,
			Tags:                   r.Tags,
			ApplicabilityRoles:     r.ApplicabilityRoles,
			ApplicabilityCondition: r.ApplicabilityCondition,
			Mappings:               mapsByRequirement[r.ID],
		})
	}

	bundle := &fwschema.Framework{
		SchemaVersion: fwschema.SchemaVersion2,
		ID:            framework.ReferenceID,
		Name:          framework.Name,
		ShortName:     framework.ShortName,
		Version:       version.Version,
		Status:        fwschema.Status(version.Status),
		Region:        framework.Region,
		Authority:     framework.Authority,
		License:       fwschema.License(framework.License),
		Description:   framework.Description,
		RevisionNotes: version.Changelog,
		Categories:    make([]fwschema.Category, 0, len(categories)),
	}
	if version.PublishedAt != nil {
		bundle.PublishedAt = version.PublishedAt.UTC().Format(time.RFC3339)
	}
	for _, c := range categories {
		bundle.Categories = append(bundle.Categories, fwschema.Category{
			Code: c.Code, Name: c.Name, Description: c.Description,
			IsOptional: c.IsOptional, ApplicabilityNote: c.ApplicabilityNote,
			Requirements: reqsByCategory[c.ID],
		})
	}

	return bundle, nil
}

// buildBundle assembles the rich exchange schema v1 object from a version and
// its controls. The signature is left unset; callers Sign it.
func buildBundle(framework coredata.Framework, version coredata.FrameworkVersion, controls coredata.Controls) *fwschema.Framework {
	bundle := &fwschema.Framework{
		SchemaVersion: fwschema.SchemaVersion,
		ID:            framework.ReferenceID,
		Name:          framework.Name,
		ShortName:     framework.ShortName,
		Version:       version.Version,
		Status:        fwschema.Status(version.Status),
		Region:        framework.Region,
		Authority:     framework.Authority,
		License:       fwschema.License(framework.License),
		Description:   framework.Description,
		RevisionNotes: version.Changelog,
		Controls:      make([]fwschema.Control, 0, len(controls)),
	}

	if version.PublishedAt != nil {
		bundle.PublishedAt = version.PublishedAt.UTC().Format(time.RFC3339)
	}

	for _, c := range controls {
		control := fwschema.Control{
			ID:          c.RefID,
			Name:        c.Name,
			Description: c.Description,
			Section:     c.Section,
			ParentID:    c.ParentRefID,
			Guidance:    c.Guidance,
			References:  c.Refs,
		}
		for _, m := range c.Mappings {
			control.Mappings = append(control.Mappings, fwschema.Mapping{Framework: m.Framework, Control: m.Control})
		}
		bundle.Controls = append(bundle.Controls, control)
	}

	return bundle
}
