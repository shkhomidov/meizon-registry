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
	"go.meizon.cloud/registry/pkg/docextract"
	"go.meizon.cloud/registry/pkg/fwflat"
	"go.meizon.cloud/registry/pkg/fwqa"
	"go.meizon.cloud/registry/pkg/gid"
	"go.meizon.cloud/registry/pkg/llm"
)

// qaGenBatchSize is the number of requirements sent to the model per call.
// Small, like the mapping adjudicator, so one weak requirement cannot derail a
// whole framework's worth of questions and progress advances steadily.
const qaGenBatchSize = 4

// qaTemplateContract is the system prompt for generating audit questions from
// requirements. It is deliberately distinct from qaContract (the framework
// pipeline's quality-review step, driven by LLMSetting.QAInstruction) — a
// different task with a different output shape, so it does not share that
// instruction. A dedicated override field can be added later if operators need
// to tune it.
const qaTemplateContract = `You are a compliance audit expert. For each requirement given, produce the ordered questions an auditor would ask to verify it — enough to establish compliance, no filler. Choose an appropriate question type per question and give assessment criteria. Return ONLY JSON matching the schema, no markdown fences, no commentary.

EVERY requirement you are given must appear in the output with at least one question — never return a requirement with an empty questions array.

Model the audit as a conversation: a question may branch to a follow-up that is only asked when a condition holds (for example, when the answer is "no" or the verdict is "partial"). Emit the follow-up as a later question in the SAME requirement, mark it "conditional": true, and point to it from the triggering question's "followUps" using "ask": <1-based position of that follow-up within this requirement's questions array>. A conditional question is asked ONLY when a follow-up reaches it, so do not also rely on it in the main sequence.

Allowed question types: yes_no, yes_no_na, yes_no_evidence, scale, single_choice, multi_choice, numeric, date, evidence, attestation, free_text.

Schema:
{"requirements":[{"ref":"<requirement ref>","questions":[{"text":"...","intent":"...","type":"<type>","weight":1,"conditional":false,"expectedEvidence":["..."],"options":[{"value":"...","label":"..."}],"assessment":{"criteria":"...","rules":[{"when":"answer == 'yes'","verdict":"compliant"}]},"followUps":[{"when":"answer == 'no'","ask":2}]}]}]}

Verdicts: compliant, partial, non_compliant, not_applicable. The "when" language allows: answer, verdict, score, value, evidence.count, selected.count, attested; operators == != < <= > >= && || and 'in [..]' / 'superset [..]'. Keep questions concise and answerable.`

// qaGenRequirement / qaGenQuestion / qaGenResponse mirror the JSON the model
// returns; they are the untrusted wire shape, converted to fwqa afterwards.
type (
	qaGenResponse struct {
		Requirements []struct {
			Ref       string          `json:"ref"`
			Questions []qaGenQuestion `json:"questions"`
		} `json:"requirements"`
	}
	qaGenQuestion struct {
		Text             string           `json:"text"`
		Intent           string           `json:"intent"`
		Type             string           `json:"type"`
		Weight           int              `json:"weight"`
		Conditional      bool             `json:"conditional"`
		ExpectedEvidence []string         `json:"expectedEvidence"`
		Options          []fwqa.Option    `json:"options"`
		Assessment       *fwqa.Assessment `json:"assessment"`
		FollowUps        []qaGenFollowUp  `json:"followUps"`
	}
	// qaGenFollowUp is the model's branch: when the condition holds, jump to the
	// question at 1-based position Ask within the SAME requirement's list. The
	// index is resolved to a deterministic question id at assembly time — the
	// model never sees our ids.
	qaGenFollowUp struct {
		When string `json:"when"`
		Ask  int    `json:"ask"`
	}
)

// qaCoverageReport is the per-generation detail stored on the job result: how
// many requirements and questions were produced, and which requirements the
// model left empty so we had to backfill (and how). It answers "what exactly did
// this run generate for each requirement".
type (
	qaCoverageReport struct {
		Requirements int              `json:"requirements"`
		Questions    int              `json:"questions"`
		Backfilled   []qaBackfillNote `json:"backfilled,omitempty"`
	}
	qaBackfillNote struct {
		Ref       string `json:"ref"`
		How       string `json:"how"` // "reask" | "synthesized"
		Questions int    `json:"questions"`
	}
)

