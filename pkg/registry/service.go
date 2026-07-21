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

// Package registry is the service layer that orchestrates the framework
// lifecycle. It owns GID generation, request validation, authorization,
// separation-of-duties enforcement, ed25519 signing and the distribution reads.
// coredata never runs business logic; every mutation goes through here inside a
// transaction.
package registry

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.gearno.de/kit/log"
	"go.gearno.de/kit/pg"
	"go.meizon.cloud/registry/pkg/coredata"
	"go.meizon.cloud/registry/pkg/crypto/cipher"
	"go.meizon.cloud/registry/pkg/crypto/passwdhash"
	"go.meizon.cloud/registry/pkg/gid"
	"go.meizon.cloud/registry/pkg/iam"
	"go.meizon.cloud/registry/pkg/iam/policy"
	"go.meizon.cloud/registry/pkg/llm"
)

// ErrForbidden is returned when the actor is not authorized for an action.
var ErrForbidden = errors.New("forbidden")

// ErrInvalidTransition is returned when a lifecycle transition is not allowed
// from the current status.
var ErrInvalidTransition = errors.New("invalid lifecycle transition")

// ErrInvalidInput marks a service error caused by what the caller asked for,
// not by a fault in the server. Without it the API layer's default turns every
// such message into an opaque "internal error" — so a precise, actionable
// explanation ("this framework has no English version yet") reaches the user as
// nothing at all.
var ErrInvalidInput = errors.New("invalid request")

// Config configures the service.
type Config struct {
	// PlatformTenant owns identities, memberships, signing keys and tokens. For
	// this build it also owns authored frameworks unless a different owner tenant
	// is supplied.
	PlatformTenant gid.TenantID

	// EncryptionKey encrypts signing-key private material at rest.
	EncryptionKey cipher.EncryptionKey

	// SuperAdmins is the env allowlist of emails permitted to hold the superadmin
	// role.
	SuperAdmins []string
}

// Service is the registry business layer.
type Service struct {
	db          *pg.Client
	policies    *iam.PolicySet
	hashProfile *passwdhash.Profile
	cfg         Config
	superAdmins map[string]struct{}
	logger      *log.Logger
	llmFactory  func(llm.Config) (llm.Client, error)
	ingestJobs  *ingestJobRegistry
}

// NewService constructs the service.
func NewService(db *pg.Client, hashProfile *passwdhash.Profile, cfg Config, logger *log.Logger) *Service {
	admins := make(map[string]struct{}, len(cfg.SuperAdmins))
	for _, e := range cfg.SuperAdmins {
		admins[strings.ToLower(strings.TrimSpace(e))] = struct{}{}
	}

	return &Service{
		db:          db,
		policies:    iam.RegistryPolicySet(),
		hashProfile: hashProfile,
		cfg:         cfg,
		superAdmins: admins,
		logger:      logger.Named("registry"),
		llmFactory:  llm.New,
		ingestJobs:  newIngestJobRegistry(),
	}
}

func (s *Service) platformScope() *coredata.Scope {
	return coredata.NewScope(s.cfg.PlatformTenant)
}

func (s *Service) isSuperAdminEmail(email string) bool {
	_, ok := s.superAdmins[strings.ToLower(strings.TrimSpace(email))]
	return ok
}

// principalFor loads the actor's role and regions and builds an IAM principal.
func (s *Service) principalFor(ctx context.Context, conn pg.Querier, actorID gid.GID) (iam.Principal, error) {
	var membership coredata.Membership
	if err := membership.LoadByIdentityID(ctx, conn, s.platformScope(), actorID); err != nil {
		return iam.Principal{}, fmt.Errorf("cannot load actor membership: %w", err)
	}

	return iam.Principal{
		ID:      actorID,
		Role:    membership.Role,
		Regions: membership.RegionList(),
	}, nil
}

// authorize checks that the actor may perform action on a resource in the given
// region. A region-less resource passes only the region-unscoped (superadmin)
// grant.
func (s *Service) authorize(ctx context.Context, conn pg.Querier, actorID gid.GID, action, region string, resource gid.GID) error {
	principal, err := s.principalFor(ctx, conn, actorID)
	if err != nil {
		return err
	}

	attrs := policy.Attributes{}
	if region != "" {
		attrs["region"] = region
	}

	result := s.policies.Authorize(principal, action, resource, attrs)

	s.logger.DebugCtx(ctx, "authorization decision",
		log.String("actor", actorID.String()),
		log.String("action", action),
		log.String("region", region),
		log.String("decision", string(result.Decision)),
		log.String("policy", result.PolicyID()),
	)

	if !result.IsAllowed() {
		return fmt.Errorf("%w: %s on region %q", ErrForbidden, action, region)
	}

	return nil
}

// recordAudit appends an immutable audit entry.
func (s *Service) recordAudit(ctx context.Context, conn pg.Querier, scope coredata.Scoper, actorID gid.GID, action, targetID, detail string) error {
	entry := coredata.AuditLogEntry{
		ID:        gid.New(scope.GetTenantID(), coredata.AuditLogEntityType),
		ActorID:   actorID.String(),
		Action:    action,
		TargetID:  targetID,
		Detail:    detail,
		CreatedAt: time.Now(),
	}
	return entry.Insert(ctx, conn, scope)
}
