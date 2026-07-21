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

import "testing"

// A small target corpus with one obviously-right answer per probe.
var targetCorpus = []mapNode{
	{Ref: "A.5.15", Name: "Access control", Description: "Rules to control physical and logical access to information shall be established and implemented based on business requirements.", Category: "Access"},
	{Ref: "A.5.17", Name: "Authentication information", Description: "Allocation and management of authentication information shall be controlled, including advising personnel on appropriate handling.", Category: "Access"},
	{Ref: "A.8.24", Name: "Use of cryptography", Description: "Rules for the effective use of cryptography, including cryptographic key management, shall be defined and implemented.", Category: "Encryption"},
	{Ref: "A.8.15", Name: "Logging", Description: "Logs that record activities, exceptions, faults and other relevant events shall be produced, stored, protected and analysed.", Category: "Logging"},
	{Ref: "A.5.29", Name: "Information security during disruption", Description: "The organization shall plan how to maintain information security at an appropriate level during disruption.", Category: "Governance"},
	{Ref: "A.6.3", Name: "Awareness training", Description: "Personnel shall receive appropriate information security awareness education and training and regular updates of policies.", Category: "HR"},
}

func refsOf(nodes []mapNode) []string {
	out := make([]string, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, n.Ref)
	}
	return out
}

func shortlisted(refs []string, want string) bool {
	for _, r := range refs {
		if r == want {
			return true
		}
	}
	return false
}

// TestRetrievalRecall is the test that actually predicts whether cross-mapping
// works: the adjudicating model can only ever find what retrieval surfaces, so
// a miss here is an invisible ceiling on the whole feature.
func TestRetrievalRecall(t *testing.T) {
	idf := buildIDF(targetCorpus)

	probes := []struct {
		src  mapNode
		want string
	}{
		{mapNode{Ref: "1", Name: "Cryptographic protection", Description: "The bank shall define rules for the use of cryptography and manage cryptographic keys throughout their lifecycle.", Category: "Encryption"}, "A.8.24"},
		{mapNode{Ref: "2", Name: "Event logging", Description: "Systems shall produce logs recording user activities, exceptions and security events, and those logs shall be protected and analysed.", Category: "Logging"}, "A.8.15"},
		{mapNode{Ref: "3", Name: "Staff security training", Description: "Employees shall receive regular information security awareness education and training.", Category: "HR"}, "A.6.3"},
		{mapNode{Ref: "4", Name: "Access rules", Description: "The organisation shall establish rules controlling logical access to information based on business requirements.", Category: "Access"}, "A.5.15"},
	}

	for _, p := range probes {
		got := refsOf(retrieveCandidates(p.src, targetCorpus, idf, 3))
		if !shortlisted(got, p.want) {
			t.Errorf("recall@3 miss for %q: want %s in candidates, got %v", p.src.Name, p.want, got)
		}
	}
}

// TestRetrievalRanksBestFirst: the true match should not merely appear, it
// should lead — the adjudicator reads the list in order.
func TestRetrievalRanksBestFirst(t *testing.T) {
	idf := buildIDF(targetCorpus)
	src := mapNode{
		Ref: "X", Name: "Cryptographic key management",
		Description: "Rules for the use of cryptography and management of cryptographic keys shall be defined.",
		Category:    "Encryption",
	}
	got := retrieveCandidates(src, targetCorpus, idf, 3)
	if len(got) == 0 || got[0].Ref != "A.8.24" {
		t.Fatalf("best candidate = %v, want A.8.24 first", refsOf(got))
	}
}

// TestRetrievalIsDeterministic: two runs must shortlist identically, otherwise
// the ledger's "already adjudicated" bookkeeping drifts between runs.
func TestRetrievalIsDeterministic(t *testing.T) {
	idf := buildIDF(targetCorpus)
	src := mapNode{Ref: "X", Name: "Logging", Description: "Produce and protect logs of security events."}
	first := refsOf(retrieveCandidates(src, targetCorpus, idf, 4))
	for i := 0; i < 5; i++ {
		again := refsOf(retrieveCandidates(src, targetCorpus, idf, 4))
		if len(first) != len(again) {
			t.Fatalf("shortlist length changed between runs")
		}
		for j := range first {
			if first[j] != again[j] {
				t.Fatalf("shortlist order changed between runs: %v vs %v", first, again)
			}
		}
	}
}

// TestRetrievalIgnoresClauseNumbers: clause numbers are the most misleading
// signal available — two unrelated standards both having a "7.2" means nothing.
func TestRetrievalIgnoresClauseNumbers(t *testing.T) {
	targets := []mapNode{
		{Ref: "7.2", Name: "Vendor management", Description: "Third party suppliers shall be assessed before engagement."},
		{Ref: "9.1", Name: "Backup copies", Description: "Backup copies of information shall be maintained and tested regularly."},
	}
	idf := buildIDF(targets)
	src := mapNode{Ref: "7.2", Name: "Backup", Description: "Backup copies shall be maintained and regularly tested."}

	got := retrieveCandidates(src, targets, idf, 2)
	if len(got) == 0 || got[0].Ref != "9.1" {
		t.Fatalf("matched on the shared clause number instead of the text: got %v, want 9.1 first", refsOf(got))
	}
}

// TestTokenSetStemsAndFilters covers the two rules that shape every score.
func TestTokenSetStemsAndFilters(t *testing.T) {
	got := tokenSet("Encryption encrypted encrypting 2024 the a of policies")
	if len(got) == 0 {
		t.Fatal("no tokens produced")
	}
	// "encryption"/"encrypted"/"encrypting" must collapse to one stem.
	stems := map[string]bool{}
	for tok := range got {
		stems[tok] = true
	}
	if len(stems) > 4 {
		t.Errorf("expected aggressive stemming, got %d distinct tokens: %v", len(stems), stems)
	}
	if got["2024"] {
		t.Error("bare numbers must be dropped — clause and year numbers are noise")
	}
	if got["the"] || got["of"] {
		t.Error("short words must be dropped")
	}
}
