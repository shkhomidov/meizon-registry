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

// Package httpx holds small JSON response helpers and service-error mapping
// shared by the console and connect REST handlers.
package httpx

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"go.meizon.cloud/registry/pkg/coredata"
	"go.meizon.cloud/registry/pkg/registry"
	"go.meizon.cloud/registry/pkg/validator"
)

// JSON writes v as an indented JSON response with the given status.
func JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

// Error writes a JSON error envelope.
func Error(w http.ResponseWriter, status int, msg string) {
	JSON(w, status, map[string]string{"error": msg})
}

// ServiceError maps a service-layer error to an HTTP status + envelope.
func ServiceError(w http.ResponseWriter, err error) {
	var verr validator.ValidationErrors
	switch {
	case errors.Is(err, registry.ErrForbidden):
		Error(w, http.StatusForbidden, err.Error())
	case errors.Is(err, registry.ErrSeparationOfDuties):
		Error(w, http.StatusConflict, err.Error())
	case errors.Is(err, registry.ErrInvalidTransition):
		Error(w, http.StatusConflict, err.Error())
	case errors.Is(err, registry.ErrInvalidCredentials):
		Error(w, http.StatusUnauthorized, "invalid credentials")
	case errors.Is(err, coredata.ErrResourceNotFound):
		Error(w, http.StatusNotFound, "not found")
	case errors.Is(err, coredata.ErrResourceAlreadyExists):
		Error(w, http.StatusConflict, "already exists")
	case errors.Is(err, registry.ErrInvalidInput):
		Error(w, http.StatusBadRequest, err.Error())
	case errors.As(err, &verr):
		Error(w, http.StatusBadRequest, err.Error())
	default:
		Error(w, http.StatusInternalServerError, "internal error")
	}
}

// DecodeJSON decodes the request body into v, returning false and writing a 400
// on failure.
func DecodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(v); err != nil {
		Error(w, http.StatusBadRequest, "invalid JSON body")
		return false
	}
	return true
}

// DecodeOptional decodes the body into v when one is present, tolerating an
// empty body (returns nil in that case).
func DecodeOptional(r *http.Request, v any) error {
	err := json.NewDecoder(r.Body).Decode(v)
	if errors.Is(err, io.EOF) {
		return nil
	}
	return err
}
