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

package fwschema

import (
	"fmt"

	"go.meizon.cloud/registry/pkg/validator"
)

const (
	idMaxLength          = 128
	nameMaxLength        = 512
	regionMaxLength      = 32
	descriptionMaxLength = 100_000
)

// Validate checks the framework against the exchange schema v1 rules. It is used
// both when a version is authored in the registry and when a bundle is imported
// into a GRC instance (strict validation of untrusted input).
func (f *Framework) Validate() error {
	v := validator.New()

	v.Check(f.SchemaVersion, "schemaVersion", validator.Required(), validator.OneOf(SchemaVersion, SchemaVersion2))
	v.Check(f.ID, "id", validator.Required(), validator.MaxLen(idMaxLength), validator.NoNewLine(), validator.PrintableText())
	v.Check(f.Name, "name", validator.Required(), validator.MaxLen(nameMaxLength), validator.NoNewLine())
	v.Check(f.ShortName, "shortName", validator.MaxLen(nameMaxLength), validator.NoNewLine())
	// Semver is recommended but not required: real-world framework versions
	// include tokens like "2022", "r5" or "4.0.1" (cross-mapping stubs address
	// them by these codes).
	v.Check(f.Version, "version", validator.Required(), validator.MaxLen(64), validator.NoNewLine())
	v.Check(string(f.Status), "status", validator.Required(), validator.OneOf(
		string(StatusDraft), string(StatusInReview), string(StatusApproved),
		string(StatusPublished), string(StatusDeprecated),
	))
	v.Check(f.Region, "region", validator.Required(), validator.MaxLen(regionMaxLength), validator.NoNewLine())
	v.Check(string(f.License), "license", validator.Required(), validator.OneOf(
		string(LicensePublicDomain), string(LicenseStatutory), string(LicenseProprietary),
	))
	v.Check(f.Description, "description", validator.MaxLen(descriptionMaxLength))

	switch f.SchemaVersion {
	case SchemaVersion2:
		f.validateV2(v)
	default:
		f.validateV1(v)
	}

	return v.Error()
}

func (f *Framework) validateV1(v *validator.Validator) {
	if len(f.Controls) == 0 {
		v.AddError("controls", validator.ErrorCodeRequired, "must contain at least one control")
	}

	seen := make(map[string]struct{}, len(f.Controls))
	for i, c := range f.Controls {
		field := fmt.Sprintf("controls[%d]", i)

		v.Check(c.ID, field+".id", validator.Required(), validator.MaxLen(idMaxLength), validator.NoNewLine())
		v.Check(c.Name, field+".name", validator.Required(), validator.MaxLen(nameMaxLength), validator.NoNewLine())
		v.Check(c.Description, field+".description", validator.MaxLen(descriptionMaxLength))

		if c.ID != "" {
			if _, dup := seen[c.ID]; dup {
				v.AddError(field+".id", validator.ErrorCodeInvalidFormat, fmt.Sprintf("duplicate control id %q", c.ID))
			}
			seen[c.ID] = struct{}{}
		}

		for j, m := range c.Mappings {
			mf := fmt.Sprintf("%s.mappings[%d]", field, j)
			v.Check(m.Framework, mf+".framework", validator.Required(), validator.NoNewLine())
			v.Check(m.Control, mf+".control", validator.Required(), validator.NoNewLine())
		}
	}

	// parentId must reference a control present in this version.
	for i, c := range f.Controls {
		if c.ParentID == nil || *c.ParentID == "" {
			continue
		}

		if _, ok := seen[*c.ParentID]; !ok {
			v.AddError(fmt.Sprintf("controls[%d].parentId", i), validator.ErrorCodeInvalidFormat,
				fmt.Sprintf("references unknown control %q", *c.ParentID))
		}
	}
}

