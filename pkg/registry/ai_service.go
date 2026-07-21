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

package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"go.gearno.de/kit/pg"
	"go.meizon.cloud/registry/pkg/coredata"
	"go.meizon.cloud/registry/pkg/fwschema"
	"go.meizon.cloud/registry/pkg/gid"
	"go.meizon.cloud/registry/pkg/iam"
	"go.meizon.cloud/registry/pkg/llm"
)

// AI authoring steps.
const (
	AIStepCategories   = "categories"
	AIStepRequirements = "requirements"
	AIStepMappings     = "mappings"
)

const maxProposalsPerStep = 50

// Proposal shapes — what the model must return and what the auditor reviews.
type (
	ProposedCategory struct {
		Code        string `json:"code"`
		Name        string `json:"name"`
		Description string `json:"description,omitempty"`
	}
	ProposedRequirement struct {
		Code        string `json:"code"`
		Number      string `json:"number,omitempty"`
		Title       string `json:"title"`
		Description string `json:"description,omitempty"`
		Guidance    string `json:"guidance,omitempty"`
	}
	ProposedMapping struct {
		ItemCode  string `json:"itemCode"`
		Relation  string `json:"relation"`
		Framework string `json:"framework"`
		Version   string `json:"version,omitempty"`
		Item      string `json:"item"`
		Notes     string `json:"notes,omitempty"`
	}
)

// AIProposals is a generation result awaiting the auditor's decision.
type AIProposals struct {
	GenerationID gid.GID               `json:"generationId"`
	Step         string                `json:"step"`
	Categories   []ProposedCategory    `json:"categories,omitempty"`
	Requirements []ProposedRequirement `json:"requirements,omitempty"`
	Mappings     []ProposedMapping     `json:"mappings,omitempty"`
}

// AIStatus tells the console whether assist is available.
type AIStatus struct {
	Configured bool   `json:"configured"`
	Provider   string `json:"provider,omitempty"`
	Model      string `json:"model,omitempty"`
}

// GetAIStatus reports whether an LLM is configured (any signed-in user).
func (s *Service) GetAIStatus(ctx context.Context) (AIStatus, error) {
	view, err := s.GetLLMSettings(ctx)
	if err != nil {
		return AIStatus{}, err
	}
	return AIStatus{Configured: view.Configured, Provider: view.Provider, Model: view.Model}, nil
}

// GenerateProposals asks the configured LLM for step proposals. Nothing is
// written to the draft; the exchange is recorded in ai_generations and the
// proposals are returned for human review.
func (s *Service) GenerateProposals(ctx context.Context, actorID, versionID gid.GID, step, brief, parentCode string) (AIProposals, error) {
	var out AIProposals
	err := s.db.WithConn(ctx, func(ctx context.Context, conn pg.Querier) error {
		version, framework, err := s.requireDraft(ctx, conn, actorID, versionID, iam.ActionControlCreate)
		if err != nil {
			return err
		}

		client, setting, err := s.llmClient(ctx, conn)
		if err != nil {
			return err
		}

		prompt, err := s.buildPrompt(ctx, conn, framework, version, step, brief, parentCode)
		if err != nil {
			return err
		}

		resp, err := client.Generate(ctx, llm.Request{
			System:    systemPrompt(framework),
			Prompt:    prompt,
			MaxTokens: setting.MaxTokens,
		})
		if err != nil {
			return fmt.Errorf("llm generation failed: %w", err)
		}

		proposals, perr := parseProposals(step, resp.Text)
		gen := coredata.AIGeneration{
			ID:                 gid.New(s.cfg.PlatformTenant, coredata.AIGenerationEntityType),
			FrameworkVersionID: version.ID,
			Step:               step,
			Provider:           client.Provider(),
			Model:              resp.Model,
			Prompt:             prompt,
			RawOutput:          resp.Text,
			Status:             "proposed",
			CreatedBy:          actorID,
			CreatedAt:          time.Now(),
		}
		if err := gen.Insert(ctx, conn, s.platformScope()); err != nil {
			return err
		}
		if perr != nil {
			return fmt.Errorf("the model returned invalid proposals: %w", perr)
		}

		proposals.GenerationID = gen.ID
		proposals.Step = step
		out = proposals

		return s.recordAudit(ctx, conn, s.platformScope(), actorID, "ai.generate", version.ID.String(),
			fmt.Sprintf("step=%s provider=%s model=%s", step, client.Provider(), resp.Model))
	})
	return out, err
}

// AIAcceptRequest carries the (possibly human-edited) proposals to apply.
type AIAcceptRequest struct {
	GenerationID    gid.GID
	Step            string
	CategoryCode    string // parent for requirements
	RequirementCode string // parent for mappings
	Categories      []ProposedCategory
	Requirements    []ProposedRequirement
	Mappings        []ProposedMapping
}

