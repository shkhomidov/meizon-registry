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

package registry_test

import (
	"context"
	"testing"

	"go.meizon.cloud/registry/pkg/fwschema"
	"go.meizon.cloud/registry/pkg/registry"
)

func pciDoc() *fwschema.Framework {
	return &fwschema.Framework{
		SchemaVersion: fwschema.SchemaVersion2,
		ID:            "pci-dss", Name: "PCI DSS", Version: "4.0.1",
		Status: fwschema.StatusDraft, Region: "GLOBAL", Authority: "PCI SSC",
		License: fwschema.LicenseProprietary,
		Categories: []fwschema.Category{{
			Code: "G4", Name: "Implement Strong Access Control Measures",
			Requirements: []fwschema.Requirement{{
				Code: "Requirement 7", Number: "7", Title: "Restrict Access by Business Need to Know",
				Sections: []fwschema.Section{{
					Code: "7.2", Title: "Access is appropriately defined and assigned.",
					Items: []fwschema.Item{
						{Code: "7.2.1", Title: "Access control model is defined", Mappings: []fwschema.ItemMapping{
							{Relation: fwschema.RelationPartial, Framework: "iso-27001", Version: "2022", Item: "A.5.15"},
							{Relation: fwschema.RelationPartial, Framework: "soc-2", Version: "2017", Item: "CC6.1"},
						}},
						{Code: "7.2.4", Title: "User accounts reviewed periodically", Mappings: []fwschema.ItemMapping{
							{Relation: fwschema.RelationEquivalent, Framework: "iso-27001", Version: "2022", Item: "A.5.18"},
						}},
					},
				}},
			}},
		}},
	}
}

func isoDoc() *fwschema.Framework {
	return &fwschema.Framework{
		SchemaVersion: fwschema.SchemaVersion2,
		ID:            "iso-27001", Name: "ISO/IEC 27001", Version: "2022",
		Status: fwschema.StatusDraft, Region: "GLOBAL", Authority: "ISO",
		License: fwschema.LicenseProprietary,
		Categories: []fwschema.Category{{
			Code: "A5", Name: "Organizational controls",
			Requirements: []fwschema.Requirement{{
				Code: "A.5", Title: "Organizational controls",
				Sections: []fwschema.Section{{
					Code: "A.5.x", Title: "Access control",
					Items: []fwschema.Item{
						{Code: "A.5.15", Title: "Access control"},
						{Code: "A.5.18", Title: "Access rights"},
					},
				}},
			}},
		}},
	}
}

// TestCrossMappingStubResolution covers the template's core scenario: import
// PCI with ISO/SOC2 stubs → publish → stubs unresolved; import + publish ISO →
// PCI→ISO stubs resolve automatically; SOC2 stubs remain; signatures untouched.
func TestCrossMappingStubResolution(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	if _, err := svc.BootstrapSuperAdmin(ctx, req(superAdminEmail, "Root")); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	superID := mustID(t, svc, superAdminEmail)
	mustCreateUser(t, svc, superID, "mod@meizon.test", "Global Moderator", "moderator", []string{"GLOBAL"})
	modID := mustID(t, svc, "mod@meizon.test")
	if err := svc.GenerateSigningKey(ctx, superID, "reg-2026"); err != nil {
		t.Fatalf("key: %v", err)
	}

	// Import + publish PCI (author = superadmin, approver = moderator).
	pci, err := svc.ImportFrameworkDoc(ctx, superID, pciDoc())
	if err != nil {
		t.Fatalf("import pci: %v", err)
	}
	if err := svc.Submit(ctx, superID, pci.VersionID); err != nil {
		t.Fatalf("submit pci: %v", err)
	}
	if err := svc.Approve(ctx, modID, pci.VersionID, "ok"); err != nil {
		t.Fatalf("approve pci: %v", err)
	}
	if err := svc.Publish(ctx, modID, pci.VersionID); err != nil {
		t.Fatalf("publish pci: %v", err)
	}

	// All three mappings are stubs (ISO + SOC2 not loaded yet).
	unresolved, err := svc.UnresolvedStubSummary(ctx)
	if err != nil {
		t.Fatalf("unresolved: %v", err)
	}
	counts := map[string]int{}
	for _, r := range unresolved {
		counts[r.TargetFrameworkCode] += r.Count
	}
	if counts["iso-27001"] != 2 || counts["soc-2"] != 1 {
		t.Fatalf("expected 2 iso + 1 soc2 stubs, got %+v", counts)
	}

	// The published bundle is signed and verifies. From v3 the mappings are not
	// inside it — they are tracked as cross-mapping rows and distributed as
	// separate mapping sets — so resolution state is read from the structure
	// view below, not the bundle.
	bundle, err := svc.ExportBundle(ctx, pci.VersionID)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if !bundle.IsV2() || bundle.Signature == nil {
		t.Fatal("expected a signed v2-family bundle")
	}
	keys, _ := svc.VerificationKeys(ctx)
	if err := bundle.Verify(keys); err != nil {
		t.Fatalf("verify pci bundle: %v", err)
	}
	hashBefore := bundle.Signature.ContentHash

	// Import + publish ISO 27001 → PCI→ISO stubs must resolve automatically.
	iso, err := svc.ImportFrameworkDoc(ctx, superID, isoDoc())
	if err != nil {
		t.Fatalf("import iso: %v", err)
	}
	if err := svc.Submit(ctx, superID, iso.VersionID); err != nil {
		t.Fatalf("submit iso: %v", err)
	}
	if err := svc.Approve(ctx, modID, iso.VersionID, "ok"); err != nil {
		t.Fatalf("approve iso: %v", err)
	}
	if err := svc.Publish(ctx, modID, iso.VersionID); err != nil {
		t.Fatalf("publish iso: %v", err)
	}

	unresolved, err = svc.UnresolvedStubSummary(ctx)
	if err != nil {
		t.Fatalf("unresolved after iso: %v", err)
	}
	counts = map[string]int{}
	for _, r := range unresolved {
		counts[r.TargetFrameworkCode] += r.Count
	}
	if counts["iso-27001"] != 0 {
		t.Fatalf("expected all iso stubs resolved, got %+v", counts)
	}
	if counts["soc-2"] != 1 {
		t.Fatalf("expected soc-2 stub to remain, got %+v", counts)
	}

	// Coverage: PCI → ISO shows 1 equivalent + 1 partial, all resolved.
	report, err := svc.CoverageFor(ctx, "pci-dss", "iso-27001")
	if err != nil {
		t.Fatalf("coverage: %v", err)
	}
	byRelation := map[string]registryCoverage{}
	for _, row := range report.Rows {
		byRelation[row.Relation] = registryCoverage{row.Total, row.Resolved}
	}
	if byRelation["equivalent"] != (registryCoverage{1, 1}) || byRelation["partial"] != (registryCoverage{1, 1}) {
		t.Fatalf("unexpected coverage rows: %+v", report.Rows)
	}

	// Resolution never changed the signed content.
	after, err := svc.ExportBundle(ctx, pci.VersionID)
	if err != nil {
		t.Fatalf("re-export: %v", err)
	}
	if after.Signature.ContentHash != hashBefore {
		t.Fatal("resolution must not change the signed content hash")
	}
	if err := after.Verify(keys); err != nil {
		t.Fatalf("pci bundle must still verify after resolution: %v", err)
	}

	// Structure view reflects resolution state for the UI.
	tree, err := svc.StructureOf(ctx, pci.VersionID, "")
	if err != nil {
		t.Fatalf("structure: %v", err)
	}
	item721 := tree[0].Requirements[0]
	resolvedCount := 0
	for _, m := range item721.Mappings {
		if m.Resolved {
			resolvedCount++
		}
	}
	if resolvedCount != 1 { // ISO resolved, SOC2 stub
		t.Fatalf("expected exactly 1 resolved mapping on 7.2.1, got %d", resolvedCount)
	}

	_ = registry.CreateFrameworkResult{}
}

