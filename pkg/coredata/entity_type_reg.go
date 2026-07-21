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

import "go.meizon.cloud/registry/pkg/gid"

// Entity type identifiers. These are stable, sequential and never reused: a
// removed type keeps a "_" placeholder so its number is retired forever.
const (
	TenantEntityType            uint16 = 0
	RegionEntityType            uint16 = 1
	IdentityEntityType          uint16 = 2
	MembershipEntityType        uint16 = 3
	FrameworkEntityType         uint16 = 4
	FrameworkVersionEntityType  uint16 = 5
	ControlEntityType           uint16 = 6
	ApprovalEntityType          uint16 = 7
	DistributionTokenEntityType uint16 = 8
	SigningKeyEntityType        uint16 = 9
	AuditLogEntityType          uint16 = 10
	DownloadEntityType          uint16 = 11

	RequirementCategoryEntityType uint16 = 12
	RequirementEntityType         uint16 = 13
	// 14 (section) and 15 (requirement item) are retired: the structure is
	// Category → Requirement → Control. Reserved, never to be reused, so old
	// GIDs stay unambiguous.
	RequirementCrossMappingEntityType uint16 = 16
	AIGenerationEntityType            uint16 = 17
	LLMSettingEntityType              uint16 = 18
	ControlLibraryEntityType          uint16 = 19
	PolicyTemplateEntityType          uint16 = 20
	EvidenceGuidanceEntityType        uint16 = 21
	FrameworkTranslationEntityType    uint16 = 22
	MappingProposalEntityType         uint16 = 23
	MappingRunEntityType              uint16 = 24
	ControlCrossMappingEntityType     uint16 = 25
	SourceDocumentEntityType          uint16 = 26
	IngestJobEntityType               uint16 = 27
)

// NewEntityFromID returns a zero-valued entity of the type encoded in id, and
// whether the type is known. Every new entity type must be added here.
func NewEntityFromID(id gid.GID) (any, bool) {
	switch id.EntityType() {
	case IdentityEntityType:
		return &Identity{ID: id}, true
	case MembershipEntityType:
		return &Membership{ID: id}, true
	case FrameworkEntityType:
		return &Framework{ID: id}, true
	case FrameworkVersionEntityType:
		return &FrameworkVersion{ID: id}, true
	case ControlEntityType:
		return &Control{ID: id}, true
	case ApprovalEntityType:
		return &Approval{ID: id}, true
	case DistributionTokenEntityType:
		return &DistributionToken{ID: id}, true
	case SigningKeyEntityType:
		return &SigningKey{ID: id}, true
	case AuditLogEntityType:
		return &AuditLogEntry{ID: id}, true
	case DownloadEntityType:
		return &Download{ID: id}, true
	case RequirementCategoryEntityType:
		return &RequirementCategory{ID: id}, true
	case RequirementEntityType:
		return &Requirement{ID: id}, true
	case RequirementCrossMappingEntityType:
		return &RequirementCrossMapping{ID: id}, true
	case AIGenerationEntityType:
		return &AIGeneration{ID: id}, true
	case LLMSettingEntityType:
		return &LLMSetting{ID: id}, true
	case ControlLibraryEntityType:
		return &ControlLibraryEntry{ID: id}, true
	case PolicyTemplateEntityType:
		return &PolicyTemplate{ID: id}, true
	case EvidenceGuidanceEntityType:
		return &EvidenceGuidance{ID: id}, true
	case FrameworkTranslationEntityType:
		return &FrameworkTranslation{ID: id}, true
	case MappingProposalEntityType:
		return &MappingProposal{ID: id}, true
	case MappingRunEntityType:
		return &MappingRun{ID: id}, true
	case ControlCrossMappingEntityType:
		return &ControlCrossMapping{ID: id}, true
	case SourceDocumentEntityType:
		return &SourceDocument{ID: id}, true
	case IngestJobEntityType:
		return &IngestJob{ID: id}, true
	default:
		return nil, false
	}
}