// AcceptProposals applies accepted proposals to the draft with origin=ai. Each
// element goes through the same services as manual authoring, so validation,
// RBAC and DRAFT rules hold. Returns the number applied.
func (s *Service) AcceptProposals(ctx context.Context, actorID, versionID gid.GID, req AIAcceptRequest) (int, error) {
	applied := 0

	switch req.Step {
	case AIStepCategories:
		for _, c := range req.Categories {
			if err := s.AddCategory(ctx, actorID, versionID, c.Code, c.Name, c.Description, "ai"); err != nil {
				return applied, fmt.Errorf("category %q: %w", c.Code, err)
			}
			applied++
		}
	case AIStepRequirements:
		for _, r := range req.Requirements {
			if err := s.AddRequirement(ctx, actorID, versionID, req.CategoryCode, r.Code, r.Number, r.Title, r.Description, "", r.Guidance, "ai"); err != nil {
				return applied, fmt.Errorf("requirement %q: %w", r.Code, err)
			}
			applied++
		}
	case AIStepMappings:
		for _, m := range req.Mappings {
			if _, err := s.AddItemMapping(ctx, actorID, AddItemMappingRequest{
				VersionID: versionID, ItemCode: m.ItemCode, Relation: m.Relation,
				TargetFramework: m.Framework, TargetVersion: m.Version,
				TargetItem: m.Item, Notes: m.Notes,
			}); err != nil {
				return applied, fmt.Errorf("mapping %s→%s: %w", m.ItemCode, m.Item, err)
			}
			applied++
		}
	default:
		return 0, fmt.Errorf("unknown step %q", req.Step)
	}

	// Record the decision on the generation and audit it.
	err := s.db.WithConn(ctx, func(ctx context.Context, conn pg.Querier) error {
		status := "accepted"
		if applied == 0 {
			status = "rejected"
		}
		gen := coredata.AIGeneration{ID: req.GenerationID}
		if err := gen.UpdateDecision(ctx, conn, s.platformScope(), status, applied); err != nil {
			return err
		}
		return s.recordAudit(ctx, conn, s.platformScope(), actorID, "ai.accept", versionID.String(),
			fmt.Sprintf("step=%s accepted=%d", req.Step, applied))
	})
	return applied, err
}

// systemPrompt frames the assistant, including the copyright guardrail for
// proprietary standards.
func systemPrompt(framework coredata.Framework) string {
	base := "You are a compliance-framework authoring assistant inside a framework registry. " +
		"You draft PROPOSALS that a human auditor will review, edit and approve — be precise, concise and use the framework's native numbering conventions. " +
		"Respond with ONLY valid JSON matching the requested schema. No markdown fences, no commentary."
	if framework.License == string(fwschema.LicenseProprietary) {
		base += " IMPORTANT: this framework is proprietary/copyrighted. Paraphrase requirement intents in your own words; NEVER reproduce verbatim text from the standard."
	}
	return base
}

// buildPrompt assembles the grounded per-step user prompt.
func (s *Service) buildPrompt(ctx context.Context, conn pg.Querier, framework coredata.Framework, version coredata.FrameworkVersion, step, brief, parentCode string) (string, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "Framework: %s (%s) version %s, region %s, license %s.\n",
		framework.Name, framework.ReferenceID, version.Version, framework.Region, framework.License)
	if brief != "" {
		fmt.Fprintf(&b, "Auditor brief: %s\n", brief)
	}

	scope := s.platformScope()

	switch step {
	case AIStepCategories:
		var existing coredata.RequirementCategories
		if err := existing.LoadAllByVersion(ctx, conn, scope, version.ID); err != nil {
			return "", err
		}
		codes := make([]string, 0, len(existing))
		for _, c := range existing {
			codes = append(codes, c.Code)
		}
		fmt.Fprintf(&b, "Existing category codes (do not repeat): %s\n", strings.Join(codes, ", "))
		fmt.Fprintf(&b, "Propose the top-level categories (goals/domains) for this framework.\n")
		fmt.Fprintf(&b, `Return JSON: {"categories":[{"code":"G1","name":"...","description":"..."}]} — at most %d.`, maxProposalsPerStep)

	case AIStepRequirements:
		if parentCode == "" {
			return "", fmt.Errorf("a category code is required for the requirements step")
		}
		fmt.Fprintf(&b, "Propose the numbered requirements for category %q.\n", parentCode)
		fmt.Fprintf(&b, `Return JSON: {"requirements":[{"code":"Requirement 7","number":"7","title":"...","description":"..."}]} — at most %d.`, maxProposalsPerStep)

	case AIStepMappings:
		catalog, err := s.publishedCatalogPrompt(ctx, conn, framework.ID)
		if err != nil {
			return "", err
		}
		var requirements coredata.Requirements
		if err := requirements.LoadAllByVersion(ctx, conn, scope, version.ID); err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "Source requirements:\n")
		for _, r := range requirements {
			fmt.Fprintf(&b, "- %s: %s\n", r.Code, r.Title)
		}
		fmt.Fprintf(&b, "Published frameworks available as mapping targets:\n%s", catalog)
		fmt.Fprintf(&b, "Propose cross-mappings from source requirements to target requirements. relation ∈ equivalent|partial|superset|subset. Prefer targets from the catalog above; you may also propose stubs to well-known frameworks not listed.\n")
		fmt.Fprintf(&b, `Return JSON: {"mappings":[{"itemCode":"7.2.1","relation":"partial","framework":"iso-27001","version":"2022","item":"A.5.15","notes":"..."}]} — at most %d.`, maxProposalsPerStep)

	default:
		return "", fmt.Errorf("unknown step %q", step)
	}

	return b.String(), nil
}

