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
	"fmt"
	"strings"
	"sync"
	"time"

	"go.gearno.de/kit/pg"
	"go.meizon.cloud/registry/pkg/coredata"
	"go.meizon.cloud/registry/pkg/docextract"
	"go.meizon.cloud/registry/pkg/fwflat"
	"go.meizon.cloud/registry/pkg/gid"
	"go.meizon.cloud/registry/pkg/llm"
)

// The document-first generator runs as a chain of small, verifiable LLM steps
// (identify → extract-per-chunk → merge → validate → QA) rather than one call
// that must "understand the whole standard at once". Each step has one job and a
// strict JSON output, so failures are local and re-runnable and nothing is
// missed. The pipeline runs asynchronously; the console polls IngestStatus and
// renders a live step checklist.

// Chunk sizing. The guide's baseline is ~12k tokens (≈48k runes) per extraction
// call; a bigger context window lets us send more per call — fewer requests on a
// large standard — but not the whole window, because asking one call to
// enumerate hundreds of requirements is exactly how items get dropped.
// Extraction accuracy — not the context window — sets this. A model asked to
// enumerate every obligation in a large slab of text starts skipping items long
// before it runs out of context, so we deliberately send SMALL chunks: more
// calls, each one easy to get right, and a failure is localised to one part.
const (
	minChunkRunes = 12_000 // ~3k tokens
	maxChunkRunes = 32_000 // ~8k tokens — below the guide's 12k baseline, on purpose
)

// chunkBudgetFor sizes a chunk. A larger context window buys a little headroom
// but never licence to send a big chunk: precision, not capacity, is the limit.
func chunkBudgetFor(provider string) int {
	budget := llm.ContextRunes(provider) / 20
	if budget > maxChunkRunes {
		return maxChunkRunes
	}
	if budget < minChunkRunes {
		return minChunkRunes
	}
	return budget
}

// stepTokens gives a step its output budget: never below floor, but honouring a
// larger configured budget. Small fixed budgets starve "thinking" models (e.g.
// gemini-2.5-*), which spend output tokens reasoning before emitting any text
// and otherwise return an empty reply.
func stepTokens(setting *coredata.LLMSetting, floor int) int {
	if setting != nil && setting.MaxTokens > floor {
		return setting.MaxTokens
	}
	return floor
}

// ---- chunking --------------------------------------------------------------

// chunkText splits the extracted document into context-sized parts on the best
// available boundary (page breaks, else blank-line paragraphs), packing blocks
// greedily under the budget and carrying a one-block overlap so a requirement
// split across a boundary is fully present in at least one chunk. A document
// under budget returns a single chunk.
func chunkText(text, provider string) []string {
	return chunkTextBudget(text, chunkBudgetFor(provider))
}

func chunkTextBudget(text string, budget int) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	if len([]rune(text)) <= budget {
		return []string{text}
	}

	blocks := splitBlocks(text)

	var chunks []string
	var cur []string
	curRunes := 0
	for _, b := range blocks {
		bRunes := len([]rune(b))
		if curRunes > 0 && curRunes+bRunes > budget {
			chunks = append(chunks, strings.Join(cur, "\n\n"))
			// one-block overlap: seed the next chunk with the last block.
			last := cur[len(cur)-1]
			cur = []string{last}
			curRunes = len([]rune(last))
		}
		cur = append(cur, b)
		curRunes += bRunes
	}
	if len(cur) > 0 {
		chunks = append(chunks, strings.Join(cur, "\n\n"))
	}
	return chunks
}

// splitBlocks breaks text into boundary-aligned blocks: page breaks first, then
// blank-line paragraphs; a block still over budget is split by lines so a single
// giant block can't blow the chunk budget.
func splitBlocks(text string) []string {
	var raw []string
	if strings.Contains(text, docextract.PageBreak) {
		raw = strings.Split(text, docextract.PageBreak)
	} else {
		raw = splitParagraphs(text)
	}

	var out []string
	for _, b := range raw {
		b = strings.TrimSpace(b)
		if b == "" {
			continue
		}
		if len([]rune(b)) <= maxChunkRunes {
			out = append(out, b)
			continue
		}
		// Oversized block — fall back to line packing.
		var cur []string
		curRunes := 0
		for _, line := range strings.Split(b, "\n") {
			lr := len([]rune(line)) + 1
			if curRunes > 0 && curRunes+lr > maxChunkRunes {
				out = append(out, strings.Join(cur, "\n"))
				cur, curRunes = nil, 0
			}
			cur = append(cur, line)
			curRunes += lr
		}
		if len(cur) > 0 {
			out = append(out, strings.Join(cur, "\n"))
		}
	}
	return out
}

