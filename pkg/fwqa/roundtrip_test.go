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

import (
	"encoding/json"
	"testing"
)

// specExample is the documented "all question types" template. Decoding and
// validating it is the guard that the code and the published spec agree — a
// field renamed here without updating the doc, or vice versa, fails this test.
const specExample = `{
  "$schema": "meizon-qa-template/v1",
  "id": "iso-27001-2022-audit",
  "framework": { "id": "iso-27001", "version": "2022", "language": "en" },
  "title": "ISO/IEC 27001:2022 Compliance Audit",
  "generatedBy": "ai",
  "scale": {
    "kind": "maturity",
    "levels": [{ "value": 0, "label": "Absent" }, { "value": 5, "label": "Optimized" }]
  },
  "verdictModel": {
    "verdicts": ["compliant", "partial", "non_compliant", "not_applicable"],
    "scoreOf": { "compliant": 1.0, "partial": 0.5, "non_compliant": 0.0, "not_applicable": null },
    "requirementRollup": "weighted_mean",
    "notApplicablePolicy": "exclude"
  },
  "sections": [
    {
      "ref": "A.5", "name": "Organizational controls", "order": 1,
      "questions": [
        {
          "id": "q-A.5.1-1", "order": 1, "requirementRef": "A.5.1", "controlRef": "access-policy",
          "text": "Is there a documented information security policy, formally approved?",
          "type": "yes_no_evidence", "required": true, "weight": 3,
          "expectedEvidence": ["Approved policy document"],
          "assessment": {
            "criteria": "Compliant only if a current, approved policy exists.",
            "rules": [
              { "when": "answer == 'yes' && evidence.count >= 1", "verdict": "compliant" },
              { "when": "answer == 'no'", "verdict": "non_compliant" }
            ]
          },
          "followUps": [
            { "when": "answer == 'no'", "askId": "q-A.5.1-2" },
            { "when": "verdict == 'partial'", "askId": "q-A.5.1-3" }
          ]
        },
        { "id": "q-A.5.1-2", "order": 2, "requirementRef": "A.5.1",
          "text": "Target date and owner?", "type": "free_text", "conditional": true },
        { "id": "q-A.5.1-3", "order": 3, "requirementRef": "A.5.1",
          "text": "How is the policy communicated?", "type": "single_choice", "conditional": true,
          "options": [{ "value": "annual", "label": "Annually" }, { "value": "none", "label": "Not" }],
          "assessment": { "rules": [{ "when": "answer in ['annual']", "verdict": "compliant" }] } },
        { "id": "q-A.5.2-1", "order": 4, "requirementRef": "A.5.2",
          "text": "Rate role maturity.", "type": "scale", "scaleRef": "maturity", "weight": 2,
          "assessment": { "rubric": [{ "level": 3, "descriptor": "Documented and owned." }],
            "rules": [{ "when": "score >= 3", "verdict": "compliant" }] } },
        { "id": "q-A.5.10-1", "order": 5, "requirementRef": "A.5.10",
          "text": "Which topics does the AUP cover?", "type": "multi_choice",
          "options": [{ "value": "email", "label": "Email" }, { "value": "internet", "label": "Internet" },
            { "value": "removable_media", "label": "Removable media" }],
          "assessment": { "rules": [
            { "when": "selected superset ['email','internet','removable_media']", "verdict": "compliant" },
            { "when": "selected.count < 2", "verdict": "non_compliant" } ] } }
      ]
    },
    {
      "ref": "A.8", "name": "Technological controls", "order": 2,
      "questions": [
        { "id": "q-A.8.8-1", "order": 1, "requirementRef": "A.8.8",
          "text": "Systems in scope for vulnerability management?", "type": "yes_no_na",
          "followUps": [{ "when": "answer == 'no' || answer == 'na'", "skipTo": "q-A.8.15-1" }] },
        { "id": "q-A.8.8-2", "order": 2, "requirementRef": "A.8.8",
          "text": "Mean time to remediate critical vulns?", "type": "numeric", "unit": "days",
          "min": 0, "max": 365, "weight": 3,
          "assessment": { "thresholds": [
            { "when": "value <= 7", "verdict": "compliant" },
            { "when": "value <= 30", "verdict": "partial" },
            { "when": "value > 30", "verdict": "non_compliant" } ] } },
        { "id": "q-A.8.15-1", "order": 3, "requirementRef": "A.8.15",
          "text": "Last log review date?", "type": "date", "weight": 2,
          "assessment": { "thresholds": [
            { "when": "ageDays <= 31", "verdict": "compliant" },
            { "when": "ageDays > 92", "verdict": "non_compliant" } ] } },
        { "id": "q-A.8.24-1", "order": 4, "requirementRef": "A.8.24",
          "text": "Confirm cryptographic conformance.", "type": "attestation", "weight": 2,
          "attestation": { "statement": "I confirm crypto use conforms.", "requireSignatory": true },
          "assessment": { "rules": [
            { "when": "attested == true && evidence.count >= 1", "verdict": "compliant" },
            { "when": "attested == false", "verdict": "non_compliant" } ] } }
      ]
    }
  ]
}`

func TestSpecExampleRoundTrips(t *testing.T) {
	var tpl Template
	if err := json.Unmarshal([]byte(specExample), &tpl); err != nil {
		t.Fatalf("the documented spec example does not decode: %v", err)
	}
	if err := tpl.Validate(); err != nil {
		t.Fatalf("the documented spec example does not validate: %v", err)
	}

	// Every documented question type appears and is accepted.
	seen := map[string]bool{}
	for _, q := range tpl.AllQuestions() {
		seen[q.Type] = true
	}
	for _, want := range []string{
		TypeYesNoEvidence, TypeFreeText, TypeSingleChoice, TypeScale,
		TypeMultiChoice, TypeYesNoNA, TypeNumeric, TypeDate, TypeAttestation,
	} {
		if !seen[want] {
			t.Errorf("spec example is missing question type %q", want)
		}
	}

	// Re-marshal and re-validate: the schema is stable through a round trip.
	raw, err := json.Marshal(&tpl)
	if err != nil {
		t.Fatalf("re-marshal: %v", err)
	}
	var again Template
	if err := json.Unmarshal(raw, &again); err != nil {
		t.Fatalf("re-decode: %v", err)
	}
	if err := again.Validate(); err != nil {
		t.Fatalf("round-tripped template does not validate: %v", err)
	}
}
