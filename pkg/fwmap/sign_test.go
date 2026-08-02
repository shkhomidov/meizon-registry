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

package fwmap

import (
	"crypto/ed25519"
	"errors"
	"testing"
)

func sampleSet() *MappingSet {
	return &MappingSet{
		Schema: SchemaMarker,
		Source: Endpoint{Framework: "iso-27001", Version: "2022"},
		Target: Endpoint{Framework: "nist-csf", Version: "2.0"},
		Requirements: []Mapping{
			{SourceRef: "A.5.1", TargetRef: "GV.PO", Relation: "equivalent"},
			{SourceRef: "A.8.2", TargetRef: "PR.AC", Relation: "partial", Notes: "subset"},
		},
		Controls: []Mapping{
			{SourceRef: "ctrl-1", TargetRef: "ctrl-x", Relation: "equivalent"},
		},
	}
}

func keys(t *testing.T) (ed25519.PrivateKey, map[string]ed25519.PublicKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	return priv, map[string]ed25519.PublicKey{"k1": pub}
}

func TestSignVerifyRoundTrip(t *testing.T) {
	priv, pinned := keys(t)
	m := sampleSet()

	if err := m.Sign(priv, "k1"); err != nil {
		t.Fatalf("sign: %v", err)
	}
	if m.Signature == nil || m.Signature.ContentHash == "" {
		t.Fatal("sign did not populate the signature block")
	}
	if err := m.Verify(pinned); err != nil {
		t.Fatalf("verify should succeed on an untouched set: %v", err)
	}
}

func TestVerifyRejectsTamper(t *testing.T) {
	priv, pinned := keys(t)
	m := sampleSet()
	if err := m.Sign(priv, "k1"); err != nil {
		t.Fatalf("sign: %v", err)
	}

	// Alter a mapping after signing; the content hash no longer matches.
	m.Requirements[0].TargetRef = "GV.XX"
	if err := m.Verify(pinned); !errors.Is(err, ErrContentHashMismatch) {
		t.Fatalf("tampered set should fail with content-hash mismatch, got: %v", err)
	}
}

func TestVerifyRejectsUnknownKey(t *testing.T) {
	priv, _ := keys(t)
	m := sampleSet()
	if err := m.Sign(priv, "k1"); err != nil {
		t.Fatalf("sign: %v", err)
	}
	// A verifier that does not pin k1 must refuse it.
	other := map[string]ed25519.PublicKey{}
	if err := m.Verify(other); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("unknown key should be refused, got: %v", err)
	}
}

func TestVerifyRejectsWrongKey(t *testing.T) {
	priv, _ := keys(t)
	otherPub, _, _ := ed25519.GenerateKey(nil)
	m := sampleSet()
	if err := m.Sign(priv, "k1"); err != nil {
		t.Fatalf("sign: %v", err)
	}
	// Right key id, wrong key material: the signature must not verify.
	wrong := map[string]ed25519.PublicKey{"k1": otherPub}
	if err := m.Verify(wrong); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("wrong key should fail signature check, got: %v", err)
	}
}

func TestVerifyUnsigned(t *testing.T) {
	_, pinned := keys(t)
	if err := sampleSet().Verify(pinned); !errors.Is(err, ErrNotSigned) {
		t.Fatalf("an unsigned set must be refused, got: %v", err)
	}
}

// TestContentHashStable pins the canonical form: the same logical set must hash
// identically regardless of Go struct field order or signature presence, or a
// consumer's re-derivation would spuriously mismatch.
func TestContentHashStable(t *testing.T) {
	a := sampleSet()
	b := sampleSet()

	ha, err := a.ContentHash()
	if err != nil {
		t.Fatalf("hash a: %v", err)
	}
	hb, err := b.ContentHash()
	if err != nil {
		t.Fatalf("hash b: %v", err)
	}
	if ha != hb {
		t.Fatalf("identical sets hashed differently: %s vs %s", ha, hb)
	}

	// Signing then re-hashing the canonical (signature-excluded) content must
	// still equal the pre-sign hash — the signature is not part of what is hashed.
	priv, _ := keys(t)
	if err := a.Sign(priv, "k1"); err != nil {
		t.Fatalf("sign: %v", err)
	}
	if a.Signature.ContentHash != ha {
		t.Fatalf("content hash changed after signing: %s vs %s", a.Signature.ContentHash, ha)
	}
}
