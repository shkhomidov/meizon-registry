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

// TestQAAndTranslationDistribution: a published framework's audit template and
// its translations are fetchable over the machine distribution API (bearer,
// region-gated), and both surface as change-feed events.
func TestQAAndTranslationDistribution(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	fake := qaTranslateFake()
	svc.SetLLMFactory(func(cfg llm.Config) (llm.Client, error) { return fake, nil })

	if _, err := svc.BootstrapSuperAdmin(ctx, req(superAdminEmail, "Root")); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	super := mustID(t, svc, superAdminEmail)
	if err := svc.SetLLMSettings(ctx, super, registry.SetLLMSettingsRequest{Provider: "openai", APIKey: "sk-test", Model: "gpt-4o"}); err != nil {
		t.Fatalf("llm settings: %v", err)
	}
	mustCreateUser(t, svc, super, "eu-auditor@meizon.test", "EU Auditor", "auditor", []string{"EU"})
	mustCreateUser(t, svc, super, "eu-mod@meizon.test", "EU Mod", "moderator", []string{"EU"})
	auditor := mustID(t, svc, "eu-auditor@meizon.test")
	mod := mustID(t, svc, "eu-mod@meizon.test")
	if err := svc.GenerateSigningKey(ctx, super, "reg-2026"); err != nil {
		t.Fatalf("signing key: %v", err)
	}

	created, err := svc.CreateFramework(ctx, auditor, registry.CreateFrameworkRequest{
		ReferenceID: "iso-27001", Name: "ISO 27001", Region: "EU", License: "public-domain",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := svc.AddCategory(ctx, auditor, created.VersionID, "A.5", "Organizational", "", ""); err != nil {
		t.Fatalf("add category: %v", err)
	}
	if err := svc.AddRequirement(ctx, auditor, created.VersionID, "A.5", "A.5.1", "", "Policies", "Define policies.", "", "", "manual"); err != nil {
		t.Fatalf("add requirement: %v", err)
	}
	addControl(t, svc, auditor, "iso-27001", "A.5.1", "Policies")

	// Generate the audit template and mark it ready (so it is distributable).
	qjob, err := svc.StartQATemplateJob(ctx, auditor, "iso-27001")
	if err != nil {
		t.Fatalf("qa job: %v", err)
	}
	if st := waitForJob(t, svc, qjob); st.Status != "done" {
		t.Fatalf("qa job %s: %s", st.Status, st.Error)
	}
	view, err := svc.QATemplateViewFor(ctx, "iso-27001", "")
	if err != nil {
		t.Fatalf("load qa view: %v", err)
	}
	tplID, err := gid.ParseGID(view.TemplateID)
	if err != nil {
		t.Fatalf("parse template id: %v", err)
	}
	if err := svc.SetQATemplateStatus(ctx, auditor, tplID, "ready"); err != nil {
		t.Fatalf("mark ready: %v", err)
	}

	// Publish (separation of duties: moderator, not the author).
	if err := svc.Submit(ctx, auditor, created.VersionID); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if err := svc.Approve(ctx, mod, created.VersionID, "ok"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if err := svc.Publish(ctx, mod, created.VersionID); err != nil {
		t.Fatalf("publish: %v", err)
	}

	// A distribution token, scoped to EU.
	token, err := svc.IssueToken(ctx, super, "grc-eu", []string{"EU"})
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}
	tc, err := svc.AuthenticateToken(ctx, token)
	if err != nil {
		t.Fatalf("auth token: %v", err)
	}

	// 1. QA template is fetchable over distribution.
	dtpl, err := svc.QATemplateDistribution(ctx, tc, "iso-27001", "latest", "")
	if err != nil {
		t.Fatalf("distribute qa: %v", err)
	}
	if len(dtpl.AllQuestions()) == 0 {
		t.Fatal("distributed QA template has no questions")
	}

	// 2. Translate to French, then fetch translated seed + QA over distribution.
	tjob, err := svc.StartTranslateJob(ctx, super, "iso-27001", "fr")
	if err != nil {
		t.Fatalf("translate job: %v", err)
	}
	if st := waitForJob(t, svc, tjob); st.Status != "done" {
		t.Fatalf("translate job %s: %s", st.Status, st.Error)
	}

	seed, err := svc.Seed(ctx, tc, "iso-27001", "latest", "fr")
	if err != nil {
		t.Fatalf("fr seed: %v", err)
	}
	var sawFr bool
	for _, c := range seed.Controls {
		if strings.HasPrefix(c.Name, "fr:") {
			sawFr = true
		}
	}
	if !sawFr {
		t.Fatalf("fr seed not translated: %+v", seed.Controls)
	}

	frQA, err := svc.QATemplateDistribution(ctx, tc, "iso-27001", "latest", "fr")
	if err != nil {
		t.Fatalf("distribute fr qa: %v", err)
	}
	for _, q := range frQA.AllQuestions() {
		if !strings.HasPrefix(q.Text, "fr:") {
			t.Fatalf("distributed fr QA not translated: %q", q.Text)
		}
	}

	// 3. The change feed announces both artifacts.
	feed, err := svc.Changes(ctx, tc, 0, 100)
	if err != nil {
		t.Fatalf("changes: %v", err)
	}
	kinds := map[string]bool{}
	for _, e := range feed.Events {
		kinds[e.Kind] = true
	}
	for _, want := range []string{"published", "qa_published", "translation_published"} {
		if !kinds[want] {
			t.Fatalf("change feed missing %q; got %v", want, kinds)
		}
	}

	// Discovery: the framework metadata lists the available languages.
	_, versions, err := svc.Versions(ctx, tc, "iso-27001")
	if err != nil {
		t.Fatalf("versions: %v", err)
	}
	if len(versions) == 0 {
		t.Fatal("no published versions")
	}
}
