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

package fwqa

import "testing"

func TestEval(t *testing.T) {
	tests := []struct {
		expr string
		ctx  EvalContext
		want bool
	}{
		// equality on strings
		{"answer == 'yes'", EvalContext{Answer: "yes"}, true},
		{"answer == 'yes'", EvalContext{Answer: "no"}, false},
		{"answer != 'no'", EvalContext{Answer: "yes"}, true},
		{"verdict == 'partial'", EvalContext{Verdict: "partial"}, true},

		// numeric comparison
		{"value <= 7", EvalContext{Value: 5}, true},
		{"value <= 7", EvalContext{Value: 8}, false},
		{"score >= 3", EvalContext{Score: 3}, true},
		{"score >= 3", EvalContext{Score: 2}, false},
		{"ageDays > 92", EvalContext{AgeDays: 100}, true},
		{"value == 0", EvalContext{Value: 0}, true},

		// evidence / selected counts
		{"evidence.count >= 1", EvalContext{EvidenceCount: 2}, true},
		{"evidence.count >= 1", EvalContext{EvidenceCount: 0}, false},
		{"selected.count < 2", EvalContext{Selected: []string{"a"}}, true},

		// booleans
		{"attested == true", EvalContext{Attested: true}, true},
		{"attested == false", EvalContext{Attested: true}, false},
		{"attested == true", EvalContext{Attested: false}, false},

		// boolean combinators + precedence (&& binds tighter than ||)
		{"answer == 'yes' && evidence.count >= 1", EvalContext{Answer: "yes", EvidenceCount: 1}, true},
		{"answer == 'yes' && evidence.count >= 1", EvalContext{Answer: "yes", EvidenceCount: 0}, false},
		{"answer == 'yes' || answer == 'na'", EvalContext{Answer: "na"}, true},
		{"answer == 'no' || answer == 'na' && verdict == 'x'", EvalContext{Answer: "no"}, true},

		// parentheses override precedence
		{"(answer == 'no' || answer == 'na') && attested == true",
			EvalContext{Answer: "na", Attested: true}, true},
		{"(answer == 'no' || answer == 'na') && attested == true",
			EvalContext{Answer: "na", Attested: false}, false},

		// in / superset
		{"answer in ['onboarding_only','intranet_passive']", EvalContext{Answer: "intranet_passive"}, true},
		{"answer in ['onboarding_only','intranet_passive']", EvalContext{Answer: "not_communicated"}, false},
		{"selected superset ['email','internet','removable_media']",
			EvalContext{Selected: []string{"email", "internet", "removable_media", "cloud_saas"}}, true},
		{"selected superset ['email','internet','removable_media']",
			EvalContext{Selected: []string{"email", "internet"}}, false},
	}

	for _, tc := range tests {
		got, err := Eval(tc.expr, tc.ctx)
		if err != nil {
			t.Errorf("Eval(%q): unexpected error %v", tc.expr, err)
			continue
		}
		if got != tc.want {
			t.Errorf("Eval(%q, %+v) = %v, want %v", tc.expr, tc.ctx, got, tc.want)
		}
	}
}

func TestEvalErrors(t *testing.T) {
	bad := []string{
		"answer =",              // dangling operator
		"answer == ",            // missing right
		"answer = 'yes'",        // single =
		"answer && ",            // missing operand
		"(answer == 'yes'",      // unclosed paren
		"answer in 'yes'",       // in needs a list
		"value <> 3",            // unknown operator
		"answer == 'yes' extra", // trailing tokens
		"'unterminated",         // bad string
	}
	for _, expr := range bad {
		if _, err := Eval(expr, EvalContext{}); err == nil {
			t.Errorf("Eval(%q): expected an error, got nil", expr)
		}
	}
}

func TestCheckExpr(t *testing.T) {
	if err := CheckExpr("answer == 'yes' && score >= 3"); err != nil {
		t.Errorf("valid expr rejected: %v", err)
	}
	if err := CheckExpr("answer =="); err == nil {
		t.Error("malformed expr accepted")
	}
}
