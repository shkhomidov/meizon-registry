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
	"go.meizon.cloud/registry/pkg/fwflat"
	"go.meizon.cloud/registry/pkg/fwschema"
	"go.meizon.cloud/registry/pkg/gid"
	"go.meizon.cloud/registry/pkg/iam"
	"go.meizon.cloud/registry/pkg/llm"
)

// Stage B of cross-mapping: adjudication.
//
// Nothing here writes a mapping. Every accepted pair becomes a PROPOSAL that an
// auditor decides on, because the failure mode of this feature is not a crash —
// it is forty confident-looking wrong rows getting rubber-stamped into what is
// ultimately a compliance artifact.

// adjudicateBatchSize: source nodes per LLM call. Each carries its own
// candidate list, so the prompt is this many × candidatesPerNode nodes.
const adjudicateBatchSize = 4

type (
	proposedMapping struct {
		Source     string  `json:"source"`
		Target     string  `json:"target"`
		Relation   string  `json:"relation"`
		Confidence float64 `json:"confidence"`
		Rationale  string  `json:"rationale"`
	}

	adjudicateResult struct {
		Mappings []proposedMapping `json:"mappings"`
	}
)

// AutoMapRequest describes one auto-mapping pass.
type AutoMapRequest struct {
	SourceRef string
	TargetRef string
	NodeKind  string // requirement | control
	Remap     bool   // re-adjudicate pairs a previous run already covered
}

// StartAutoMapJob runs candidate retrieval + adjudication asynchronously and
// returns a job id the console polls with the shared ingest status shape.
func (s *Service) StartAutoMapJob(ctx context.Context, actorID gid.GID, req AutoMapRequest) (string, error) {
	if req.NodeKind == "" {
		req.NodeKind = coredata.MappingNodeRequirement
	}
	if req.NodeKind != coredata.MappingNodeRequirement && req.NodeKind != coredata.MappingNodeControl {
		return "", fmt.Errorf("%w: unknown node kind %q", ErrInvalidInput, req.NodeKind)
	}
	if req.SourceRef == req.TargetRef {
		return "", fmt.Errorf("%w: a framework cannot be mapped to itself", ErrInvalidInput)
	}

	client, setting, err := s.ingestPreflight(ctx, actorID)
	if err != nil {
		return "", err
	}

	// Authorize synchronously so a permission problem is an immediate error
	// rather than a job that fails a minute later.
	var sourceVersionID gid.GID
	var targetVersion string
	err = s.db.WithConn(ctx, func(ctx context.Context, conn pg.Querier) error {
		scope := s.platformScope()

		var source coredata.Framework
		if err := source.LoadByReferenceID(ctx, conn, scope, req.SourceRef); err != nil {
			return fmt.Errorf("source framework %q: %w", req.SourceRef, err)
		}
		if err := s.authorize(ctx, conn, actorID, iam.ActionControlEdit, source.Region, source.ID); err != nil {
			return err
		}
		id, err := s.latestVersionIDConn(ctx, conn, source.ID)
		if err != nil {
			return err
		}
		sourceVersionID = id

		var target coredata.Framework
		if err := target.LoadByReferenceID(ctx, conn, scope, req.TargetRef); err != nil {
			return fmt.Errorf("target framework %q: %w", req.TargetRef, err)
		}
		targetVersionID, err := s.latestVersionIDConn(ctx, conn, target.ID)
		if err != nil {
			return err
		}
		targetVersion, err = s.versionString(ctx, conn, targetVersionID)
		return err
	})
	if err != nil {
		return "", err
	}

	// Both sides are read in the canonical language. English is the spine the
	// registry maps in; comparing an Uzbek requirement against an English one
	// by token overlap retrieves nothing and would quietly report "no overlap"
	// when the truth is "not comparable". Refuse rather than mislead.
	for _, ref := range []string{req.SourceRef, req.TargetRef} {
		ok, cerr := s.HasCanonical(ctx, ref)
		if cerr != nil {
			return "", cerr
		}
		if !ok {
			return "", fmt.Errorf(
				"%w: %q has no English version yet — mapping compares frameworks in English, "+
					"so add the language first (it is generated automatically for new frameworks)",
				ErrInvalidInput, ref)
		}
	}

	sourceDoc, err := s.ExportFlatIn(ctx, req.SourceRef, CanonicalLanguage)
	if err != nil {
		return "", err
	}
	targetDoc, err := s.ExportFlatIn(ctx, req.TargetRef, CanonicalLanguage)
	if err != nil {
		return "", err
	}

	job := &ingestJob{
		id:        gid.New(s.cfg.PlatformTenant, coredata.IngestJobEntityType).String(),
		status:    "running",
		step:      "plan",
		createdAt: time.Now(),
		persist:   &jobStore{svc: s},
	}
	s.ingestJobs.add(job)
	s.startJobRecord(ctx, job.id, coredata.JobKindAutoMap, req.TargetRef, req.SourceRef, actorID)

	go func() {
		bg := context.Background()
		err := s.runAutoMap(bg, job, client, setting, autoMapInput{
			req:             req,
			sourceVersionID: sourceVersionID,
			targetVersion:   targetVersion,
			source:          sourceDoc,
			target:          targetDoc,
			actorID:         actorID,
		})
		job.finish(nil, nil, "", err)
	}()

	return job.id, nil
}

