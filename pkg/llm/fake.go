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

package llm

import "context"

// Fake is a deterministic in-memory provider for tests.
//
// Two modes. Route, if set, selects a response by inspecting the request — so a
// test keys responses to pipeline steps and stays correct however many times
// each step runs. Otherwise Responses are returned in order (last one repeated
// on overflow), which couples the test to the exact call count and is fragile
// when a step's call count changes.
type Fake struct {
	Responses []string
	// Route returns the response for a request, or "" to fall back to Responses.
	Route func(Request) string
	Calls []Request
	i     int
}

func (f *Fake) Provider() string { return ProviderFake }

func (f *Fake) Generate(_ context.Context, req Request) (Response, error) {
	f.Calls = append(f.Calls, req)

	if f.Route != nil {
		if text := f.Route(req); text != "" {
			return Response{Text: text, Model: "fake-1"}, nil
		}
	}

	text := `{}`
	if len(f.Responses) > 0 {
		if f.i < len(f.Responses) {
			text = f.Responses[f.i]
			f.i++
		} else {
			text = f.Responses[len(f.Responses)-1]
		}
	}
	return Response{Text: text, Model: "fake-1"}, nil
}