func splitParagraphs(text string) []string {
	// Collapse runs of blank lines into paragraph boundaries.
	parts := strings.Split(text, "\n\n")
	var out []string
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			out = append(out, p)
		}
	}
	return out
}

// ---- per-step model I/O ----------------------------------------------------

// ---- pipeline --------------------------------------------------------------

// ingestResult is the pipeline's product: the assembled flat framework plus
// review-only provenance and the advisory QA note.
type ingestResult struct {
	doc         *fwflat.Framework
	prov        map[string]string // requirement ref → source excerpt
	note        string
	warnings    []string // advisory quality findings; never fatal
	duplicates  []fwflat.DuplicateGroup
	synthesized []string // requirement refs that got a placeholder control
}

// normalizeLanguage trims an ISO-639-1 code to lower case. An unrecognisable
// value becomes empty rather than a guess: an empty source language reads as
// "unknown" everywhere, while a wrong one would send the translate step at the
// wrong pair.
func normalizeLanguage(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	if len(v) < 2 || len(v) > 8 {
		return ""
	}
	for _, r := range v {
		if (r < 'a' || r > 'z') && r != '-' {
			return ""
		}
	}
	return v
}

// runPipeline executes the guide's chain — identify → extract per chunk → merge
// → controls per batch → assemble → validate → QA — reporting progress through
// the job after every unit of work so the console can render it live.
func (s *Service) runPipeline(ctx context.Context, job *ingestJob, client llm.Client, setting *coredata.LLMSetting, docText, brief string) (*ingestResult, error) {
	chunks := chunkText(docText, setting.Provider)
	if len(chunks) == 0 {
		return nil, fmt.Errorf("the document is empty")
	}

	// Step 0 is code: the document is already text, chunked on block boundaries.
	job.set("chunk", len(chunks), len(chunks))

	// Step 1 — identify (first chunk + the brief as a hint).
	job.set("identify", 0, 1)
	meta, err := s.stepIdentifyFlat(ctx, client, setting, chunks[0])
	if err != nil {
		return nil, fmt.Errorf("identify: %w", err)
	}
	job.set("identify", 1, 1)

	// Step 2 — extract, one call per chunk.
	results := make([]extractResult, 0, len(chunks))
	for i, chunk := range chunks {
		job.set("extract", i, len(chunks))
		res, err := s.stepExtractFlat(ctx, client, setting, meta, chunk, i+1, len(chunks))
		if err != nil {
			return nil, fmt.Errorf("extract part %d/%d: %w", i+1, len(chunks), err)
		}
		results = append(results, res)
		job.set("extract", i+1, len(chunks))
	}

	// Merge (code): dedupe by ref, longer description wins, drop dangling refs.
	job.set("merge", 1, 1)
	merged := mergeExtract(results)
	if len(merged.requirements) == 0 {
		return nil, fmt.Errorf("merge: no requirements were extracted from any part")
	}

	// Step 3 — controls, batched, feeding known refs forward so the library
	// converges instead of sprouting near-duplicates.
	reqIdx := make(map[string]int, len(merged.requirements))
	for i, r := range merged.requirements {
		reqIdx[r.Ref] = i
	}
	var controls []fwflat.Control
	byRef := map[string]bool{}
	batches := (len(merged.requirements) + controlsBatchSize - 1) / controlsBatchSize
	for b := 0; b < batches; b++ {
		job.set("controls", b, batches)
		lo := b * controlsBatchSize
		hi := min(lo+controlsBatchSize, len(merged.requirements))
		known := make([]string, 0, len(controls))
		for _, c := range controls {
			known = append(known, c.Ref)
		}
		res, err := s.stepControls(ctx, client, setting, meta.Name, merged.requirements[lo:hi], known)
		if err != nil {
			return nil, fmt.Errorf("controls batch %d/%d: %w", b+1, batches, err)
		}
		controls = applyControls(controls, byRef, res, reqIdx, merged.requirements)
		job.set("controls", b+1, batches)
	}

	// Step 4 — assemble + validate (code only, so the file is reproducible).
	job.set("assemble", 1, 1)
	doc := &fwflat.Framework{
		Schema:       fwflat.SchemaMarker,
		ID:           meta.ID,
		Name:         meta.Name,
		Version:      meta.Version,
		Category:     meta.Category,
		Language:     normalizeLanguage(meta.Language),
		Regions:      meta.Regions,
		Categories:   merged.categories,
		Requirements: merged.requirements,
		Controls:     controls,
	}
	doc.Normalize()
	synthesized := guaranteeControls(doc)

	job.set("validate", 1, 1)
	valOpts := fwflat.ValidateOptions{ExpectedRequirements: countNumberedHeadings(docText)}
	if err := doc.Validate(valOpts); err != nil {
		return nil, fmt.Errorf("validate: %w", err)
	}
	// Quality smells do not fail the run — a reviewer decides. Failing here
	// would discard the OCR and every LLM call already paid for over a
	// heuristic that real standards legitimately trip.
	warnings := doc.Warnings(valOpts)
	duplicates := doc.DuplicateDescriptions()

	// QA read-back — advisory only, never edits the file.
	job.set("qa", 0, 1)
	note := s.stepQAFlat(ctx, client, setting, doc)
	job.set("qa", 1, 1)

	return &ingestResult{doc: doc, prov: merged.provenance, note: note, warnings: warnings, duplicates: duplicates, synthesized: synthesized}, nil
}