type autoMapInput struct {
	req             AutoMapRequest
	sourceVersionID gid.GID
	targetVersion   string
	source          *fwflat.Framework
	target          *fwflat.Framework
	actorID         gid.GID
}

// runAutoMap: retrieve candidates per source node, adjudicate in batches, and
// record every proposal plus the ledger entry.
func (s *Service) runAutoMap(ctx context.Context, job *ingestJob, client llm.Client, setting *coredata.LLMSetting, in autoMapInput) error {
	sourceNodes := nodesOf(in.source, in.req.NodeKind)
	targetNodes := nodesOf(in.target, in.req.NodeKind)
	if len(sourceNodes) == 0 {
		return fmt.Errorf("the source framework has no %ss", in.req.NodeKind)
	}
	if len(targetNodes) == 0 {
		return fmt.Errorf("the target framework has no %ss", in.req.NodeKind)
	}

	// Skip source refs a previous run already adjudicated. This is the whole
	// point of the ledger: without it a re-run re-pays for every node the model
	// correctly said "no match" for, which is most of them.
	done := map[string]bool{}
	if !in.req.Remap {
		var err error
		done, err = s.adjudicatedRefs(ctx, in.sourceVersionID, in.req.TargetRef, in.req.NodeKind)
		if err != nil {
			return err
		}
	}
	pending := make([]mapNode, 0, len(sourceNodes))
	for _, n := range sourceNodes {
		if !done[n.Ref] {
			pending = append(pending, n)
		}
	}
	if len(pending) == 0 {
		job.set("done", 1, 1)
		return s.recordMappingRun(ctx, in, nil, 0, 0, setting.Model)
	}

	job.set("retrieve", 0, len(pending))
	idf := buildIDF(targetNodes)
	shortlists := make([][]mapNode, len(pending))
	pairs := 0
	for i, n := range pending {
		shortlists[i] = retrieveCandidates(n, targetNodes, idf, candidatesPerNode)
		pairs += len(shortlists[i])
		job.set("retrieve", i+1, len(pending))
	}

	batches := (len(pending) + adjudicateBatchSize - 1) / adjudicateBatchSize
	var proposals []proposedMapping
	adjudicated := make([]string, 0, len(pending))

	for b := 0; b < batches; b++ {
		job.set("adjudicate", b, batches)
		lo := b * adjudicateBatchSize
		hi := min(lo+adjudicateBatchSize, len(pending))

		out, err := s.stepAdjudicate(ctx, client, setting,
			in.source.Name, in.target.Name, in.req.NodeKind,
			pending[lo:hi], shortlists[lo:hi])
		if err != nil {
			return fmt.Errorf("adjudicate batch %d/%d: %w", b+1, batches, err)
		}
		proposals = append(proposals, out...)
		for _, n := range pending[lo:hi] {
			adjudicated = append(adjudicated, n.Ref)
		}
		job.set("adjudicate", b+1, batches)
	}

	job.set("record", 1, 1)
	stored, err := s.storeProposals(ctx, in, proposals)
	if err != nil {
		return err
	}
	return s.recordMappingRun(ctx, in, adjudicated, pairs, stored, setting.Model)
}

