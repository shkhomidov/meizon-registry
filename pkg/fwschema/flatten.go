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

// GRCSeed is the flat lowest-common-denominator format the GRC platform's
// importFramework mutation accepts directly. The field layout mirrors the seed
// JSON the GRC already ships (id, name, controls[{id,name,description}]), so a
// seed produced here imports unchanged.
type GRCSeed struct {
	ID       string           `json:"id"`
	Name     string           `json:"name"`
	Controls []GRCSeedControl `json:"controls"`
}

// GRCSeedControl is a single flattened control. The rich control's ref id maps
// to id, its name to name and its description to description; richer fields
// (section, guidance, references, mappings) are dropped in the flat form.
type GRCSeedControl struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// Flatten losslessly reduces the rich framework to the flat GRC seed. For a v2
// document it walks the hierarchy in authored order, emitting one seed control
// per item; for v1 it copies the flat controls. Order and identifiers are
// preserved so the output is byte-stable for a given framework version.
func (f *Framework) Flatten() GRCSeed {
	seed := GRCSeed{
		ID:       f.ID,
		Name:     f.Name,
		Controls: []GRCSeedControl{},
	}

	if f.IsV2() {
		f.WalkAssessable(func(_ *Category, req *Requirement, item *Item) {
			if item != nil {
				seed.Controls = append(seed.Controls, GRCSeedControl{
					ID:          item.Code,
					Name:        item.Title,
					Description: item.Description,
				})
				return
			}
			seed.Controls = append(seed.Controls, GRCSeedControl{
				ID:          req.Code,
				Name:        req.Title,
				Description: req.Description,
			})
		})
		return seed
	}

	for _, c := range f.Controls {
		seed.Controls = append(seed.Controls, GRCSeedControl{
			ID:          c.ID,
			Name:        c.Name,
			Description: c.Description,
		})
	}

	return seed
}
