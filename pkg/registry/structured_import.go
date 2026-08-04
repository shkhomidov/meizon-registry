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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"go.meizon.cloud/registry/pkg/coredata"
	"go.meizon.cloud/registry/pkg/fwflat"
	"go.meizon.cloud/registry/pkg/gid"
)

// A framework JSON export round-trips: the console downloads a flat
// meizon-framework/v2 file, and re-uploading it should reproduce the framework,
// not ask a language model to re-derive it from its own output. That would be
// slower, cost tokens, and could hallucinate a difference. So a JSON upload that
// already IS a framework skips the pipeline entirely and is imported as authored.
//
// Only the flat format is detected here. A nested fwschema document has its own
// route (POST /frameworks/import); anything else is not a framework and falls
// through to the normal text-or-generate path.

// DetectFrameworkJSON reports whether an upload is a flat framework export and,
// if so, returns the parsed document. It is deliberately strict: it triggers
// only on the exact "$schema": "meizon-framework/v2" marker, so an arbitrary
// JSON document (which the generator can still read as source text) is never
// mistaken for a framework and silently imported without the model.
func DetectFrameworkJSON(filename string, data []byte) (*fwflat.Framework, bool) {
	if !strings.HasSuffix(strings.ToLower(filename), ".json") {
		return nil, false
	}
	// Cheap marker check before a full decode: the flat format always carries
	// the literal schema string, so its absence rules the file out immediately.
	if !bytes.Contains(data, []byte(fwflat.SchemaMarker)) {
		return nil, false
	}

	var doc fwflat.Framework
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&doc); err != nil {
		// Has the marker but does not cleanly decode — treat as not-a-framework
		// rather than erroring, so the file can still be tried as source text.
		return nil, false
	}
	if doc.Schema != fwflat.SchemaMarker {
		return nil, false
	}
	return &doc, true
}

// controlListDoc is a plain "list of controls" JSON: an id/name header and a flat
// controls array of {id, name}. Public framework catalogues commonly export this
// shape; it is NOT the meizon-framework/v2 export. Extra fields (logo, etc.) are
// ignored.
type controlListDoc struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Version  string `json:"version"`
	Controls []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"controls"`
}

// DetectControlListJSON recognizes a plain control-list JSON and converts it to a
// flat framework, so a structured list is imported 1:1 rather than re-derived by
// the model (slow, costly, and lossy — a chunked LLM pass can drop or rename
// controls). It is strict: a .json upload, no meizon-framework/v2 marker (that is
// DetectFrameworkJSON's job), and a non-empty controls array whose every entry
// carries an id and a name. Anything else falls through to the generate pipeline.
func DetectControlListJSON(filename string, data []byte) (*fwflat.Framework, bool) {
	if !strings.HasSuffix(strings.ToLower(filename), ".json") {
		return nil, false
	}
	if bytes.Contains(data, []byte(fwflat.SchemaMarker)) {
		return nil, false // a framework export — handled by DetectFrameworkJSON
	}
	if !bytes.Contains(data, []byte(`"controls"`)) {
		return nil, false
	}
	var cl controlListDoc
	if err := json.Unmarshal(data, &cl); err != nil {
		return nil, false
	}
	if len(cl.Controls) == 0 {
		return nil, false
	}
	for _, c := range cl.Controls {
		if strings.TrimSpace(c.ID) == "" || strings.TrimSpace(c.Name) == "" {
			return nil, false // not a clean id+name control list
		}
	}
	return controlListToFlat(cl), true
}

// controlListToFlat maps a control list to a flat framework: each control becomes
// a requirement (ref = control id), and its category is taken from the name
// prefix ("Organizational - Access control" -> category "Organizational",
// requirement "Access control"). Category refs are slugged and de-collided.
func controlListToFlat(cl controlListDoc) *fwflat.Framework {
	name := strings.TrimSpace(cl.Name)
	if name == "" {
		name = strings.TrimSpace(cl.ID)
	}
	seed := strings.TrimSpace(cl.ID)
	if seed == "" {
		seed = name
	}
	version := strings.TrimSpace(cl.Version)
	if version == "" {
		version = "1.0"
	}

	doc := &fwflat.Framework{
		Schema:  fwflat.SchemaMarker,
		ID:      slugify(seed),
		Name:    name,
		Version: version,
	}

	catRefByName := map[string]string{}
	usedRef := map[string]bool{}
	for _, c := range cl.Controls {
		category, title := splitControlName(c.Name)
		ref, ok := catRefByName[category]
		if !ok {
			base := slugify(category)
			if base == "" {
				base = "general"
			}
			ref = base
			for n := 2; usedRef[ref]; n++ {
				ref = fmt.Sprintf("%s-%d", base, n)
			}
			usedRef[ref] = true
			catRefByName[category] = ref
			doc.Categories = append(doc.Categories, fwflat.Category{Ref: ref, Name: category})
		}
		doc.Requirements = append(doc.Requirements, fwflat.Requirement{
			Ref:      strings.TrimSpace(c.ID),
			Category: ref,
			Name:     title,
		})
	}
	return doc
}

// splitControlName splits "Category - Title" on the first " - " separator. A name
// with no separator is placed under a "General" category.
func splitControlName(name string) (category, title string) {
	name = strings.TrimSpace(name)
	if i := strings.Index(name, " - "); i >= 0 {
		cat := strings.TrimSpace(name[:i])
		rest := strings.TrimSpace(name[i+3:])
		if cat != "" && rest != "" {
			return cat, rest
		}
	}
	return "General", name
}

// StartStructuredImportJob accepts an already-structured framework as a finished
// generation, with no model involved. It produces the same reviewable job a
// generation would, so the console's review-and-accept flow is identical whether
// the framework came from a PDF via the model or from a JSON file as authored.
func (s *Service) StartStructuredImportJob(ctx context.Context, actorID gid.GID, doc *fwflat.Framework, filename string) (string, error) {
	doc.Normalize()
	// Validate up front so a malformed export fails fast at upload, with the
	// schema error, rather than surfacing later at accept time.
	if err := doc.Validate(fwflat.ValidateOptions{}); err != nil {
		return "", fmt.Errorf("%w: the JSON is not a valid framework: %v", ErrInvalidInput, err)
	}

	job := &ingestJob{
		id:        gid.New(s.cfg.PlatformTenant, coredata.IngestJobEntityType).String(),
		status:    "running",
		step:      "import",
		createdAt: time.Now(),
		persist:   &jobStore{svc: s},
	}
	s.ingestJobs.add(job)
	s.startJobRecord(ctx, job.id, coredata.JobKindGenerate, filename, "", actorID)

	// No goroutine: there is no model call to wait on, so the job completes
	// within the request. The console still polls for it, exactly as it does for
	// a slow generation — the flow does not need to know this one was instant.
	out := &GeneratedFramework{
		GenerationID: gid.New(s.cfg.PlatformTenant, coredata.AIGenerationEntityType),
		Document:     doc,
		Summary:      summarizeFlat(doc),
		Provenance:   map[string]string{},
		Note:         "Imported directly from a structured framework file; no AI generation was run.",
		Warnings:     doc.Warnings(fwflat.ValidateOptions{}),
		Duplicates:   doc.DuplicateDescriptions(),
	}
	job.finish(out, nil, "", nil)

	return job.id, nil
}
