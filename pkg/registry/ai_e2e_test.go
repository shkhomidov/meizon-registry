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

// TestAIGenerateAcceptPipeline covers the human-in-the-loop flow with the fake
// provider: configure settings → generate proposals (recorded, not applied) →
// accept an edited subset → rows land with origin=ai; the copyright guardrail
// reaches the system prompt for proprietary frameworks.
func TestAIGenerateAcceptPipeline(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	fake := &llm.Fake{Responses: []string{
		`{"categories":[{"code":"G1","name":"Govern"},{"code":"G2","name":"Protect"},{"code":"BAD","name":"Discard me"}]}`,
	}}
	svc.SetLLMFactory(func(cfg llm.Config) (llm.Client, error) { return fake, nil })

	if _, err := svc.BootstrapSuperAdmin(ctx, req(superAdminEmail, "Root")); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	superID := mustID(t, svc, superAdminEmail)

	// Superadmin configures the provider in settings (stored encrypted).
	if err := svc.SetLLMSettings(ctx, superID, registry.SetLLMSettingsRequest{
		Provider: "openai", APIKey: "sk-test", Model: "gpt-4o",
	}); err != nil {
		t.Fatalf("set settings: %v", err)
	}
	view, err := svc.GetLLMSettings(ctx)
	if err != nil || !view.Configured || view.Provider != "openai" {
		t.Fatalf("settings view wrong: %+v err=%v", view, err)
	}

	// Non-superadmin cannot change settings.
	mustCreateUser(t, svc, superID, "auditor@meizon.test", "Auditor", "auditor", []string{"GLOBAL"})
	auditorID := mustID(t, svc, "auditor@meizon.test")
	if err := svc.SetLLMSettings(ctx, auditorID, registry.SetLLMSettingsRequest{Provider: "gemini", APIKey: "x"}); err == nil {
		t.Fatal("auditor must not be able to change LLM settings")
	}

	// Auditor drafts a proprietary framework and generates category proposals.
	created, err := svc.CreateFramework(ctx, auditorID, registry.CreateFrameworkRequest{
		ReferenceID: "acme-std", Name: "ACME Standard", Region: "GLOBAL", License: "proprietary",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	proposals, err := svc.GenerateProposals(ctx, auditorID, created.VersionID, registry.AIStepCategories, "security standard", "")
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(proposals.Categories) != 3 || proposals.GenerationID.String() == "" {
		t.Fatalf("unexpected proposals: %+v", proposals)
	}

	// Copyright guardrail reached the system prompt.
	if len(fake.Calls) == 0 || !strings.Contains(fake.Calls[0].System, "NEVER reproduce verbatim") {
		t.Fatal("proprietary guardrail missing from system prompt")
	}

	// Nothing applied yet.
	tree, _ := svc.StructureOf(ctx, created.VersionID, "")
	if len(tree) != 0 {
		t.Fatalf("proposals must not auto-apply, tree has %d categories", len(tree))
	}

	// Auditor accepts two (one edited) and discards the third.
	edited := proposals.Categories[1]
	edited.Name = "Protect (edited by auditor)"
	applied, err := svc.AcceptProposals(ctx, auditorID, created.VersionID, registry.AIAcceptRequest{
		GenerationID: proposals.GenerationID,
		Step:         registry.AIStepCategories,
		Categories:   []registry.ProposedCategory{proposals.Categories[0], edited},
	})
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	if applied != 2 {
		t.Fatalf("expected 2 applied, got %d", applied)
	}

	tree, err = svc.StructureOf(ctx, created.VersionID, "")
	if err != nil || len(tree) != 2 {
		t.Fatalf("expected 2 categories after accept, got %d (err=%v)", len(tree), err)
	}
	if tree[1].Name != "Protect (edited by auditor)" {
		t.Fatalf("edited proposal not honored: %+v", tree[1])
	}
}
