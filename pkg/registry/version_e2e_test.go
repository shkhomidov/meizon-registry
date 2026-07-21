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

	"go.meizon.cloud/registry/pkg/llm"
	"go.meizon.cloud/registry/pkg/registry"
)

// TestNextVersionJob runs the staged pipeline for a revised document and checks
// the diff against the framework's current version.
func TestNextVersionJob(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	// Two full runs: v1 then v2 (identify, extract, controls, qa each).
	fake := &llm.Fake{Responses: []string{
		stageIdentify, stageExtract, stageControls, "Looks complete.",
		stageIdentify, stageExtract, stageControls, "Looks complete.",
	}}
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

	// v1 via the pipeline.
	jobID, err := svc.StartGenerateJob(ctx, auditorID, registry.DocInput{Text: "Access Control. Define an access control model."}, "")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	st := waitForJob(t, svc, jobID)
	if st.Status != "done" {
		t.Fatalf("v1 job %s: %s", st.Status, st.Error)
	}
	if _, err := svc.AcceptGeneratedFlat(ctx, auditorID, st.Result.Document, ""); err != nil {
		t.Fatalf("accept v1: %v", err)
	}

	// v2 from a revised document — same content, so everything is unchanged.
	jobID2, err := svc.StartNextVersionJob(ctx, auditorID, "acme-std", registry.DocInput{Text: "revised text"}, "", "2.0")
	if err != nil {
		t.Fatalf("start next version: %v", err)
	}
	st2 := waitForJob(t, svc, jobID2)
	if st2.Status != "done" {
		t.Fatalf("v2 job %s: %s", st2.Status, st2.Error)
	}
	if st2.Baseline != "1.0" {
		t.Errorf("baseline = %q, want 1.0", st2.Baseline)
	}
	if st2.Diff["req:AC-1"] != registry.ChangeUnchanged {
		t.Errorf("AC-1 should be unchanged, got %q", st2.Diff["req:AC-1"])
	}

	// Duplicate-version guard.
	if _, err := svc.StartNextVersionJob(ctx, auditorID, "acme-std", registry.DocInput{Text: "x"}, "", "1.0"); err == nil {
		t.Error("generating an already-existing version must fail")
	}
}