type registryCoverage struct{ total, resolved int }

// TestMapPublishedV3Framework pins the "map anytime, update, republish" behavior:
// a v3 framework (the default since Phase 16) can be cross-mapped AFTER it is
// published, and doing so must not disturb its own signed bundle — the mappings
// ride in a separate mapping set, not the framework signature.
func TestMapPublishedV3Framework(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	if _, err := svc.BootstrapSuperAdmin(ctx, req(superAdminEmail, "Root")); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	superID := mustID(t, svc, superAdminEmail)
	mustCreateUser(t, svc, superID, "mod@meizon.test", "Global Moderator", "moderator", []string{"GLOBAL"})
	modID := mustID(t, svc, "mod@meizon.test")
	if err := svc.GenerateSigningKey(ctx, superID, "reg-2026"); err != nil {
		t.Fatalf("key: %v", err)
	}

	// Import + publish ISO (author = superadmin, approver = moderator). A plain
	// import with no explicit schema publishes as v3.
	iso, err := svc.ImportFrameworkDoc(ctx, superID, isoDoc())
	if err != nil {
		t.Fatalf("import iso: %v", err)
	}
	if err := svc.Submit(ctx, superID, iso.VersionID); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if err := svc.Approve(ctx, modID, iso.VersionID, "ok"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if err := svc.Publish(ctx, modID, iso.VersionID); err != nil {
		t.Fatalf("publish: %v", err)
	}

	// The framework's own signature before any mapping is added.
	before, err := svc.ExportBundle(ctx, iso.VersionID)
	if err != nil {
		t.Fatalf("export before: %v", err)
	}
	if before.Signature == nil {
		t.Fatal("published bundle must be signed")
	}
	hashBefore := before.Signature.ContentHash

	// Add a cross-mapping to the PUBLISHED framework. Before requireMappable this
	// failed with "structure can only change in a DRAFT".
	if _, err := svc.AddItemMapping(ctx, superID, registry.AddItemMappingRequest{
		VersionID:       iso.VersionID,
		ItemCode:        "A.5.15",
		Relation:        string(fwschema.RelationEquivalent),
		TargetFramework: "soc-2",
		TargetVersion:   "2017",
		TargetItem:      "CC6.1",
		Notes:           "added after publish",
	}); err != nil {
		t.Fatalf("mapping a published v3 framework must be allowed: %v", err)
	}

	// The framework's own signed bundle must be byte-for-byte unchanged — a v3
	// bundle does not carry its mappings, so the signature still verifies.
	after, err := svc.ExportBundle(ctx, iso.VersionID)
	if err != nil {
		t.Fatalf("export after: %v", err)
	}
	if after.Signature == nil || after.Signature.ContentHash != hashBefore {
		t.Fatalf("adding a mapping changed the framework's signed content: %q -> %q",
			hashBefore, after.Signature.ContentHash)
	}
	keys, _ := svc.VerificationKeys(ctx)
	if err := after.Verify(keys); err != nil {
		t.Fatalf("framework signature must remain valid after mapping: %v", err)
	}
}
