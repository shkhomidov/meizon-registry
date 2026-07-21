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

// Package fwschema is the interchange contract between the Meizon Registry and
// GRC instances. It defines the rich exchange schema v1 that a published
// framework version serialises to, its validation rules, a deterministic
// canonicalisation used for content hashing and ed25519 signing, and a Flatten
// method that losslessly reduces a rich framework to the flat GRC seed the
// platform imports. Both the registry and every GRC instance depend on this
// package so a bundle round-trips with zero manual editing.
package fwschema

// SchemaVersion is the version string every rich bundle must carry.
const SchemaVersion = "1.0"

// SignatureAlg is the only signing algorithm supported by v1.
const SignatureAlg = "ed25519"

// Status is the lifecycle status embedded in a serialised framework version.
type Status string

const (
	StatusDraft      Status = "DRAFT"
	StatusInReview   Status = "IN_REVIEW"
	StatusApproved   Status = "APPROVED"
	StatusPublished  Status = "PUBLISHED"
	StatusDeprecated Status = "DEPRECATED"
)

// IsValid reports whether s is a known lifecycle status.
func (s Status) IsValid() bool {
	switch s {
	case StatusDraft, StatusInReview, StatusApproved, StatusPublished, StatusDeprecated:
		return true
	default:
		return false
	}
}

// License classifies distribution rights. Proprietary frameworks (SOC 2, ISO,
// CIS, …) may only be distributed to owning tenants; public-domain and
// statutory frameworks (NIST, GDPR, HIPAA, …) may be public. See the copyright
// gate in the distribution layer.
type License string

const (
	LicensePublicDomain License = "public-domain"
	LicenseStatutory    License = "statutory"
	LicenseProprietary  License = "proprietary"
)

// IsValid reports whether l is a known license classification.
func (l License) IsValid() bool {
	switch l {
	case LicensePublicDomain, LicenseStatutory, LicenseProprietary:
		return true
	default:
		return false
	}
}

// IsPublicDistributable reports whether a framework with this license may be
// served to the public catalog.
func (l License) IsPublicDistributable() bool {
	return l == LicensePublicDomain || l == LicenseStatutory
}

type (
	// Framework is the rich exchange schema v1 root object.
	Framework struct {
		SchemaVersion string  `json:"schemaVersion"`
		ID            string  `json:"id"`
		Name          string  `json:"name"`
		ShortName     string  `json:"shortName,omitempty"`
		Version       string  `json:"version"`
		Status        Status  `json:"status"`
		Region        string  `json:"region"`
		Authority     string  `json:"authority,omitempty"`
		License       License `json:"license"`
		Description   string  `json:"description,omitempty"`
		RevisionNotes string  `json:"revisionNotes,omitempty"`
		PublishedAt   string  `json:"publishedAt,omitempty"`
		// Controls is the flat v1 body (schemaVersion 1.0).
		Controls []Control `json:"controls,omitempty"`
		// Categories is the hierarchical v2 body (schemaVersion 2.0).
		Categories []Category `json:"categories,omitempty"`
		Signature  *Signature `json:"signature,omitempty"`
	}

	// Control is a single requirement within a framework version.
	Control struct {
		ID          string    `json:"id"`
		Name        string    `json:"name"`
		Description string    `json:"description"`
		Section     string    `json:"section,omitempty"`
		ParentID    *string   `json:"parentId,omitempty"`
		Guidance    string    `json:"guidance,omitempty"`
		References  []string  `json:"references,omitempty"`
		Mappings    []Mapping `json:"mappings,omitempty"`
	}

	// Mapping expresses a cross-framework relationship between this control and a
	// control in another framework.
	Mapping struct {
		Framework string `json:"framework"`
		Control   string `json:"control"`
	}

	// Signature is the detached ed25519 signature over the canonicalised content
	// (everything except the signature block). ContentHash is a sha256 digest of
	// the same canonical bytes and lets a verifier detect content tampering
	// independently of the signature check.
	Signature struct {
		Alg         string `json:"alg"`
		KeyID       string `json:"keyId"`
		Value       string `json:"value"`
		ContentHash string `json:"contentHash"`
	}
)
