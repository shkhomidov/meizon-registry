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

package console_v1

import (
	"errors"
	"net/http"

	"go.meizon.cloud/registry/pkg/gid"
	"go.meizon.cloud/registry/pkg/registry"
	"go.meizon.cloud/registry/pkg/server/api/authn"
	"go.meizon.cloud/registry/pkg/server/api/httpx"
)

func (h *Handler) aiStatus(w http.ResponseWriter, r *http.Request) {
	status, err := h.svc.GetAIStatus(r.Context())
	if err != nil {
		httpx.ServiceError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, status)
}

func (h *Handler) aiGenerate(w http.ResponseWriter, r *http.Request) {
	actor, _ := authn.IdentityFrom(r.Context())
	versionID, err := h.latestVersionFor(r)
	if err != nil {
		httpx.ServiceError(w, err)
		return
	}
	var body struct{ Step, Brief, Parent string }
	if !httpx.DecodeJSON(w, r, &body) {
		return
	}
	proposals, err := h.svc.GenerateProposals(r.Context(), actor, versionID, body.Step, body.Brief, body.Parent)
	if err != nil {
		if errors.Is(err, registry.ErrLLMNotConfigured) {
			httpx.Error(w, http.StatusPreconditionFailed, "LLM is not configured — ask a superadmin to set it up in Settings")
			return
		}
		httpx.ServiceError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, proposals)
}

func (h *Handler) aiAccept(w http.ResponseWriter, r *http.Request) {
	actor, _ := authn.IdentityFrom(r.Context())
	versionID, err := h.latestVersionFor(r)
	if err != nil {
		httpx.ServiceError(w, err)
		return
	}
	var body struct {
		GenerationID string                         `json:"generationId"`
		Step         string                         `json:"step"`
		CategoryCode string                         `json:"categoryCode"`
		Requirement  string                         `json:"requirementCode"`
		Categories   []registry.ProposedCategory    `json:"categories"`
		Requirements []registry.ProposedRequirement `json:"requirements"`
		Mappings     []registry.ProposedMapping     `json:"mappings"`
	}
	if !httpx.DecodeJSON(w, r, &body) {
		return
	}
	genID, err := gid.ParseGID(body.GenerationID)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid generation id")
		return
	}

	applied, err := h.svc.AcceptProposals(r.Context(), actor, versionID, registry.AIAcceptRequest{
		GenerationID: genID, Step: body.Step,
		CategoryCode: body.CategoryCode, RequirementCode: body.Requirement,
		Categories: body.Categories, Requirements: body.Requirements,
		Mappings: body.Mappings,
	})
	if err != nil {
		httpx.ServiceError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]int{"applied": applied})
}

func (h *Handler) getLLMSettings(w http.ResponseWriter, r *http.Request) {
	view, err := h.svc.GetLLMSettings(r.Context())
	if err != nil {
		httpx.ServiceError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, view)
}

func (h *Handler) putLLMSettings(w http.ResponseWriter, r *http.Request) {
	actor, _ := authn.IdentityFrom(r.Context())
	var body struct {
		Provider              string `json:"provider"`
		APIKey                string `json:"apiKey"`
		Model                 string `json:"model"`
		BaseURL               string `json:"baseUrl"`
		MaxTokens             int    `json:"maxTokens"`
		GenerationInstruction string `json:"generationInstruction"`
		IdentifyInstruction   string `json:"identifyInstruction"`
		ControlsInstruction   string `json:"controlsInstruction"`
		QAInstruction         string `json:"qaInstruction"`
		TranslateInstruction  string `json:"translateInstruction"`
		MappingInstruction    string `json:"mappingInstruction"`
		OCRProvider           string `json:"ocrProvider"`
		OCRAPIKey             string `json:"ocrApiKey"`
		OCRModel              string `json:"ocrModel"`
		ClearOCRKey           bool   `json:"clearOcrKey"`
	}
	if !httpx.DecodeJSON(w, r, &body) {
		return
	}
	if err := h.svc.SetLLMSettings(r.Context(), actor, registry.SetLLMSettingsRequest{
		Provider: body.Provider, APIKey: body.APIKey, Model: body.Model,
		BaseURL: body.BaseURL, MaxTokens: body.MaxTokens,
		GenerationInstruction: body.GenerationInstruction,
		IdentifyInstruction:   body.IdentifyInstruction,
		ControlsInstruction:   body.ControlsInstruction,
		QAInstruction:         body.QAInstruction,
		TranslateInstruction:  body.TranslateInstruction,
		MappingInstruction:    body.MappingInstruction,
		OCRProvider:           body.OCRProvider,
		OCRAPIKey:             body.OCRAPIKey,
		OCRModel:              body.OCRModel,
		ClearOCRKey:           body.ClearOCRKey,
	}); err != nil {
		httpx.ServiceError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *Handler) testLLMSettings(w http.ResponseWriter, r *http.Request) {
	actor, _ := authn.IdentityFrom(r.Context())
	reply, err := h.svc.TestLLM(r.Context(), actor)
	if err != nil {
		if errors.Is(err, registry.ErrLLMNotConfigured) {
			httpx.Error(w, http.StatusPreconditionFailed, "no provider configured yet")
			return
		}
		httpx.Error(w, http.StatusBadGateway, err.Error())
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]string{"reply": reply})
}
