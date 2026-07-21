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

import "strings"

// ActionMatcher performs wildcard matching for "service:resource:operation"
// actions. Supported patterns: "*", "service:*" (== "service:*:*") and any
// three-part pattern with "*" per segment.
type ActionMatcher struct{}

// NewActionMatcher creates a new action matcher.
func NewActionMatcher() *ActionMatcher {
	return &ActionMatcher{}
}

// Matches reports whether pattern matches the concrete target action.
func (m *ActionMatcher) Matches(pattern, target string) bool {
	if pattern == "*" {
		return true
	}

	patternParts := strings.Split(pattern, ":")
	targetParts := strings.Split(target, ":")

	if len(targetParts) != 3 {
		return false
	}

	switch len(patternParts) {
	case 1:
		return false
	case 2:
		if patternParts[1] == "*" {
			return patternParts[0] == targetParts[0] || patternParts[0] == "*"
		}
		return false
	case 3:
		return m.matchPart(patternParts[0], targetParts[0]) &&
			m.matchPart(patternParts[1], targetParts[1]) &&
			m.matchPart(patternParts[2], targetParts[2])
	default:
		return false
	}
}

func (m *ActionMatcher) matchPart(pattern, target string) bool {
	if pattern == "*" {
		return true
	}

	return pattern == target
}

// MatchesAny reports whether any pattern matches the target action.
func (m *ActionMatcher) MatchesAny(patterns []string, target string) bool {
	for _, pattern := range patterns {
		if m.Matches(pattern, target) {
			return true
		}
	}

	return false
}
