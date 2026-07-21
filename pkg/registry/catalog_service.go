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
	"time"

	"go.gearno.de/kit/pg"
	"go.meizon.cloud/registry/pkg/coredata"
	"go.meizon.cloud/registry/pkg/gid"
	"go.meizon.cloud/registry/pkg/iam"
	"go.meizon.cloud/registry/pkg/validator"
)

// Catalog views (console API shapes). Item/control references are by code —
// the human-readable currency of the whole system.
type (
	EvidenceView struct {
		ID                   gid.GID `json:"id"`
		Type                 string  `json:"type"`
		Hint                 string  `json:"hint,omitempty"`
		RenewalCadenceMonths *int    `json:"renewalCadenceMonths,omitempty"`
		Origin               string  `json:"origin"`
	}

	ControlEntryView struct {
		ID          gid.GID        `json:"id"`
		Code        string         `json:"code"`
		Name        string         `json:"name"`
		Description string         `json:"description,omitempty"`
		Domain      string         `json:"domain,omitempty"`
		Origin      string         `json:"origin"`
		Items       []string       `json:"items"`
		Evidence    []EvidenceView `json:"evidence"`
	}

	PolicyTemplateView struct {
		ID       gid.GID  `json:"id"`
		Name     string   `json:"name"`
		Body     string   `json:"body"`
		Origin   string   `json:"origin"`
		Controls []string `json:"controls"`
	}
)

// catalogAuth loads a framework by reference and authorizes a catalog edit in
// its region. Catalog rows are registry-side annotations (not signed content),
// so they are not DRAFT-gated.
func (s *Service) catalogAuth(ctx context.Context, conn pg.Querier, actorID gid.GID, ref, action string) (coredata.Framework, error) {
	var framework coredata.Framework
	if err := framework.LoadByReferenceID(ctx, conn, s.platformScope(), ref); err != nil {
		return framework, err
	}
	if err := s.authorize(ctx, conn, actorID, action, framework.Region, framework.ID); err != nil {
		return framework, err
	}
	return framework, nil
}

// latestRequirementsByCode maps the latest version's requirement codes to ids.
func (s *Service) latestRequirementsByCode(ctx context.Context, conn pg.Querier, frameworkID gid.GID) (map[string]gid.GID, map[gid.GID]string, error) {
	var versions coredata.FrameworkVersions
	if err := versions.LoadAllByFramework(ctx, conn, s.platformScope(), frameworkID); err != nil {
		return nil, nil, err
	}
	byCode := map[string]gid.GID{}
	byID := map[gid.GID]string{}
	if len(versions) == 0 {
		return byCode, byID, nil
	}
	var requirements coredata.Requirements
	if err := requirements.LoadAllByVersion(ctx, conn, s.platformScope(), versions[0].ID); err != nil {
		return nil, nil, err
	}
	for _, r := range requirements {
		byCode[r.Code] = r.ID
		byID[r.ID] = r.Code
	}
	return byCode, byID, nil
}

// ControlsOfFramework returns a framework's control library with item links and
// evidence guidance.
func (s *Service) ControlsOfFramework(ctx context.Context, ref string) ([]ControlEntryView, error) {
	out := []ControlEntryView{}
	err := s.db.WithConn(ctx, func(ctx context.Context, conn pg.Querier) error {
		scope := s.platformScope()

		var framework coredata.Framework
		if err := framework.LoadByReferenceID(ctx, conn, scope, ref); err != nil {
			return err
		}

		var entries coredata.ControlLibraryEntries
		if err := entries.LoadAllByFramework(ctx, conn, scope, framework.ID); err != nil {
			return err
		}
		if len(entries) == 0 {
			return nil
		}

		ids := make([]gid.GID, 0, len(entries))
		for _, e := range entries {
			ids = append(ids, e.ID)
		}

		var links coredata.ControlRequirementLinks
		if err := links.LoadAllByControls(ctx, conn, scope, ids); err != nil {
			return err
		}
		var evidence coredata.EvidenceGuidances
		if err := evidence.LoadAllByControls(ctx, conn, scope, ids); err != nil {
			return err
		}
		_, codeByID, err := s.latestRequirementsByCode(ctx, conn, framework.ID)
		if err != nil {
			return err
		}

		itemsByControl := map[gid.GID][]string{}
		for _, l := range links {
			if code, ok := codeByID[l.RequirementID]; ok {
				itemsByControl[l.ControlID] = append(itemsByControl[l.ControlID], code)
			}
		}
		evidenceByControl := map[gid.GID][]EvidenceView{}
		for _, ev := range evidence {
			evidenceByControl[ev.ControlID] = append(evidenceByControl[ev.ControlID], EvidenceView{
				ID: ev.ID, Type: ev.Type, Hint: ev.Hint,
				RenewalCadenceMonths: ev.RenewalCadenceMonths, Origin: ev.Origin,
			})
		}

		for _, e := range entries {
			items := itemsByControl[e.ID]
			if items == nil {
				items = []string{}
			}
			evs := evidenceByControl[e.ID]
			if evs == nil {
				evs = []EvidenceView{}
			}
			out = append(out, ControlEntryView{
				ID: e.ID, Code: e.Code, Name: e.Name, Description: e.Description,
				Domain: e.Domain, Origin: e.Origin, Items: items, Evidence: evs,
			})
		}
		return nil
	})
	return out, err
}

