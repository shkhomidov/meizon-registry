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
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"testing"

	"go.meizon.cloud/registry/pkg/fwschema"
)

func sampleFramework() *fwschema.Framework {
	parent := "3.1"
	return &fwschema.Framework{
		SchemaVersion: fwschema.SchemaVersion,
		ID:            "nist-800-171-r2",
		Name:          "NIST SP 800-171 Rev 2",
		ShortName:     "NIST 800-171",
		Version:       "2.1.0",
		Status:        fwschema.StatusPublished,
		Region:        "US",
		Authority:     "NIST",
		License:       fwschema.LicensePublicDomain,
		Description:   "Protecting Controlled Unclassified Information.",
		RevisionNotes: "initial import",
		PublishedAt:   "2026-07-13T00:00:00Z",
		Controls: []fwschema.Control{
			{
				ID:          "3.1.1",
				Name:        "Limit system access",
				Description: "Limit information system access to authorized users.",
				Section:     "3.1 Access Control",
				ParentID:    &parent,
				Guidance:    "Access enforcement mechanisms...",
				References:  []string{"AC-2", "AC-3"},
				Mappings:    []fwschema.Mapping{{Framework: "nist-800-53-r5", Control: "AC-2"}},
			},
			{
				ID:          "3.1",
				Name:        "Access Control family",
				Description: "The access control requirements.",
			},
		},
	}
}

func TestValidateOK(t *testing.T) {
	if err := sampleFramework().Validate(); err != nil {
		t.Fatalf("expected valid framework, got: %v", err)
	}
}

func TestValidateRejectsBadInput(t *testing.T) {
	cases := map[string]func(*fwschema.Framework){
		"missing id":        func(f *fwschema.Framework) { f.ID = "" },
		"missing version":   func(f *fwschema.Framework) { f.Version = "" },
		"bad status":        func(f *fwschema.Framework) { f.Status = "LIVE" },
		"bad license":       func(f *fwschema.Framework) { f.License = "gpl" },
		"no controls":       func(f *fwschema.Framework) { f.Controls = nil },
		"duplicate control": func(f *fwschema.Framework) { f.Controls[1].ID = "3.1.1" },
		"dangling parent":   func(f *fwschema.Framework) { orphan := "nope"; f.Controls[0].ParentID = &orphan },
		"bad schemaVersion": func(f *fwschema.Framework) { f.SchemaVersion = "2.0" },
	}

	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			f := sampleFramework()
			mutate(f)
			if err := f.Validate(); err == nil {
				t.Fatalf("expected validation error for %q, got nil", name)
			}
		})
	}
}

func TestCanonicalizeDeterministic(t *testing.T) {
	f := sampleFramework()

	a, err := f.Canonicalize()
	if err != nil {
		t.Fatalf("canonicalize: %v", err)
	}

	// A signature block must not affect the canonical bytes.
	f.Signature = &fwschema.Signature{Alg: "ed25519", KeyID: "k", Value: "x", ContentHash: "sha256:y"}
	b, err := f.Canonicalize()
	if err != nil {
		t.Fatalf("canonicalize: %v", err)
	}

	if !bytes.Equal(a, b) {
		t.Fatal("canonical bytes changed when a signature block was present")
	}

	// Re-decoding and re-encoding the same logical framework yields identical
	// canonical bytes regardless of source field ordering.
	var reordered fwschema.Framework
	if err := json.Unmarshal(a, &reordered); err != nil {
		t.Fatalf("unmarshal canonical: %v", err)
	}
	c, err := reordered.Canonicalize()
	if err != nil {
		t.Fatalf("canonicalize reordered: %v", err)
	}
	if !bytes.Equal(a, c) {
		t.Fatal("canonicalization is not stable across a JSON round trip")
	}
}

func TestSignVerifyRoundTrip(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}

	f := sampleFramework()
	if err := f.Sign(priv, "reg-2026"); err != nil {
		t.Fatalf("sign: %v", err)
	}

	if f.Signature == nil || f.Signature.KeyID != "reg-2026" {
		t.Fatal("signature block not populated")
	}

	keys := map[string]ed25519.PublicKey{"reg-2026": pub}
	if err := f.Verify(keys); err != nil {
		t.Fatalf("verify: %v", err)
	}
}

func TestVerifyRejectsTamper(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	keys := map[string]ed25519.PublicKey{"reg-2026": pub}

	// Tamper with content after signing.
	f := sampleFramework()
	_ = f.Sign(priv, "reg-2026")
	f.Controls[0].Description = "malicious change"
	if err := f.Verify(keys); err == nil {
		t.Fatal("expected verification to fail after content tampering")
	}

	// Unknown key id.
	g := sampleFramework()
	_ = g.Sign(priv, "attacker-key")
	if err := g.Verify(keys); err == nil {
		t.Fatal("expected verification to fail for untrusted key id")
	}

	// Missing signature.
	h := sampleFramework()
	if err := h.Verify(keys); err == nil {
		t.Fatal("expected verification to fail when unsigned")
	}

	// Signature bytes flipped, content hash left intact.
	i := sampleFramework()
	_ = i.Sign(priv, "reg-2026")
	i.Signature.Value = "AAAA" + i.Signature.Value[4:]
	if err := i.Verify(keys); err == nil {
		t.Fatal("expected verification to fail when signature value is altered")
	}
}

func TestFlattenRoundTrip(t *testing.T) {
	f := sampleFramework()
	seed := f.Flatten()

	if seed.ID != f.ID || seed.Name != f.Name {
		t.Fatal("seed metadata does not match source")
	}
	if len(seed.Controls) != len(f.Controls) {
		t.Fatalf("expected %d controls, got %d", len(f.Controls), len(seed.Controls))
	}
	for i, c := range f.Controls {
		if seed.Controls[i].ID != c.ID || seed.Controls[i].Name != c.Name || seed.Controls[i].Description != c.Description {
			t.Fatalf("control %d not flattened losslessly", i)
		}
	}

	// The flat seed is byte-stable for a given version.
	a, _ := json.Marshal(seed)
	b, _ := json.Marshal(f.Flatten())
	if !bytes.Equal(a, b) {
		t.Fatal("flatten output is not deterministic")
	}
}
