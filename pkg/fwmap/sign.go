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
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
)

var (
	// ErrNotSigned is returned when verifying a set that carries no signature.
	ErrNotSigned = errors.New("fwmap: mapping set is not signed")
	// ErrUnknownKey is returned when the signature references a key id not in
	// the caller's pinned set.
	ErrUnknownKey = errors.New("fwmap: signature key id is not trusted")
	// ErrContentHashMismatch is returned when the recomputed hash disagrees with
	// the one in the signature block (content tampering).
	ErrContentHashMismatch = errors.New("fwmap: content hash mismatch")
	// ErrBadSignature is returned when the ed25519 signature does not verify.
	ErrBadSignature = errors.New("fwmap: signature verification failed")
)

// Canonicalize returns the deterministic JSON encoding with the signature block
// omitted, by round-tripping through a generic map so encoding/json emits keys
// in sorted order recursively. This is the exact byte sequence hashed and
// signed, and re-derived by a verifier — the same technique as fwschema, so a
// mapping set and a framework are canonicalised identically.
func (m *MappingSet) Canonicalize() ([]byte, error) {
	unsigned := *m
	unsigned.Signature = nil

	raw, err := json.Marshal(&unsigned)
	if err != nil {
		return nil, fmt.Errorf("cannot marshal mapping set: %w", err)
	}

	var generic any
	if err := json.Unmarshal(raw, &generic); err != nil {
		return nil, fmt.Errorf("cannot normalize mapping set: %w", err)
	}

	canonical, err := json.Marshal(generic)
	if err != nil {
		return nil, fmt.Errorf("cannot marshal canonical mapping set: %w", err)
	}
	return canonical, nil
}

// ContentHash returns the "sha256:<hex>" digest of the canonical content.
func (m *MappingSet) ContentHash() (string, error) {
	canonical, err := m.Canonicalize()
	if err != nil {
		return "", err
	}
	return hashCanonical(canonical), nil
}

func hashCanonical(canonical []byte) string {
	sum := sha256.Sum256(canonical)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Sign canonicalises the set and attaches an ed25519 signature over the
// canonical bytes, with the content hash and key id. Any prior signature is
// replaced.
func (m *MappingSet) Sign(priv ed25519.PrivateKey, keyID string) error {
	if len(priv) != ed25519.PrivateKeySize {
		return fmt.Errorf("fwmap: invalid ed25519 private key size %d", len(priv))
	}
	if keyID == "" {
		return errors.New("fwmap: signing key id is required")
	}

	m.Signature = nil
	canonical, err := m.Canonicalize()
	if err != nil {
		return err
	}
	sig := ed25519.Sign(priv, canonical)
	m.Signature = &Signature{
		Alg:         SignatureAlg,
		KeyID:       keyID,
		Value:       base64.StdEncoding.EncodeToString(sig),
		ContentHash: hashCanonical(canonical),
	}
	return nil
}

// Verify checks the set's signature against pinned public keys indexed by key
// id: it re-derives the canonical content, confirms the embedded hash matches
// (defeating tampering), then verifies the ed25519 signature. Same path online
// and air-gapped.
func (m *MappingSet) Verify(pubkeys map[string]ed25519.PublicKey) error {
	if m.Signature == nil {
		return ErrNotSigned
	}
	sigBlock := m.Signature

	if sigBlock.Alg != SignatureAlg {
		return fmt.Errorf("fwmap: unsupported signature algorithm %q", sigBlock.Alg)
	}
	pub, ok := pubkeys[sigBlock.KeyID]
	if !ok {
		return fmt.Errorf("%w: %q", ErrUnknownKey, sigBlock.KeyID)
	}
	if len(pub) != ed25519.PublicKeySize {
		return fmt.Errorf("fwmap: invalid ed25519 public key size %d for key %q", len(pub), sigBlock.KeyID)
	}

	canonical, err := m.Canonicalize()
	if err != nil {
		return err
	}
	if hashCanonical(canonical) != sigBlock.ContentHash {
		return ErrContentHashMismatch
	}
	sig, err := base64.StdEncoding.DecodeString(sigBlock.Value)
	if err != nil {
		return fmt.Errorf("fwmap: cannot decode signature value: %w", err)
	}
	if !ed25519.Verify(pub, canonical, sig) {
		return ErrBadSignature
	}
	return nil
}
