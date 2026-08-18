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
	"strings"
	"time"

	"go.gearno.de/kit/pg"
	"go.meizon.cloud/registry/pkg/coredata"
	"go.meizon.cloud/registry/pkg/gid"
	"go.meizon.cloud/registry/pkg/iam"
)

// ErrInvalidCredentials is returned when authentication fails.
var ErrInvalidCredentials = errors.New("invalid credentials")

// CreateIdentityRequest describes a new identity.
type CreateIdentityRequest struct {
	Email    string
	FullName string
	Password string
}

func (s *Service) hashPassword(password string) ([]byte, error) {
	if len(password) < 8 {
		return nil, fmt.Errorf("password must be at least 8 characters")
	}
	return s.hashProfile.HashPassword([]byte(password))
}

// SignUp self-registers an identity with no role. The account is active but has
// no access until a superadmin assigns a role.
func (s *Service) SignUp(ctx context.Context, req CreateIdentityRequest) (gid.GID, error) {
	var id gid.GID
	err := s.db.WithTx(ctx, func(ctx context.Context, tx pg.Tx) error {
		created, err := s.createIdentityTx(ctx, tx, req, coredata.IdentityStatusActive)
		if err != nil {
			return err
		}
		id = created.ID
		return s.recordAudit(ctx, tx, s.platformScope(), created.ID, "identity.signup", created.ID.String(), req.Email)
	})
	return id, err
}

