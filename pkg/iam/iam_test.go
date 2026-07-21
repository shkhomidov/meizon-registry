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

package iam_test

import (
	"testing"

	"go.meizon.cloud/registry/pkg/gid"
	"go.meizon.cloud/registry/pkg/iam"
	"go.meizon.cloud/registry/pkg/iam/policy"
)

func euResource() (gid.GID, policy.Attributes) {
	return gid.New(gid.NewTenantID(), 4), policy.Attributes{"region": "EU"}
}

func usResource() (gid.GID, policy.Attributes) {
	return gid.New(gid.NewTenantID(), 4), policy.Attributes{"region": "US"}
}

func TestAuditorCanAuthorInRegionButNotApprove(t *testing.T) {
	ps := iam.RegistryPolicySet()
	auditor := iam.Principal{ID: gid.New(gid.NewTenantID(), 2), Role: iam.RoleAuditor, Regions: []string{"EU"}}
	res, attrs := euResource()

	if !ps.IsAllowed(auditor, iam.ActionFrameworkCreate, res, attrs) {
		t.Fatal("EU auditor should be able to create an EU framework")
	}
	if !ps.IsAllowed(auditor, iam.ActionFrameworkSubmit, res, attrs) {
		t.Fatal("EU auditor should be able to submit an EU framework")
	}
	if ps.IsAllowed(auditor, iam.ActionVersionApprove, res, attrs) {
		t.Fatal("auditor must NOT be able to approve")
	}
	if ps.IsAllowed(auditor, iam.ActionVersionPublish, res, attrs) {
		t.Fatal("auditor must NOT be able to publish")
	}
}

func TestAuditorCannotTouchOtherRegion(t *testing.T) {
	ps := iam.RegistryPolicySet()
	auditor := iam.Principal{ID: gid.New(gid.NewTenantID(), 2), Role: iam.RoleAuditor, Regions: []string{"EU"}}
	res, attrs := usResource()

	if ps.IsAllowed(auditor, iam.ActionFrameworkCreate, res, attrs) {
		t.Fatal("EU auditor must NOT create a US framework")
	}
	if ps.IsAllowed(auditor, iam.ActionFrameworkGet, res, attrs) {
		t.Fatal("EU auditor must NOT read a US framework")
	}
}

func TestModeratorCanApproveAndPublishInRegion(t *testing.T) {
	ps := iam.RegistryPolicySet()
	mod := iam.Principal{ID: gid.New(gid.NewTenantID(), 2), Role: iam.RoleModerator, Regions: []string{"EU"}}
	res, attrs := euResource()

	if !ps.IsAllowed(mod, iam.ActionVersionApprove, res, attrs) {
		t.Fatal("EU moderator should approve an EU version")
	}
	if !ps.IsAllowed(mod, iam.ActionVersionPublish, res, attrs) {
		t.Fatal("EU moderator should publish an EU version")
	}

	usRes, usAttrs := usResource()
	if ps.IsAllowed(mod, iam.ActionVersionApprove, usRes, usAttrs) {
		t.Fatal("EU moderator must NOT approve a US version")
	}
}

func TestModeratorWithMultipleRegions(t *testing.T) {
	ps := iam.RegistryPolicySet()
	mod := iam.Principal{ID: gid.New(gid.NewTenantID(), 2), Role: iam.RoleModerator, Regions: []string{"EU", "US"}}

	euRes, euAttrs := euResource()
	usRes, usAttrs := usResource()

	if !ps.IsAllowed(mod, iam.ActionVersionPublish, euRes, euAttrs) {
		t.Fatal("EU/US moderator should publish EU")
	}
	if !ps.IsAllowed(mod, iam.ActionVersionPublish, usRes, usAttrs) {
		t.Fatal("EU/US moderator should publish US")
	}
}

func TestGlobalRegionIsWildcard(t *testing.T) {
	ps := iam.RegistryPolicySet()
	// An auditor scoped to GLOBAL can author in any concrete region...
	globalAuditor := iam.Principal{ID: gid.New(gid.NewTenantID(), 2), Role: iam.RoleAuditor, Regions: []string{"GLOBAL"}}

	euRes, euAttrs := euResource()
	usRes, usAttrs := usResource()
	if !ps.IsAllowed(globalAuditor, iam.ActionFrameworkCreate, euRes, euAttrs) {
		t.Fatal("GLOBAL auditor should author an EU framework")
	}
	if !ps.IsAllowed(globalAuditor, iam.ActionFrameworkCreate, usRes, usAttrs) {
		t.Fatal("GLOBAL auditor should author a US framework")
	}
	// ...but still cannot approve (role limit, not region).
	if ps.IsAllowed(globalAuditor, iam.ActionVersionApprove, euRes, euAttrs) {
		t.Fatal("GLOBAL auditor must still not approve")
	}

	// A GLOBAL moderator can approve/publish in any region.
	globalMod := iam.Principal{ID: gid.New(gid.NewTenantID(), 2), Role: iam.RoleModerator, Regions: []string{"GLOBAL"}}
	if !ps.IsAllowed(globalMod, iam.ActionVersionPublish, usRes, usAttrs) {
		t.Fatal("GLOBAL moderator should publish a US version")
	}

	// A GLOBAL-region framework is still reachable by a GLOBAL principal.
	glRes := gid.New(gid.NewTenantID(), 4)
	glAttrs := policy.Attributes{"region": "GLOBAL"}
	if !ps.IsAllowed(globalAuditor, iam.ActionFrameworkCreate, glRes, glAttrs) {
		t.Fatal("GLOBAL auditor should author a GLOBAL framework")
	}
}

func TestSuperAdminBypassesRegion(t *testing.T) {
	ps := iam.RegistryPolicySet()
	sa := iam.Principal{ID: gid.New(gid.NewTenantID(), 2), Role: iam.RoleSuperAdmin, Regions: nil}

	usRes, usAttrs := usResource()
	if !ps.IsAllowed(sa, iam.ActionVersionPublish, usRes, usAttrs) {
		t.Fatal("superadmin should publish in any region")
	}
	if !ps.IsAllowed(sa, iam.ActionUserManage, usRes, usAttrs) {
		t.Fatal("superadmin should manage users")
	}
	// Even with no region attribute at all.
	if !ps.IsAllowed(sa, iam.ActionKeyManage, gid.New(gid.NewTenantID(), 9), nil) {
		t.Fatal("superadmin should manage signing keys regardless of region")
	}
}

func TestNonSuperAdminCannotGovern(t *testing.T) {
	ps := iam.RegistryPolicySet()
	res, attrs := euResource()

	for _, role := range []string{iam.RoleAuditor, iam.RoleModerator} {
		p := iam.Principal{ID: gid.New(gid.NewTenantID(), 2), Role: role, Regions: []string{"EU"}}
		if ps.IsAllowed(p, iam.ActionUserManage, res, attrs) {
			t.Fatalf("%s must NOT manage users", role)
		}
		if ps.IsAllowed(p, iam.ActionKeyManage, res, attrs) {
			t.Fatalf("%s must NOT manage signing keys", role)
		}
	}
}
