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

package registry

import (
	"strings"

	"go.meizon.cloud/registry/pkg/coredata"
	"go.meizon.cloud/registry/pkg/fwflat"
	"go.meizon.cloud/registry/pkg/gid"
)

// GeneratedFramework is a v2 framework the model produced from a source
// document, awaiting the auditor's review. It is NOT yet persisted.
//
// Provenance links each generated node back to the exact passage in the source
// document that produced it (keys: "cat:<code>", "req:<code>", "item:<code>").
// It is review-only — used to highlight the source in the split editor — and is
// never persisted into the framework or its signed content. DocumentText is the
// source text the review UI renders on the right pane.
type GeneratedFramework struct {
	GenerationID gid.GID           `json:"generationId"`
	Document     *fwflat.Framework `json:"document"`
	Summary      GeneratedSummary  `json:"summary"`
	Provenance   map[string]string `json:"provenance"`
	DocumentText string            `json:"documentText"`
	// Note is an advisory QA remark from the pipeline's final check (e.g.
	// "section 8 looks thin") — surfaced in the review UI, never auto-applied.
	Note string `json:"note,omitempty"`
	// SynthesizedControls lists requirement refs that got a placeholder control
	// because the model proposed none — the weakest output, flagged for review.
	SynthesizedControls []string `json:"synthesizedControls,omitempty"`
	// Warnings are advisory quality findings from validation. They never block
	// acceptance — the reviewer decides whether they matter.
	Warnings []string `json:"warnings,omitempty"`
	// Duplicates groups requirements sharing an identical description, so the
	// editor can mark them in place rather than naming refs in a sentence.
	Duplicates []fwflat.DuplicateGroup `json:"duplicates,omitempty"`
}

// GeneratedSummary is a quick digest for the review UI.
type GeneratedSummary struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Version      string `json:"version"`
	Category     string `json:"category"`
	Region       string `json:"region"`
	Categories   int    `json:"categories"`
	Requirements int    `json:"requirements"`
	Controls     int    `json:"controls"`
	Links        int    `json:"links"`
}

// summarizeFlat digests a flat framework for the review UI.
func summarizeFlat(doc *fwflat.Framework) GeneratedSummary {
	c := doc.Count()
	return GeneratedSummary{
		ID: doc.ID, Name: doc.Name, Version: doc.Version,
		Category: doc.Category, Region: doc.PrimaryRegion(),
		Categories: c.Categories, Requirements: c.Requirements,
		Controls: c.Controls, Links: c.Links,
	}
}

const maxGeneratedItems = 400

// Generation runs as several LLM steps, each with its own steerable instruction
// that a superadmin can view and edit in Settings. The mandatory output contract
// for each step (JSON-only, schema, provenance) is applied in code on top of the
// instruction, so editing an instruction can never break the output format.
const (
	// DefaultGenerationInstruction steers the EXTRACT step (the bulk of the work).
	DefaultGenerationInstruction = "Write every name, title, description and guidance in the SAME language as the source document — never translate; keep the document's own language and terminology. " +
		"Preserve the full wording of each requirement — complete titles and descriptions; do not truncate, summarise to a few words, or drop detail. " +
		"Paraphrase in your own words; do not reproduce long verbatim passages of copyrighted text. If the document is proprietary, keep it paraphrased but still complete."

	// DefaultIdentifyInstruction steers the IDENTIFY step.
	DefaultIdentifyInstruction = "Never guess a version, date or publisher — use an empty string when the document does not state it. " +
		"Prefer the document's own title and terminology for the name, and keep the id a short kebab-case slug."

	// DefaultControlsInstruction steers the CONTROLS step.
	DefaultControlsInstruction = "Propose the concrete control an organisation would implement, not a restatement of the requirement. " +
		"Reuse an existing control ref whenever the same implementation satisfies several requirements rather than creating near-duplicates. " +
		"Keep control descriptions generic and reusable — never name a specific company, product or tool."

	// DefaultQAInstruction steers the final QA review.
	DefaultQAInstruction = "Judge whether the extracted structure looks complete for a standard of this type. " +
		"Call out anything obviously missing, mis-grouped or duplicated. Be brief and concrete."

	// DefaultMappingInstruction steers the cross-mapping adjudication step.
	DefaultMappingInstruction = "Map on the OBLIGATION, not on wording: two requirements match when satisfying one substantially satisfies the other. " +
		"Shared vocabulary alone is not a mapping — most requirement pairs across two frameworks genuinely do not correspond, and returning none is the correct answer far more often than not. " +
		"Reserve equivalent for near-identical scope; use partial when they overlap but neither covers the other."

	// DefaultTranslateInstruction steers the TRANSLATE step.
	DefaultTranslateInstruction = "Translate the meaning, not the words: produce text a compliance professional in the target language would recognise as natural. " +
		"Keep regulator names, standard names, legal citations and defined terms in their original form. " +
		"Preserve the full detail of each obligation — never summarise, shorten or merge sentences."
)

