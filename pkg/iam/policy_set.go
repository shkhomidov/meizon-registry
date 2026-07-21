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

import (
	"strings"

	"go.meizon.cloud/registry/pkg/gid"
	"go.meizon.cloud/registry/pkg/iam/policy"
)

// PolicySet maps role names to policies, plus policies applied to every
// authenticated identity regardless of role.
type PolicySet struct {
	rolePolicies     map[string][]*policy.Policy
	identityPolicies []*policy.Policy
}

// NewPolicySet returns an empty policy set.
func NewPolicySet() *PolicySet {
	return &PolicySet{rolePolicies: map[string][]*policy.Policy{}}
}

// AddRolePolicy attaches policies to a role.
func (ps *PolicySet) AddRolePolicy(role string, policies ...*policy.Policy) *PolicySet {
	ps.rolePolicies[role] = append(ps.rolePolicies[role], policies...)
	return ps
}

// AddIdentityScopedPolicy attaches policies applied to all authenticated
// identities.
func (ps *PolicySet) AddIdentityScopedPolicy(policies ...*policy.Policy) *PolicySet {
	ps.identityPolicies = append(ps.identityPolicies, policies...)
	return ps
}

func (ps *PolicySet) policiesForRole(role string) []*policy.Policy {
	out := make([]*policy.Policy, 0, len(ps.identityPolicies)+2)
	out = append(out, ps.identityPolicies...)
	out = append(out, ps.rolePolicies[role]...)
	return out
}

// Principal is the actor being authorized: a specific identity holding one role
// with a set of regions.
type Principal struct {
	ID      gid.GID
	Role    string
	Regions []string
}

func (p Principal) attributes() policy.Attributes {
	return policy.Attributes{
		"id":      p.ID.String(),
		"role":    p.Role,
		"regions": strings.Join(p.Regions, ","),
	}
}

// RegionGlobal is a wildcard region: a region-scoped principal whose regions
// include it may act in ANY region (superadmin bypasses region scoping outright;
// this lets a global auditor/moderator do the same within their role's actions).
const RegionGlobal = "GLOBAL"

// Authorize evaluates whether the principal may perform action on the resource,
// given the resource's attributes (which must include "region" for region-scoped
// checks). It returns the raw evaluation result so callers can log the decision.
func (ps *PolicySet) Authorize(
	p Principal,
	action string,
	resource gid.GID,
	resourceAttrs policy.Attributes,
) policy.EvaluationResult {
	if resourceAttrs == nil {
		resourceAttrs = policy.Attributes{}
	}

	principalAttrs := p.attributes()
	// GLOBAL acts as an all-regions wildcard: fold the resource's region into the
	// principal's region set so the region condition matches for any region.
	if region := resourceAttrs["region"]; region != "" && containsRegion(p.Regions, RegionGlobal) {
		principalAttrs["regions"] = strings.Join(append(append([]string{}, p.Regions...), region), ",")
	}

	req := policy.AuthorizationRequest{
		Principal: p.ID,
		Resource:  resource,
		Action:    action,
		ConditionContext: policy.ConditionContext{
			Principal: principalAttrs,
			Resource:  resourceAttrs,
		},
	}

	return policy.NewEvaluator().Evaluate(req, ps.policiesForRole(p.Role))
}

func containsRegion(regions []string, target string) bool {
	for _, r := range regions {
		if r == target {
			return true
		}
	}
	return false
}

// IsAllowed is a convenience wrapper over Authorize.
func (ps *PolicySet) IsAllowed(
	p Principal,
	action string,
	resource gid.GID,
	resourceAttrs policy.Attributes,
) bool {
	return ps.Authorize(p, action, resource, resourceAttrs).IsAllowed()
}
