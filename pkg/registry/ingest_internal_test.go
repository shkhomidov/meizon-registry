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
	"strings"
	"testing"

	"go.meizon.cloud/registry/pkg/fwflat"
)

// TestChunkText splits on block boundaries with a one-block overlap, and passes
// a small document through as a single chunk.
func TestChunkText(t *testing.T) {
	if got := chunkTextBudget("small doc", 48000); len(got) != 1 || got[0] != "small doc" {
		t.Fatalf("small doc should be one chunk, got %v", got)
	}

	// Chunks stay small for extraction accuracy: even the largest context window
	// must not push a chunk past the ceiling.
	if b := chunkBudgetFor("gemini"); b > maxChunkRunes {
		t.Errorf("chunk budget %d exceeds the accuracy ceiling %d", b, maxChunkRunes)
	}
	if b := chunkBudgetFor("gemini"); b < minChunkRunes {
		t.Errorf("chunk budget %d below the floor %d", b, minChunkRunes)
	}
	if b := chunkBudgetFor("unknown"); b < minChunkRunes {
		t.Errorf("unknown provider should fall back to the baseline, got %d", b)
	}

	text := strings.Repeat("a", 20) + "\n\n" + strings.Repeat("b", 20) + "\n\n" +
		strings.Repeat("c", 20) + "\n\n" + strings.Repeat("d", 20)
	chunks := chunkTextBudget(text, 45)
	if len(chunks) != 3 {
		t.Fatalf("expected 3 chunks, got %d: %v", len(chunks), chunks)
	}
	// One-block overlap: the B block ends chunk 0 and starts chunk 1.
	if !strings.Contains(chunks[0], "b") || !strings.Contains(chunks[1], "b") {
		t.Errorf("expected overlap of block B across chunks 0 and 1")
	}
}

// TestMergeExtract dedupes across chunks by ref and keeps the longer text.
func TestMergeExtract(t *testing.T) {
	mk := func(ref, name, desc, excerpt string) extractResult {
		var r extractResult
		r.Categories = []fwflat.Category{{Ref: "AC", Name: "Access Control"}}
		r.Requirements = append(r.Requirements, struct {
			Ref           string `json:"ref"`
			Category      string `json:"category"`
			Name          string `json:"name"`
			Description   string `json:"description"`
			SourceExcerpt string `json:"sourceExcerpt"`
		}{Ref: ref, Category: "AC", Name: name, Description: desc, SourceExcerpt: excerpt})
		return r
	}

	// AC-1 straddles the chunk boundary: the second copy is complete.
	merged := mergeExtract([]extractResult{
		mk("AC-1", "Policy", "short", "policy excerpt"),
		mk("AC-1", "Policy", "a much longer and complete description", ""),
	})

	if len(merged.categories) != 1 {
		t.Fatalf("categories should dedupe to 1, got %d", len(merged.categories))
	}
	if len(merged.requirements) != 1 {
		t.Fatalf("requirements should dedupe to 1, got %d", len(merged.requirements))
	}
	if merged.requirements[0].Description != "a much longer and complete description" {
		t.Errorf("the longer description must win, got %q", merged.requirements[0].Description)
	}
	if merged.provenance["AC-1"] != "policy excerpt" {
		t.Errorf("provenance lost: %v", merged.provenance)
	}
}

// TestGuaranteeControls gives every requirement a control, synthesizing one when
// the model proposed none, and reports which were synthesized.
func TestGuaranteeControls(t *testing.T) {
	doc := &fwflat.Framework{
		Requirements: []fwflat.Requirement{
			{Ref: "AC-1", Name: "Has one", Controls: []string{"access-policy"}},
			{Ref: "AC-2", Name: "Has none"},
		},
		Controls: []fwflat.Control{{Ref: "access-policy", Name: "Access control policy"}},
	}

	synth := guaranteeControls(doc)
	if len(synth) != 1 || synth[0] != "AC-2" {
		t.Fatalf("expected AC-2 to be synthesized, got %v", synth)
	}
	if len(doc.Requirements[1].Controls) != 1 {
		t.Fatal("AC-2 still has no control")
	}
	if err := doc.Validate(fwflat.ValidateOptions{}); err == nil {
		// Validate needs id/name/schema; only the >=1-control gate matters here.
		t.Log("document validates")
	}
}
