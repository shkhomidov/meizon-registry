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
	"testing"
	"time"

	"go.meizon.cloud/registry/pkg/llm"
	"go.meizon.cloud/registry/pkg/registry"
)

const (
	stageIdentify = `{"id":"acme-std","name":"ACME Standard","version":"1.0","category":"cybersecurity","regions":["GLOBAL"],"issuingBody":"ACME"}`
	stageExtract  = `{"categories":[{"ref":"AC","name":"Access Control"}],"requirements":[{"ref":"AC-1","category":"AC","name":"Define access model","description":"least privilege","sourceExcerpt":"Define an access control model"}]}`
	stageControls = `{"controls":[{"ref":"access-policy","name":"Access control policy","description":"A written policy.","category":"Policy"}],"links":[{"requirement":"AC-1","controls":["access-policy"]}]}`
)

// waitForJob polls a generation job until it leaves the running state.
func waitForJob(t *testing.T, svc *registry.Service, jobID string) registry.IngestStatus {
	t.Helper()
	ctx := context.Background()
	for i := 0; i < 200; i++ {
		st, err := svc.IngestJobStatus(ctx, jobID)
		if err != nil {
			t.Fatalf("status: %v", err)
		}
		if st.Status != "running" {
			return st
		}
		time.Sleep(15 * time.Millisecond)
	}
	t.Fatal("job did not finish in time")
	return registry.IngestStatus{}
}

// TestStagedGenerationJob runs the async pipeline (identify → extract → merge →
// validate → QA) end to end and checks the assembled proposal + provenance.
func TestStagedGenerationJob(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	fake := &llm.Fake{Responses: []string{stageIdentify, stageExtract, stageControls, "Looks complete."}}
	svc.SetLLMFactory(func(cfg llm.Config) (llm.Client, error) { return fake, nil })

	if _, err := svc.BootstrapSuperAdmin(ctx, req(superAdminEmail, "Root")); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	superID := mustID(t, svc, superAdminEmail)
	if err := svc.SetLLMSettings(ctx, superID, registry.SetLLMSettingsRequest{Provider: "openai", APIKey: "sk-test", Model: "gpt-4o"}); err != nil {
		t.Fatalf("settings: %v", err)
	}
	mustCreateUser(t, svc, superID, "auditor@meizon.test", "Auditor", "auditor", []string{"GLOBAL"})
	auditorID := mustID(t, svc, "auditor@meizon.test")

	jobID, err := svc.StartGenerateJob(ctx, auditorID, registry.DocInput{Text: "Access Control. Restrict access. Define an access control model."}, "")
	if err != nil {
		t.Fatalf("start job: %v", err)
	}

	st := waitForJob(t, svc, jobID)
	if st.Status != "done" {
		t.Fatalf("job status = %q, error=%q", st.Status, st.Error)
	}
	if st.Result == nil || st.Result.Document == nil {
		t.Fatal("finished job carries no result")
	}
	doc := st.Result.Document
	if doc.ID != "acme-std" || len(doc.Categories) != 1 || len(doc.Requirements) != 1 {
		t.Fatalf("unexpected assembled doc: id=%q categories=%d requirements=%d", doc.ID, len(doc.Categories), len(doc.Requirements))
	}
	if len(doc.Controls) != 1 || len(doc.Requirements[0].Controls) != 1 {
		t.Fatalf("controls step did not link: controls=%d links=%v", len(doc.Controls), doc.Requirements[0].Controls)
	}
	if st.Result.Provenance["AC-1"] != "Define an access control model" {
		t.Errorf("provenance not carried: %v", st.Result.Provenance)
	}
	if st.Result.Note != "" {
		t.Errorf("Looks complete. should map to an empty note, got %q", st.Result.Note)
	}

	// The reviewed proposal accepts as a DRAFT with origin=ai.
	created, err := svc.AcceptGeneratedFlat(ctx, auditorID, doc, "")
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	tree, _ := svc.StructureOf(ctx, created.VersionID)
	if len(tree) != 1 || tree[0].Code != "AC" {
		t.Fatalf("flat category did not convert: %+v", tree)
	}
	if tree[0].Requirements[0].Origin != "ai" {
		t.Errorf("accepted structure not origin=ai: %+v", tree)
	}
}

// TestStagedGenerationJobError surfaces a step failure as a job error.
func TestStagedGenerationJobError(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	fake := &llm.Fake{Responses: []string{"not json at all"}}
	svc.SetLLMFactory(func(cfg llm.Config) (llm.Client, error) { return fake, nil })

	if _, err := svc.BootstrapSuperAdmin(ctx, req(superAdminEmail, "Root")); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	superID := mustID(t, svc, superAdminEmail)
	if err := svc.SetLLMSettings(ctx, superID, registry.SetLLMSettingsRequest{Provider: "openai", APIKey: "sk-test", Model: "gpt-4o"}); err != nil {
		t.Fatalf("settings: %v", err)
	}

	jobID, err := svc.StartGenerateJob(ctx, superID, registry.DocInput{Text: "some document text"}, "")
	if err != nil {
		t.Fatalf("start job: %v", err)
	}
	st := waitForJob(t, svc, jobID)
	if st.Status != "error" || st.Error == "" {
		t.Fatalf("expected job error, got status=%q error=%q", st.Status, st.Error)
	}
}