// publishedCatalogPrompt renders the requirement catalogs of published
// frameworks (for grounding mapping proposals), capped to keep prompts bounded.
func (s *Service) publishedCatalogPrompt(ctx context.Context, conn pg.Querier, excludeFrameworkID gid.GID) (string, error) {
	scope := s.platformScope()
	var frameworks coredata.Frameworks
	if err := frameworks.LoadAll(ctx, conn, scope); err != nil {
		return "", err
	}

	var b strings.Builder
	fwCount := 0
	for _, f := range frameworks {
		if f.ID == excludeFrameworkID || fwCount >= 10 {
			continue
		}
		var latest coredata.FrameworkVersion
		if err := latest.LoadLatestPublished(ctx, conn, f.ID); err != nil {
			continue // unpublished — skip
		}
		var requirements coredata.Requirements
		if err := requirements.LoadAllByVersion(ctx, conn, scope, latest.ID); err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "* %s @ %s:\n", f.ReferenceID, latest.Version)
		for i, it := range requirements {
			if i >= 100 {
				fmt.Fprintf(&b, "  … (truncated)\n")
				break
			}
			fmt.Fprintf(&b, "  - %s: %s\n", it.Code, it.Title)
		}
		fwCount++
	}
	if b.Len() == 0 {
		b.WriteString("(none published yet — propose stubs by well-known framework codes)\n")
	}
	return b.String(), nil
}

// parseProposals decodes and sanity-checks the model output for a step.
func parseProposals(step, text string) (AIProposals, error) {
	cleaned := strings.TrimSpace(text)
	cleaned = strings.TrimPrefix(cleaned, "```json")
	cleaned = strings.TrimPrefix(cleaned, "```")
	cleaned = strings.TrimSuffix(cleaned, "```")
	cleaned = strings.TrimSpace(cleaned)

	var out AIProposals
	if err := json.Unmarshal([]byte(cleaned), &out); err != nil {
		return out, fmt.Errorf("invalid JSON: %w", err)
	}

	switch step {
	case AIStepCategories:
		if len(out.Categories) == 0 {
			return out, fmt.Errorf("no categories proposed")
		}
		out.Categories = capSlice(out.Categories)
		for _, c := range out.Categories {
			if c.Code == "" || c.Name == "" {
				return out, fmt.Errorf("category missing code or name")
			}
		}
	case AIStepRequirements:
		if len(out.Requirements) == 0 {
			return out, fmt.Errorf("no requirements proposed")
		}
		out.Requirements = capSlice(out.Requirements)
		for _, r := range out.Requirements {
			if r.Code == "" || r.Title == "" {
				return out, fmt.Errorf("requirement missing code or title")
			}
		}
	case AIStepMappings:
		if len(out.Mappings) == 0 {
			return out, fmt.Errorf("no mappings proposed")
		}
		out.Mappings = capSlice(out.Mappings)
		for _, m := range out.Mappings {
			if m.ItemCode == "" || m.Framework == "" || m.Item == "" {
				return out, fmt.Errorf("mapping missing itemCode, framework or item")
			}
			if !fwschema.MappingRelation(m.Relation).IsValid() {
				return out, fmt.Errorf("mapping has invalid relation %q", m.Relation)
			}
		}
	default:
		return out, fmt.Errorf("unknown step %q", step)
	}

	return out, nil
}

func capSlice[T any](in []T) []T {
	if len(in) > maxProposalsPerStep {
		return in[:maxProposalsPerStep]
	}
	return in
}
