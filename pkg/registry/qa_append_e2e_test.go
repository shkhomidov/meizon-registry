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

	"go.meizon.cloud/registry/pkg/gid"
	"go.meizon.cloud/registry/pkg/llm"
	"go.meizon.cloud/registry/pkg/registry"
)

// qaFake returns a mock LLM that answers the QA contract. Each requirement gets a
// yes_no_evidence anchor that branches (on "no") to a conditional free_text
// follow-up — enough to exercise follow-up id wiring through persistence.
func qaFake() *llm.Fake {
	return &llm.Fake{Route: func(req llm.Request) string {
		if !strings.Contains(req.System, "compliance audit expert") {
			return ""
		}
		var refs []string
		for _, line := range strings.Split(req.Prompt, "\n") {
			if s := strings.TrimSpace(line); strings.HasPrefix(s, "- ref:") {
				refs = append(refs, strings.TrimSpace(strings.TrimPrefix(s, "- ref:")))
			}
		}
		var b strings.Builder
		b.WriteString(`{"requirements":[`)
		for i, ref := range refs {
			if i > 0 {
				b.WriteString(",")
			}
			b.WriteString(`{"ref":"` + ref + `","questions":[`)
			b.WriteString(`{"text":"Is ` + ref + ` implemented?","type":"yes_no_evidence","weight":2,` +
				`"assessment":{"rules":[{"when":"answer == 'yes'","verdict":"compliant"},{"when":"answer == 'no'","verdict":"non_compliant"}]},` +
				`"followUps":[{"when":"answer == 'no'","ask":2}]},`)
			b.WriteString(`{"text":"What is the remediation plan for ` + ref + `?","type":"free_text","conditional":true}`)
			b.WriteString(`]}`)
		}
		b.WriteString(`]}`)
		return b.String()
	}}
}

// qaSetup boots a service with the QA mock LLM and a draft framework carrying one
// category and one requirement (A.5.1). Returns the service, the actor, and the
// framework ref.
func qaSetup(t *testing.T) (*registry.Service, gid.GID, gid.GID) {
	t.Helper()
	svc, _ := newTestService(t)
	ctx := context.Background()

	fake := qaFake()
	svc.SetLLMFactory(func(cfg llm.Config) (llm.Client, error) { return fake, nil })

	if _, err := svc.BootstrapSuperAdmin(ctx, req(superAdminEmail, "Root")); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	super := mustID(t, svc, superAdminEmail)
	if err := svc.SetLLMSettings(ctx, super, registry.SetLLMSettingsRequest{Provider: "openai", APIKey: "sk-test", Model: "gpt-4o"}); err != nil {
		t.Fatalf("llm settings: %v", err)
	}

	created, err := svc.CreateFramework(ctx, super, registry.CreateFrameworkRequest{
		ReferenceID: "iso-27001", Name: "ISO 27001", Region: "EU", License: "public-domain",
	})
	if err != nil {
		t.Fatalf("create framework: %v", err)
	}
	if err := svc.AddCategory(ctx, super, created.VersionID, "A.5", "Organizational", "", ""); err != nil {
		t.Fatalf("add category: %v", err)
	}
	if err := svc.AddRequirement(ctx, super, created.VersionID, "A.5", "A.5.1", "", "Policies", "Define policies.", "", "", "manual"); err != nil {
		t.Fatalf("add requirement: %v", err)
	}
	return svc, super, created.VersionID
}

