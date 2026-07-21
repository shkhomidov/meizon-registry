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

package validator

import (
	"fmt"
	"strings"
)

type ErrorCode string

const (
	ErrorCodeRequired      ErrorCode = "REQUIRED"
	ErrorCodeInvalidFormat ErrorCode = "INVALID_FORMAT"
	ErrorCodeOutOfRange    ErrorCode = "OUT_OF_RANGE"
	ErrorCodeTooShort      ErrorCode = "TOO_SHORT"
	ErrorCodeTooLong       ErrorCode = "TOO_LONG"
	ErrorCodeInvalidEnum   ErrorCode = "INVALID_ENUM"
	ErrorCodeInvalidGID    ErrorCode = "INVALID_GID"
	ErrorCodeUnsafeContent ErrorCode = "UNSAFE_CONTENT"
	ErrorCodeCustom        ErrorCode = "CUSTOM"
)

type ValidationError struct {
	Field   string
	Code    ErrorCode
	Message string
	Value   any
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("%s %s", e.Field, e.Message)
}

type ValidationErrors []*ValidationError

func (ve ValidationErrors) Error() string {
	if len(ve) == 0 {
		return ""
	}

	messages := make([]string, 0, len(ve))
	for _, err := range ve {
		messages = append(messages, err.Error())
	}

	return strings.Join(messages, "; ")
}

func (ve ValidationErrors) HasErrors() bool {
	return len(ve) > 0
}

func (ve ValidationErrors) First() *ValidationError {
	if len(ve) == 0 {
		return nil
	}

	return ve[0]
}

func newValidationError(code ErrorCode, message string) *ValidationError {
	return &ValidationError{Code: code, Message: message}
}