// stepQAFlat is best-effort: any error yields an empty note rather than failing
// the whole pipeline.
func (s *Service) stepQAFlat(ctx context.Context, client llm.Client, setting *coredata.LLMSetting, doc *fwflat.Framework) string {
	var sample []string
	for _, r := range doc.Requirements {
		if len(sample) >= 10 {
			break
		}
		sample = append(sample, r.Ref)
	}
	c := doc.Count()
	prompt := fmt.Sprintf(
		"You QA an extracted compliance framework. Framework %q (%s) was extracted into %d categories and %d requirements. "+
			"Sample requirement refs: %s.\n\nIn ONE short sentence, note anything that looks missing, mis-grouped or duplicated "+
			"for a standard of this type. If it looks complete, reply exactly \"Looks complete.\"",
		doc.Name, doc.Version, c.Categories, c.Requirements, strings.Join(sample, ", "))

	resp, err := client.Generate(ctx, llm.Request{
		System:      qaSystemPrompt(setting),
		Prompt:      prompt,
		MaxTokens:   stepTokens(setting, 1024),
		Temperature: 0,
	})
	if err != nil {
		return ""
	}
	note := strings.TrimSpace(resp.Text)
	if note == "Looks complete." {
		return ""
	}
	return note
}

// ---- async job registry ----------------------------------------------------

// IngestStatus is the pollable state of a generation job.
type IngestStatus struct {
	Status   string              `json:"status"` // running | done | error
	Step     string              `json:"step"`   // identify | extract | merge | validate | qa
	Progress IngestProgress      `json:"progress"`
	Result   *GeneratedFramework `json:"result,omitempty"`
	Diff     map[string]string   `json:"diff,omitempty"`            // next-version only
	Baseline string              `json:"baselineVersion,omitempty"` // next-version only
	Coverage *qaCoverageReport   `json:"coverage,omitempty"`        // qa_template only
	Error    string              `json:"error,omitempty"`
}

// IngestProgress is the chunk counter for the extract step.
type IngestProgress struct {
	Current int `json:"current"`
	Total   int `json:"total"`
}

type ingestJob struct {
	id        string
	mu        sync.Mutex
	status    string
	step      string
	current   int
	total     int
	result    *GeneratedFramework
	diff      map[string]string
	baseline  string
	err       string
	createdAt time.Time

	// persist mirrors every transition to the database, so a job survives the
	// tab that started it and the process that runs it. In-memory stays the
	// fast path for polling; the database is the durable record.
	persist *jobStore
}

func (j *ingestJob) set(step string, current, total int) {
	j.mu.Lock()
	j.step = step
	j.current = current
	j.total = total
	j.mu.Unlock()

	if j.persist != nil {
		j.persist.progress(j.id, step, current, total)
	}
}

func (j *ingestJob) finish(res *GeneratedFramework, diff map[string]string, baseline string, err error) {
	j.mu.Lock()
	if err != nil {
		j.status = "error"
		j.err = err.Error()
	} else {
		j.status = "done"
		j.result = res
		j.diff = diff
		j.baseline = baseline
	}
	status, errText := j.status, j.err
	j.mu.Unlock()

	if j.persist != nil {
		j.persist.finish(j.id, status, res, diff, baseline, errText)
	}
}

