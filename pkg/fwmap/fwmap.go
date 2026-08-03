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

// Package fwmap is the exchange format for cross-framework mappings.
//
// Mappings could not live inside a framework bundle: a distributed bundle is
// re-assembled from live tables on every request while its signature is the
// stored one, so editing a mapping after publication would change the bytes and
// make every consumer reject the bundle as tampered. So a mapping set is its own
// signed, independently-published artifact — adding or correcting a mapping
// never touches the frameworks it connects.
//
// It carries the same ed25519 signing as pkg/fwschema, deliberately: the trust
// mechanism a consumer must reason about is identical for a framework and for a
// mapping set.
package fwmap

// SchemaMarker is the literal "$schema" value of the format.
const SchemaMarker = "meizon-mappingset/v1"

// SignatureAlg is the only signing algorithm this format supports.
const SignatureAlg = "ed25519"

type (
	// MappingSet is every cross-mapping from one source framework version to one
	// target framework version, signed as a unit. Both ends are addressed by
	// code, never by a resolved id, so a set can be authored and published
	// before the target framework is loaded on the consumer — it resolves late,
	// or dangles harmlessly.
	MappingSet struct {
		Schema       string    `json:"$schema"`
		Source       Endpoint  `json:"source"`
		Target       Endpoint  `json:"target"`
		Requirements []Mapping `json:"requirementMappings"`
		Controls     []Mapping `json:"controlMappings"`

		// Provenance a consumer can display without a second request. These are
		// part of the signed content, so they cannot be altered after issue.
		ApprovedBy  string `json:"approvedBy,omitempty"`
		PublishedBy string `json:"publishedBy,omitempty"`
		PublishedAt string `json:"publishedAt,omitempty"`

		Signature *Signature `json:"signature,omitempty"`
	}

	// Endpoint identifies one side of the mapping by framework reference and
	// version.
	Endpoint struct {
		Framework string `json:"framework"`
		Version   string `json:"version"`
	}

	// Mapping relates one source node to one target node by their codes.
	// Relation classifies the correspondence (equivalent, partial, superset,
	// subset). SourceRef is a code in the source framework; TargetRef a code in
	// the target.
	Mapping struct {
		SourceRef string `json:"sourceRef"`
		TargetRef string `json:"targetRef"`
		Relation  string `json:"relation"`
		Notes     string `json:"notes,omitempty"`
	}

	// Signature is the detached ed25519 signature block, identical in shape to a
	// framework's so a verifier treats both the same way.
	Signature struct {
		Alg         string `json:"alg"`
		KeyID       string `json:"keyId"`
		Value       string `json:"value"`
		ContentHash string `json:"contentHash"`
	}
)

// Pair is the identity of a mapping set: which source version maps to which
// target version. It is what the distribution catalog and the unique constraint
// key on.
type Pair struct {
	SourceFramework string
	SourceVersion   string
	TargetFramework string
	TargetVersion   string
}

// Pair returns this set's identity.
func (m *MappingSet) Pair() Pair {
	return Pair{
		SourceFramework: m.Source.Framework,
		SourceVersion:   m.Source.Version,
		TargetFramework: m.Target.Framework,
		TargetVersion:   m.Target.Version,
	}
}

// IsEmpty reports whether the set carries no mappings at all — worth refusing to
// publish, since an empty signed artifact conveys nothing.
func (m *MappingSet) IsEmpty() bool {
	return len(m.Requirements) == 0 && len(m.Controls) == 0
}
