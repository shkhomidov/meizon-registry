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

package policy

// Policy is a named collection of statements attached to a role or principal.
type Policy struct {
	ID          string
	Name        string
	Description string
	Statements  []Statement
}

// NewPolicy creates a new policy.
func NewPolicy(id, name string, statements ...Statement) *Policy {
	return &Policy{ID: id, Name: name, Statements: statements}
}

// WithDescription sets the description and returns the policy for chaining.
func (p *Policy) WithDescription(desc string) *Policy {
	p.Description = desc
	return p
}

// AddStatement appends a statement to the policy.
func (p *Policy) AddStatement(stmt Statement) {
	p.Statements = append(p.Statements, stmt)
}

// Allow creates an allow statement over the given actions.
func Allow(actions ...string) Statement {
	return Statement{Effect: EffectAllow, Actions: actions}
}

// Deny creates a deny statement over the given actions.
func Deny(actions ...string) Statement {
	return Statement{Effect: EffectDeny, Actions: actions}
}

// WithSID sets the statement id and returns the statement for chaining.
func (s Statement) WithSID(sid string) Statement {
	s.SID = sid
	return s
}

// WithResources sets the resource patterns and returns the statement.
func (s Statement) WithResources(resources ...ResourcePattern) Statement {
	s.Resources = resources
	return s
}

// WithConditions sets the conditions and returns the statement.
func (s Statement) WithConditions(conditions ...Condition) Statement {
	s.Conditions = conditions
	return s
}

// When is a readable alias for WithConditions.
func (s Statement) When(conditions ...Condition) Statement {
	return s.WithConditions(conditions...)
}

// Equals creates an Equals condition.
func Equals(key string, values ...string) Condition {
	return Condition{Operator: ConditionEquals, Key: key, Values: values}
}

// NotEquals creates a NotEquals condition.
func NotEquals(key string, values ...string) Condition {
	return Condition{Operator: ConditionNotEquals, Key: key, Values: values}
}

// In creates an In condition: the key's value must be one of the resolved
// values (a value may itself be a comma-separated set, e.g. principal.regions).
func In(key string, values ...string) Condition {
	return Condition{Operator: ConditionIn, Key: key, Values: values}
}

// NotIn creates a NotIn condition.
func NotIn(key string, values ...string) Condition {
	return Condition{Operator: ConditionNotIn, Key: key, Values: values}
}
