// Copyright (c) 2026 Meizon Inc.

package registryd

import "testing"

// TestConsoleBasePathIsDerivedFromTheBaseURL.
//
// The console's mount point and its asset prefix have to agree, and the way
// they are kept in agreement is that both come from one setting. This is the
// server half of that; Vite's `base` and the router's basename are the client
// half, and they read import.meta.env.BASE_URL for the same reason.
//
// The failure when they disagree is why this is tested rather than assumed:
// the HTML loads, every asset 404s, and the page renders blank with nothing in
// any log. That looks like a broken build, not a routing mistake, and it costs
// an afternoon.
func TestConsoleBasePathIsDerivedFromTheBaseURL(t *testing.T) {
	cases := map[string]string{
		// The shared host this change exists for.
		"https://app.meizon.io/registry":  "/registry",
		"https://app.meizon.io/registry/": "/registry",

		// The standalone shape, which must keep working unchanged: the old
		// framework.meizon.io, and any self-hosted mirror at a root.
		"https://framework.meizon.io":  "",
		"https://framework.meizon.io/": "",
		"http://localhost:8080":        "",
		"http://localhost:8080/":       "",

		// Nested, because a reverse proxy in front of a reverse proxy is a
		// thing customers do.
		"https://example.test/tools/registry": "/tools/registry",

		// A malformed value yields the root rather than an error. Deliberate:
		// this decides where a UI is mounted, and refusing to start over a
		// cosmetic setting would take distribution down with it -- and
		// distribution is what the GRC server and every desktop depend on.
		//
		// url.Parse accepts all of these WITHOUT error, which is why the
		// function checks for a scheme and host rather than trusting err.
		"":              "",
		"not a url":     "",
		"app.meizon.io": "",
		"/registry":     "",
		"://broken":     "",
	}
	for in, want := range cases {
		if got := consoleBasePath(in); got != want {
			t.Errorf("consoleBasePath(%q) = %q, want %q", in, got, want)
		}
	}
}