func (s *Service) createIdentityTx(ctx context.Context, tx pg.Tx, req CreateIdentityRequest, status coredata.IdentityStatus) (*coredata.Identity, error) {
	email := strings.ToLower(strings.TrimSpace(req.Email))
	if email == "" || !strings.Contains(email, "@") {
		return nil, fmt.Errorf("a valid email is required")
	}
	if strings.TrimSpace(req.FullName) == "" {
		return nil, fmt.Errorf("full name is required")
	}

	hashed, err := s.hashPassword(req.Password)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	identity := &coredata.Identity{
		ID:             gid.New(s.cfg.PlatformTenant, coredata.IdentityEntityType),
		Email:          email,
		FullName:       strings.TrimSpace(req.FullName),
		HashedPassword: hashed,
		Status:         status,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	if err := identity.Insert(ctx, tx, s.platformScope()); err != nil {
		return nil, fmt.Errorf("cannot create identity: %w", err)
	}

	return identity, nil
}

// BootstrapSuperAdmin creates (or promotes) a superadmin. It is gated by the env
// allowlist: the email must be listed in REGISTRYD_SUPER_ADMINS. This is the
// only path that does not itself require an existing superadmin actor.
func (s *Service) BootstrapSuperAdmin(ctx context.Context, req CreateIdentityRequest) (gid.GID, error) {
	if !s.isSuperAdminEmail(req.Email) {
		return gid.Nil, fmt.Errorf("%w: %q is not in the superadmin allowlist", ErrForbidden, req.Email)
	}

	var id gid.GID
	err := s.db.WithTx(ctx, func(ctx context.Context, tx pg.Tx) error {
		identity := &coredata.Identity{}
		err := identity.LoadByEmail(ctx, tx, s.platformScope(), strings.ToLower(strings.TrimSpace(req.Email)))
		switch {
		case errors.Is(err, coredata.ErrResourceNotFound):
			identity, err = s.createIdentityTx(ctx, tx, req, coredata.IdentityStatusActive)
			if err != nil {
				return err
			}
		case err != nil:
			return err
		default:
			// The identity already exists, so re-running bootstrap RESETS its
			// password. This is the supported recovery for a forgotten superadmin
			// password — gated by the same env allowlist, so only an allowlisted
			// email can do it, and it needs shell access to the server anyway.
			if strings.TrimSpace(req.Password) != "" {
				hashed, herr := s.hashPassword(req.Password)
				if herr != nil {
					return herr
				}
				identity.HashedPassword = hashed
				identity.UpdatedAt = time.Now()
				if err := identity.Update(ctx, tx, s.platformScope()); err != nil {
					return fmt.Errorf("cannot reset password: %w", err)
				}
			}
		}

		id = identity.ID
		if err := s.upsertMembershipTx(ctx, tx, identity.ID, iam.RoleSuperAdmin, nil); err != nil {
			return err
		}
		return s.recordAudit(ctx, tx, s.platformScope(), identity.ID, "identity.bootstrap_superadmin", identity.ID.String(), req.Email)
	})
	return id, err
}

// CreateUser is a superadmin convenience: create an identity and assign a role
// in one step.
func (s *Service) CreateUser(ctx context.Context, actorID gid.GID, req CreateIdentityRequest, role string, regions []string) (gid.GID, error) {
	if !iam.IsValidRole(role) {
		return gid.Nil, fmt.Errorf("unknown role %q", role)
	}

	var id gid.GID
	err := s.db.WithTx(ctx, func(ctx context.Context, tx pg.Tx) error {
		if err := s.authorize(ctx, tx, actorID, iam.ActionUserManage, "", gid.Nil); err != nil {
			return err
		}
		if role == iam.RoleSuperAdmin && !s.isSuperAdminEmail(req.Email) {
			return fmt.Errorf("%w: superadmin role requires the email to be in the allowlist", ErrForbidden)
		}

		identity, err := s.createIdentityTx(ctx, tx, req, coredata.IdentityStatusActive)
		if err != nil {
			return err
		}
		id = identity.ID

		if err := s.upsertMembershipTx(ctx, tx, identity.ID, role, regions); err != nil {
			return err
		}
		return s.recordAudit(ctx, tx, s.platformScope(), actorID, "identity.create_user", identity.ID.String(),
			fmt.Sprintf("%s role=%s regions=%s", req.Email, role, strings.Join(regions, ",")))
	})
	return id, err
}

// AssignRole sets the role and regions of an existing identity (superadmin only).
func (s *Service) AssignRole(ctx context.Context, actorID gid.GID, targetEmail, role string, regions []string) error {
	if !iam.IsValidRole(role) {
		return fmt.Errorf("unknown role %q", role)
	}

	return s.db.WithTx(ctx, func(ctx context.Context, tx pg.Tx) error {
		if err := s.authorize(ctx, tx, actorID, iam.ActionUserManage, "", gid.Nil); err != nil {
			return err
		}
		if role == iam.RoleSuperAdmin && !s.isSuperAdminEmail(targetEmail) {
			return fmt.Errorf("%w: superadmin role requires the email to be in the allowlist", ErrForbidden)
		}

		var target coredata.Identity
		if err := target.LoadByEmail(ctx, tx, s.platformScope(), strings.ToLower(strings.TrimSpace(targetEmail))); err != nil {
			return fmt.Errorf("cannot load target identity: %w", err)
		}

		if err := s.upsertMembershipTx(ctx, tx, target.ID, role, regions); err != nil {
			return err
		}
		return s.recordAudit(ctx, tx, s.platformScope(), actorID, "identity.assign_role", target.ID.String(),
			fmt.Sprintf("role=%s regions=%s", role, strings.Join(regions, ",")))
	})
}

func (s *Service) upsertMembershipTx(ctx context.Context, tx pg.Tx, identityID gid.GID, role string, regions []string) error {
	regionStr := strings.Join(regions, ",")

	var existing coredata.Membership
	err := existing.LoadByIdentityID(ctx, tx, s.platformScope(), identityID)
	switch {
	case errors.Is(err, coredata.ErrResourceNotFound):
		now := time.Now()
		membership := coredata.Membership{
			ID:         gid.New(s.cfg.PlatformTenant, coredata.MembershipEntityType),
			IdentityID: identityID,
			Role:       role,
			Regions:    regionStr,
			CreatedAt:  now,
			UpdatedAt:  now,
		}
		return membership.Insert(ctx, tx, s.platformScope())
	case err != nil:
		return err
	default:
		existing.Role = role
		existing.Regions = regionStr
		existing.UpdatedAt = time.Now()
		return existing.Update(ctx, tx, s.platformScope())
	}
}

// Authenticate verifies an email/password pair and returns the identity id.
func (s *Service) Authenticate(ctx context.Context, email, password string) (gid.GID, error) {
	var id gid.GID
	err := s.db.WithConn(ctx, func(ctx context.Context, conn pg.Querier) error {
		var identity coredata.Identity
		if err := identity.LoadByEmail(ctx, conn, s.platformScope(), strings.ToLower(strings.TrimSpace(email))); err != nil {
			if errors.Is(err, coredata.ErrResourceNotFound) {
				return ErrInvalidCredentials
			}
			return err
		}

		if identity.Status != coredata.IdentityStatusActive {
			return ErrInvalidCredentials
		}

		ok, err := s.hashProfile.ComparePasswordAndHash([]byte(password), identity.HashedPassword)
		if err != nil || !ok {
			return ErrInvalidCredentials
		}

		id = identity.ID
		return nil
	})
	return id, err
}

// IdentityIDByEmail resolves an email to an identity id (used by the CLI).
func (s *Service) IdentityIDByEmail(ctx context.Context, email string) (gid.GID, error) {
	var id gid.GID
	err := s.db.WithConn(ctx, func(ctx context.Context, conn pg.Querier) error {
		var identity coredata.Identity
		if err := identity.LoadByEmail(ctx, conn, s.platformScope(), strings.ToLower(strings.TrimSpace(email))); err != nil {
			return err
		}
		id = identity.ID
		return nil
	})
	return id, err
}
