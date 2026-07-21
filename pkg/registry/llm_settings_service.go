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
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.gearno.de/kit/pg"
	"go.meizon.cloud/registry/pkg/coredata"
	"go.meizon.cloud/registry/pkg/crypto/cipher"
	"go.meizon.cloud/registry/pkg/gid"
	"go.meizon.cloud/registry/pkg/iam"
	"go.meizon.cloud/registry/pkg/llm"
	"go.meizon.cloud/registry/pkg/ocr"
)

// ErrLLMNotConfigured is returned when AI assist is used before a superadmin has
// configured a provider in settings.
var ErrLLMNotConfigured = errors.New("llm provider is not configured")

// ErrOCRNotConfigured is returned when a scanned document needs OCR but no OCR
// credential has been set up.
var ErrOCRNotConfigured = errors.New("ocr is not configured")

// LLMSettingsView is the settings page shape. The API key is never returned —
// only whether one is configured.
type LLMSettingsView struct {
	Provider   string `json:"provider"`
	Model      string `json:"model"`
	BaseURL    string `json:"baseUrl,omitempty"`
	MaxTokens  int    `json:"maxTokens"`
	Configured bool   `json:"configured"`

	// OCR settings. Like the LLM key, the OCR key is never returned — only
	// whether one is stored.
	OCRProvider     string    `json:"ocrProvider,omitempty"`
	OCRModel        string    `json:"ocrModel,omitempty"`
	OCRConfigured   bool      `json:"ocrConfigured"`
	OCRDefaultModel string    `json:"ocrDefaultModel"`
	UpdatedAt       time.Time `json:"updatedAt,omitempty"`
	// GenerationInstruction is the effective steerable instruction (the stored
	// custom text, or the built-in default when none is set); DefaultInstruction
	// is always the built-in default so the UI can offer a "reset". Neither is
	// secret. Kept for the extract step specifically.
	GenerationInstruction string `json:"generationInstruction"`
	DefaultInstruction    string `json:"defaultInstruction"`
	// Steps lists every LLM step of the generation pipeline with its editable
	// instruction, its default, and the fixed contract applied on top — so the
	// settings page can show the full prompt set, not just one box.
	Steps []StepInstruction `json:"steps"`
}

// stepInstructionsFor builds the per-step view from stored settings (nil = all
// defaults).
func stepInstructionsFor(s *coredata.LLMSetting) []StepInstruction {
	pick := func(stored, def string) string {
		if s != nil && strings.TrimSpace(stored) != "" {
			return stored
		}
		return def
	}
	var identify, extract, controls, qa, translate, mapping string
	if s != nil {
		identify, extract, controls, qa = s.IdentifyInstruction, s.GenerationInstruction, s.ControlsInstruction, s.QAInstruction
		translate, mapping = s.TranslateInstruction, s.MappingInstruction
	}
	return []StepInstruction{
		{
			Key: "identify", Label: "1 · Identify the standard",
			Description: "Reads the first chunk and returns the framework's identity (id, name, version, region, license, authority).",
			Instruction: pick(identify, DefaultIdentifyInstruction), Default: DefaultIdentifyInstruction,
			Contract: identifyContract,
		},
		{
			Key: "extract", Label: "2 · Extract requirements",
			Description: "Runs once per document chunk and returns that part's categories → requirements → sections → items, with a source excerpt per node.",
			Instruction: pick(extract, DefaultGenerationInstruction), Default: DefaultGenerationInstruction,
			Contract: extractContract,
		},
		{
			Key: "controls", Label: "3 · Suggest controls",
			Description: "Runs once per batch of ~15 requirements and proposes the control(s) an organisation would implement, reusing earlier control refs so the library converges.",
			Instruction: pick(controls, DefaultControlsInstruction), Default: DefaultControlsInstruction,
			Contract: controlsContract,
		},
		{
			Key: "qa", Label: "4 · Quality check",
			Description: "Reviews the merged result and returns one advisory sentence shown to the auditor. Never changes the framework.",
			Instruction: pick(qa, DefaultQAInstruction), Default: DefaultQAInstruction,
			Contract: qaContract,
		},
		{
			Key: "translate", Label: "5 · Translate",
			Description: "Runs once per batch of ~20 nodes when a language is added. Translates names and descriptions only — refs are echoed back unchanged and re-checked in code, because every control link and cross-mapping addresses nodes by ref.",
			Instruction: pick(translate, DefaultTranslateInstruction), Default: DefaultTranslateInstruction,
			Contract: translateContract,
		},
		{
			Key: "mapping", Label: "6 · Cross-map frameworks",
			Description: "Runs once per batch of ~4 requirements when auto-mapping. Each is shown a shortlist of candidate targets and may only choose from it; anything else is dropped. Output is a proposal an auditor accepts or rejects — never a stored mapping.",
			Instruction: pick(mapping, DefaultMappingInstruction), Default: DefaultMappingInstruction,
			Contract: mappingContract,
		},
	}
}