// TestQAFollowUpsSurvivePersistence is the regression guard for the id round-trip:
// a template generated WITH follow-ups must still validate after being stored and
// reloaded, with the follow-up askId pointing at the reloaded (gid) question id —
// not the transient schema id it was authored with.
func TestQAFollowUpsSurvivePersistence(t *testing.T) {
	svc, super, _ := qaSetup(t)
	ctx := context.Background()

	jobID, err := svc.StartQATemplateJob(ctx, super, "iso-27001")
	if err != nil {
		t.Fatalf("start qa job: %v", err)
	}
	if st := waitForJob(t, svc, jobID); st.Status != "done" {
		t.Fatalf("qa job %s: %s", st.Status, st.Error)
	}

	tpl, err := svc.QATemplateFor(ctx, "iso-27001")
	if err != nil {
		t.Fatalf("load template: %v", err)
	}
	// The reload must validate — a mismatched follow-up target would fail here.
	if err := tpl.Validate(); err != nil {
		t.Fatalf("reloaded template with follow-ups must validate: %v", err)
	}

	ids := map[string]bool{}
	for _, q := range tpl.AllQuestions() {
		ids[q.ID] = true
	}
	var branches int
	for _, q := range tpl.AllQuestions() {
		for _, f := range q.FollowUps {
			branches++
			if f.AskID == "" {
				t.Fatalf("follow-up on %s has no askId", q.ID)
			}
			if strings.HasPrefix(f.AskID, "q-") {
				t.Fatalf("follow-up askId %q is a schema id, not a reloaded question id", f.AskID)
			}
			if !ids[f.AskID] {
				t.Fatalf("follow-up askId %q does not match any question id", f.AskID)
			}
		}
	}
	if branches == 0 {
		t.Fatal("expected at least one follow-up branch in the reloaded template")
	}
}

// TestQAAppendOnManualRequirement: adding a requirement after a template exists
// generates and merges its questions, leaving the existing requirement's
// questions intact.
func TestQAAppendOnManualRequirement(t *testing.T) {
	svc, super, versionID := qaSetup(t)
	ctx := context.Background()

	jobID, err := svc.StartQATemplateJob(ctx, super, "iso-27001")
	if err != nil {
		t.Fatalf("start qa job: %v", err)
	}
	if st := waitForJob(t, svc, jobID); st.Status != "done" {
		t.Fatalf("qa job %s: %s", st.Status, st.Error)
	}

	before, err := svc.QATemplateFor(ctx, "iso-27001")
	if err != nil {
		t.Fatalf("load template: %v", err)
	}
	beforeCount := len(before.AllQuestions())
	if beforeCount == 0 {
		t.Fatal("expected questions for the initial requirement")
	}

	// Add a new requirement manually, then append its questions.
	if err := svc.AddRequirement(ctx, super, versionID, "A.5", "A.5.9", "", "Asset inventory", "Maintain an inventory.", "", "", "manual"); err != nil {
		t.Fatalf("add requirement: %v", err)
	}
	svc.AppendQATemplateQuestions(ctx, super, "iso-27001", "A.5.9")

	after, err := svc.QATemplateFor(ctx, "iso-27001")
	if err != nil {
		t.Fatalf("reload template: %v", err)
	}
	if err := after.Validate(); err != nil {
		t.Fatalf("template after append must validate: %v", err)
	}
	if len(after.AllQuestions()) <= beforeCount {
		t.Fatalf("append should have added questions: before=%d after=%d", beforeCount, len(after.AllQuestions()))
	}

	byRef := map[string]int{}
	for _, q := range after.AllQuestions() {
		byRef[q.RequirementRef]++
	}
	if byRef["A.5.1"] == 0 {
		t.Fatal("the original requirement's questions must be preserved")
	}
	if byRef["A.5.9"] == 0 {
		t.Fatal("the newly added requirement must have questions")
	}

	// Idempotent: a second append for the same requirement changes nothing.
	countAfter := len(after.AllQuestions())
	svc.AppendQATemplateQuestions(ctx, super, "iso-27001", "A.5.9")
	again, err := svc.QATemplateFor(ctx, "iso-27001")
	if err != nil {
		t.Fatalf("reload template: %v", err)
	}
	if len(again.AllQuestions()) != countAfter {
		t.Fatalf("re-appending an already-covered requirement must be a no-op: %d -> %d", countAfter, len(again.AllQuestions()))
	}
}
