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

package registry_test

import (
	"context"
	"strings"
	"testing"

	"go.meizon.cloud/registry/pkg/llm"
	"go.meizon.cloud/registry/pkg/registry"
)

// TestQATemplateGeneration walks the full generation path against a real DB with
// a mock LLM: create a draft framework, generate an audit template on the draft,
// and assert it persists as a valid DRAFT template whose questions bind to the
// framework's requirement refs.
func TestQATemplateGeneration(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	// The model returns audit questions for whatever refs are in the prompt.
	// Keyed by content (the qa contract), not call order — the desync lesson.
	fake := &llm.Fake{Route: func(req llm.Request) string {
		if !strings.Contains(req.System, "compliance audit expert") {
			return ""
		}
		// Echo a question for each ref mentioned in the prompt.
		var reqs []string
		for _, line := range strings.Split(req.Prompt, "\n") {
			if s := strings.TrimSpace(line); strings.HasPrefix(s, "- ref:") {
				reqs = append(reqs, strings.TrimSpace(strings.TrimPrefix(s, "- ref:")))
			}
		}
		var b strings.Builder
		b.WriteString(`{"requirements":[`)
		for i, ref := range reqs {
			if i > 0 {
				b.WriteString(",")
			}
			b.WriteString(`{"ref":"` + ref + `","questions":[{"text":"Is ` + ref + ` implemented?","type":"yes_no_evidence","weight":2,"assessment":{"rules":[{"when":"answer == 'yes'","verdict":"compliant"},{"when":"answer == 'no'","verdict":"non_compliant"}]}}]}`)
		}
		b.WriteString(`]}`)
		return b.String()
	}}
	svc.SetLLMFactory(func(cfg llm.Config) (llm.Client, error) { return fake, nil })

	if _, err := svc.BootstrapSuperAdmin(ctx, req(superAdminEmail, "Root")); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	super := mustID(t, svc, superAdminEmail)
	if err := svc.SetLLMSettings(ctx, super, registry.SetLLMSettingsRequest{Provider: "openai", APIKey: "sk-test", Model: "gpt-4o"}); err != nil {
		t.Fatalf("llm settings: %v", err)
	}
	// A draft framework with two requirements under one category.
	created, err := svc.CreateFramework(ctx, super, registry.CreateFrameworkRequest{
		ReferenceID: "iso-27001", Name: "ISO 27001", Region: "EU", License: "public-domain",
	})
	if err != nil {
		t.Fatalf("create framework: %v", err)
	}
	if err := svc.AddCategory(ctx, super, created.VersionID, "A.5", "Organizational", "", ""); err != nil {
		t.Fatalf("add category: %v", err)
	}
	if err := svc.AddRequirement(ctx, super, created.VersionID, "A.5", "A.5.1", "", "Policies", "Define policies.", "", "", ""); err != nil {
		t.Fatalf("add requirement: %v", err)
	}
	if err := svc.AddRequirement(ctx, super, created.VersionID, "A.5", "A.5.2", "", "Roles", "Assign roles.", "", "", ""); err != nil {
		t.Fatalf("add requirement: %v", err)
	}

	// The audit template is authored on the DRAFT, alongside the requirements —
	// it deliberately does NOT require a published version.
	jobID, err := svc.StartQATemplateJob(ctx, super, "iso-27001")
	if err != nil {
		t.Fatalf("start qa job: %v", err)
	}
	st := waitForJob(t, svc, jobID)
	if st.Status != "done" {
		t.Fatalf("qa job %s: %s", st.Status, st.Error)
	}

	// The console view carries the DB id (for edit endpoints) and status. A
	// freshly generated template is a draft, authored beside the requirements.
	view, err := svc.QATemplateViewFor(ctx, "iso-27001", "")
	if err != nil {
		t.Fatalf("load template view: %v", err)
	}
	if view.Status != "draft" {
		t.Fatalf("a freshly generated template must be draft, got %q", view.Status)
	}
	if view.TemplateID == "" {
		t.Fatal("template view must expose the DB template id for edits")
	}

	tpl, err := svc.QATemplateFor(ctx, "iso-27001")
	if err != nil {
		t.Fatalf("load template: %v", err)
	}
	if err := tpl.Validate(); err != nil {
		t.Fatalf("stored template does not validate: %v", err)
	}
	if len(tpl.Sections) != 1 || tpl.Sections[0].Ref != "A.5" {
		t.Fatalf("expected one A.5 section, got %+v", tpl.Sections)
	}
	refs := map[string]bool{}
	for _, q := range tpl.AllQuestions() {
		refs[q.RequirementRef] = true
	}
	if !refs["A.5.1"] || !refs["A.5.2"] {
		t.Fatalf("questions must bind to both requirement refs, got %v", refs)
	}

	// Every question is yes_no_evidence (per the mock) and carries assessment
	// rules — the generated template is scoreable, not just a list of prompts.
	for _, q := range tpl.AllQuestions() {
		if q.Assessment == nil || len(q.Assessment.Rules) == 0 {
			t.Fatalf("question %s has no assessment rules", q.ID)
		}
	}
}
