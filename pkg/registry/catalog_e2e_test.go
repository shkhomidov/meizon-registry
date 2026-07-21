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

	"go.meizon.cloud/registry/pkg/gid"
	"go.meizon.cloud/registry/pkg/registry"
)

// TestCatalogLifecycle covers the template catalogs end to end: a control
// library entry linked to items, evidence guidance rows, and a policy template
// (markdown body) linked to the control — plus region authorization.
func TestCatalogLifecycle(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	if _, err := svc.BootstrapSuperAdmin(ctx, req(superAdminEmail, "Root")); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	superID := mustID(t, svc, superAdminEmail)
	mustCreateUser(t, svc, superID, "us-aud@meizon.test", "US Auditor", "auditor", []string{"US"})
	usAuditor := mustID(t, svc, "us-aud@meizon.test")

	// Import a small framework (GLOBAL) with two items.
	created, err := svc.ImportFrameworkDoc(ctx, superID, pciDoc())
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	_ = created

	// Add a control entry linked to both items.
	controlID, err := svc.AddControlEntry(ctx, superID, "pci-dss", "PCI-7.2", "Access is defined and assigned", "desc", "access_control", []string{"7.2.1", "7.2.4"})
	if err != nil {
		t.Fatalf("add control: %v", err)
	}

	// Unknown item code fails.
	if _, err := svc.AddControlEntry(ctx, superID, "pci-dss", "PCI-X", "Bad", "", "", []string{"nope"}); err == nil {
		t.Fatal("expected unknown item code to fail")
	}

	// Region isolation: a US-scoped auditor cannot edit a GLOBAL framework's catalog.
	if _, err := svc.AddControlEntry(ctx, usAuditor, "pci-dss", "PCI-Y", "Nope", "", "", nil); err == nil {
		t.Fatal("expected region-scoped auditor to be denied")
	}

	// Evidence guidance.
	cadence := 3
	if _, err := svc.AddEvidence(ctx, superID, "pci-dss", controlID, "document", "Quarterly access review report", &cadence); err != nil {
		t.Fatalf("add evidence: %v", err)
	}
	if _, err := svc.AddEvidence(ctx, superID, "pci-dss", controlID, "carrier_pigeon", "x", nil); err == nil {
		t.Fatal("expected invalid evidence type to fail")
	}

	// Policy template with markdown body linked to the control.
	tplID, err := svc.UpsertPolicyTemplate(ctx, superID, "pci-dss", gid.Nil, "Access Control Policy", "# Access Control\n\nAll access is role-based…", []string{"PCI-7.2"})
	if err != nil {
		t.Fatalf("add policy template: %v", err)
	}
	// Update body + keep link.
	if _, err := svc.UpsertPolicyTemplate(ctx, superID, "pci-dss", tplID, "Access Control Policy v2", "# Updated", []string{"PCI-7.2"}); err != nil {
		t.Fatalf("update policy template: %v", err)
	}

	// Views assemble correctly.
	controls, err := svc.ControlsOfFramework(ctx, "pci-dss")
	if err != nil || len(controls) != 1 {
		t.Fatalf("controls view: %v (n=%d)", err, len(controls))
	}
	c := controls[0]
	if c.Code != "PCI-7.2" || len(c.Items) != 2 || len(c.Evidence) != 1 || c.Evidence[0].Type != "document" {
		t.Fatalf("unexpected control view: %+v", c)
	}

	policies, err := svc.PolicyTemplatesOfFramework(ctx, "pci-dss")
	if err != nil || len(policies) != 1 {
		t.Fatalf("policies view: %v (n=%d)", err, len(policies))
	}
	p := policies[0]
	if p.Name != "Access Control Policy v2" || p.Body != "# Updated" || len(p.Controls) != 1 || p.Controls[0] != "PCI-7.2" {
		t.Fatalf("unexpected policy view: %+v", p)
	}

	_ = registry.PolicyTemplateView{}
}
