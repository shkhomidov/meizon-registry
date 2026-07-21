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

// Package validator provides a small fluent validation framework. A request
// struct's Validate method builds a Validator, runs Check on each field with a
// list of ValidatorFuncs, and returns the accumulated ValidationErrors. Pointer
// values are dereferenced automatically, so a nil pointer passes every
// non-Required validator.
package validator

import (
	"reflect"
)

type Validator struct {
	errors ValidationErrors
}

func New() *Validator {
	return &Validator{errors: ValidationErrors{}}
}

// Check runs each validator against value under the given field name,
// accumulating any errors. Pointer values are fully dereferenced first.
func (v *Validator) Check(value any, field string, validators ...ValidatorFunc) {
	if len(validators) == 0 {
		return
	}

	actualValue := value
	if value != nil {
		val := reflect.ValueOf(value)
		for val.Kind() == reflect.Pointer && !val.IsNil() {
			val = val.Elem()
			actualValue = val.Interface()
		}

		if val.Kind() == reflect.Pointer && val.IsNil() {
			actualValue = nil
		}
	}

	for _, validate := range validators {
		if err := validate(actualValue); err != nil {
			v.errors = append(v.errors, &ValidationError{
				Field:   field,
				Code:    err.Code,
				Message: err.Message,
				Value:   value,
			})
		}
	}
}

// CheckEach invokes fn for each element of a slice value (dereferencing
// pointers), reporting an error if the value is not a slice.
func (v *Validator) CheckEach(items any, field string, fn func(index int, item any)) {
	if items == nil {
		return
	}

	if slice, ok := items.([]any); ok {
		for i, item := range slice {
			fn(i, item)
		}

		return
	}

	val := reflect.ValueOf(items)
	for val.Kind() == reflect.Pointer {
		if val.IsNil() {
			return
		}

		val = val.Elem()
	}

	if val.Kind() != reflect.Slice {
		v.errors = append(v.errors, &ValidationError{
			Field:   field,
			Code:    ErrorCodeInvalidFormat,
			Message: "expected a slice",
			Value:   items,
		})

		return
	}

	for i := 0; i < val.Len(); i++ {
		fn(i, val.Index(i).Interface())
	}
}

// AddError records an ad-hoc validation error, for business rules that cannot be
// expressed as a per-field ValidatorFunc.
func (v *Validator) AddError(field string, code ErrorCode, message string) {
	v.errors = append(v.errors, &ValidationError{Field: field, Code: code, Message: message})
}

func (v *Validator) Error() error {
	if len(v.errors) == 0 {
		return nil
	}

	return v.errors
}

type ValidatorFunc func(value any) *ValidationError
