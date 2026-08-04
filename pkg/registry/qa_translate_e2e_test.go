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
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"go.meizon.cloud/registry/pkg/llm"
	"go.meizon.cloud/registry/pkg/registry"
)

// qaTranslateFake answers two contracts: QA generation (a branching question per
// requirement) and translation (echo refs, prefix translated text with "fr:").
func qaTranslateFake() *llm.Fake {
	return &llm.Fake{Route: func(req llm.Request) string {
		switch {
		case strings.Contains(req.System, "compliance audit expert"):
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
				b.WriteString(`{"text":"Remediation plan for ` + ref + `?","type":"free_text","conditional":true}`)
				b.WriteString(`]}`)
			}
			b.WriteString(`]}`)
			return b.String()

		case strings.Contains(req.System, "You translate compliance framework text"):
			i := strings.Index(req.Prompt, "{")
			var in struct {
				Nodes []struct{ Ref, Name, Description string } `json:"nodes"`
			}
			_ = json.Unmarshal([]byte(req.Prompt[i:]), &in)
			var b strings.Builder
			b.WriteString(`{"nodes":[`)
			for k, n := range in.Nodes {
				if k > 0 {
					b.WriteString(",")
				}
				desc := ""
				if n.Description != "" {
					desc = "fr:" + n.Description
				}
				ref, _ := json.Marshal(n.Ref)
				name, _ := json.Marshal("fr:" + n.Name)
				dj, _ := json.Marshal(desc)
				fmt.Fprintf(&b, `{"ref":%s,"name":%s,"description":%s}`, ref, name, dj)
			}
			b.WriteString(`]}`)
			return b.String()
		}
		return ""
	}}
}

// TestQATemplateTranslatedWithFramework: translating a framework also translates
// its audit template into the same language — human text is translated, structure
// and machine fields (refs, verdicts, when-expressions, follow-up wiring) are
// preserved, and the reloaded translation validates.
func TestQATemplateTranslatedWithFramework(t *testing.T) {
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

	created, err := svc.CreateFramework(ctx, super, registry.CreateFrameworkRequest{
		ReferenceID: "iso-27001", Name: "ISO 27001", Region: "EU", License: "public-domain",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := svc.AddCategory(ctx, super, created.VersionID, "A.5", "Organizational", "", ""); err != nil {
		t.Fatalf("add category: %v", err)
	}
	if err := svc.AddRequirement(ctx, super, created.VersionID, "A.5", "A.5.1", "", "Policies", "Define policies.", "", "", "manual"); err != nil {
		t.Fatalf("add requirement: %v", err)
	}

	// Generate the canonical audit template.
	jobID, err := svc.StartQATemplateJob(ctx, super, "iso-27001")
	if err != nil {
		t.Fatalf("qa job: %v", err)
	}
	if st := waitForJob(t, svc, jobID); st.Status != "done" {
		t.Fatalf("qa job %s: %s", st.Status, st.Error)
	}
	canonical, err := svc.QATemplateFor(ctx, "iso-27001")
	if err != nil {
		t.Fatalf("load canonical: %v", err)
	}

	// Translate the framework into French — the QA template must follow.
	tjob, err := svc.StartTranslateJob(ctx, super, "iso-27001", "fr")
	if err != nil {
		t.Fatalf("translate job: %v", err)
	}
	if st := waitForJob(t, svc, tjob); st.Status != "done" {
		t.Fatalf("translate job %s: %s", st.Status, st.Error)
	}

	fr, err := svc.QATemplateViewFor(ctx, "iso-27001", "fr")
	if err != nil {
		t.Fatalf("load fr template: %v", err)
	}
	if fr.Language != "fr" {
		t.Fatalf("expected fr template, got language %q (fell back to canonical?)", fr.Language)
	}
	if err := fr.Template.Validate(); err != nil {
		t.Fatalf("translated template must validate: %v", err)
	}

	// Same structure: question count and requirement refs preserved.
	if len(fr.AllQuestions()) != len(canonical.AllQuestions()) {
		t.Fatalf("question count changed: canonical %d, fr %d", len(canonical.AllQuestions()), len(fr.AllQuestions()))
	}
	for _, q := range fr.AllQuestions() {
		// Human text is translated...
		if !strings.HasPrefix(q.Text, "fr:") {
			t.Fatalf("question %s text not translated: %q", q.ID, q.Text)
		}
		// ...but machine fields are not.
		if q.Assessment != nil {
			for _, r := range q.Assessment.Rules {
				if strings.HasPrefix(r.When, "fr:") || strings.HasPrefix(r.Verdict, "fr:") {
					t.Fatalf("assessment rule was translated: when=%q verdict=%q", r.When, r.Verdict)
				}
			}
		}
	}

	// The structure view also switches into the translated language.
	tree, err := svc.StructureOf(ctx, created.VersionID, "fr")
	if err != nil {
		t.Fatalf("structure fr: %v", err)
	}
	var sawReq bool
	for _, c := range tree {
		if c.Code != "A.5" {
			continue
		}
		if !strings.HasPrefix(c.Name, "fr:") {
			t.Fatalf("category name not translated: %q", c.Name)
		}
		for _, r := range c.Requirements {
			if r.Code == "A.5.1" {
				sawReq = true
				if !strings.HasPrefix(r.Title, "fr:") {
					t.Fatalf("requirement title not translated: %q", r.Title)
				}
			}
		}
	}
	if !sawReq {
		t.Fatal("A.5.1 not present in the fr structure view")
	}

	// A language that was never translated falls back to canonical.
	de, err := svc.QATemplateViewFor(ctx, "iso-27001", "de")
	if err != nil {
		t.Fatalf("load de: %v", err)
	}
	if de.Language != "" {
		t.Fatalf("untranslated language should fall back to canonical, got %q", de.Language)
	}
}