// AddControlEntry creates a control library entry linked to items (by code).
func (s *Service) AddControlEntry(ctx context.Context, actorID gid.GID, ref, code, name, description, domain string, itemCodes []string) (gid.GID, error) {
	v := validator.New()
	v.Check(code, "code", validator.Required(), validator.MaxLen(128), validator.NoNewLine())
	v.Check(name, "name", validator.Required(), validator.MaxLen(512), validator.NoNewLine())
	if err := v.Error(); err != nil {
		return gid.Nil, err
	}

	var id gid.GID
	err := s.db.WithTx(ctx, func(ctx context.Context, tx pg.Tx) error {
		framework, err := s.catalogAuth(ctx, tx, actorID, ref, iam.ActionControlCreate)
		if err != nil {
			return err
		}
		scope := s.platformScope()

		fwID := framework.ID
		entry := coredata.ControlLibraryEntry{
			ID:          gid.New(s.cfg.PlatformTenant, coredata.ControlLibraryEntityType),
			FrameworkID: &fwID,
			Code:        strings.TrimSpace(code),
			Name:        strings.TrimSpace(name),
			Description: description,
			Domain:      strings.TrimSpace(domain),
			CreatedAt:   time.Now(),
		}
		if err := entry.Insert(ctx, tx, scope); err != nil {
			return err
		}
		id = entry.ID

		requirementsByCode, _, err := s.latestRequirementsByCode(ctx, tx, framework.ID)
		if err != nil {
			return err
		}
		for _, itemCode := range itemCodes {
			requirementID, ok := requirementsByCode[strings.TrimSpace(itemCode)]
			if !ok {
				return fmt.Errorf("%w: requirement %q is not in the latest version", ErrInvalidInput, itemCode)
			}
			if err := (coredata.ControlRequirementLink{ControlID: entry.ID, RequirementID: requirementID}).Insert(ctx, tx, scope); err != nil {
				return err
			}
		}
		return nil
	})
	return id, err
}

// DeleteControlEntry removes a control entry (links and evidence cascade).
func (s *Service) DeleteControlEntry(ctx context.Context, actorID gid.GID, ref string, controlID gid.GID) error {
	return s.db.WithTx(ctx, func(ctx context.Context, tx pg.Tx) error {
		if _, err := s.catalogAuth(ctx, tx, actorID, ref, iam.ActionControlDelete); err != nil {
			return err
		}
		return coredata.ControlLibraryEntry{}.Delete(ctx, tx, s.platformScope(), controlID)
	})
}

// AddEvidence records evidence guidance on a control.
func (s *Service) AddEvidence(ctx context.Context, actorID gid.GID, ref string, controlID gid.GID, evidenceType, hint string, cadence *int) (gid.GID, error) {
	if !coredata.EvidenceType(evidenceType).IsValid() {
		return gid.Nil, fmt.Errorf("%w: unknown evidence type %q", ErrInvalidInput, evidenceType)
	}

	var id gid.GID
	err := s.db.WithTx(ctx, func(ctx context.Context, tx pg.Tx) error {
		if _, err := s.catalogAuth(ctx, tx, actorID, ref, iam.ActionControlEdit); err != nil {
			return err
		}
		ev := coredata.EvidenceGuidance{
			ID:                   gid.New(s.cfg.PlatformTenant, coredata.EvidenceGuidanceEntityType),
			ControlID:            controlID,
			Type:                 evidenceType,
			Hint:                 hint,
			RenewalCadenceMonths: cadence,
			CreatedAt:            time.Now(),
		}
		if err := ev.Insert(ctx, tx, s.platformScope()); err != nil {
			return err
		}
		id = ev.ID
		return nil
	})
	return id, err
}

// DeleteEvidence removes an evidence guidance row.
func (s *Service) DeleteEvidence(ctx context.Context, actorID gid.GID, ref string, evidenceID gid.GID) error {
	return s.db.WithTx(ctx, func(ctx context.Context, tx pg.Tx) error {
		if _, err := s.catalogAuth(ctx, tx, actorID, ref, iam.ActionControlDelete); err != nil {
			return err
		}
		return coredata.EvidenceGuidance{}.Delete(ctx, tx, s.platformScope(), evidenceID)
	})
}

