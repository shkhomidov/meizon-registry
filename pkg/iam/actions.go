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

// Package iam holds the registry's roles, action constants, role policies and
// the authorization entry point. Roles are region-scoped (auditor, moderator)
// or global (superadmin); the region axis is enforced by an ABAC condition that
// requires a resource's region to be within the principal's region set.
package iam

// Role names. superadmin is global; moderator and auditor are region-scoped.
const (
	RoleSuperAdmin = "superadmin"
	RoleModerator  = "moderator"
	RoleAuditor    = "auditor"
)

// IsValidRole reports whether r is a known role.
func IsValidRole(r string) bool {
	switch r {
	case RoleSuperAdmin, RoleModerator, RoleAuditor:
		return true
	default:
		return false
	}
}

// Action constants, formatted service:resource:operation.
const (
	// Framework drafts and catalog entries.
	ActionFrameworkCreate = "registry:framework:create"
	ActionFrameworkEdit   = "registry:framework:edit"
	ActionFrameworkSubmit = "registry:framework:submit"
	ActionFrameworkGet    = "registry:framework:get"
	ActionFrameworkList   = "registry:framework:list"
	ActionFrameworkDelete = "registry:framework:delete"

	// Version lifecycle transitions.
	ActionVersionReview    = "registry:version:review"
	ActionVersionApprove   = "registry:version:approve"
	ActionVersionReject    = "registry:version:reject"
	ActionVersionPublish   = "registry:version:publish"
	ActionVersionDeprecate = "registry:version:deprecate"
	ActionVersionGet       = "registry:version:get"
	ActionVersionList      = "registry:version:list"

	// Controls within a draft version.
	ActionControlCreate = "registry:control:create"
	ActionControlEdit   = "registry:control:edit"
	ActionControlDelete = "registry:control:delete"
	ActionControlGet    = "registry:control:get"
	ActionControlList   = "registry:control:list"

	// Platform governance (superadmin only via the "*" grant).
	ActionUserManage     = "registry:user:manage"
	ActionTokenManage    = "registry:token:manage"
	ActionKeyManage      = "registry:key:manage"
	ActionAuditRead      = "registry:audit:get"
	ActionMappingResolve = "registry:mapping:resolve"
	ActionSettingsManage = "registry:settings:manage"
)