func (f *Framework) validateV2(v *validator.Validator) {
	if len(f.Categories) == 0 {
		v.AddError("categories", validator.ErrorCodeRequired, "must contain at least one category")
	}
	if len(f.Controls) > 0 {
		v.AddError("controls", validator.ErrorCodeInvalidFormat, "a 2.0 document uses categories, not flat controls")
	}

	catCodes := map[string]struct{}{}
	itemCodes := map[string]struct{}{}
	itemCount := 0

	for ci, cat := range f.Categories {
		cf := fmt.Sprintf("categories[%d]", ci)
		v.Check(cat.Code, cf+".code", validator.Required(), validator.MaxLen(idMaxLength), validator.NoNewLine())
		v.Check(cat.Name, cf+".name", validator.Required(), validator.MaxLen(nameMaxLength), validator.NoNewLine())
		if cat.Code != "" {
			if _, dup := catCodes[cat.Code]; dup {
				v.AddError(cf+".code", validator.ErrorCodeInvalidFormat, fmt.Sprintf("duplicate category code %q", cat.Code))
			}
			catCodes[cat.Code] = struct{}{}
		}

		for ri, req := range cat.Requirements {
			rf := fmt.Sprintf("%s.requirements[%d]", cf, ri)
			v.Check(req.Code, rf+".code", validator.Required(), validator.MaxLen(idMaxLength), validator.NoNewLine())
			v.Check(req.Title, rf+".title", validator.Required(), validator.MaxLen(nameMaxLength), validator.NoNewLine())
			v.Check(req.Description, rf+".description", validator.MaxLen(descriptionMaxLength))
			if req.ItemType != "" && !req.ItemType.IsValid() {
				v.AddError(rf+".itemType", validator.ErrorCodeInvalidEnum, fmt.Sprintf("unknown item type %q", req.ItemType))
			}

			// A requirement with no sections is itself the assessable unit, so
			// its code shares the one code space with legacy item codes.
			if len(req.Sections) == 0 {
				itemCount++
				if req.Code != "" {
					if _, dup := itemCodes[req.Code]; dup {
						v.AddError(rf+".code", validator.ErrorCodeInvalidFormat, fmt.Sprintf("duplicate requirement code %q", req.Code))
					}
					itemCodes[req.Code] = struct{}{}
				}
			}
			checkMappings(v, rf, req.Mappings)

			for si, sec := range req.Sections {
				sf := fmt.Sprintf("%s.sections[%d]", rf, si)
				v.Check(sec.Code, sf+".code", validator.Required(), validator.MaxLen(idMaxLength), validator.NoNewLine())
				v.Check(sec.Title, sf+".title", validator.Required(), validator.MaxLen(nameMaxLength), validator.NoNewLine())

				for ii, item := range sec.Items {
					itemCount++
					xf := fmt.Sprintf("%s.items[%d]", sf, ii)
					v.Check(item.Code, xf+".code", validator.Required(), validator.MaxLen(idMaxLength), validator.NoNewLine())
					v.Check(item.Title, xf+".title", validator.Required(), validator.MaxLen(nameMaxLength), validator.NoNewLine())
					v.Check(item.Description, xf+".description", validator.MaxLen(descriptionMaxLength))
					if item.ItemType != "" && !item.ItemType.IsValid() {
						v.AddError(xf+".itemType", validator.ErrorCodeInvalidEnum, fmt.Sprintf("unknown item type %q", item.ItemType))
					}

					if item.Code != "" {
						if _, dup := itemCodes[item.Code]; dup {
							v.AddError(xf+".code", validator.ErrorCodeInvalidFormat, fmt.Sprintf("duplicate item code %q", item.Code))
						}
						itemCodes[item.Code] = struct{}{}
					}

					checkMappings(v, xf, item.Mappings)
				}
			}
		}
	}

	if itemCount == 0 && len(f.Categories) > 0 {
		v.AddError("categories", validator.ErrorCodeRequired, "must contain at least one requirement")
	}
}

// checkMappings validates a set of cross-mappings under the given field path.
// Mappings hang off requirements now and off legacy items, so both call here.
func checkMappings(v *validator.Validator, field string, mappings []ItemMapping) {
	for mi, m := range mappings {
		mf := fmt.Sprintf("%s.mappings[%d]", field, mi)
		if !m.Relation.IsValid() {
			v.AddError(mf+".relation", validator.ErrorCodeInvalidEnum, fmt.Sprintf("unknown relation %q", m.Relation))
		}
		v.Check(m.Framework, mf+".framework", validator.Required(), validator.NoNewLine())
		v.Check(m.Item, mf+".item", validator.Required(), validator.NoNewLine())
	}
}
