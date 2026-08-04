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
	"errors"
	"strconv"
	"strings"

	"go.meizon.cloud/registry/pkg/coredata"
	"go.meizon.cloud/registry/pkg/fwqa"
	"go.meizon.cloud/registry/pkg/llm"
)

// translateQATemplate translates a framework's canonical audit template into
// targetLang and stores it as that language's copy, so translating a framework
// also translates its QA scheme. Best-effort by contract: a no-op (nil) when the
// framework has no audit template. Only human-facing text is translated — ids,
// refs, types, verdicts, weights and the `when` expressions (including choice
// option VALUES) are preserved verbatim so scoring is language-independent.
func (s *Service) translateQATemplate(ctx context.Context, client llm.Client, setting *coredata.LLMSetting, frameworkRef, targetLang string) error {
	row, tpl, err := s.loadQATemplate(ctx, frameworkRef, "")
	if errors.Is(err, coredata.ErrResourceNotFound) {
		return nil // no audit template to translate
	}
	if err != nil {
		return err
	}

	fields := qaTranslatableFields(tpl)
	src := make([]string, len(fields))
	for i, p := range fields {
		src[i] = *p
	}
	translated, err := s.translateStrings(ctx, client, setting, targetLang, src)
	if err != nil {
		return err
	}
	for i, p := range fields {
		*p = translated[i]
	}
	tpl.Framework.Language = targetLang

	_, err = s.persistQATemplate(ctx, row.FrameworkVersionID, tpl, row.Status, targetLang)
	return err
}

// qaTranslatableFields returns pointers to every human-facing string in a
// template, in a stable order, so a translated slice can be written straight
// back. It deliberately excludes machine-meaningful values (option Value,
// `when` expressions, verdicts, refs, types).
func qaTranslatableFields(tpl *fwqa.Template) []*string {
	var out []*string
	add := func(p *string) {
		if strings.TrimSpace(*p) != "" {
			out = append(out, p)
		}
	}
	add(&tpl.Title)
	for si := range tpl.Sections {
		sec := &tpl.Sections[si]
		add(&sec.Name)
		for qi := range sec.Questions {
			q := &sec.Questions[qi]
			add(&q.Text)
			add(&q.Intent)
			for ei := range q.ExpectedEvidence {
				add(&q.ExpectedEvidence[ei])
			}
			for oi := range q.Options {
				add(&q.Options[oi].Label) // label only — Value is machine-meaningful
			}
			add(&q.Placeholder)
			if q.Attestation != nil {
				add(&q.Attestation.Statement)
			}
			if q.Assessment != nil {
				add(&q.Assessment.Criteria)
				for ri := range q.Assessment.Rubric {
					add(&q.Assessment.Rubric[ri].Descriptor)
				}
			}
		}
	}
	return out
}

// translateStrings translates a flat, order-stable list of strings into
// targetLang, batched through the same LLM step the framework translation uses.
// Order is preserved and any string the model drops keeps its source text, so
// the result is always the same length as the input.
func (s *Service) translateStrings(ctx context.Context, client llm.Client, setting *coredata.LLMSetting, targetLang string, in []string) ([]string, error) {
	out := make([]string, len(in))
	copy(out, in)
	for lo := 0; lo < len(in); lo += translateBatchSize {
		hi := min(lo+translateBatchSize, len(in))
		batch := make([]translateNode, 0, hi-lo)
		for i := lo; i < hi; i++ {
			if strings.TrimSpace(in[i]) == "" {
				continue
			}
			batch = append(batch, translateNode{Ref: strconv.Itoa(i), Name: in[i]})
		}
		if len(batch) == 0 {
			continue
		}
		res, err := s.stepTranslate(ctx, client, setting, batch, targetLang)
		if err != nil {
			return nil, err
		}
		for _, n := range res {
			if idx, e := strconv.Atoi(n.Ref); e == nil && idx >= 0 && idx < len(out) && strings.TrimSpace(n.Name) != "" {
				out[idx] = n.Name
			}
		}
	}
	return out, nil
}