// StartQATemplateJob generates an audit template for a framework's current
// (latest) version — draft or published — one batch at a time, and returns the
// job id to poll. The result stored on the job is the template id. The audit is
// authored alongside the requirements now, so it deliberately does NOT require a
// published version; it targets whatever the latest version is.
func (s *Service) StartQATemplateJob(ctx context.Context, actorID gid.GID, frameworkRef string) (string, error) {
	client, setting, err := s.ingestPreflight(ctx, actorID)
	if err != nil {
		return "", err
	}

	var versionID gid.GID
	err = s.db.WithConn(ctx, func(ctx context.Context, conn pg.Querier) error {
		scope := s.platformScope()
		var framework coredata.Framework
		if err := framework.LoadByReferenceID(ctx, conn, scope, frameworkRef); err != nil {
			return err
		}
		versionID, err = s.latestVersionIDConn(ctx, conn, framework.ID)
		return err
	})
	if err != nil {
		return "", err
	}
	// ExportFlat resolves the same latest version, so the doc and versionID agree.
	doc, err := s.ExportFlat(ctx, frameworkRef)
	if err != nil {
		return "", err
	}

	return s.startQATemplateJobForVersion(ctx, actorID, client, setting, frameworkRef, versionID, doc), nil
}

// startQATemplateJobForVersion records and launches the async generation for a
// specific version + already-resolved doc. Shared by the manual entrypoint above
// and the in-flow auto-trigger, so both produce identical jobs on the jobs page.
func (s *Service) startQATemplateJobForVersion(ctx context.Context, actorID gid.GID, client llm.Client, setting *coredata.LLMSetting, frameworkRef string, versionID gid.GID, doc *fwflat.Framework) string {
	jobID := gid.New(s.cfg.PlatformTenant, coredata.IngestJobEntityType).String()
	s.startJobRecord(ctx, jobID, coredata.JobKindQATemplate, doc.Name, frameworkRef, actorID)

	go func() {
		bg := context.Background()
		templateID, report, gerr := s.runQAGeneration(bg, jobID, client, setting, frameworkRef, versionID, doc)
		s.finishQAJob(bg, jobID, templateID, report, gerr)
	}()

	return jobID
}

// AutoStartQATemplate best-effort launches an audit-template job for a freshly
// created draft version, so questions are ready to review beside the
// requirements. It is silent when no LLM is configured (a manual import keeps the
// Generate button) or when the framework has no requirements to audit. It never
// blocks or fails the caller's accept flow.
func (s *Service) AutoStartQATemplate(ctx context.Context, actorID gid.GID, frameworkRef string, versionID gid.GID, doc *fwflat.Framework) {
	if doc == nil || len(doc.Requirements) == 0 {
		return
	}
	client, setting, err := s.ingestPreflight(ctx, actorID)
	if err != nil {
		s.logger.InfoCtx(ctx, "skipping automatic QA template generation: "+err.Error())
		return
	}
	s.startQATemplateJobForVersion(ctx, actorID, client, setting, frameworkRef, versionID, doc)
}

// runQAGeneration walks the requirements in batches, asks the model per batch,
// guarantees every requirement ends up with at least one question, assembles a
// validated fwqa.Template and persists it. It returns a coverage report of what
// was produced for each requirement so the job result can surface it.
func (s *Service) runQAGeneration(ctx context.Context, jobID string, client llm.Client, setting *coredata.LLMSetting, frameworkRef string, versionID gid.GID, doc *fwflat.Framework) (gid.GID, *qaCoverageReport, error) {
	catName := map[string]string{}
	catOrder := map[string]int{}
	for i, c := range doc.Categories {
		catName[c.Ref] = c.Name
		catOrder[c.Ref] = i + 1
	}

	// questionsByReq collects the model's questions for each requirement ref.
	questionsByReq := map[string][]qaGenQuestion{}
	total := len(doc.Requirements)
	for lo := 0; lo < total; lo += qaGenBatchSize {
		hi := min(lo+qaGenBatchSize, total)
		batch := doc.Requirements[lo:hi]

		resp, err := client.Generate(ctx, llm.Request{
			System: qaTemplateContract,
			Prompt: qaBatchPrompt(batch),
		})
		if err != nil {
			return gid.Nil, nil, fmt.Errorf("qa generation: %w", err)
		}
		var parsed qaGenResponse
		if err := json.Unmarshal([]byte(stripFences(resp.Text)), &parsed); err != nil {
			return gid.Nil, nil, fmt.Errorf("qa generation: model returned invalid JSON: %w", err)
		}
		for _, r := range parsed.Requirements {
			questionsByReq[r.Ref] = append(questionsByReq[r.Ref], r.Questions...)
		}
		s.qaProgress(ctx, jobID, hi, total)
	}

	// Coverage pass: no requirement may be left without questions. Re-ask each
	// empty one on its own (the model reliably answers a single requirement),
	// then synthesize a baseline question for any that are still empty.
	report := s.ensureRequirementCoverage(ctx, client, doc, questionsByReq)

	tpl := s.assembleQATemplate(frameworkRef, doc, questionsByReq, catName, catOrder, setting)
	if err := tpl.Validate(); err != nil {
		return gid.Nil, nil, fmt.Errorf("generated template failed validation: %w", err)
	}
	templateID, err := s.persistQATemplate(ctx, versionID, tpl)
	if err != nil {
		return gid.Nil, nil, err
	}
	return templateID, report, nil
}

