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

	"github.com/go-chi/chi/v5"
	"go.meizon.cloud/registry/pkg/fwqa"
	"go.meizon.cloud/registry/pkg/gid"
	"go.meizon.cloud/registry/pkg/registry"
	"go.meizon.cloud/registry/pkg/server/api/authn"
	"go.meizon.cloud/registry/pkg/server/api/httpx"
)

// qaGenerate starts an audit-template generation job for a framework's published
// requirements. Returns a job id to poll via the existing generate-status route.
func (h *Handler) qaGenerate(w http.ResponseWriter, r *http.Request) {
	actor, _ := authn.IdentityFrom(r.Context())
	jobID, err := h.svc.StartQATemplateJob(r.Context(), actor, chi.URLParam(r, "ref"))
	if err != nil {
		if errors.Is(err, registry.ErrLLMNotConfigured) {
			httpx.Error(w, http.StatusPreconditionFailed, "LLM is not configured — ask a superadmin to set it up in Settings")
			return
		}
		httpx.ServiceError(w, err)
		return
	}
	httpx.JSON(w, http.StatusAccepted, map[string]any{"jobId": jobID})
}

// qaTemplate returns the assembled audit template for a framework.
func (h *Handler) qaTemplate(w http.ResponseWriter, r *http.Request) {
	tpl, err := h.svc.QATemplateViewFor(r.Context(), chi.URLParam(r, "ref"), r.URL.Query().Get("lang"))
	if err != nil {
		httpx.ServiceError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, tpl)
}

// qaEvaluate runs one answered question through the shared runner. This is the
// single evaluator; the chat preview posts an answer and gets back the verdict,
// score, and which follow-ups fired.
func (h *Handler) qaEvaluate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		QuestionID string      `json:"questionId"`
		Answer     fwqa.Answer `json:"answer"`
	}
	if !httpx.DecodeJSON(w, r, &body) {
		return
	}
	res, err := h.svc.EvaluateQAAnswer(r.Context(), chi.URLParam(r, "ref"), body.QuestionID, body.Answer)
	if err != nil {
		httpx.ServiceError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, res)
}

func (h *Handler) qaUpdateQuestion(w http.ResponseWriter, r *http.Request) {
	actor, _ := authn.IdentityFrom(r.Context())
	templateID, ok := parseGID(w, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	questionID, ok := parseGID(w, chi.URLParam(r, "qid"))
	if !ok {
		return
	}
	var q fwqa.Question
	if !httpx.DecodeJSON(w, r, &q) {
		return
	}
	if err := h.svc.UpdateQAQuestion(r.Context(), actor, templateID, questionID, q); err != nil {
		httpx.ServiceError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *Handler) qaDeleteQuestion(w http.ResponseWriter, r *http.Request) {
	actor, _ := authn.IdentityFrom(r.Context())
	templateID, ok := parseGID(w, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	questionID, ok := parseGID(w, chi.URLParam(r, "qid"))
	if !ok {
		return
	}
	if err := h.svc.DeleteQAQuestion(r.Context(), actor, templateID, questionID); err != nil {
		httpx.ServiceError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *Handler) qaReorder(w http.ResponseWriter, r *http.Request) {
	actor, _ := authn.IdentityFrom(r.Context())
	templateID, ok := parseGID(w, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	var body struct {
		Order map[string]int `json:"order"`
	}
	if !httpx.DecodeJSON(w, r, &body) {
		return
	}
	if err := h.svc.ReorderQAQuestions(r.Context(), actor, templateID, body.Order); err != nil {
		httpx.ServiceError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *Handler) qaSetStatus(w http.ResponseWriter, r *http.Request) {
	actor, _ := authn.IdentityFrom(r.Context())
	templateID, ok := parseGID(w, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	var body struct {
		Status string `json:"status"`
	}
	if !httpx.DecodeJSON(w, r, &body) {
		return
	}
	if err := h.svc.SetQATemplateStatus(r.Context(), actor, templateID, body.Status); err != nil {
		httpx.ServiceError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// parseGID parses a path GID or writes a 400 and returns ok=false.
func parseGID(w http.ResponseWriter, s string) (gid.GID, bool) {
	id, err := gid.ParseGID(s)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid id")
		return gid.Nil, false
	}
	return id, true
}