func (j *ingestJob) snapshot() IngestStatus {
	j.mu.Lock()
	defer j.mu.Unlock()
	return IngestStatus{
		Status:   j.status,
		Step:     j.step,
		Progress: IngestProgress{Current: j.current, Total: j.total},
		Result:   j.result,
		Diff:     j.diff,
		Baseline: j.baseline,
		Error:    j.err,
	}
}

type ingestJobRegistry struct {
	mu   sync.Mutex
	jobs map[string]*ingestJob
}

func newIngestJobRegistry() *ingestJobRegistry {
	return &ingestJobRegistry{jobs: map[string]*ingestJob{}}
}

const ingestJobTTL = 30 * time.Minute

func (r *ingestJobRegistry) add(j *ingestJob) {
	r.mu.Lock()
	defer r.mu.Unlock()
	// opportunistic GC of finished/stale jobs.
	for id, old := range r.jobs {
		if now := j.createdAt; now.Sub(old.createdAt) > ingestJobTTL {
			delete(r.jobs, id)
		}
	}
	r.jobs[j.id] = j
}

func (r *ingestJobRegistry) get(id string) (*ingestJob, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	j, ok := r.jobs[id]
	return j, ok
}

// StartGenerateJob kicks off the staged pipeline for a NEW framework and returns
// a job id immediately. The console polls IngestJobStatus for progress; the
// finished job carries the same GeneratedFramework the review UI consumes.
func (s *Service) StartGenerateJob(ctx context.Context, actorID gid.GID, in DocInput, brief string) (string, error) {
	if strings.TrimSpace(in.Text) == "" && !in.NeedsOCR() {
		return "", fmt.Errorf("the document is empty")
	}
	client, setting, err := s.ingestPreflight(ctx, actorID)
	if err != nil {
		return "", err
	}

	firstStep := "identify"
	if in.NeedsOCR() {
		firstStep = "ocr"
	}
	job := &ingestJob{
		id:        gid.New(s.cfg.PlatformTenant, coredata.IngestJobEntityType).String(),
		status:    "running",
		step:      firstStep,
		createdAt: time.Now(),
		persist:   &jobStore{svc: s},
	}
	s.ingestJobs.add(job)
	s.startJobRecord(ctx, job.id, coredata.JobKindGenerate, in.Filename, "", actorID)

	go func() {
		s.llmSem <- struct{}{}
		defer func() { <-s.llmSem }()
		bg := context.Background()
		docText, oerr := s.resolveText(bg, job, actorID, in)
		if oerr != nil {
			job.finish(nil, nil, "", oerr)
			return
		}
		res, perr := s.runPipeline(bg, job, client, setting, docText, brief)
		if perr != nil {
			job.finish(nil, nil, "", perr)
			return
		}
		out := &GeneratedFramework{
			GenerationID:        gid.New(s.cfg.PlatformTenant, coredata.AIGenerationEntityType),
			Document:            res.doc,
			Summary:             summarizeFlat(res.doc),
			Provenance:          res.prov,
			DocumentText:        docText,
			Note:                res.note,
			Warnings:            res.warnings,
			Duplicates:          res.duplicates,
			SynthesizedControls: res.synthesized,
		}
		s.auditIngest(bg, actorID, client, setting, res.doc, "ai.generate_framework", "")
		job.finish(out, nil, "", nil)
	}()

	return job.id, nil
}

