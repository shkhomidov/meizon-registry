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

// Package policy implements a small attribute-based access-control engine.
// Actions are dotted "service:resource:operation" strings matched with
// wildcards; statements allow or deny actions on resource patterns subject to
// conditions; evaluation follows AWS semantics of explicit deny > allow >
// implicit deny. The registry layers role and region conditions on top.
package policy

import (
	"strings"

	"go.meizon.cloud/registry/pkg/gid"
)

// Effect represents whether a statement allows or denies access.
type Effect string

const (
	EffectAllow Effect = "allow"
	EffectDeny  Effect = "deny"
)

// Statement is a single permission rule within a policy.
type Statement struct {
	SID        string
	Effect     Effect
	Actions    []string
	Resources  []ResourcePattern
	Conditions []Condition
}

// ResourcePattern matches resources by tenant and/or entity type. Nil fields are
// wildcards.
type ResourcePattern struct {
	TenantID   *gid.TenantID
	EntityType *uint16
}

// MatchesResource reports whether the pattern matches a resource GID.
func (p ResourcePattern) MatchesResource(resource gid.GID) bool {
	if p.TenantID != nil && *p.TenantID != resource.TenantID() {
		return false
	}

	if p.EntityType != nil && *p.EntityType != resource.EntityType() {
		return false
	}

	return true
}

// Condition is an attribute-based constraint, e.g. resource.region ∈ principal.regions.
type Condition struct {
	Operator ConditionOperator
	Key      string
	Values   []string
}

// ConditionOperator defines how condition values are compared.
type ConditionOperator string

const (
	ConditionEquals    ConditionOperator = "Equals"
	ConditionNotEquals ConditionOperator = "NotEquals"
	ConditionIn        ConditionOperator = "In"
	ConditionNotIn     ConditionOperator = "NotIn"
)

type (
	// Attributes is a flat key/value bag consumed by condition evaluation.
	Attributes = map[string]string

	// AttributesByID groups Attributes by resource id.
	AttributesByID = map[gid.GID]Attributes
)

// ConditionContext provides attribute values for condition evaluation.
type ConditionContext struct {
	Principal Attributes
	Resource  Attributes
}

// Evaluate reports whether the condition is satisfied by the context.
func (c Condition) Evaluate(ctx ConditionContext) bool {
	value, ok := resolveKey(c.Key, ctx)
	if !ok {
		return false
	}

	switch c.Operator {
	case ConditionEquals:
		for _, v := range c.Values {
			resolved, ok := resolveValue(v, ctx)
			if ok && value == resolved {
				return true
			}
		}
		return false

	case ConditionNotEquals:
		for _, v := range c.Values {
			resolved, ok := resolveValue(v, ctx)
			if ok && value == resolved {
				return false
			}
		}
		return true

	case ConditionIn:
		for _, v := range c.Values {
			resolved, ok := resolveValue(v, ctx)
			if !ok {
				continue
			}
			if strings.Contains(resolved, ",") {
				for _, item := range strings.Split(resolved, ",") {
					if value == strings.TrimSpace(item) {
						return true
					}
				}
				continue
			}
			if value == resolved {
				return true
			}
		}
		return false

	case ConditionNotIn:
		for _, v := range c.Values {
			resolved, ok := resolveValue(v, ctx)
			if !ok {
				continue
			}
			if strings.Contains(resolved, ",") {
				for _, item := range strings.Split(resolved, ",") {
					if value == strings.TrimSpace(item) {
						return false
					}
				}
				continue
			}
			if value == resolved {
				return false
			}
		}
		return true

	default:
		return false
	}
}

func resolveKey(key string, ctx ConditionContext) (string, bool) {
	if strings.HasPrefix(key, "principal.") {
		val, ok := ctx.Principal[key[len("principal."):]]
		return val, ok
	}

	if strings.HasPrefix(key, "resource.") {
		val, ok := ctx.Resource[key[len("resource."):]]
		return val, ok
	}

	return "", false
}

func resolveValue(value string, ctx ConditionContext) (string, bool) {
	if strings.HasPrefix(value, "principal.") || strings.HasPrefix(value, "resource.") {
		return resolveKey(value, ctx)
	}

	return value, true
}
