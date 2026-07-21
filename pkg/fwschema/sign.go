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
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
)

var (
	// ErrNotSigned is returned when verifying a framework that carries no
	// signature block.
	ErrNotSigned = errors.New("fwschema: framework is not signed")

	// ErrUnknownKey is returned when the signature references a key id that is
	// not in the caller's pinned key set.
	ErrUnknownKey = errors.New("fwschema: signature key id is not trusted")

	// ErrContentHashMismatch is returned when the recomputed content hash does
	// not match the value embedded in the signature block (content tampering).
	ErrContentHashMismatch = errors.New("fwschema: content hash mismatch")

	// ErrBadSignature is returned when the ed25519 signature does not verify.
	ErrBadSignature = errors.New("fwschema: signature verification failed")
)

// Sign canonicalises the framework and attaches an ed25519 signature over the
// canonical bytes, along with the content hash and the signing key id. Any
// pre-existing signature is replaced.
func (f *Framework) Sign(priv ed25519.PrivateKey, keyID string) error {
	if len(priv) != ed25519.PrivateKeySize {
		return fmt.Errorf("fwschema: invalid ed25519 private key size %d", len(priv))
	}

	if keyID == "" {
		return errors.New("fwschema: signing key id is required")
	}

	f.Signature = nil

	canonical, err := f.Canonicalize()
	if err != nil {
		return err
	}

	sig := ed25519.Sign(priv, canonical)

	f.Signature = &Signature{
		Alg:         SignatureAlg,
		KeyID:       keyID,
		Value:       base64.StdEncoding.EncodeToString(sig),
		ContentHash: hashCanonical(canonical),
	}

	return nil
}

// Verify checks the framework's signature against a set of pinned public keys
// indexed by key id. It re-derives the canonical content, confirms the embedded
// content hash matches (defeating content tampering) and then verifies the
// ed25519 signature. The same code path is used for online sync and air-gapped
// bundle import.
func (f *Framework) Verify(pubkeys map[string]ed25519.PublicKey) error {
	if f.Signature == nil {
		return ErrNotSigned
	}

	sigBlock := f.Signature

	if sigBlock.Alg != SignatureAlg {
		return fmt.Errorf("fwschema: unsupported signature algorithm %q", sigBlock.Alg)
	}

	pub, ok := pubkeys[sigBlock.KeyID]
	if !ok {
		return fmt.Errorf("%w: %q", ErrUnknownKey, sigBlock.KeyID)
	}

	if len(pub) != ed25519.PublicKeySize {
		return fmt.Errorf("fwschema: invalid ed25519 public key size %d for key %q", len(pub), sigBlock.KeyID)
	}

	canonical, err := f.Canonicalize()
	if err != nil {
		return err
	}

	if hashCanonical(canonical) != sigBlock.ContentHash {
		return ErrContentHashMismatch
	}

	sig, err := base64.StdEncoding.DecodeString(sigBlock.Value)
	if err != nil {
		return fmt.Errorf("fwschema: cannot decode signature value: %w", err)
	}

	if !ed25519.Verify(pub, canonical, sig) {
		return ErrBadSignature
	}

	return nil
}