// StartNextVersionJob runs the same pipeline for a revised document, then diffs
// the result against the framework's current latest version on completion.
func (s *Service) StartNextVersionJob(ctx context.Context, actorID gid.GID, ref string, in DocInput, brief, newVersion string) (string, error) {
	if strings.TrimSpace(in.Text) == "" && !in.NeedsOCR() {
		return "", fmt.Errorf("the document is empty")
	}
	if strings.TrimSpace(newVersion) == "" {
		return "", fmt.Errorf("a new version identifier is required")
	}

	// Preflight: principal + llm client + load baseline structure and guard the
	// version, all under one connection before the job goes async.
	var client llm.Client
	var setting *coredata.LLMSetting
	var baseline []StructureCategory
	var baselineVersion string
	var frameworkRegion string
	err := s.db.WithConn(ctx, func(ctx context.Context, conn pg.Querier) error {
		if _, err := s.principalFor(ctx, conn, actorID); err != nil {
			return err
		}
		c, st, err := s.llmClient(ctx, conn)
		if err != nil {
			return err
		}
		client, setting = c, st

		var framework coredata.Framework
		if err := framework.LoadByReferenceID(ctx, conn, s.platformScope(), ref); err != nil {
			return err
		}
		frameworkRegion = framework.Region
		var versions coredata.FrameworkVersions
		if err := versions.LoadAllByFramework(ctx, conn, s.platformScope(), framework.ID); err != nil {
			return err
		}
		if len(versions) == 0 {
			return fmt.Errorf("framework %q has no versions", ref)
		}
		for _, v := range versions {
			if v.Version == newVersion {
				return fmt.Errorf("version %q already exists for %q", newVersion, ref)
			}
		}
		baselineVersion = versions[0].Version
		base, err := s.structureOfTx(ctx, conn, versions[0].ID)
		if err != nil {
			return err
		}
		baseline = base
		return nil
	})
	if err != nil {
		return "", err
	}
	_ = frameworkRegion

	settingCopy := *setting
	firstStep := "identify"
	if in.NeedsOCR() {
		firstStep = "ocr"
	}
	job := &ingestJob{
		id:        gid.New(s.cfg.PlatformTenant, coredata.IngestJobEntityType).String(),
		status:    "running",
		step:      firstStep,
		createdAt: time.Now(),
		persist:   &jobStore{svc: s},
	}
	s.ingestJobs.add(job)
	s.startJobRecord(ctx, job.id, coredata.JobKindGenerate, in.Filename, "", actorID)

	go func() {
		s.llmSem <- struct{}{}
		defer func() { <-s.llmSem }()
		bg := context.Background()
		docText, oerr := s.resolveText(bg, job, actorID, in)
		if oerr != nil {
			job.finish(nil, nil, "", oerr)
			return
		}
		res, perr := s.runPipeline(bg, job, client, &settingCopy, docText, brief)
		if perr != nil {
			job.finish(nil, nil, "", perr)
			return
		}
		res.doc.ID = ref
		res.doc.Version = newVersion
		out := &GeneratedFramework{
			GenerationID:        gid.New(s.cfg.PlatformTenant, coredata.AIGenerationEntityType),
			Document:            res.doc,
			Summary:             summarizeFlat(res.doc),
			Provenance:          res.prov,
			DocumentText:        docText,
			Note:                res.note,
			Warnings:            res.warnings,
			Duplicates:          res.duplicates,
			SynthesizedControls: res.synthesized,
		}
		s.auditIngest(bg, actorID, client, &settingCopy, res.doc, "ai.generate_version", "")
		job.finish(out, diffFlatAgainstBaseline(baseline, res.doc), baselineVersion, nil)
	}()

	return job.id, nil
}

// IngestJobStatus returns the current state of a generation job.
// IngestJobStatus resolves a job from memory when this process owns it, and
// from the database otherwise — a different tab, or after a restart. The
// in-memory copy is preferred only because it is current to the tick.
func (s *Service) IngestJobStatus(ctx context.Context, jobID string) (IngestStatus, error) {
	if job, ok := s.ingestJobs.get(jobID); ok {
		return job.snapshot(), nil
	}
	return s.jobStatusFromStore(ctx, jobID)
}

// ingestPreflight validates the actor and loads the LLM client under one
// connection, returning a detached copy of the setting for the async goroutine.
func (s *Service) ingestPreflight(ctx context.Context, actorID gid.GID) (llm.Client, *coredata.LLMSetting, error) {
	var client llm.Client
	var setting *coredata.LLMSetting
	err := s.db.WithConn(ctx, func(ctx context.Context, conn pg.Querier) error {
		if _, err := s.principalFor(ctx, conn, actorID); err != nil {
			return err
		}
		c, st, err := s.llmClient(ctx, conn)
		if err != nil {
			return err
		}
		client = c
		cp := *st
		setting = &cp
		return nil
	})
	return client, setting, err
}

func (s *Service) auditIngest(ctx context.Context, actorID gid.GID, client llm.Client, setting *coredata.LLMSetting, doc *fwflat.Framework, action, target string) {
	_ = s.db.WithConn(ctx, func(ctx context.Context, conn pg.Querier) error {
		c := doc.Count()
		return s.recordAudit(ctx, conn, s.platformScope(), actorID, action, target,
			fmt.Sprintf("staged provider=%s model=%s id=%s requirements=%d controls=%d",
				client.Provider(), setting.Model, doc.ID, c.Requirements, c.Controls))
	})
}
