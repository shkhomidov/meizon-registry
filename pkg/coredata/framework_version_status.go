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

package coredata

// FrameworkVersionStatus is the lifecycle state of a version. The valid
// transitions are DRAFT → IN_REVIEW → APPROVED → PUBLISHED → DEPRECATED.
type FrameworkVersionStatus string

const (
	FrameworkVersionStatusDraft      FrameworkVersionStatus = "DRAFT"
	FrameworkVersionStatusInReview   FrameworkVersionStatus = "IN_REVIEW"
	FrameworkVersionStatusApproved   FrameworkVersionStatus = "APPROVED"
	FrameworkVersionStatusPublished  FrameworkVersionStatus = "PUBLISHED"
	FrameworkVersionStatusDeprecated FrameworkVersionStatus = "DEPRECATED"
)

// IsValid reports whether s is a known status.
func (s FrameworkVersionStatus) IsValid() bool {
	switch s {
	case FrameworkVersionStatusDraft, FrameworkVersionStatusInReview,
		FrameworkVersionStatusApproved, FrameworkVersionStatusPublished,
		FrameworkVersionStatusDeprecated:
		return true
	default:
		return false
	}
}

func (s FrameworkVersionStatus) String() string { return string(s) }

// IdentityStatus is the activation state of an identity.
type IdentityStatus string

const (
	IdentityStatusPending IdentityStatus = "pending"
	IdentityStatusActive  IdentityStatus = "active"
)

// IsValid reports whether s is a known identity status.
func (s IdentityStatus) IsValid() bool {
	return s == IdentityStatusPending || s == IdentityStatusActive
}

func (s IdentityStatus) String() string { return string(s) }

// ApprovalDecision records a reviewer's verdict on a version.
type ApprovalDecision string

const (
	ApprovalDecisionApproved ApprovalDecision = "approved"
	ApprovalDecisionRejected ApprovalDecision = "rejected"
)

// IsValid reports whether d is a known decision.
func (d ApprovalDecision) IsValid() bool {
	return d == ApprovalDecisionApproved || d == ApprovalDecisionRejected
}

func (d ApprovalDecision) String() string { return string(d) }
