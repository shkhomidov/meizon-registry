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

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"time"

	"github.com/jackc/pgx/v5"
	"go.gearno.de/kit/pg"
	"go.meizon.cloud/registry/pkg/gid"
)

// LLMSetting is the tenant-wide LLM provider configuration, managed from the
// superadmin settings page. The API key is AES-256-GCM encrypted at rest and
// never leaves the server.
type LLMSetting struct {
	ID                    gid.GID `db:"id"`
	Provider              string  `db:"provider"`
	EncryptedAPIKey       []byte  `db:"encrypted_api_key"`
	Model                 string  `db:"model"`
	BaseURL               string  `db:"base_url"`
	MaxTokens             int     `db:"max_tokens"`
	GenerationInstruction string  `db:"generation_instruction"`
	IdentifyInstruction   string  `db:"identify_instruction"`
	ControlsInstruction   string  `db:"controls_instruction"`
	QAInstruction         string  `db:"qa_instruction"`
	TranslateInstruction  string  `db:"translate_instruction"`
	MappingInstruction    string  `db:"mapping_instruction"`
	// OCR is a separate service with its own credential: reusing the LLM key
	// would send a Mistral key to Gemini.
	OCRProvider        string    `db:"ocr_provider"`
	EncryptedOCRAPIKey []byte    `db:"encrypted_ocr_api_key"`
	OCRModel           string    `db:"ocr_model"`
	UpdatedBy          string    `db:"updated_by"`
	UpdatedAt          time.Time `db:"updated_at"`
}

const llmSettingColumns = `id, provider, encrypted_api_key, model, base_url, max_tokens, generation_instruction, identify_instruction, controls_instruction, qa_instruction, translate_instruction, mapping_instruction, ocr_provider, encrypted_ocr_api_key, ocr_model, updated_by, updated_at`

// Upsert writes the tenant's single settings row.
func (s LLMSetting) Upsert(ctx context.Context, conn pg.Tx, scope Scoper) error {
	q := `
INSERT INTO llm_settings (tenant_id, id, provider, encrypted_api_key, model, base_url, max_tokens, generation_instruction, identify_instruction, controls_instruction, qa_instruction, translate_instruction, mapping_instruction, ocr_provider, encrypted_ocr_api_key, ocr_model, updated_by, updated_at)
VALUES (@tenant_id, @id, @provider, @encrypted_api_key, @model, @base_url, @max_tokens, @generation_instruction, @identify_instruction, @controls_instruction, @qa_instruction, @translate_instruction, @mapping_instruction, @ocr_provider, @encrypted_ocr_api_key, @ocr_model, @updated_by, @updated_at)
ON CONFLICT ON CONSTRAINT llm_settings_tenant_unique DO UPDATE SET
  provider = EXCLUDED.provider,
  encrypted_api_key = EXCLUDED.encrypted_api_key,
  model = EXCLUDED.model,
  base_url = EXCLUDED.base_url,
  max_tokens = EXCLUDED.max_tokens,
  generation_instruction = EXCLUDED.generation_instruction,
  identify_instruction = EXCLUDED.identify_instruction,
  controls_instruction = EXCLUDED.controls_instruction,
  qa_instruction = EXCLUDED.qa_instruction,
  translate_instruction = EXCLUDED.translate_instruction,
  mapping_instruction = EXCLUDED.mapping_instruction,
  ocr_provider = EXCLUDED.ocr_provider,
  encrypted_ocr_api_key = EXCLUDED.encrypted_ocr_api_key,
  ocr_model = EXCLUDED.ocr_model,
  updated_by = EXCLUDED.updated_by,
  updated_at = EXCLUDED.updated_at;`

	args := pgx.StrictNamedArgs{
		"tenant_id":              scope.GetTenantID(),
		"id":                     s.ID,
		"provider":               s.Provider,
		"encrypted_api_key":      s.EncryptedAPIKey,
		"model":                  s.Model,
		"base_url":               s.BaseURL,
		"max_tokens":             s.MaxTokens,
		"generation_instruction": s.GenerationInstruction,
		"identify_instruction":   s.IdentifyInstruction,
		"controls_instruction":   s.ControlsInstruction,
		"qa_instruction":         s.QAInstruction,
		"translate_instruction":  s.TranslateInstruction,
		"mapping_instruction":    s.MappingInstruction,
		"ocr_provider":           s.OCRProvider,
		"encrypted_ocr_api_key":  s.EncryptedOCRAPIKey,
		"ocr_model":              s.OCRModel,
		"updated_by":             s.UpdatedBy,
		"updated_at":             s.UpdatedAt,
	}

	_, err := conn.Exec(ctx, q, args)
	return err
}

// Load reads the tenant's settings row.
func (s *LLMSetting) Load(ctx context.Context, conn pg.Querier, scope Scoper) error {
	q := fmt.Sprintf(`SELECT %s FROM llm_settings WHERE %s LIMIT 1;`, llmSettingColumns, scope.SQLFragment())

	args := pgx.StrictNamedArgs{}
	maps.Copy(args, scope.SQLArguments())

	rows, err := conn.Query(ctx, q, args)
	if err != nil {
		return fmt.Errorf("cannot query llm settings: %w", err)
	}

	setting, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[LLMSetting])
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrResourceNotFound
		}
		return fmt.Errorf("cannot collect llm settings: %w", err)
	}

	*s = setting
	return nil
}
