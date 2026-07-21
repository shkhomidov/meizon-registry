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

package iam

import "go.meizon.cloud/registry/pkg/iam/policy"

// regionCondition requires the resource's region to be within the principal's
// region set. principal.regions is a comma-joined field (e.g. "EU,US") and the
// In operator treats it as a set. Statements carrying this condition are denied
// when a resource exposes no region attribute, so region-scoped roles cannot act
// on region-less or cross-region resources. A principal whose regions include
// GLOBAL is treated as an all-regions wildcard (see PolicySet.Authorize).
var regionCondition = policy.In("resource.region", "principal.regions")

// SuperAdminPolicy grants everything with no region condition, so superadmins
// bypass region scoping. Assignment of this role is additionally gated by the
// REGISTRYD_SUPER_ADMINS env allowlist at the service boundary.
var SuperAdminPolicy = policy.NewPolicy(
	"registry:superadmin",
	"Registry Super Admin",
	policy.Allow("*").WithSID("full-access"),
).WithDescription("Platform governance: users, keys, tenants, tokens, cross-region")

// ModeratorPolicy is the review/approve/publish persona, scoped to its regions.
// It may author, submit, review, approve, reject, publish and deprecate within
// its regions, and read the catalog. It cannot manage users, keys or tokens.
var ModeratorPolicy = policy.NewPolicy(
	"registry:moderator",
	"Registry Moderator",
	policy.Allow(
		ActionFrameworkCreate, ActionFrameworkEdit, ActionFrameworkSubmit,
		ActionFrameworkGet, ActionFrameworkList, ActionFrameworkDelete,
		ActionVersionReview, ActionVersionApprove, ActionVersionReject,
		ActionVersionPublish, ActionVersionDeprecate,
		ActionVersionGet, ActionVersionList,
		ActionControlCreate, ActionControlEdit, ActionControlDelete,
		ActionControlGet, ActionControlList,
	).WithSID("moderate-in-region").When(regionCondition),
).WithDescription("Review, approve and publish framework versions in the moderator's regions")

// AuditorPolicy is the authoring persona, scoped to its regions. It may create,
// edit and submit drafts and read, but never review, approve, publish or
// deprecate.
var AuditorPolicy = policy.NewPolicy(
	"registry:auditor",
	"Registry Auditor",
	policy.Allow(
		ActionFrameworkCreate, ActionFrameworkEdit, ActionFrameworkSubmit,
		ActionFrameworkGet, ActionFrameworkList,
		ActionVersionGet, ActionVersionList,
		ActionControlCreate, ActionControlEdit, ActionControlDelete,
		ActionControlGet, ActionControlList,
	).WithSID("author-in-region").When(regionCondition),
).WithDescription("Author and submit framework drafts in the auditor's regions")

// RegistryPolicySet is the platform's default role→policy mapping.
func RegistryPolicySet() *PolicySet {
	return NewPolicySet().
		AddRolePolicy(RoleSuperAdmin, SuperAdminPolicy).
		AddRolePolicy(RoleModerator, ModeratorPolicy).
		AddRolePolicy(RoleAuditor, AuditorPolicy)
}
