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

// The runner is the single source of truth for turning an answer into a verdict
// and deciding what a follow-up triggers. Both the chat preview and any real
// audit session go through it, so the flow logic exists once. The runner is
// stateless: it evaluates one answered question and reports what that answer
// scores and what it triggers; the caller owns the cursor and queue.

// Answer is the caller's response to one question, normalized into the variables
// the `when` language reads.
type Answer struct {
	Value    string   // yes/no/na, a choice value, or free text
	Score    float64  // scale questions
	Numeric  float64  // numeric questions (the `value` variable)
	AgeDays  float64  // date questions (freshness)
	Evidence int      // attached evidence count
	Selected []string // multi_choice selections
	Attested bool     // attestation
}

func (a Answer) evalContext(verdict string) EvalContext {
	return EvalContext{
		Answer:        a.Value,
		Verdict:       verdict,
		Score:         a.Score,
		Value:         a.Numeric,
		AgeDays:       a.AgeDays,
		EvidenceCount: a.Evidence,
		Selected:      a.Selected,
		Attested:      a.Attested,
	}
}

// StepResult is the outcome of answering one question.
type StepResult struct {
	Verdict   string   // "" if the question is unscored (e.g. free_text)
	Score     *float64 // from the verdict model; nil when not_applicable or unscored
	FollowUps []string // conditional question ids to ask now, in order
	SkipTo    string   // if set, jump the main sequence to this question id
}

// Assess evaluates a question's assessment rules (then thresholds) against an
// answer, returning the first matching verdict. An empty verdict means the
// question is not scored on its own (e.g. free_text context questions).
func (q *Question) Assess(a Answer) (string, error) {
	if q.Assessment == nil {
		return "", nil
	}
	ctx := a.evalContext("")
	for _, r := range append(append([]Rule{}, q.Assessment.Rules...), q.Assessment.Thresholds...) {
		ok, err := Eval(r.When, ctx)
		if err != nil {
			return "", err
		}
		if ok {
			return r.Verdict, nil
		}
	}
	return "", nil
}

// Step assesses an answer and evaluates the question's follow-ups. Follow-ups
// see the verdict just assessed, so a rule like `verdict == 'partial'` works.
func (t *Template) Step(questionID string, a Answer) (StepResult, error) {
	q := t.QuestionByID(questionID)
	if q == nil {
		return StepResult{}, errUnknownQuestion(questionID)
	}

	verdict, err := q.Assess(a)
	if err != nil {
		return StepResult{}, err
	}

	res := StepResult{Verdict: verdict, Score: t.ScoreFor(verdict)}

	ctx := a.evalContext(verdict)
	for _, f := range q.FollowUps {
		ok, err := Eval(f.When, ctx)
		if err != nil {
			return StepResult{}, err
		}
		if !ok {
			continue
		}
		if f.SkipTo != "" {
			res.SkipTo = f.SkipTo
		}
		if f.AskID != "" {
			res.FollowUps = append(res.FollowUps, f.AskID)
		}
	}
	return res, nil
}

// ScoreFor maps a verdict to its numeric score via the verdict model. Returns
// nil when the verdict is excluded (not_applicable) or there is no model.
func (t *Template) ScoreFor(verdict string) *float64 {
	if verdict == "" || t.VerdictModel == nil {
		return nil
	}
	if s, ok := t.VerdictModel.ScoreOf[verdict]; ok {
		return s
	}
	return nil
}

// MainSequence returns the non-conditional questions in ask order — the spine
// the caller walks, inserting follow-ups as they fire.
func (t *Template) MainSequence() []*Question {
	var out []*Question
	for _, q := range t.AllQuestions() {
		if !q.Conditional {
			out = append(out, q)
		}
	}
	return out
}

type unknownQuestionError string

func (e unknownQuestionError) Error() string { return "fwqa: unknown question " + string(e) }

func errUnknownQuestion(id string) error { return unknownQuestionError(id) }