// ensureRequirementCoverage backfills requirements the model returned empty and
// records how each gap was closed. It mutates questionsByReq in place and returns
// the coverage report.
func (s *Service) ensureRequirementCoverage(ctx context.Context, client llm.Client, doc *fwflat.Framework, questionsByReq map[string][]qaGenQuestion) *qaCoverageReport {
	report := &qaCoverageReport{Requirements: len(doc.Requirements)}

	for i := range doc.Requirements {
		req := doc.Requirements[i]
		if len(questionsByReq[req.Ref]) > 0 {
			continue
		}

		// Re-ask this single requirement once. Because only one requirement is in
		// the prompt, accept whatever questions come back regardless of the ref
		// the model echoes.
		if qs := s.reaskOne(ctx, client, req); len(qs) > 0 {
			questionsByReq[req.Ref] = qs
			report.Backfilled = append(report.Backfilled, qaBackfillNote{Ref: req.Ref, How: "reask", Questions: len(qs)})
			continue
		}

		// Still nothing — synthesize a baseline so the requirement is never silent.
		questionsByReq[req.Ref] = []qaGenQuestion{synthesizedQuestion(req)}
		report.Backfilled = append(report.Backfilled, qaBackfillNote{Ref: req.Ref, How: "synthesized", Questions: 1})
	}

	for _, qs := range questionsByReq {
		report.Questions += len(qs)
	}
	return report
}

// reaskOne asks the model for one requirement in isolation and returns its
// questions, or nil on any error or empty response — the caller then synthesizes.
func (s *Service) reaskOne(ctx context.Context, client llm.Client, req fwflat.Requirement) []qaGenQuestion {
	resp, err := client.Generate(ctx, llm.Request{
		System: qaTemplateContract,
		Prompt: qaBatchPrompt([]fwflat.Requirement{req}),
	})
	if err != nil {
		return nil
	}
	var parsed qaGenResponse
	if err := json.Unmarshal([]byte(stripFences(resp.Text)), &parsed); err != nil {
		return nil
	}
	var out []qaGenQuestion
	for _, r := range parsed.Requirements {
		out = append(out, r.Questions...)
	}
	return out
}

// synthesizedQuestion is the last-resort baseline for a requirement the model
// would not answer: a single evidence-backed yes/no whose verdict is decided by
// its own rules, so the requirement still participates in scoring.
func synthesizedQuestion(req fwflat.Requirement) qaGenQuestion {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = req.Ref
	}
	return qaGenQuestion{
		Text:             fmt.Sprintf("Is the requirement %q implemented, and can you provide evidence?", name),
		Intent:           "Baseline check inserted automatically because the model returned no questions for this requirement.",
		Type:             fwqa.TypeYesNoEvidence,
		Weight:           1,
		ExpectedEvidence: []string{"Supporting documentation or records"},
		Assessment: &fwqa.Assessment{
			Criteria: "Compliant only if the requirement is implemented and evidence is provided.",
			Rules: []fwqa.Rule{
				{When: "answer == 'yes'", Verdict: fwqa.VerdictCompliant},
				{When: "answer == 'no'", Verdict: fwqa.VerdictNonCompliant},
			},
		},
	}
}

