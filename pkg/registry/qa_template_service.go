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

Allowed question types: yes_no, yes_no_na, yes_no_evidence, scale, single_choice, multi_choice, numeric, date, evidence, attestation, free_text.

Schema:
{"requirements":[{"ref":"<requirement ref>","questions":[{"text":"...","intent":"...","type":"<type>","weight":1,"expectedEvidence":["..."],"options":[{"value":"...","label":"..."}],"assessment":{"criteria":"...","rules":[{"when":"answer == 'yes'","verdict":"compliant"}]}}]}]}

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
		ExpectedEvidence []string         `json:"expectedEvidence"`
		Options          []fwqa.Option    `json:"options"`
		Assessment       *fwqa.Assessment `json:"assessment"`
	}
)

// StartQATemplateJob generates an audit template from a framework's published
// requirements, one batch at a time, and returns the job id to poll. The result
// stored on the job is the template id.
func (s *Service) StartQATemplateJob(ctx context.Context, actorID gid.GID, frameworkRef string) (string, error) {
	client, setting, err := s.ingestPreflight(ctx, actorID)
	if err != nil {
		return "", err
	}

	// An audit is of PUBLISHED requirements. Resolve the framework's latest
	// published version and refuse if there is none.
	var versionID gid.GID
	var doc *fwflat.Framework
	err = s.db.WithConn(ctx, func(ctx context.Context, conn pg.Querier) error {
		scope := s.platformScope()
		var framework coredata.Framework
		if err := framework.LoadByReferenceID(ctx, conn, scope, frameworkRef); err != nil {
			return err
		}
		var pub coredata.FrameworkVersion
		if err := pub.LoadLatestPublished(ctx, conn, framework.ID); err != nil {
			return fmt.Errorf("%w: %q has no published version to audit", ErrInvalidInput, frameworkRef)
		}
		versionID = pub.ID
		return nil
	})
	if err != nil {
		return "", err
	}
	if doc, err = s.ExportFlat(ctx, frameworkRef); err != nil {
		return "", err
	}

	jobID := gid.New(s.cfg.PlatformTenant, coredata.IngestJobEntityType).String()
	s.startJobRecord(ctx, jobID, coredata.JobKindQATemplate, doc.Name, frameworkRef, actorID)

	go func() {
		bg := context.Background()
		templateID, gerr := s.runQAGeneration(bg, jobID, client, setting, frameworkRef, versionID, doc)
		s.finishQAJob(bg, jobID, templateID, gerr)
	}()

	return jobID, nil
}

// runQAGeneration walks the requirements in batches, asks the model per batch,
// assembles a validated fwqa.Template and persists it.
func (s *Service) runQAGeneration(ctx context.Context, jobID string, client llm.Client, setting *coredata.LLMSetting, frameworkRef string, versionID gid.GID, doc *fwflat.Framework) (gid.GID, error) {
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
			return gid.Nil, fmt.Errorf("qa generation: %w", err)
		}
		var parsed qaGenResponse
		if err := json.Unmarshal([]byte(stripFences(resp.Text)), &parsed); err != nil {
			return gid.Nil, fmt.Errorf("qa generation: model returned invalid JSON: %w", err)
		}
		for _, r := range parsed.Requirements {
			questionsByReq[r.Ref] = append(questionsByReq[r.Ref], r.Questions...)
		}
		s.qaProgress(ctx, jobID, hi, total)
	}

	tpl := s.assembleQATemplate(frameworkRef, doc, questionsByReq, catName, catOrder, setting)
	if err := tpl.Validate(); err != nil {
		return gid.Nil, fmt.Errorf("generated template failed validation: %w", err)
	}
	return s.persistQATemplate(ctx, versionID, tpl)
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
		for i, gq := range byReq[req.Ref] {
			qtype := gq.Type
			if !fwqa.KnownTypes[qtype] {
				// Never trust an unknown type from the model; a free-text
				// fallback is always answerable and always scoreable manually.
				qtype = fwqa.TypeFreeText
			}
			q := fwqa.Question{
				ID:               fmt.Sprintf("q-%s-%d", req.Ref, i+1),
				Order:            len(sec.Questions) + 1,
				RequirementRef:   req.Ref,
				Text:             docextract.Sanitize(gq.Text),
				Intent:           docextract.Sanitize(gq.Intent),
				Type:             qtype,
				Weight:           gq.Weight,
				ExpectedEvidence: sanitizeSlice(gq.ExpectedEvidence),
				Options:          gq.Options,
				Assessment:       sanitizeAssessment(gq.Assessment),
			}
			sec.Questions = append(sec.Questions, q)
		}
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
			Status:             coredata.QATemplateStatusReady,
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

func (s *Service) finishQAJob(ctx context.Context, jobID string, templateID gid.GID, genErr error) {
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
		result, _ = json.Marshal(map[string]string{"templateId": templateID.String()})
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
