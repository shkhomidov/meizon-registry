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

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"go.gearno.de/kit/unit"
	"go.meizon.cloud/registry/pkg/registryd"
)

var (
	version = "unknown"
	env     = "unknown"
)

func main() {
	impl := registryd.New()
	u := unit.NewUnit(impl, "registryd", version, env)

	err := u.Run()
	if err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintf(os.Stderr,
			`{"time": %q, "msg": %q, "version": %q, "environment": %q, "level": "ERROR", "name": "registryd", "error": %q}`+"\n",
			time.Now().Format(time.RFC3339), "cannot run registryd", version, env, err.Error())
		os.Exit(1)
	}

	os.Exit(0)
}