// nodesOf projects a framework into comparable nodes of one kind.
func nodesOf(doc *fwflat.Framework, kind string) []mapNode {
	if kind == coredata.MappingNodeControl {
		out := make([]mapNode, 0, len(doc.Controls))
		for _, c := range doc.Controls {
			out = append(out, mapNode{Ref: c.Ref, Name: c.Name, Description: c.Description, Category: c.Category})
		}
		return out
	}
	out := make([]mapNode, 0, len(doc.Requirements))
	for _, r := range doc.Requirements {
		out = append(out, mapNode{Ref: r.Ref, Name: r.Name, Description: r.Description, Category: r.Category})
	}
	return out
}

func adjudicateUserPrompt(sourceName, targetName, kind string, batch []mapNode, shortlists [][]mapNode) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Source framework: %s\nTarget framework: %s\nNode type: %s\n\n", sourceName, targetName, kind)
	for i, src := range batch {
		fmt.Fprintf(&b, "### SOURCE %s\n%s\n", src.Ref, src.Name)
		if src.Description != "" {
			fmt.Fprintf(&b, "%s\n", truncate(src.Description, 900))
		}
		fmt.Fprintf(&b, "\nCandidate targets for %s (choose only from these refs, or none):\n", src.Ref)
		for _, c := range shortlists[i] {
			fmt.Fprintf(&b, "- %s: %s", c.Ref, c.Name)
			if c.Description != "" {
				fmt.Fprintf(&b, " — %s", truncate(c.Description, 300))
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	return b.String()
}

// stepAdjudicate asks the model which candidate pairs are genuine mappings.
//
// Every result is filtered against the refs actually sent. A hallucinated ref is
// DROPPED, never repaired: it would become a stub that silently never resolves,
// visible only as a permanently unresolved row nobody looks at.
func (s *Service) stepAdjudicate(ctx context.Context, client llm.Client, setting *coredata.LLMSetting, sourceName, targetName, kind string, batch []mapNode, shortlists [][]mapNode) ([]proposedMapping, error) {
	allowed := map[string]map[string]bool{}
	for i, src := range batch {
		set := map[string]bool{}
		for _, c := range shortlists[i] {
			set[c.Ref] = true
		}
		allowed[src.Ref] = set
	}

	var last error
	for attempt := 0; attempt < 3; attempt++ {
		p := adjudicateUserPrompt(sourceName, targetName, kind, batch, shortlists)
		if attempt > 0 && last != nil {
			p += fmt.Sprintf("\n\nYour previous output was rejected: %s. Return ONLY corrected JSON.", last)
		}
		resp, err := client.Generate(ctx, llm.Request{
			System:      mappingSystemPrompt(setting),
			Prompt:      p,
			MaxTokens:   stepTokens(setting, 2048),
			Temperature: 0,
		})
		if err != nil {
			return nil, err
		}

		var out adjudicateResult
		if err := json.Unmarshal([]byte(stripFences(resp.Text)), &out); err != nil {
			last = fmt.Errorf("not valid JSON: %v", err)
			continue
		}

		// An empty result is a legitimate answer — most requirement pairs in two
		// unrelated frameworks genuinely do not map — so it is accepted, not
		// retried. Retrying "none" is how a model gets pushed into inventing.
		kept := make([]proposedMapping, 0, len(out.Mappings))
		for _, m := range out.Mappings {
			targets, ok := allowed[m.Source]
			if !ok || !targets[m.Target] {
				continue // ref not among the candidates we sent
			}
			if !fwschema.MappingRelation(m.Relation).IsValid() {
				continue
			}
			if m.Confidence < 0 || m.Confidence > 1 {
				m.Confidence = 0
			}
			m.Rationale = truncate(strings.TrimSpace(m.Rationale), 400)
			kept = append(kept, m)
		}
		return kept, nil
	}
	return nil, last
}

// storeProposals records proposals for auditor review. Returns how many were
// newly stored.
func (s *Service) storeProposals(ctx context.Context, in autoMapInput, proposals []proposedMapping) (int, error) {
	if len(proposals) == 0 {
		return 0, nil
	}
	stored := 0
	err := s.db.WithTx(ctx, func(ctx context.Context, tx pg.Tx) error {
		scope := s.platformScope()
		now := time.Now()
		for _, m := range proposals {
			row := coredata.MappingProposal{
				ID:                       gid.New(s.cfg.PlatformTenant, coredata.MappingProposalEntityType),
				SourceFrameworkVersionID: in.sourceVersionID,
				NodeKind:                 in.req.NodeKind,
				SourceRef:                m.Source,
				TargetFrameworkCode:      in.req.TargetRef,
				TargetFrameworkVersion:   in.targetVersion,
				TargetRef:                m.Target,
				Relation:                 m.Relation,
				Confidence:               m.Confidence,
				Rationale:                m.Rationale,
				Status:                   coredata.ProposalPending,
				CreatedAt:                now,
			}
			if err := row.Insert(ctx, tx, scope); err != nil {
				return err
			}
			stored++
		}
		return nil
	})
	return stored, err
}

// recordMappingRun writes the ledger entry that makes the next run incremental.
func (s *Service) recordMappingRun(ctx context.Context, in autoMapInput, adjudicated []string, pairs, proposed int, model string) error {
	return s.db.WithTx(ctx, func(ctx context.Context, tx pg.Tx) error {
		run := coredata.MappingRun{
			ID:                       gid.New(s.cfg.PlatformTenant, coredata.MappingRunEntityType),
			SourceFrameworkVersionID: in.sourceVersionID,
			TargetFrameworkCode:      in.req.TargetRef,
			TargetFrameworkVersion:   in.targetVersion,
			NodeKind:                 in.req.NodeKind,
			AdjudicatedRefs:          adjudicated,
			PairsConsidered:          pairs,
			Proposed:                 proposed,
			Model:                    model,
			CompletedAt:              time.Now(),
		}
		if err := run.Upsert(ctx, tx, s.platformScope()); err != nil {
			return err
		}
		return s.recordAudit(ctx, tx, s.platformScope(), in.actorID, "mapping.automap", in.req.SourceRef,
			fmt.Sprintf("target=%s kind=%s adjudicated=%d pairs=%d proposed=%d model=%s",
				in.req.TargetRef, in.req.NodeKind, len(adjudicated), pairs, proposed, model))
	})
}

// adjudicatedRefs returns the source refs a previous run already considered.
func (s *Service) adjudicatedRefs(ctx context.Context, versionID gid.GID, targetCode, kind string) (map[string]bool, error) {
	out := map[string]bool{}
	err := s.db.WithConn(ctx, func(ctx context.Context, conn pg.Querier) error {
		var runs coredata.MappingRuns
		if err := runs.LoadAllByVersion(ctx, conn, s.platformScope(), versionID); err != nil {
			return err
		}
		for _, r := range runs {
			if r.TargetFrameworkCode != targetCode || r.NodeKind != kind {
				continue
			}
			for _, ref := range r.AdjudicatedRefs {
				out[ref] = true
			}
		}
		return nil
	})
	return out, err
}
