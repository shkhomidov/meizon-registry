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
	"regexp"
	"strings"

	"go.meizon.cloud/registry/pkg/coredata"
	"go.meizon.cloud/registry/pkg/fwflat"
	"go.meizon.cloud/registry/pkg/llm"
)

// The steps of docs/DOCUMENT-TO-FRAMEWORK-V2-GUIDE.md. Each is one small LLM
// request with a strict JSON contract; every merge is plain code so the result
// is reproducible.

// controlsBatchSize: fewer requirements per request than the guide's ~15, so
// each control proposal is reasoned about in a smaller, more focused context.
const controlsBatchSize = 8

// ---- Step 1: identify ------------------------------------------------------

type identifyResult struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	Category    string   `json:"category"`
	Regions     []string `json:"regions"`
	IssuingBody string   `json:"issuingBody"` // aids identification; dropped at assembly
	Language    string   `json:"language"`    // ISO-639-1 of the source document
}

func identifyUserPrompt(chunk string) string {
	return "Identify this standard:\n\n" + chunk
}

func (s *Service) stepIdentifyFlat(ctx context.Context, client llm.Client, setting *coredata.LLMSetting, chunk string) (identifyResult, error) {
	var last error
	for attempt := 0; attempt < 3; attempt++ {
		p := identifyUserPrompt(chunk)
		if attempt > 0 && last != nil {
			p += fmt.Sprintf("\n\nYour previous output was rejected: %s. Return ONLY corrected JSON.", last)
		}
		resp, err := client.Generate(ctx, llm.Request{
			System:      identifySystemPrompt(setting),
			Prompt:      p,
			MaxTokens:   stepTokens(setting, 2048),
			Temperature: 0, // identity must be read off the page, not invented
		})
		if err != nil {
			return identifyResult{}, fmt.Errorf("llm: %w", err)
		}
		var out identifyResult
		if err := json.Unmarshal([]byte(stripFences(resp.Text)), &out); err != nil {
			last = err
			continue
		}
		if strings.TrimSpace(out.ID) == "" || strings.TrimSpace(out.Name) == "" {
			last = fmt.Errorf("id and name are required")
			continue
		}
		if out.Category != "" && !containsStr(fwflat.FrameworkCategories, out.Category) {
			last = fmt.Errorf("category must be one of %s", strings.Join(fwflat.FrameworkCategories, "|"))
			continue
		}
		if len(out.Regions) == 0 {
			out.Regions = []string{"GLOBAL"}
		}
		return out, nil
	}
	return identifyResult{}, last
}

// ---- Step 2: extract categories + requirements (per chunk) -----------------

type extractResult struct {
	Categories   []fwflat.Category `json:"categories"`
	Requirements []struct {
		Ref           string `json:"ref"`
		Category      string `json:"category"`
		Name          string `json:"name"`
		Description   string `json:"description"`
		SourceExcerpt string `json:"sourceExcerpt"`
	} `json:"requirements"`
}

func extractUserPrompt(meta identifyResult, chunk string, part, total int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Framework: %s %s\nPart %d of %d. Extract categories and requirements from:\n\n", meta.Name, meta.Version, part, total)
	b.WriteString(chunk)
	return b.String()
}

func (s *Service) stepExtractFlat(ctx context.Context, client llm.Client, setting *coredata.LLMSetting, meta identifyResult, chunk string, part, total int) (extractResult, error) {
	var last error
	for attempt := 0; attempt < 3; attempt++ {
		p := extractUserPrompt(meta, chunk, part, total)
		if attempt > 0 && last != nil {
			p += fmt.Sprintf("\n\nYour previous output was rejected: %s. Return ONLY corrected JSON.", last)
		}
		resp, err := client.Generate(ctx, llm.Request{
			System:      extractSystemPrompt(setting),
			Prompt:      p,
			MaxTokens:   stepTokens(setting, 4096),
			Temperature: 0, // extraction is transcription: never creative
		})
		if err != nil {
			return extractResult{}, fmt.Errorf("llm: %w", err)
		}
		var out extractResult
		if err := json.Unmarshal([]byte(stripFences(resp.Text)), &out); err != nil {
			last = err
			continue
		}
		return out, nil
	}
	return extractResult{}, last
}