// StepInstruction describes one editable step for the Settings page.
type StepInstruction struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Description string `json:"description"`
	Instruction string `json:"instruction"` // effective (stored or default)
	Default     string `json:"default"`
	Contract    string `json:"contract"` // read-only, always applied
}

// identifySystemPrompt / qaSystemPrompt mirror frameworkSystemPrompt: fixed
// contract first, then the superadmin-editable instruction.
func identifySystemPrompt(setting *coredata.LLMSetting) string {
	instruction := DefaultIdentifyInstruction
	if setting != nil && strings.TrimSpace(setting.IdentifyInstruction) != "" {
		instruction = setting.IdentifyInstruction
	}
	return identifyContract + "\n\nInstructions:\n" + instruction
}

func qaSystemPrompt(setting *coredata.LLMSetting) string {
	instruction := DefaultQAInstruction
	if setting != nil && strings.TrimSpace(setting.QAInstruction) != "" {
		instruction = setting.QAInstruction
	}
	return qaContract + "\n\nInstructions:\n" + instruction
}

// The fixed contracts of docs/DOCUMENT-TO-FRAMEWORK-V2-GUIDE.md. These are
// always applied in code; the editable instruction is appended underneath, so a
// superadmin can steer behaviour but never break the output shape.
const (
	identifyContract = `You identify compliance standards. From the document excerpt, extract the framework's identity. Return ONLY JSON matching the schema — no markdown fences, no commentary.

Fields:
- id: kebab-case slug (e.g. "nist-csf-2.0", "gdpr", "pci-dss-4.0.1")
- name: short display name (e.g. "NIST CSF 2.0", "GDPR")
- version: the edition/version string exactly as printed, else ""
- category: one of cybersecurity | data_protection | payment_security | healthcare_privacy | financial_resilience | privacy | other
- regions: array of GLOBAL | a continent (AFRICA, ASIA, EUROPE, NORTH_AMERICA, SOUTH_AMERICA, OCEANIA) | a bloc (EU, EEA) | ISO 3166-1 alpha-2 country codes. Default ["GLOBAL"] if not jurisdiction-bound.
- issuingBody: the publishing organization, else ""
- language: ISO-639-1 code of the language the document is WRITTEN in (e.g. "en", "uz", "ru"). Report the document's own language — never the language of the standard's origin country.`

	extractFlatContract = `You extract the structure of a compliance standard. From this part of the document, return the categories (top-level groupings) and the requirements (individual, testable obligations). Return ONLY JSON — no markdown fences, no commentary.

Shape: {"categories":[{"ref":"...","name":"..."}],"requirements":[{"ref":"...","category":"...","name":"...","description":"...","sourceExcerpt":"..."}]}

Rules:
- category.ref = the group's own code/letters (e.g. "AC", "GV", "CH2", "3"); category.name = its title.
- requirement.ref = the requirement's own number from the document, verbatim and unique (e.g. "AC-1", "3.3.2", "Art.5", "GV.OC-01"). NEVER invent numbering; if the document has no numbering, slugify the title.
- requirement.category = the ref of the category it belongs to, or "" if this part shows no grouping.
- requirement.name = a short title (≤ 90 chars).
- requirement.description = the obligation text, faithfully and completely. One requirement per distinct obligation; do NOT merge two numbered items into one, and do NOT split one obligation across two.
- requirement.sourceExcerpt = a short verbatim substring (≤240 chars) of this part that the requirement came from, so a reviewer can locate it. Never published.
- Extract ONLY what appears in this part. Do not carry over or infer items from other parts. Emit [] for a section that has none. Preserve document order.`

	controlsContract = `You are a GRC implementation expert. For each requirement, propose the concrete control(s) an organization would implement to satisfy it. Return ONLY JSON — no markdown fences, no commentary.

Shape: {"controls":[{"ref":"...","name":"...","description":"...","category":"..."}],"links":[{"requirement":"<req ref>","controls":["<control ref>"]}]}

Rules:
- Every requirement in the batch MUST appear in links with at least one control ref.
- control.ref = kebab-case slug, stable and unique (e.g. "mfa-enforced", "access-control-policy", "central-audit-logging").
- control.name = short imperative title (≤ 80 chars).
- control.description = 1–2 sentences on what implementing it means.
- control.category = one of: Policy | Access | Logging | Encryption | Network | Endpoint | Vulnerability | Incident | Vendor | HR | Physical | Governance | Monitoring | Backup | Other.`

	qaContract = "You review an extracted compliance framework. Reply with ONE plain-text sentence only; if nothing looks wrong reply exactly \"Looks complete.\""

	// mappingContract is fixed. The model may only SELECT from the candidate
	// refs it was given: a ref it invented would become a stub that never
	// resolves and shows up as nothing at all.
	mappingContract = "You decide which compliance requirements in two frameworks correspond. " +
		"Respond with ONLY valid JSON — no markdown fences, no commentary.\n\n" +
		"Return: {\"mappings\":[{\"source\":\"<source ref>\",\"target\":\"<ref copied from that source's candidate list>\"," +
		"\"relation\":\"equivalent|partial|superset|subset\",\"confidence\":0.0-1.0,\"rationale\":\"<one sentence>\"}]}\n\n" +
		"Rules:\n" +
		"- target MUST be copied verbatim from the candidate list shown for that source. Never invent, adjust or guess a ref.\n" +
		"- Return {\"mappings\":[]} when nothing genuinely corresponds. That is a valid and common answer.\n" +
		"- relation is from the SOURCE's point of view: superset = the source covers more than the target.\n" +
		"- confidence is your honest belief the mapping survives an auditor's review.\n" +
		"- rationale states WHY in one sentence, concretely (\"both mandate MFA for privileged remote access\")."

	// translateContract is fixed. The refs are the spine of the whole system —
	// control links, cross-mappings and every other language overlay address
	// nodes by ref — so the model is told, and then forced in code, to echo them
	// back unchanged and to translate text only.
	translateContract = "You translate compliance framework text into a target language. " +
		"Respond with ONLY valid JSON — no markdown fences, no commentary.\n\n" +
		"Return: {\"nodes\":[{\"ref\":\"<echoed EXACTLY as given>\",\"name\":\"<translated>\",\"description\":\"<translated>\"}]}\n\n" +
		"Rules:\n" +
		"- Echo every ref back character-for-character. NEVER translate, normalise, reformat or invent a ref.\n" +
		"- Return one entry for every node you were given, in the same order.\n" +
		"- Translate name and description only. An empty description stays empty.\n" +
		"- Numbers, codes and citations inside the text keep their original form."
)