// PolicyTemplatesOfFramework returns a framework's policy templates with their
// linked control codes.
func (s *Service) PolicyTemplatesOfFramework(ctx context.Context, ref string) ([]PolicyTemplateView, error) {
	out := []PolicyTemplateView{}
	err := s.db.WithConn(ctx, func(ctx context.Context, conn pg.Querier) error {
		scope := s.platformScope()

		var framework coredata.Framework
		if err := framework.LoadByReferenceID(ctx, conn, scope, ref); err != nil {
			return err
		}

		var templates coredata.PolicyTemplates
		if err := templates.LoadAllByFramework(ctx, conn, scope, framework.ID); err != nil {
			return err
		}
		if len(templates) == 0 {
			return nil
		}

		ids := make([]gid.GID, 0, len(templates))
		for _, t := range templates {
			ids = append(ids, t.ID)
		}
		var links coredata.PolicyControlLinks
		if err := links.LoadAllByTemplates(ctx, conn, scope, ids); err != nil {
			return err
		}

		var entries coredata.ControlLibraryEntries
		if err := entries.LoadAllByFramework(ctx, conn, scope, framework.ID); err != nil {
			return err
		}
		controlCodeByID := map[gid.GID]string{}
		for _, e := range entries {
			controlCodeByID[e.ID] = e.Code
		}

		controlsByTemplate := map[gid.GID][]string{}
		for _, l := range links {
			if code, ok := controlCodeByID[l.ControlID]; ok {
				controlsByTemplate[l.PolicyTemplateID] = append(controlsByTemplate[l.PolicyTemplateID], code)
			}
		}

		for _, t := range templates {
			controls := controlsByTemplate[t.ID]
			if controls == nil {
				controls = []string{}
			}
			out = append(out, PolicyTemplateView{ID: t.ID, Name: t.Name, Body: t.Body, Origin: t.Origin, Controls: controls})
		}
		return nil
	})
	return out, err
}

// UpsertPolicyTemplate creates or updates a policy template (name, markdown
// body, linked controls by code). A nil id creates.
func (s *Service) UpsertPolicyTemplate(ctx context.Context, actorID gid.GID, ref string, templateID gid.GID, name, body string, controlCodes []string) (gid.GID, error) {
	v := validator.New()
	v.Check(name, "name", validator.Required(), validator.MaxLen(512), validator.NoNewLine())
	if err := v.Error(); err != nil {
		return gid.Nil, err
	}

	var id gid.GID
	err := s.db.WithTx(ctx, func(ctx context.Context, tx pg.Tx) error {
		framework, err := s.catalogAuth(ctx, tx, actorID, ref, iam.ActionControlEdit)
		if err != nil {
			return err
		}
		scope := s.platformScope()

		if templateID == gid.Nil {
			fwID := framework.ID
			tpl := coredata.PolicyTemplate{
				ID:          gid.New(s.cfg.PlatformTenant, coredata.PolicyTemplateEntityType),
				FrameworkID: &fwID,
				Name:        strings.TrimSpace(name),
				Body:        body,
				CreatedAt:   time.Now(),
			}
			if err := tpl.Insert(ctx, tx, scope); err != nil {
				return err
			}
			id = tpl.ID
		} else {
			tpl := coredata.PolicyTemplate{ID: templateID, Name: strings.TrimSpace(name), Body: body}
			if err := tpl.Update(ctx, tx, scope); err != nil {
				return err
			}
			id = templateID
			if err := (coredata.PolicyControlLink{}).DeleteByTemplate(ctx, tx, scope, templateID); err != nil {
				return err
			}
		}

		// Resolve control codes → ids and (re)link.
		var entries coredata.ControlLibraryEntries
		if err := entries.LoadAllByFramework(ctx, tx, scope, framework.ID); err != nil {
			return err
		}
		byCode := map[string]gid.GID{}
		for _, e := range entries {
			byCode[e.Code] = e.ID
		}
		for _, code := range controlCodes {
			controlID, ok := byCode[strings.TrimSpace(code)]
			if !ok {
				return fmt.Errorf("%w: control %q is not in this framework's library", ErrInvalidInput, code)
			}
			if err := (coredata.PolicyControlLink{PolicyTemplateID: id, ControlID: controlID}).Insert(ctx, tx, scope); err != nil {
				return err
			}
		}
		return nil
	})
	return id, err
}

// DeletePolicyTemplate removes a policy template.
func (s *Service) DeletePolicyTemplate(ctx context.Context, actorID gid.GID, ref string, templateID gid.GID) error {
	return s.db.WithTx(ctx, func(ctx context.Context, tx pg.Tx) error {
		if _, err := s.catalogAuth(ctx, tx, actorID, ref, iam.ActionControlDelete); err != nil {
			return err
		}
		return coredata.PolicyTemplate{}.Delete(ctx, tx, s.platformScope(), templateID)
	})
}
