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

package fwschema_test

import (
	"crypto/ed25519"
	"testing"

	"go.meizon.cloud/registry/pkg/fwschema"
)

// sampleV2 mirrors the universal template's PCI DSS slice with ISO/SOC 2 stubs.
func sampleV2() *fwschema.Framework {
	return &fwschema.Framework{
		SchemaVersion: fwschema.SchemaVersion2,
		ID:            "pci-dss",
		Name:          "PCI DSS",
		Version:       "4.0.1",
		Status:        fwschema.StatusPublished,
		Region:        "GLOBAL",
		Authority:     "PCI SSC",
		License:       fwschema.LicenseProprietary,
		Categories: []fwschema.Category{
			{
				Code: "G4", Name: "Implement Strong Access Control Measures",
				Requirements: []fwschema.Requirement{
					{
						Code: "Requirement 7", Number: "7",
						Title: "Restrict Access by Business Need to Know",
						Sections: []fwschema.Section{
							{
								Code: "7.2", Title: "Access is appropriately defined and assigned.",
								Items: []fwschema.Item{
									{
										Code: "7.2.1", Title: "Access control model is defined",
										ItemType:             fwschema.ItemTypeControlRequirement,
										ValidationApproaches: []string{"defined", "customized"},
										Mappings: []fwschema.ItemMapping{
											{Relation: fwschema.RelationPartial, Framework: "ISO27001", Version: "2022", Item: "A.5.15", Notes: "Access control rules"},
											{Relation: fwschema.RelationPartial, Framework: "SOC2", Version: "2017", Item: "CC6.1"},
										},
									},
									{
										Code: "7.2.4", Title: "User accounts are reviewed periodically",
										Mappings: []fwschema.ItemMapping{
											{Relation: fwschema.RelationEquivalent, Framework: "ISO27001", Version: "2022", Item: "A.5.18"},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
}

func TestV2ValidateOK(t *testing.T) {
	if err := sampleV2().Validate(); err != nil {
		t.Fatalf("expected valid v2 framework, got: %v", err)
	}
}

func TestV2ValidateRejects(t *testing.T) {
	cases := map[string]func(*fwschema.Framework){
		"no categories": func(f *fwschema.Framework) { f.Categories = nil },
		"bad relation": func(f *fwschema.Framework) {
			f.Categories[0].Requirements[0].Sections[0].Items[0].Mappings[0].Relation = "sibling"
		},
		"missing target": func(f *fwschema.Framework) {
			f.Categories[0].Requirements[0].Sections[0].Items[0].Mappings[0].Item = ""
		},
		"dup item code": func(f *fwschema.Framework) { f.Categories[0].Requirements[0].Sections[0].Items[1].Code = "7.2.1" },
		"item no title": func(f *fwschema.Framework) { f.Categories[0].Requirements[0].Sections[0].Items[0].Title = "" },
		"mixed bodies": func(f *fwschema.Framework) {
			f.Controls = []fwschema.Control{{ID: "x", Name: "x"}}
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			f := sampleV2()
			mutate(f)
			if err := f.Validate(); err == nil {
				t.Fatalf("expected validation error for %q", name)
			}
		})
	}
}

func TestV2FlattenWalksItems(t *testing.T) {
	seed := sampleV2().Flatten()
	if len(seed.Controls) != 2 {
		t.Fatalf("expected 2 seed controls, got %d", len(seed.Controls))
	}
	if seed.Controls[0].ID != "7.2.1" || seed.Controls[1].ID != "7.2.4" {
		t.Fatalf("unexpected order/ids: %+v", seed.Controls)
	}
	if seed.Controls[0].Name != "Access control model is defined" {
		t.Fatalf("item title not carried to seed name")
	}
}

func TestV2SignVerifyResolutionIndependent(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	keys := map[string]ed25519.PublicKey{"k": pub}

	f := sampleV2()
	if err := f.Sign(priv, "k"); err != nil {
		t.Fatalf("sign: %v", err)
	}
	if err := f.Verify(keys); err != nil {
		t.Fatalf("verify: %v", err)
	}

	// Tampering with a mapping (the cross-mapping content) must be detected.
	f.Categories[0].Requirements[0].Sections[0].Items[0].Mappings[0].Relation = fwschema.RelationEquivalent
	if err := f.Verify(keys); err == nil {
		t.Fatal("expected tampered mapping to fail verification")
	}
}