// mergeExtract applies the guide's Step 2 merge: dedupe by ref, longer
// description wins on the chunk overlap, drop dangling category refs.
type mergedStructure struct {
	categories   []fwflat.Category
	requirements []fwflat.Requirement
	provenance   map[string]string // requirement ref → source excerpt (review only)
}

func mergeExtract(chunks []extractResult) mergedStructure {
	out := mergedStructure{provenance: map[string]string{}}

	catSeen := map[string]bool{}
	for _, c := range chunks {
		for _, cat := range c.Categories {
			ref := strings.TrimSpace(cat.Ref)
			if ref == "" || catSeen[ref] {
				continue // first wins
			}
			catSeen[ref] = true
			out.categories = append(out.categories, fwflat.Category{Ref: ref, Name: cat.Name})
		}
	}

	reqIndex := map[string]int{}
	for _, c := range chunks {
		for _, r := range c.Requirements {
			ref := strings.TrimSpace(r.Ref)
			if ref == "" {
				continue
			}
			if excerpt := strings.TrimSpace(r.SourceExcerpt); excerpt != "" {
				if cur, ok := out.provenance[ref]; !ok || len(excerpt) > len(cur) {
					out.provenance[ref] = excerpt
				}
			}
			if i, ok := reqIndex[ref]; ok {
				// Overlap: keep the longer description (the short one was cut
				// at the chunk boundary) and fill any missing field.
				exist := out.requirements[i]
				if len(strings.TrimSpace(r.Description)) > len(strings.TrimSpace(exist.Description)) {
					exist.Description = r.Description
				}
				if strings.TrimSpace(exist.Name) == "" {
					exist.Name = r.Name
				}
				if strings.TrimSpace(exist.Category) == "" {
					exist.Category = r.Category
				}
				out.requirements[i] = exist
				continue
			}
			reqIndex[ref] = len(out.requirements)
			out.requirements = append(out.requirements, fwflat.Requirement{
				Ref: ref, Category: strings.TrimSpace(r.Category), Name: r.Name, Description: r.Description,
			})
		}
	}

	// Drop dangling category references rather than failing the whole run.
	for i := range out.requirements {
		if c := out.requirements[i].Category; c != "" && !catSeen[c] {
			out.requirements[i].Category = ""
		}
	}
	return out
}

// ---- Step 3: controls (batched) --------------------------------------------

type controlsResult struct {
	Controls []fwflat.Control `json:"controls"`
	Links    []struct {
		Requirement string   `json:"requirement"`
		Controls    []string `json:"controls"`
	} `json:"links"`
}

