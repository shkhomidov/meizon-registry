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

import "go.meizon.cloud/registry/pkg/gid"

// Decision is the result of a policy evaluation.
type Decision string

const (
	DecisionAllow   Decision = "allow"
	DecisionDeny    Decision = "deny"
	DecisionNoMatch Decision = "no_match"
)

// EvaluationResult carries the decision and the statement/policy that produced
// it, for decision logging.
type EvaluationResult struct {
	Decision         Decision
	MatchedStatement *Statement
	MatchedPolicy    *Policy
}

// IsAllowed reports whether access should be granted.
func (r EvaluationResult) IsAllowed() bool {
	return r.Decision == DecisionAllow
}

// PolicyID returns the matched statement SID or policy id for logging.
func (r EvaluationResult) PolicyID() string {
	if r.MatchedStatement != nil && r.MatchedStatement.SID != "" {
		return r.MatchedStatement.SID
	}
	if r.MatchedPolicy != nil {
		return r.MatchedPolicy.ID
	}
	return ""
}

// AuthorizationRequest carries everything needed to evaluate access.
type AuthorizationRequest struct {
	Principal        gid.GID
	Resource         gid.GID
	Action           string
	ConditionContext ConditionContext
}

// Evaluator applies policies with AWS-style semantics: explicit deny > explicit
// allow > implicit deny.
type Evaluator struct {
	matcher *ActionMatcher
}

// NewEvaluator creates a new evaluator.
func NewEvaluator() *Evaluator {
	return &Evaluator{matcher: NewActionMatcher()}
}

// Evaluate applies the given policies to the request.
func (e *Evaluator) Evaluate(req AuthorizationRequest, policies []*Policy) EvaluationResult {
	var allowResult *EvaluationResult

	for _, pol := range policies {
		for i := range pol.Statements {
			stmt := &pol.Statements[i]

			if !e.statementMatches(stmt, req) {
				continue
			}

			if stmt.Effect == EffectDeny {
				return EvaluationResult{
					Decision:         DecisionDeny,
					MatchedStatement: stmt,
					MatchedPolicy:    pol,
				}
			}

			if stmt.Effect == EffectAllow && allowResult == nil {
				allowResult = &EvaluationResult{
					Decision:         DecisionAllow,
					MatchedStatement: stmt,
					MatchedPolicy:    pol,
				}
			}
		}
	}

	if allowResult != nil {
		return *allowResult
	}

	return EvaluationResult{Decision: DecisionNoMatch}
}

func (e *Evaluator) statementMatches(stmt *Statement, req AuthorizationRequest) bool {
	if !e.matcher.MatchesAny(stmt.Actions, req.Action) {
		return false
	}

	if len(stmt.Resources) > 0 {
		matched := false
		for _, pattern := range stmt.Resources {
			if pattern.MatchesResource(req.Resource) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}

	for _, condition := range stmt.Conditions {
		if !condition.Evaluate(req.ConditionContext) {
			return false
		}
	}

	return true
}