// GetLLMSettings returns the current settings (without the key).
func (s *Service) GetLLMSettings(ctx context.Context) (LLMSettingsView, error) {
	view := LLMSettingsView{
		GenerationInstruction: DefaultGenerationInstruction,
		DefaultInstruction:    DefaultGenerationInstruction,
		OCRDefaultModel:       ocr.DefaultModel,
		Steps:                 stepInstructionsFor(nil),
	}
	err := s.db.WithConn(ctx, func(ctx context.Context, conn pg.Querier) error {
		var setting coredata.LLMSetting
		if err := setting.Load(ctx, conn, s.platformScope()); err != nil {
			if errors.Is(err, coredata.ErrResourceNotFound) {
				return nil // unconfigured
			}
			return err
		}
		instruction := setting.GenerationInstruction
		if instruction == "" {
			instruction = DefaultGenerationInstruction
		}
		view = LLMSettingsView{
			Provider: setting.Provider, Model: setting.Model, BaseURL: setting.BaseURL,
			MaxTokens: setting.MaxTokens, Configured: len(setting.EncryptedAPIKey) > 0,
			OCRProvider:           setting.OCRProvider,
			OCRModel:              setting.OCRModel,
			OCRConfigured:         len(setting.EncryptedOCRAPIKey) > 0,
			OCRDefaultModel:       ocr.DefaultModel,
			UpdatedAt:             setting.UpdatedAt,
			GenerationInstruction: instruction,
			DefaultInstruction:    DefaultGenerationInstruction,
			Steps:                 stepInstructionsFor(&setting),
		}
		return nil
	})
	return view, err
}

// SetLLMSettingsRequest updates the provider configuration. An empty APIKey
// keeps the previously stored key (so model/URL edits don't require re-entry).
type SetLLMSettingsRequest struct {
	Provider              string
	APIKey                string
	Model                 string
	BaseURL               string
	MaxTokens             int
	GenerationInstruction string
	IdentifyInstruction   string
	ControlsInstruction   string
	QAInstruction         string
	TranslateInstruction  string
	MappingInstruction    string

	// OCR is configured alongside the LLM but is a separate service with its
	// own credential. An empty OCRAPIKey keeps the stored one, exactly like the
	// LLM key, so editing the model never forces re-entry.
	OCRProvider string
	OCRAPIKey   string
	OCRModel    string
	// ClearOCRKey removes the stored OCR key (an empty OCRAPIKey means "keep").
	ClearOCRKey bool
}

// SetLLMSettings stores the configuration (superadmin only). The key is
// AES-256-GCM encrypted with the platform encryption key.
func (s *Service) SetLLMSettings(ctx context.Context, actorID gid.GID, req SetLLMSettingsRequest) error {
	if !llm.IsValidProvider(req.Provider) {
		return fmt.Errorf("unknown provider %q (openai|anthropic|gemini)", req.Provider)
	}
	if req.Model == "" {
		req.Model = llm.DefaultModel(req.Provider)
	}
	if req.MaxTokens <= 0 {
		req.MaxTokens = llm.DefaultMaxTokens(req.Provider)
	}

	return s.db.WithTx(ctx, func(ctx context.Context, tx pg.Tx) error {
		if err := s.authorize(ctx, tx, actorID, iam.ActionSettingsManage, "", gid.Nil); err != nil {
			return err
		}
		scope := s.platformScope()

		var existing coredata.LLMSetting
		hasExisting := existing.Load(ctx, tx, scope) == nil

		var encrypted []byte
		switch {
		case strings.TrimSpace(req.APIKey) != "":
			enc, err := cipher.Encrypt([]byte(strings.TrimSpace(req.APIKey)), s.cfg.EncryptionKey)
			if err != nil {
				return fmt.Errorf("cannot encrypt api key: %w", err)
			}
			encrypted = enc
		case hasExisting:
			encrypted = existing.EncryptedAPIKey
		default:
			return fmt.Errorf("an API key is required")
		}

		// The OCR key follows the same keep-unless-replaced rule as the LLM key.
		var encryptedOCR []byte
		switch {
		case req.ClearOCRKey:
			encryptedOCR = nil
		case strings.TrimSpace(req.OCRAPIKey) != "":
			enc, err := cipher.Encrypt([]byte(strings.TrimSpace(req.OCRAPIKey)), s.cfg.EncryptionKey)
			if err != nil {
				return fmt.Errorf("cannot encrypt ocr api key: %w", err)
			}
			encryptedOCR = enc
		case hasExisting:
			encryptedOCR = existing.EncryptedOCRAPIKey
		}

		setting := coredata.LLMSetting{
			ID:                    gid.New(s.cfg.PlatformTenant, coredata.LLMSettingEntityType),
			Provider:              req.Provider,
			EncryptedAPIKey:       encrypted,
			Model:                 req.Model,
			BaseURL:               strings.TrimSpace(req.BaseURL),
			MaxTokens:             req.MaxTokens,
			GenerationInstruction: strings.TrimSpace(req.GenerationInstruction),
			IdentifyInstruction:   strings.TrimSpace(req.IdentifyInstruction),
			ControlsInstruction:   strings.TrimSpace(req.ControlsInstruction),
			QAInstruction:         strings.TrimSpace(req.QAInstruction),
			TranslateInstruction:  strings.TrimSpace(req.TranslateInstruction),
			MappingInstruction:    strings.TrimSpace(req.MappingInstruction),
			OCRProvider:           strings.TrimSpace(req.OCRProvider),
			EncryptedOCRAPIKey:    encryptedOCR,
			OCRModel:              strings.TrimSpace(req.OCRModel),
			UpdatedBy:             actorID.String(),
			UpdatedAt:             time.Now(),
		}
		if hasExisting {
			setting.ID = existing.ID
		}
		if err := setting.Upsert(ctx, tx, scope); err != nil {
			return err
		}

		return s.recordAudit(ctx, tx, scope, actorID, "settings.llm", setting.ID.String(),
			fmt.Sprintf("provider=%s model=%s ocr=%s", req.Provider, req.Model, orNone(req.OCRProvider)))
	})
}