func controlsUserPrompt(name string, batch []fwflat.Requirement, knownRefs []string) string {
	type lean struct {
		Ref         string `json:"ref"`
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	items := make([]lean, 0, len(batch))
	for _, r := range batch {
		items = append(items, lean{Ref: r.Ref, Name: r.Name, Description: r.Description})
	}
	blob, _ := json.Marshal(items)

	var b strings.Builder
	fmt.Fprintf(&b, "Framework: %s\n\nRequirements:\n%s\n\n", name, string(blob))
	if len(knownRefs) > 0 {
		fmt.Fprintf(&b, "Existing control refs you may reuse (from earlier batches):\n%s\n", strings.Join(knownRefs, ", "))
	} else {
		b.WriteString("Existing control refs you may reuse: (none yet)\n")
	}
	return b.String()
}

func (s *Service) stepControls(ctx context.Context, client llm.Client, setting *coredata.LLMSetting, name string, batch []fwflat.Requirement, knownRefs []string) (controlsResult, error) {
	var last error
	for attempt := 0; attempt < 3; attempt++ {
		p := controlsUserPrompt(name, batch, knownRefs)
		if attempt > 0 && last != nil {
			p += fmt.Sprintf("\n\nYour previous output was rejected: %s. Return ONLY corrected JSON.", last)
		}
		resp, err := client.Generate(ctx, llm.Request{
			System:      controlsSystemPrompt(setting),
			Prompt:      p,
			MaxTokens:   stepTokens(setting, 4096),
			Temperature: 0.2, // the only step that drafts new prose
		})
		if err != nil {
			return controlsResult{}, fmt.Errorf("llm: %w", err)
		}
		var out controlsResult
		if err := json.Unmarshal([]byte(stripFences(resp.Text)), &out); err != nil {
			last = err
			continue
		}
		return out, nil
	}
	return controlsResult{}, last
}

// applyControls merges a batch into the accumulating control library and links.
// Union by ref (first definition wins); links set requirement.controls.
func applyControls(controls []fwflat.Control, byRef map[string]bool, res controlsResult, reqIdx map[string]int, reqs []fwflat.Requirement) []fwflat.Control {
	for _, c := range res.Controls {
		ref := strings.TrimSpace(c.Ref)
		if ref == "" || byRef[ref] {
			continue
		}
		if c.Category != "" && !containsStr(fwflat.ControlCategories, c.Category) {
			c.Category = "Other"
		}
		byRef[ref] = true
		c.Ref = ref
		controls = append(controls, c)
	}
	for _, l := range res.Links {
		i, ok := reqIdx[strings.TrimSpace(l.Requirement)]
		if !ok {
			continue
		}
		for _, ref := range l.Controls {
			ref = strings.TrimSpace(ref)
			if ref == "" || !byRef[ref] {
				continue // never link a control that was not defined
			}
			reqs[i].Controls = append(reqs[i].Controls, ref)
		}
	}
	return controls
}

// guaranteeControls implements the guide's "every requirement gets ≥1 control".
// Synthesized fallbacks are returned so the caller can log/surface them.
func guaranteeControls(doc *fwflat.Framework) []string {
	byRef := map[string]bool{}
	for _, c := range doc.Controls {
		byRef[c.Ref] = true
	}
	var synthesized []string
	for i := range doc.Requirements {
		r := &doc.Requirements[i]
		if len(r.Controls) > 0 {
			continue
		}
		ref := slugify(r.Ref) + "-control"
		if !byRef[ref] {
			byRef[ref] = true
			doc.Controls = append(doc.Controls, fwflat.Control{
				Ref:         ref,
				Name:        truncate("Implement "+r.Name, 80),
				Description: "Placeholder control synthesized because the model proposed none — review and replace.",
				Category:    "Other",
			})
		}
		r.Controls = []string{ref}
		synthesized = append(synthesized, r.Ref)
	}
	return synthesized
}

// ---- helpers ---------------------------------------------------------------

var (
	nonSlug         = regexp.MustCompile(`[^a-z0-9]+`)
	numberedHeading = regexp.MustCompile(`(?m)^\s{0,3}(?:#{1,6}\s*)?((?:\d+\.){1,5}\d*|[A-Z]{2,4}[-.]\d+(?:\.\d+)*|Art(?:icle)?\.?\s*\d+)\b`)
)

func slugify(s string) string {
	out := nonSlug.ReplaceAllString(strings.ToLower(strings.TrimSpace(s)), "-")
	return strings.Trim(out, "-")
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// stripFences removes a ```json … ``` wrapper some models add despite the
// JSON-only contract.
func stripFences(text string) string {
	c := strings.TrimSpace(text)
	c = strings.TrimPrefix(c, "```json")
	c = strings.TrimPrefix(c, "```")
	c = strings.TrimSuffix(c, "```")
	return strings.TrimSpace(c)
}

func containsStr(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// countNumberedHeadings estimates how many numbered obligations the source
// document contains, feeding the guide's coverage gate (validation check 8).
func countNumberedHeadings(text string) int {
	seen := map[string]bool{}
	for _, m := range numberedHeading.FindAllStringSubmatch(text, -1) {
		if len(m) > 1 {
			seen[strings.TrimSpace(m[1])] = true
		}
	}
	return len(seen)
}