// assembleQATemplate turns the model's per-requirement questions into a
// validated fwqa.Template: one section per category, questions ordered, ids and
// follow-up wiring assigned deterministically. Every string is sanitized before
// it can reach jsonb.
func (s *Service) assembleQATemplate(frameworkRef string, doc *fwflat.Framework, byReq map[string][]qaGenQuestion, catName map[string]string, catOrder map[string]int, setting *coredata.LLMSetting) *fwqa.Template {
	secByRef := map[string]*fwqa.Section{}
	var sections []*fwqa.Section
	ensureSection := func(ref string) *fwqa.Section {
		if s := secByRef[ref]; s != nil {
			return s
		}
		name := catName[ref]
		if name == "" {
			name = ref
		}
		sec := &fwqa.Section{Ref: ref, Name: name, Order: catOrder[ref]}
		secByRef[ref] = sec
		sections = append(sections, sec)
		return sec
	}

	for _, req := range doc.Requirements {
		secRef := req.Category
		if secRef == "" {
			secRef = "_uncategorized"
		}
		sec := ensureSection(secRef)
		gqs := byReq[req.Ref]
		base := len(sec.Questions) // question orders continue across a section

		// Build this requirement's questions first, then reconcile the follow-up
		// wiring against them, so a branch can only target a sibling question.
		built := make([]fwqa.Question, 0, len(gqs))
		qid := func(idx1 int) string { return fmt.Sprintf("q-%s-%d", req.Ref, idx1) }
		for i, gq := range gqs {
			qtype := gq.Type
			if !fwqa.KnownTypes[qtype] {
				// Never trust an unknown type from the model; a free-text
				// fallback is always answerable and always scoreable manually.
				qtype = fwqa.TypeFreeText
			}
			built = append(built, fwqa.Question{
				ID:               qid(i + 1),
				Order:            base + i + 1,
				RequirementRef:   req.Ref,
				Text:             docextract.Sanitize(gq.Text),
				Intent:           docextract.Sanitize(gq.Intent),
				Type:             qtype,
				Weight:           gq.Weight,
				Conditional:      gq.Conditional,
				ExpectedEvidence: sanitizeSlice(gq.ExpectedEvidence),
				Options:          gq.Options,
				Assessment:       sanitizeAssessment(gq.Assessment),
				FollowUps:        resolveFollowUps(req.Ref, i, gqs),
			})
		}
		reconcileConditionals(built)
		sec.Questions = append(sec.Questions, built...)
	}

	out := &fwqa.Template{
		Schema:      fwqa.SchemaMarker,
		ID:          frameworkRef + "-audit",
		Framework:   fwqa.FrameworkRef{ID: doc.ID, Version: doc.Version, Language: doc.Language},
		Title:       doc.Name + " — Compliance Audit",
		GeneratedBy: "ai",
		Model:       setting.Model,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Scale: &fwqa.Scale{Kind: "maturity", Levels: []fwqa.ScaleLevel{
			{Value: 0, Label: "Absent"}, {Value: 1, Label: "Initial"}, {Value: 2, Label: "Repeatable"},
			{Value: 3, Label: "Defined"}, {Value: 4, Label: "Managed"}, {Value: 5, Label: "Optimized"},
		}},
		VerdictModel: defaultVerdictModel(),
	}
	for _, sec := range sections {
		out.Sections = append(out.Sections, *sec)
	}
	return out
}

// resolveFollowUps turns the model's index-based branches for the question at
// position self (0-based) into fwqa follow-ups with deterministic askId targets.
// It drops anything that would produce an invalid template: a self-loop, an
// out-of-range target, or a condition the evaluator cannot parse — better to lose
// one branch than fail the whole generation.
func resolveFollowUps(reqRef string, self int, gqs []qaGenQuestion) []fwqa.FollowUp {
	src := gqs[self].FollowUps
	if len(src) == 0 {
		return nil
	}
	var out []fwqa.FollowUp
	for _, fu := range src {
		when := docextract.Sanitize(fu.When)
		if fwqa.CheckExpr(when) != nil {
			continue
		}
		if fu.Ask < 1 || fu.Ask > len(gqs) || fu.Ask-1 == self {
			continue
		}
		out = append(out, fwqa.FollowUp{
			When:  when,
			AskID: fmt.Sprintf("q-%s-%d", reqRef, fu.Ask),
		})
	}
	return out
}

// reconcileConditionals clears the conditional flag on any question that no
// sibling follow-up actually reaches. Validate() rejects an unreachable
// conditional question; a model that marks one conditional but forgets to branch
// to it would otherwise sink the run, so we demote it to a normal question.
func reconcileConditionals(qs []fwqa.Question) {
	targeted := map[string]bool{}
	for _, q := range qs {
		for _, f := range q.FollowUps {
			if f.AskID != "" {
				targeted[f.AskID] = true
			}
		}
	}
	for i := range qs {
		if qs[i].Conditional && !targeted[qs[i].ID] {
			qs[i].Conditional = false
		}
	}
}

func defaultVerdictModel() *fwqa.VerdictModel {
	f := func(v float64) *float64 { return &v }
	return &fwqa.VerdictModel{
		Verdicts: []string{fwqa.VerdictCompliant, fwqa.VerdictPartial, fwqa.VerdictNonCompliant, fwqa.VerdictNotApplicable},
		ScoreOf: map[string]*float64{
			fwqa.VerdictCompliant: f(1.0), fwqa.VerdictPartial: f(0.5),
			fwqa.VerdictNonCompliant: f(0.0), fwqa.VerdictNotApplicable: nil,
		},
		RequirementRollup:   "weighted_mean",
		NotApplicablePolicy: "exclude",
	}
}

