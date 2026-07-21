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
	"regexp"
	"strings"
	"unicode"

	"go.meizon.cloud/registry/pkg/gid"
)

var semverPattern = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

// asString extracts a string from a value that is already dereferenced by
// Check. Non-string values yield ("", false).
func asString(value any) (string, bool) {
	if value == nil {
		return "", false
	}

	switch v := value.(type) {
	case string:
		return v, true
	case fmt.Stringer:
		return v.String(), true
	default:
		return "", false
	}
}

// Required fails when the value is nil or an empty/whitespace-only string.
func Required() ValidatorFunc {
	return func(value any) *ValidationError {
		if value == nil {
			return newValidationError(ErrorCodeRequired, "is required")
		}

		if s, ok := asString(value); ok && strings.TrimSpace(s) == "" {
			return newValidationError(ErrorCodeRequired, "is required")
		}

		return nil
	}
}

// NotEmpty fails when a present string is empty after trimming.
func NotEmpty() ValidatorFunc {
	return func(value any) *ValidationError {
		if value == nil {
			return nil
		}

		if s, ok := asString(value); ok && strings.TrimSpace(s) == "" {
			return newValidationError(ErrorCodeRequired, "must not be empty")
		}

		return nil
	}
}

// MaxLen fails when a present string exceeds n runes.
func MaxLen(n int) ValidatorFunc {
	return func(value any) *ValidationError {
		s, ok := asString(value)
		if !ok {
			return nil
		}

		if len([]rune(s)) > n {
			return newValidationError(ErrorCodeTooLong, fmt.Sprintf("must be at most %d characters", n))
		}

		return nil
	}
}

// MinLen fails when a present string is shorter than n runes.
func MinLen(n int) ValidatorFunc {
	return func(value any) *ValidationError {
		s, ok := asString(value)
		if !ok {
			return nil
		}

		if len([]rune(s)) < n {
			return newValidationError(ErrorCodeTooShort, fmt.Sprintf("must be at least %d characters", n))
		}

		return nil
	}
}

// OneOf fails when a present string is not one of the allowed values.
func OneOf(allowed ...string) ValidatorFunc {
	return func(value any) *ValidationError {
		s, ok := asString(value)
		if !ok {
			return nil
		}

		for _, a := range allowed {
			if s == a {
				return nil
			}
		}

		return newValidationError(ErrorCodeInvalidEnum, fmt.Sprintf("must be one of %s", strings.Join(allowed, ", ")))
	}
}

// Semver fails when a present string is not a strict MAJOR.MINOR.PATCH version.
func Semver() ValidatorFunc {
	return func(value any) *ValidationError {
		s, ok := asString(value)
		if !ok || s == "" {
			return nil
		}

		if !semverPattern.MatchString(s) {
			return newValidationError(ErrorCodeInvalidFormat, "must be a semantic version (MAJOR.MINOR.PATCH)")
		}

		return nil
	}
}

// NoNewLine fails when a present string contains a line break.
func NoNewLine() ValidatorFunc {
	return func(value any) *ValidationError {
		s, ok := asString(value)
		if !ok {
			return nil
		}

		if strings.ContainsAny(s, "\r\n") {
			return newValidationError(ErrorCodeUnsafeContent, "must not contain line breaks")
		}

		return nil
	}
}

// PrintableText fails when a present string contains control characters other
// than tab, carriage return and newline.
func PrintableText() ValidatorFunc {
	return func(value any) *ValidationError {
		s, ok := asString(value)
		if !ok {
			return nil
		}

		for _, r := range s {
			if unicode.IsControl(r) && r != '\t' && r != '\n' && r != '\r' {
				return newValidationError(ErrorCodeUnsafeContent, "must not contain control characters")
			}
		}

		return nil
	}
}

// GID fails when a present value is not a valid GID of one of the given entity
// types. An empty entityTypes list accepts any type.
func GID(entityTypes ...uint16) ValidatorFunc {
	return func(value any) *ValidationError {
		if value == nil {
			return nil
		}

		var id gid.GID
		switch v := value.(type) {
		case gid.GID:
			id = v
		case string:
			parsed, err := gid.ParseGID(v)
			if err != nil {
				return newValidationError(ErrorCodeInvalidGID, "is not a valid identifier")
			}
			id = parsed
		default:
			return newValidationError(ErrorCodeInvalidGID, "is not a valid identifier")
		}

		if !id.IsValid() {
			return newValidationError(ErrorCodeInvalidGID, "is not a valid identifier")
		}

		if len(entityTypes) == 0 {
			return nil
		}

		for _, et := range entityTypes {
			if id.EntityType() == et {
				return nil
			}
		}

		return newValidationError(ErrorCodeInvalidGID, "is not the expected kind of identifier")
	}
}