// extractSystemPrompt is the flat pipeline's Step 2 system prompt.
func extractSystemPrompt(setting *coredata.LLMSetting) string {
	instruction := DefaultGenerationInstruction
	if setting != nil && strings.TrimSpace(setting.GenerationInstruction) != "" {
		instruction = setting.GenerationInstruction
	}
	return extractFlatContract + "\n\nInstructions:\n" + instruction
}

// mappingSystemPrompt is the cross-mapping adjudication system prompt.
func mappingSystemPrompt(setting *coredata.LLMSetting) string {
	instruction := DefaultMappingInstruction
	if setting != nil && strings.TrimSpace(setting.MappingInstruction) != "" {
		instruction = setting.MappingInstruction
	}
	return mappingContract + "\n\nInstructions:\n" + instruction
}

// translateSystemPrompt is the translation step's system prompt.
func translateSystemPrompt(setting *coredata.LLMSetting) string {
	instruction := DefaultTranslateInstruction
	if setting != nil && strings.TrimSpace(setting.TranslateInstruction) != "" {
		instruction = setting.TranslateInstruction
	}
	return translateContract + "\n\nInstructions:\n" + instruction
}

// controlsSystemPrompt is the flat pipeline's Step 3 system prompt.
func controlsSystemPrompt(setting *coredata.LLMSetting) string {
	instruction := DefaultControlsInstruction
	if setting != nil && strings.TrimSpace(setting.ControlsInstruction) != "" {
		instruction = setting.ControlsInstruction
	}
	return controlsContract + "\n\nInstructions:\n" + instruction
}

// frameworkSystemPrompt combines the mandatory output contract (always applied)
// with the steerable authoring instruction (superadmin-editable in Settings;
// falls back to DefaultGenerationInstruction when empty).
const extractContract = "You convert a source compliance standard document into the Meizon framework exchange schema v2.0. " +
	"Extract the real structure and numbering from the document. Respond with ONLY valid JSON — no markdown fences, no commentary. " +
	"On each category, requirement and item include a \"sourceExcerpt\": the exact substring (≤240 chars) copied verbatim from the source document that this node was derived from — used only to locate the passage in the reviewer's UI, never published."

	// provNode is a shadow of the exchange hierarchy that captures only the codes
	// and the review-only sourceExcerpt — fields that fwschema.Framework drops. It
	// lets us build a provenance map without polluting the signed content schema.