// persistQATemplate writes the template row and replaces its questions in one
// transaction, marking it ready.
func (s *Service) persistQATemplate(ctx context.Context, versionID gid.GID, tpl *fwqa.Template) (gid.GID, error) {
	var templateID gid.GID
	err := s.db.WithTx(ctx, func(ctx context.Context, tx pg.Tx) error {
		scope := s.platformScope()
		now := time.Now()

		scaleJSON, _ := json.Marshal(tpl.Scale)
		vmJSON, _ := json.Marshal(tpl.VerdictModel)

		row := coredata.QATemplate{
			ID:                 gid.New(s.cfg.PlatformTenant, coredata.QATemplateEntityType),
			FrameworkVersionID: versionID,
			FrameworkRef:       tpl.Framework.ID,
			Title:              tpl.Title,
			Status:             coredata.QATemplateStatusDraft,
			GeneratedBy:        tpl.GeneratedBy,
			Model:              tpl.Model,
			Scale:              scaleJSON,
			VerdictModel:       vmJSON,
			CreatedAt:          now,
			UpdatedAt:          now,
		}
		if err := row.UpsertTemplate(ctx, tx, scope); err != nil {
			return err
		}
		templateID = row.ID

		questions := qaQuestionRows(row.ID, s.cfg.PlatformTenant, tpl, now)
		return coredata.QATemplate{}.ReplaceQuestions(ctx, tx, scope, row.ID, questions)
	})
	return templateID, err
}

// qaQuestionRows flattens a template to storable rows, stripping the structural
// fields into columns and leaving the rest as the body.
func qaQuestionRows(templateID gid.GID, tenant gid.TenantID, tpl *fwqa.Template, now time.Time) coredata.QAQuestions {
	var rows coredata.QAQuestions
	for _, sec := range tpl.Sections {
		for _, q := range sec.Questions {
			body, _ := json.Marshal(q)
			rows = append(rows, &coredata.QAQuestion{
				ID:             gid.New(tenant, coredata.QAQuestionEntityType),
				TemplateID:     templateID,
				SectionRef:     sec.Ref,
				SectionName:    sec.Name,
				SectionOrder:   sec.Order,
				Ord:            q.Order,
				RequirementRef: q.RequirementRef,
				ControlRef:     q.ControlRef,
				Type:           q.Type,
				Body:           body,
				CreatedAt:      now,
			})
		}
	}
	return rows
}

func (s *Service) qaProgress(ctx context.Context, jobID string, current, total int) {
	id, err := gid.ParseGID(jobID)
	if err != nil {
		return
	}
	_ = s.db.WithConn(ctx, func(ctx context.Context, conn pg.Querier) error {
		return (coredata.IngestJob{}).UpdateProgress(ctx, conn, s.platformScope(), id, "questions", current, total)
	})
}

func (s *Service) finishQAJob(ctx context.Context, jobID string, templateID gid.GID, report *qaCoverageReport, genErr error) {
	id, err := gid.ParseGID(jobID)
	if err != nil {
		return
	}
	status := coredata.JobStatusDone
	var result []byte
	errText := ""
	if genErr != nil {
		status = coredata.JobStatusError
		errText = genErr.Error()
	} else {
		result, _ = json.Marshal(map[string]any{
			"templateId": templateID.String(),
			"coverage":   report,
		})
	}
	if err := s.db.WithConn(ctx, func(ctx context.Context, conn pg.Querier) error {
		return (coredata.IngestJob{}).Finish(ctx, conn, s.platformScope(), id, status, result, errText)
	}); err != nil {
		s.logger.ErrorCtx(ctx, "cannot record qa job completion: "+err.Error())
	}
}

func qaBatchPrompt(batch []fwflat.Requirement) string {
	var b strings.Builder
	b.WriteString("Requirements:\n")
	for _, r := range batch {
		fmt.Fprintf(&b, "- ref: %s\n  name: %s\n  description: %s\n", r.Ref, r.Name, r.Description)
	}
	return b.String()
}

func sanitizeSlice(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	for _, s := range in {
		out = append(out, docextract.Sanitize(s))
	}
	return out
}

func sanitizeAssessment(a *fwqa.Assessment) *fwqa.Assessment {
	if a == nil {
		return nil
	}
	a.Criteria = docextract.Sanitize(a.Criteria)
	return a
}
