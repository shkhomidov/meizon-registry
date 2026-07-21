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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// Canonicalize returns the deterministic JSON encoding of the framework with the
// signature block omitted. It is stable regardless of Go struct field order:
// the object is round-tripped through a generic map so that encoding/json emits
// object keys in sorted order recursively. This is the exact byte sequence that
// is hashed and signed, and that a verifier re-derives.
func (f *Framework) Canonicalize() ([]byte, error) {
	unsigned := *f
	unsigned.Signature = nil

	raw, err := json.Marshal(&unsigned)
	if err != nil {
		return nil, fmt.Errorf("cannot marshal framework: %w", err)
	}

	var generic any
	if err := json.Unmarshal(raw, &generic); err != nil {
		return nil, fmt.Errorf("cannot normalize framework: %w", err)
	}

	// encoding/json marshals map[string]any keys in sorted order, giving a
	// canonical, implementation-independent form.
	canonical, err := json.Marshal(generic)
	if err != nil {
		return nil, fmt.Errorf("cannot marshal canonical framework: %w", err)
	}

	return canonical, nil
}

// ContentHash returns the "sha256:<hex>" digest of the canonicalised content.
func (f *Framework) ContentHash() (string, error) {
	canonical, err := f.Canonicalize()
	if err != nil {
		return "", err
	}

	return hashCanonical(canonical), nil
}

func hashCanonical(canonical []byte) string {
	sum := sha256.Sum256(canonical)
	return "sha256:" + hex.EncodeToString(sum[:])
}
