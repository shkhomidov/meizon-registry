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

// Package bootstrap renders REGISTRYD_* environment variables into a registryd
// configuration document. A resolver transparently dereferences secret
// references so secrets need not be inlined in the environment.
package bootstrap

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Secret-reference prefixes. file:// reads a secret from a local file (e.g. a
// mounted Kubernetes secret). The awssm:// / awsps:// prefixes are reserved for
// AWS Secrets Manager and SSM Parameter Store; wiring the AWS SDK is a follow-up,
// so they currently produce an explicit error rather than silently passing a
// literal ref through.
const (
	fileRefPrefix  = "file://"
	awsSMRefPrefix = "awssm://"
	awsPSRefPrefix = "awsps://"
)

// Resolver reads environment variables and resolves secret references.
type Resolver struct {
	err error
}

// NewResolver returns a resolver.
func NewResolver() *Resolver { return &Resolver{} }

// Err returns the first resolution error encountered, if any.
func (r *Resolver) Err() error { return r.err }

func (r *Resolver) resolve(raw string) string {
	switch {
	case strings.HasPrefix(raw, fileRefPrefix):
		path := strings.TrimPrefix(raw, fileRefPrefix)
		data, err := os.ReadFile(path)
		if err != nil {
			r.setErr(fmt.Errorf("cannot read secret file %q: %w", path, err))
			return ""
		}
		return strings.TrimSpace(string(data))
	case strings.HasPrefix(raw, awsSMRefPrefix), strings.HasPrefix(raw, awsPSRefPrefix):
		r.setErr(fmt.Errorf("aws secret reference %q is not supported in this build; use file:// or an inline value", raw))
		return ""
	default:
		return raw
	}
}

func (r *Resolver) setErr(err error) {
	if r.err == nil {
		r.err = err
	}
}

func (r *Resolver) getEnv(key string) string {
	return r.resolve(os.Getenv(key))
}

func (r *Resolver) getEnvOrDefault(key, def string) string {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	return r.resolve(v)
}

func (r *Resolver) getEnvIntOrDefault(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		r.setErr(fmt.Errorf("invalid integer for %s: %w", key, err))
		return def
	}
	return n
}