// llmClient builds a provider client from stored settings.
func (s *Service) llmClient(ctx context.Context, conn pg.Querier) (llm.Client, *coredata.LLMSetting, error) {
	var setting coredata.LLMSetting
	if err := setting.Load(ctx, conn, s.platformScope()); err != nil {
		if errors.Is(err, coredata.ErrResourceNotFound) {
			return nil, nil, ErrLLMNotConfigured
		}
		return nil, nil, err
	}

	apiKey, err := cipher.Decrypt(setting.EncryptedAPIKey, s.cfg.EncryptionKey)
	if err != nil {
		return nil, nil, fmt.Errorf("cannot decrypt llm api key: %w", err)
	}

	client, err := s.llmFactory(llm.Config{
		Provider: setting.Provider,
		APIKey:   string(apiKey),
		Model:    setting.Model,
		BaseURL:  setting.BaseURL,
	})
	if err != nil {
		return nil, nil, err
	}
	return client, &setting, nil
}

// TestLLM performs a tiny generation to validate the stored configuration
// (superadmin only). Returns the provider's reply snippet.
func (s *Service) TestLLM(ctx context.Context, actorID gid.GID) (string, error) {
	var reply string
	err := s.db.WithConn(ctx, func(ctx context.Context, conn pg.Querier) error {
		if err := s.authorize(ctx, conn, actorID, iam.ActionSettingsManage, "", gid.Nil); err != nil {
			return err
		}
		client, _, err := s.llmClient(ctx, conn)
		if err != nil {
			return err
		}
		// Budget generously even for a one-word reply: "thinking" models spend
		// output tokens reasoning before emitting any text, so a tight cap makes
		// a perfectly healthy provider look broken (finishReason=MAX_TOKENS,
		// zero content).
		resp, err := client.Generate(ctx, llm.Request{
			System:    "You are a connectivity test. Reply with exactly: OK",
			Prompt:    "ping",
			MaxTokens: 2048,
		})
		if err != nil {
			return err
		}
		reply = strings.TrimSpace(resp.Text)
		if len(reply) > 120 {
			reply = reply[:120]
		}
		return nil
	})
	return reply, err
}

// SetLLMFactory overrides the provider constructor (tests).
func (s *Service) SetLLMFactory(f func(llm.Config) (llm.Client, error)) {
	s.llmFactory = f
}

// orNone labels an unset value in audit text.
func orNone(v string) string {
	if strings.TrimSpace(v) == "" {
		return "none"
	}
	return v
}

// ocrClient builds an OCR client from stored settings. ErrOCRNotConfigured is
// returned when no OCR credential exists, so the caller can tell a scanned
// upload apart from a broken one and say which it is.
func (s *Service) ocrClient(ctx context.Context, conn pg.Querier) (ocr.Client, error) {
	var setting coredata.LLMSetting
	if err := setting.Load(ctx, conn, s.platformScope()); err != nil {
		if errors.Is(err, coredata.ErrResourceNotFound) {
			return nil, ErrOCRNotConfigured
		}
		return nil, err
	}
	if len(setting.EncryptedOCRAPIKey) == 0 {
		return nil, ErrOCRNotConfigured
	}
	key, err := cipher.Decrypt(setting.EncryptedOCRAPIKey, s.cfg.EncryptionKey)
	if err != nil {
		return nil, fmt.Errorf("cannot decrypt ocr api key: %w", err)
	}
	return ocr.New(ocr.Config{
		Provider: setting.OCRProvider,
		APIKey:   string(key),
		Model:    setting.OCRModel,
	})
}
