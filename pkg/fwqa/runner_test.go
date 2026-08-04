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

func TestStepAssessesAndTriggersFollowUp(t *testing.T) {
	tpl := validTemplate() // from validate_test.go

	// q1 is yes_no_evidence: "yes" + evidence -> compliant, no follow-up.
	res, err := tpl.Step("q1", Answer{Value: "yes", Evidence: 1})
	if err != nil {
		t.Fatalf("step: %v", err)
	}
	if res.Verdict != VerdictCompliant {
		t.Fatalf("verdict = %q, want compliant", res.Verdict)
	}
	if res.Score == nil || *res.Score != 1.0 {
		t.Fatalf("score = %v, want 1.0", res.Score)
	}
	if len(res.FollowUps) != 0 {
		t.Fatalf("compliant answer should trigger no follow-up, got %v", res.FollowUps)
	}

	// "no" -> non_compliant AND fires the follow-up asking q2.
	res, err = tpl.Step("q1", Answer{Value: "no"})
	if err != nil {
		t.Fatalf("step: %v", err)
	}
	if res.Verdict != VerdictNonCompliant {
		t.Fatalf("verdict = %q, want non_compliant", res.Verdict)
	}
	if res.Score == nil || *res.Score != 0.0 {
		t.Fatalf("score = %v, want 0.0", res.Score)
	}
	if len(res.FollowUps) != 1 || res.FollowUps[0] != "q2" {
		t.Fatalf("expected follow-up [q2], got %v", res.FollowUps)
	}
}

func TestStepUnknownQuestion(t *testing.T) {
	if _, err := validTemplate().Step("ghost", Answer{}); err == nil {
		t.Fatal("stepping an unknown question should error")
	}
}

func TestMainSequenceExcludesConditional(t *testing.T) {
	seq := validTemplate().MainSequence()
	for _, q := range seq {
		if q.ID == "q2" {
			t.Fatal("conditional q2 must not be in the main sequence")
		}
	}
	if len(seq) != 2 { // q1, q3
		t.Fatalf("main sequence should be q1,q3; got %d", len(seq))
	}
}

func TestScoreForNotApplicableExcluded(t *testing.T) {
	tpl := validTemplate()
	if s := tpl.ScoreFor(VerdictNotApplicable); s != nil {
		t.Fatalf("not_applicable should have nil score, got %v", *s)
	}
}
