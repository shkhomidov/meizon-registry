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
	"go.meizon.cloud/registry/pkg/gid"
	"go.meizon.cloud/registry/pkg/iam"
	"go.meizon.cloud/registry/pkg/llm"
)

// CanonicalLanguage is the language the registry maps and reasons in. A
// framework's structure lives once, in the canonical record; every other
// language is a text-only overlay keyed by ref.
const CanonicalLanguage = "en"

// translateBatchSize: nodes per LLM call. Small enough that a single rejected
// batch is cheap to retry, large enough that a 142-requirement framework is
// ~10 calls rather than 142.
const translateBatchSize = 20

// translateNode is one unit of translatable text.
type translateNode struct {
	Kind        string `json:"-"`
	Ref         string `json:"ref"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type translateResult struct {
	Nodes []translateNode `json:"nodes"`
}

// TranslationView reports a framework's available languages to the console.
type TranslationView struct {
	SourceLanguage string                 `json:"sourceLanguage"`
	Canonical      string                 `json:"canonical"`
	Languages      []coredata.LanguageRow `json:"languages"`
}

// TranslationsOf lists the languages a framework's latest version carries.
func (s *Service) TranslationsOf(ctx context.Context, ref string) (TranslationView, error) {
	out := TranslationView{Canonical: CanonicalLanguage}
	err := s.db.WithConn(ctx, func(ctx context.Context, conn pg.Querier) error {
		scope := s.platformScope()

		var framework coredata.Framework
		if err := framework.LoadByReferenceID(ctx, conn, scope, ref); err != nil {
			return err
		}
		out.SourceLanguage = framework.SourceLanguage

		versionID, verr := s.latestVersionIDConn(ctx, conn, framework.ID)
		if verr != nil {
			return verr
		}
		rows, err := (coredata.FrameworkTranslations{}).LanguagesOfVersion(ctx, conn, scope, versionID)
		if err != nil {
			return err
		}
		out.Languages = rows
		return nil
	})
	if out.Languages == nil {
		out.Languages = []coredata.LanguageRow{}
	}
	return out, err
}

// StartTranslateJob translates a framework's latest version into targetLang,
// asynchronously. Returns a job id the console polls with the same status shape
// as document ingestion.
func (s *Service) StartTranslateJob(ctx context.Context, actorID gid.GID, ref, targetLang string) (string, error) {
	targetLang = strings.ToLower(strings.TrimSpace(targetLang))
	if targetLang == "" {
		return "", fmt.Errorf("%w: a target language is required", ErrInvalidInput)
	}

	client, setting, err := s.ingestPreflight(ctx, actorID)
	if err != nil {
		return "", err
	}

	// Authorize and snapshot the source document synchronously, so a permission
	// error is an immediate 403 rather than a job that fails a minute later.
	var doc *fwflat.Framework
	var versionID gid.GID
	var sourceLang string
	err = s.db.WithConn(ctx, func(ctx context.Context, conn pg.Querier) error {
		scope := s.platformScope()

		var framework coredata.Framework
		if err := framework.LoadByReferenceID(ctx, conn, scope, ref); err != nil {
			return err
		}
		if err := s.authorize(ctx, conn, actorID, iam.ActionControlEdit, framework.Region, framework.ID); err != nil {
			return err
		}
		sourceLang = framework.SourceLanguage
		id, err := s.latestVersionIDConn(ctx, conn, framework.ID)
		if err != nil {
			return err
		}
		versionID = id
		return nil
	})
	if err != nil {
		return "", err
	}
	if targetLang == sourceLang {
		return "", fmt.Errorf("%w: the framework is already authored in %q", ErrInvalidInput, targetLang)
	}

	doc, err = s.ExportFlat(ctx, ref)
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
	s.startJobRecord(ctx, job.id, coredata.JobKindTranslate, targetLang, ref, actorID)

	go func() {
		ctx := context.Background()
		err := s.runTranslate(ctx, job, client, setting, doc, versionID, targetLang)
		job.finish(nil, nil, "", err)
		if err == nil {
			s.auditTranslate(ctx, actorID, client, setting, ref, targetLang, len(collectNodes(doc)))
		}
	}()

	return job.id, nil
}

// collectNodes flattens a framework into the nodes that carry translatable
// text, in a stable order. The framework itself is node_ref "" so its own name
// and description translate too.
func collectNodes(doc *fwflat.Framework) []translateNode {
	nodes := []translateNode{{
		Kind: coredata.TranslationNodeFramework, Ref: "",
		Name: doc.Name, Description: doc.Category,
	}}
	for _, c := range doc.Categories {
		nodes = append(nodes, translateNode{Kind: coredata.TranslationNodeCategory, Ref: c.Ref, Name: c.Name})
	}
	for _, r := range doc.Requirements {
		nodes = append(nodes, translateNode{
			Kind: coredata.TranslationNodeRequirement, Ref: r.Ref,
			Name: r.Name, Description: r.Description,
		})
	}
	for _, c := range doc.Controls {
		nodes = append(nodes, translateNode{
			Kind: coredata.TranslationNodeControl, Ref: c.Ref,
			Name: c.Name, Description: c.Description,
		})
	}
	return nodes
}

// runTranslate drives the batched translation and persists the overlay.
func (s *Service) runTranslate(ctx context.Context, job *ingestJob, client llm.Client, setting *coredata.LLMSetting, doc *fwflat.Framework, versionID gid.GID, targetLang string) error {
	nodes := collectNodes(doc)
	total := (len(nodes) + translateBatchSize - 1) / translateBatchSize

	translated := make([]translateNode, 0, len(nodes))
	for i := 0; i < len(nodes); i += translateBatchSize {
		end := min(i+translateBatchSize, len(nodes))
		batch := nodes[i:end]
		job.set("translate", i/translateBatchSize+1, total)

		out, err := s.stepTranslate(ctx, client, setting, batch, targetLang)
		if err != nil {
			return fmt.Errorf("translate batch %d/%d: %w", i/translateBatchSize+1, total, err)
		}
		translated = append(translated, out...)
	}

	job.set("persist", 1, 1)
	return s.persistTranslation(ctx, versionID, targetLang, translated)
}

func translateUserPrompt(batch []translateNode, targetLang string) string {
	payload, _ := json.Marshal(struct {
		Nodes []translateNode `json:"nodes"`
	}{batch})
	return fmt.Sprintf("Target language: %s\n\nTranslate these nodes:\n%s", targetLang, payload)
}

// stepTranslate translates one batch and returns it with refs and kinds
// restored from the request.
//
// The model's refs are never trusted: the result is re-keyed against the batch
// that was sent, and a node the model failed to return keeps its ORIGINAL text
// rather than vanishing. A missing translation is a cosmetic gap; a dropped or
// re-keyed node would break control links and cross-mappings silently.
func (s *Service) stepTranslate(ctx context.Context, client llm.Client, setting *coredata.LLMSetting, batch []translateNode, targetLang string) ([]translateNode, error) {
	var last error
	for attempt := 0; attempt < 3; attempt++ {
		p := translateUserPrompt(batch, targetLang)
		if attempt > 0 && last != nil {
			p += fmt.Sprintf("\n\nYour previous output was rejected: %s. Return ONLY corrected JSON.", last)
		}
		resp, err := client.Generate(ctx, llm.Request{
			System:      translateSystemPrompt(setting),
			Prompt:      p,
			MaxTokens:   stepTokens(setting, 2048),
			Temperature: 0,
		})
		if err != nil {
			return nil, err
		}

		var out translateResult
		if err := json.Unmarshal([]byte(stripFences(resp.Text)), &out); err != nil {
			last = fmt.Errorf("not valid JSON: %v", err)
			continue
		}
		if len(out.Nodes) == 0 {
			last = fmt.Errorf("no nodes returned")
			continue
		}

		byRef := map[string]translateNode{}
		for _, n := range out.Nodes {
			byRef[n.Ref] = n
		}

		merged := make([]translateNode, 0, len(batch))
		missing := 0
		for _, src := range batch {
			t, ok := byRef[src.Ref]
			if !ok || strings.TrimSpace(t.Name) == "" {
				// Keep the source text under the source ref.
				merged = append(merged, src)
				missing++
				continue
			}
			merged = append(merged, translateNode{
				Kind: src.Kind, Ref: src.Ref, // always the ref we sent
				Name:        t.Name,
				Description: t.Description,
			})
		}

		// A couple of gaps are tolerable; wholesale ref drift means the model
		// ignored the contract and the batch is worth retrying.
		if missing*2 > len(batch) {
			last = fmt.Errorf("%d of %d refs came back changed or missing", missing, len(batch))
			continue
		}
		return merged, nil
	}
	return nil, last
}

// persistTranslation writes the overlay for one language in a single
// transaction, replacing whatever was there before.
func (s *Service) persistTranslation(ctx context.Context, versionID gid.GID, language string, nodes []translateNode) error {
	return s.db.WithTx(ctx, func(ctx context.Context, tx pg.Tx) error {
		scope := s.platformScope()
		now := time.Now()

		if err := (coredata.FrameworkTranslation{}).DeleteByVersionAndLanguage(ctx, tx, scope, versionID, language); err != nil {
			return err
		}
		for _, n := range nodes {
			row := coredata.FrameworkTranslation{
				ID:                 gid.New(s.cfg.PlatformTenant, coredata.FrameworkTranslationEntityType),
				FrameworkVersionID: versionID,
				Language:           language,
				NodeKind:           n.Kind,
				NodeRef:            n.Ref,
				Name:               n.Name,
				Description:        n.Description,
				Origin:             "ai",
				CreatedAt:          now,
			}
			if err := row.Upsert(ctx, tx, scope); err != nil {
				return fmt.Errorf("%s %q: %w", n.Kind, n.Ref, err)
			}
		}
		return nil
	})
}

// ExportFlatIn renders a framework in the requested language: the canonical
// structure with the language's text overlaid by ref. An empty language, or the
// framework's own source language, returns the canonical record unchanged.
func (s *Service) ExportFlatIn(ctx context.Context, ref, language string) (*fwflat.Framework, error) {
	doc, err := s.ExportFlat(ctx, ref)
	if err != nil {
		return nil, err
	}
	language = strings.ToLower(strings.TrimSpace(language))
	if language == "" {
		return doc, nil
	}

	var overlay coredata.FrameworkTranslations
	err = s.db.WithConn(ctx, func(ctx context.Context, conn pg.Querier) error {
		scope := s.platformScope()

		var framework coredata.Framework
		if err := framework.LoadByReferenceID(ctx, conn, scope, ref); err != nil {
			return err
		}
		if language == framework.SourceLanguage {
			return nil // the canonical record already is this language
		}
		versionID, err := s.latestVersionIDConn(ctx, conn, framework.ID)
		if err != nil {
			return err
		}
		return overlay.LoadAllByVersionAndLanguage(ctx, conn, scope, versionID, language)
	})
	if err != nil {
		return nil, err
	}
	if len(overlay) == 0 {
		return doc, nil
	}

	applyOverlay(doc, overlay)
	doc.Language = language
	return doc, nil
}

// applyOverlay swaps in translated text by ref. Structure is never touched: a
// node with no translation keeps its canonical text rather than going blank.
func applyOverlay(doc *fwflat.Framework, overlay coredata.FrameworkTranslations) {
	byKey := map[string]*coredata.FrameworkTranslation{}
	for _, t := range overlay {
		byKey[t.NodeKind+"\x00"+t.NodeRef] = t
	}
	pick := func(kind, ref string) *coredata.FrameworkTranslation {
		return byKey[kind+"\x00"+ref]
	}

	if t := pick(coredata.TranslationNodeFramework, ""); t != nil {
		if t.Name != "" {
			doc.Name = t.Name
		}
		if t.Description != "" {
			doc.Category = t.Description
		}
	}
	for i := range doc.Categories {
		if t := pick(coredata.TranslationNodeCategory, doc.Categories[i].Ref); t != nil && t.Name != "" {
			doc.Categories[i].Name = t.Name
		}
	}
	for i := range doc.Requirements {
		t := pick(coredata.TranslationNodeRequirement, doc.Requirements[i].Ref)
		if t == nil {
			continue
		}
		if t.Name != "" {
			doc.Requirements[i].Name = t.Name
		}
		if t.Description != "" {
			doc.Requirements[i].Description = t.Description
		}
	}
	for i := range doc.Controls {
		t := pick(coredata.TranslationNodeControl, doc.Controls[i].Ref)
		if t == nil {
			continue
		}
		if t.Name != "" {
			doc.Controls[i].Name = t.Name
		}
		if t.Description != "" {
			doc.Controls[i].Description = t.Description
		}
	}
}

func (s *Service) auditTranslate(ctx context.Context, actorID gid.GID, client llm.Client, setting *coredata.LLMSetting, ref, language string, nodes int) {
	_ = s.db.WithConn(ctx, func(ctx context.Context, conn pg.Querier) error {
		return s.recordAudit(ctx, conn, s.platformScope(), actorID, "framework.translate", ref,
			fmt.Sprintf("language=%s nodes=%d provider=%s model=%s", language, nodes, client.Provider(), setting.Model))
	})
}

// EnsureCanonicalTranslation starts an English translation when a framework
// does not already have one.
//
// English is the canonical language: cross-mapping runs on it, so a framework
// without an English record cannot be mapped against anything. Making it
// automatic means that never has to be remembered — and skipping frameworks
// already authored in English means it is never paid for twice.
//
// Best-effort and asynchronous: a framework must still be created when the LLM
// is unconfigured or the translation fails. Returns the job id when one starts.
func (s *Service) EnsureCanonicalTranslation(ctx context.Context, actorID gid.GID, ref string) string {
	needed, err := s.needsCanonicalTranslation(ctx, ref)
	if err != nil {
		s.logger.WarnCtx(ctx, fmt.Sprintf("cannot check translations for %q: %v", ref, err))
		return ""
	}
	if !needed {
		return ""
	}

	jobID, err := s.StartTranslateJob(ctx, actorID, ref, CanonicalLanguage)
	if err != nil {
		// Most often: no LLM configured. Worth saying, never worth failing on.
		s.logger.WarnCtx(ctx, fmt.Sprintf("cannot start the English translation of %q: %v", ref, err))
		return ""
	}
	s.logger.InfoCtx(ctx, fmt.Sprintf("started the English translation of %q", ref))
	return jobID
}

// needsCanonicalTranslation reports whether a framework lacks an English
// record. A framework authored in English already IS the canonical record.
func (s *Service) needsCanonicalTranslation(ctx context.Context, ref string) (bool, error) {
	var needed bool
	err := s.db.WithConn(ctx, func(ctx context.Context, conn pg.Querier) error {
		scope := s.platformScope()

		var framework coredata.Framework
		if err := framework.LoadByReferenceID(ctx, conn, scope, ref); err != nil {
			return err
		}
		if framework.SourceLanguage == CanonicalLanguage {
			return nil // already English — translating it would duplicate it
		}

		// A translation already under way counts as covered. Without this a
		// second trigger starts an identical run: the same LLM calls billed
		// twice, both racing to write the same overlay rows.
		running, err := (coredata.IngestJob{}).HasRunning(ctx, conn, scope, coredata.JobKindTranslate, ref)
		if err != nil {
			return err
		}
		if running {
			return nil
		}

		versionID, verr := s.latestVersionIDConn(ctx, conn, framework.ID)
		if verr != nil {
			return verr
		}
		rows, err := (coredata.FrameworkTranslations{}).LanguagesOfVersion(ctx, conn, scope, versionID)
		if err != nil {
			return err
		}
		for _, r := range rows {
			if r.Language == CanonicalLanguage {
				return nil // already translated
			}
		}
		needed = true
		return nil
	})
	return needed, err
}

// FrameworksMissingCanonical lists frameworks with no English record, for the
// backfill action.
func (s *Service) FrameworksMissingCanonical(ctx context.Context) ([]string, error) {
	var all coredata.Frameworks
	if err := s.db.WithConn(ctx, func(ctx context.Context, conn pg.Querier) error {
		return all.LoadAll(ctx, conn, s.platformScope())
	}); err != nil {
		return nil, err
	}

	var refs []string
	for _, f := range all {
		needed, err := s.needsCanonicalTranslation(ctx, f.ReferenceID)
		if err != nil || !needed {
			continue
		}
		refs = append(refs, f.ReferenceID)
	}
	return refs, nil
}

// TranslateMissingCanonical starts an English translation for every framework
// that lacks one. Returns the refs it started.
func (s *Service) TranslateMissingCanonical(ctx context.Context, actorID gid.GID) ([]string, error) {
	refs, err := s.FrameworksMissingCanonical(ctx)
	if err != nil {
		return nil, err
	}
	started := make([]string, 0, len(refs))
	for _, ref := range refs {
		if jobID := s.EnsureCanonicalTranslation(ctx, actorID, ref); jobID != "" {
			started = append(started, ref)
		}
	}
	return started, nil
}

// HasCanonical reports whether a framework can be read in the canonical
// language — either it was authored in English, or an English overlay exists.
//
// ExportFlatIn falls back to the stored text when an overlay is missing, which
// is right for display but wrong for mapping: comparing Russian against English
// retrieves nothing and would be reported as "no overlap" when the truth is
// "not comparable". Callers that reason across frameworks must check first.
func (s *Service) HasCanonical(ctx context.Context, ref string) (bool, error) {
	var ok bool
	err := s.db.WithConn(ctx, func(ctx context.Context, conn pg.Querier) error {
		scope := s.platformScope()

		var framework coredata.Framework
		if err := framework.LoadByReferenceID(ctx, conn, scope, ref); err != nil {
			return err
		}
		if framework.SourceLanguage == CanonicalLanguage {
			ok = true
			return nil
		}
		versionID, err := s.latestVersionIDConn(ctx, conn, framework.ID)
		if err != nil {
			return err
		}
		rows, err := (coredata.FrameworkTranslations{}).LanguagesOfVersion(ctx, conn, scope, versionID)
		if err != nil {
			return err
		}
		for _, r := range rows {
			if r.Language == CanonicalLanguage {
				ok = true
			}
		}
		return nil
	})
	return ok, err
}
