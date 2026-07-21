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

package registry

import (
	"math"
	"sort"
	"strings"
	"unicode"
)

// Stage A of cross-mapping: candidate retrieval.
//
// Mapping 142 source requirements against a 200-requirement target is 28,400
// pairs. Sending them all to a model is absurd, and stuffing both frameworks in
// one prompt loses recall in the middle. So this narrows each source node to a
// handful of plausible targets first, and only those reach the LLM.
//
// Retrieval is done here in Go rather than with pg_trgm: no extension to
// install (registryd's database role may not be allowed to create one), no
// migration risk, deterministic, and directly unit-testable — which matters,
// because RECALL IS THE CEILING ON THE WHOLE FEATURE. The model can only ever
// find what this function surfaces.

// candidatesPerNode is how many targets each source node is compared against.
// Wide enough that the right answer is almost always present, narrow enough
// that a batch stays inside a sane prompt.
const candidatesPerNode = 12

// mapNode is one comparable unit — a requirement or a control — in the
// canonical (English) language.
type mapNode struct {
	Ref         string
	Name        string
	Description string
	Category    string
}

type scoredNode struct {
	Node  mapNode
	Score float64
}

// retrieveCandidates ranks targets against one source node by IDF-weighted
// token overlap: rare shared words ("cryptographic", "revocation") say far more
// about a match than common ones ("the", "must", "information", "security"),
// which in a compliance corpus appear nearly everywhere.
func retrieveCandidates(src mapNode, targets []mapNode, idf map[string]float64, limit int) []mapNode {
	srcTokens := tokenSet(src.Name + " " + src.Description)
	if len(srcTokens) == 0 {
		return nil
	}

	scored := make([]scoredNode, 0, len(targets))
	for _, t := range targets {
		tgtTokens := tokenSet(t.Name + " " + t.Description)
		if len(tgtTokens) == 0 {
			continue
		}
		var shared, srcWeight, tgtWeight float64
		for tok := range srcTokens {
			w := idf[tok]
			srcWeight += w
			if tgtTokens[tok] {
				shared += w
			}
		}
		for tok := range tgtTokens {
			tgtWeight += idf[tok]
		}
		if shared == 0 || srcWeight == 0 || tgtWeight == 0 {
			continue
		}
		// Cosine-like: normalised by both sides, so a long target does not win
		// simply by containing more words.
		score := shared / math.Sqrt(srcWeight*tgtWeight)

		// Same-category nudge. A governance requirement rarely maps to a
		// physical-security one, but the categories are different vocabularies
		// across frameworks, so this only breaks ties — it never gates.
		if src.Category != "" && src.Category == t.Category {
			score *= 1.15
		}
		scored = append(scored, scoredNode{Node: t, Score: score})
	}

	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].Score != scored[j].Score {
			return scored[i].Score > scored[j].Score
		}
		return scored[i].Node.Ref < scored[j].Node.Ref // stable across runs
	})

	if len(scored) > limit {
		scored = scored[:limit]
	}
	out := make([]mapNode, 0, len(scored))
	for _, s := range scored {
		out = append(out, s.Node)
	}
	return out
}

// buildIDF computes inverse document frequency over the target corpus, so
// scoring reflects what is distinctive within the framework being mapped
// against rather than a fixed stopword list.
func buildIDF(nodes []mapNode) map[string]float64 {
	df := map[string]int{}
	for _, n := range nodes {
		for tok := range tokenSet(n.Name + " " + n.Description) {
			df[tok]++
		}
	}
	total := float64(len(nodes))
	idf := make(map[string]float64, len(df))
	for tok, count := range df {
		// +1 smoothing; a token in every document scores ~0 and drops out.
		idf[tok] = math.Log(1 + total/float64(1+count))
	}
	return idf
}

// tokenSet lowercases, splits on non-letters/digits and drops very short words
// and pure numbers. Numbers are dropped deliberately: clause numbers like "7.2"
// are the single most misleading signal here — two unrelated frameworks both
// having a section 7.2 means nothing at all.
func tokenSet(s string) map[string]bool {
	out := map[string]bool{}
	for _, field := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		if len(field) < 4 {
			continue
		}
		if isAllDigits(field) {
			continue
		}
		out[stem(field)] = true
	}
	return out
}

func isAllDigits(s string) bool {
	for _, r := range s {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

// stem is a deliberately crude English suffix trim — enough to match
// "encrypted"/"encryption"/"encrypting" without pulling in a stemmer
// dependency. It only ever runs on canonical-language text.
func stem(w string) string {
	for _, suffix := range []string{"ations", "ation", "ing", "ed", "es", "s"} {
		if len(w) > len(suffix)+3 && strings.HasSuffix(w, suffix) {
			return w[:len(w)-len(suffix)]
		}
	}
	return w
}
