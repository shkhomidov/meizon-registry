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

package registry_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"go.meizon.cloud/registry/pkg/gid"
	"go.meizon.cloud/registry/pkg/registry"
)

// syncFixture stands up governance, a signing key and the three identities the
// moderation rules distinguish between.
type syncFixture struct {
	svc     *registry.Service
	super   gid.GID
	auditor gid.GID
	mod     gid.GID
	mod2    gid.GID
}

func newSyncFixture(t *testing.T) *syncFixture {
	t.Helper()
	svc, _ := newTestService(t)
	ctx := context.Background()

	if _, err := svc.BootstrapSuperAdmin(ctx, req(superAdminEmail, "Root")); err != nil {
		t.Fatalf("bootstrap superadmin: %v", err)
	}
	super := mustID(t, svc, superAdminEmail)

	mustCreateUser(t, svc, super, "sync-auditor@meizon.test", "Auditor", "auditor", []string{"EU"})
	mustCreateUser(t, svc, super, "sync-mod@meizon.test", "Moderator", "moderator", []string{"EU"})
	mustCreateUser(t, svc, super, "sync-mod2@meizon.test", "Moderator Two", "moderator", []string{"EU"})

	if err := svc.GenerateSigningKey(ctx, super, "reg-test"); err != nil {
		t.Fatalf("generate signing key: %v", err)
	}

	return &syncFixture{
		svc:     svc,
		super:   super,
		auditor: mustID(t, svc, "sync-auditor@meizon.test"),
		mod:     mustID(t, svc, "sync-mod@meizon.test"),
		mod2:    mustID(t, svc, "sync-mod2@meizon.test"),
	}
}

// authorDraft creates a framework with one control, authored by the auditor.
func (f *syncFixture) authorDraft(t *testing.T, ref, region string) gid.GID {
	t.Helper()
	created, err := f.svc.CreateFramework(context.Background(), f.super, registry.CreateFrameworkRequest{
		ReferenceID: ref, Name: "Framework " + ref, Region: region,
		Authority: "test", License: "public-domain",
	})
	if err != nil {
		t.Fatalf("create framework %s: %v", ref, err)
	}
	addControl(t, f.svc, f.super, ref, "c-1", "A control")
	return created.VersionID
}

// release drives a draft all the way to PUBLISHED using distinct identities, so
// separation of duties is satisfied.
func (f *syncFixture) release(t *testing.T, versionID gid.GID) {
	t.Helper()
	f.releaseWith(t, versionID, f.mod)
}

// releaseWith is release with an explicit moderator, for frameworks outside the
// default EU region — a moderator may only act within their own regions, so the
// EU moderator genuinely cannot approve a US framework.
func (f *syncFixture) releaseWith(t *testing.T, versionID, moderator gid.GID) {
	t.Helper()
	ctx := context.Background()
	if err := f.svc.Submit(ctx, f.super, versionID); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if err := f.svc.Approve(ctx, moderator, versionID, "ok"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if err := f.svc.Publish(ctx, moderator, versionID); err != nil {
		t.Fatalf("publish: %v", err)
	}
}

func (f *syncFixture) tokenFor(t *testing.T, name string, regions []string) registry.TokenContext {
	t.Helper()
	plaintext, err := f.svc.IssueToken(context.Background(), f.super, name, regions)
	if err != nil {
		t.Fatalf("issue token %s: %v", name, err)
	}
	tc, err := f.svc.AuthenticateToken(context.Background(), plaintext)
	if err != nil {
		t.Fatalf("authenticate token %s: %v", name, err)
	}
	return tc
}

// TestChangeFeedCursorIsContiguousUnderConcurrentPublish is the load-bearing
// test for the whole sync protocol.
//
// The failure it guards against is invisible in normal use: with a bigserial
// sequence, two concurrent publishes can take seq 101 and 102 and commit in the
// opposite order. A consumer that polls in between reads 102, stores it as its
// cursor, and asks next time for "everything after 102" — so 101, committed
// afterwards, is never delivered. The consumer reports a successful sync while
// silently missing a published framework, which for compliance data is worse
// than a visible failure.
//
// Assigning seq under an advisory lock held to commit makes sequence order equal
// commit order, so a contiguous run with no duplicates is the observable
// property. Concurrency here is real, not simulated.
func TestChangeFeedCursorIsContiguousUnderConcurrentPublish(t *testing.T) {
	f := newSyncFixture(t)
	ctx := context.Background()

	const publishers = 8

	// Author every draft up front; only the publish step races.
	versions := make([]gid.GID, publishers)
	for i := range versions {
		versions[i] = f.authorDraft(t, fmt.Sprintf("concurrent-%02d", i), "EU")
		if err := f.svc.Submit(ctx, f.super, versions[i]); err != nil {
			t.Fatalf("submit %d: %v", i, err)
		}
		if err := f.svc.Approve(ctx, f.mod, versions[i], "ok"); err != nil {
			t.Fatalf("approve %d: %v", i, err)
		}
	}

	var wg sync.WaitGroup
	errs := make([]error, publishers)
	start := make(chan struct{})
	for i := range versions {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start // release them together, maximising overlap
			errs[i] = f.svc.Publish(ctx, f.mod, versions[i])
		}(i)
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("concurrent publish %d: %v", i, err)
		}
	}

	tc := f.tokenFor(t, "grc-concurrent", []string{"EU"})
	feed, err := f.svc.Changes(ctx, tc, 0, 1000)
	if err != nil {
		t.Fatalf("changes: %v", err)
	}

	if len(feed.Events) != publishers {
		t.Fatalf("expected %d events, got %d", publishers, len(feed.Events))
	}

	// Contiguous, strictly ascending, no duplicates: every sequence number
	// between the first and the last is accounted for, so no cursor position
	// can skip an event.
	seen := map[int64]bool{}
	for i, e := range feed.Events {
		if want := int64(i + 1); e.Seq != want {
			t.Fatalf("event %d has seq %d, want %d — the sequence has a gap or is out of order", i, e.Seq, want)
		}
		if seen[e.Seq] {
			t.Fatalf("seq %d appears twice", e.Seq)
		}
		seen[e.Seq] = true
	}
}

// TestChangeFeedDeliversEveryEventToAPollingConsumer walks the feed the way a
// real consumer does — page by page, persisting the cursor after each page —
// and asserts nothing is dropped at a page boundary.
func TestChangeFeedDeliversEveryEventToAPollingConsumer(t *testing.T) {
	f := newSyncFixture(t)
	ctx := context.Background()

	const total = 7
	for i := range total {
		ref := fmt.Sprintf("paged-%02d", i)
		f.release(t, f.authorDraft(t, ref, "EU"))
	}

	tc := f.tokenFor(t, "grc-paged", []string{"EU"})

	// Page size deliberately does not divide the total, so the last page is
	// partial — the case where an off-by-one in the cursor shows up.
	var (
		cursor    int64
		collected []string
		pages     int
	)
	for {
		feed, err := f.svc.Changes(ctx, tc, cursor, 3)
		if err != nil {
			t.Fatalf("changes at cursor %d: %v", cursor, err)
		}
		pages++
		for _, e := range feed.Events {
			collected = append(collected, e.Framework)
		}
		cursor = feed.NextSeq
		if !feed.HasMore {
			break
		}
		if pages > 10 {
			t.Fatal("feed did not terminate — HasMore never went false")
		}
	}

	if len(collected) != total {
		t.Fatalf("consumer collected %d frameworks across %d pages, want %d", len(collected), pages, total)
	}

	// Re-polling from the final cursor must be empty, not a replay of the tail.
	feed, err := f.svc.Changes(ctx, tc, cursor, 3)
	if err != nil {
		t.Fatalf("final poll: %v", err)
	}
	if len(feed.Events) != 0 {
		t.Fatalf("expected an idle poll to return nothing, got %d events", len(feed.Events))
	}
	if feed.HeadSeq != cursor {
		t.Fatalf("caught-up consumer has cursor %d but head is %d", cursor, feed.HeadSeq)
	}
}

// TestDeprecationReachesConsumers covers the signal that had no representation
// at all before: a version being withdrawn. A consumer that already imported it
// cannot otherwise tell retirement apart from the registry being unreachable.
func TestDeprecationReachesConsumers(t *testing.T) {
	f := newSyncFixture(t)
	ctx := context.Background()

	versionID := f.authorDraft(t, "to-be-retired", "EU")
	f.release(t, versionID)

	tc := f.tokenFor(t, "grc-retire", []string{"EU"})

	feed, err := f.svc.Changes(ctx, tc, 0, 100)
	if err != nil {
		t.Fatalf("changes: %v", err)
	}
	if len(feed.Events) != 1 || feed.Events[0].Kind != "published" {
		t.Fatalf("expected one published event, got %+v", feed.Events)
	}
	cursor := feed.NextSeq

	if err := f.svc.Deprecate(ctx, f.mod, versionID); err != nil {
		t.Fatalf("deprecate: %v", err)
	}

	feed, err = f.svc.Changes(ctx, tc, cursor, 100)
	if err != nil {
		t.Fatalf("changes after deprecate: %v", err)
	}
	if len(feed.Events) != 1 {
		t.Fatalf("expected exactly one new event, got %d", len(feed.Events))
	}
	if feed.Events[0].Kind != "deprecated" {
		t.Fatalf("expected a deprecated event, got %q", feed.Events[0].Kind)
	}
	if feed.Events[0].Framework != "to-be-retired" {
		t.Fatalf("deprecation names the wrong framework: %q", feed.Events[0].Framework)
	}
}

// TestChangeFeedRespectsRegionScope: the feed must not leak the existence of
// frameworks a token could never fetch. HeadSeq is checked too — a global head
// would disclose that events exist elsewhere.
func TestChangeFeedRespectsRegionScope(t *testing.T) {
	f := newSyncFixture(t)
	ctx := context.Background()

	mustCreateUser(t, f.svc, f.super, "sync-us-mod@meizon.test", "US Moderator", "moderator", []string{"US"})
	usMod := mustID(t, f.svc, "sync-us-mod@meizon.test")

	f.release(t, f.authorDraft(t, "eu-framework", "EU"))
	f.releaseWith(t, f.authorDraft(t, "us-framework", "US"), usMod)

	euToken := f.tokenFor(t, "grc-eu-scoped", []string{"EU"})
	feed, err := f.svc.Changes(ctx, euToken, 0, 100)
	if err != nil {
		t.Fatalf("eu changes: %v", err)
	}
	if len(feed.Events) != 1 || feed.Events[0].Framework != "eu-framework" {
		t.Fatalf("EU token should see only eu-framework, got %+v", feed.Events)
	}
	if feed.HeadSeq != feed.Events[0].Seq {
		t.Fatalf("HeadSeq %d leaks out-of-scope activity (visible event is seq %d)",
			feed.HeadSeq, feed.Events[0].Seq)
	}

	// An unscoped token is the operator's view and sees both.
	allToken := f.tokenFor(t, "grc-unscoped", nil)
	feed, err = f.svc.Changes(ctx, allToken, 0, 100)
	if err != nil {
		t.Fatalf("unscoped changes: %v", err)
	}
	if len(feed.Events) != 2 {
		t.Fatalf("unscoped token should see both frameworks, got %d", len(feed.Events))
	}
}

// TestAuthorCannotPublishOwnVersion pins the separation-of-duties rule added on
// the publish transition. Approve was already guarded; publish was not, so an
// author holding the moderator role could obtain a colleague's approval and then
// release their own work unreviewed by anyone else at the moment it went out.
func TestAuthorCannotPublishOwnVersion(t *testing.T) {
	f := newSyncFixture(t)
	ctx := context.Background()

	// mod authors this one, so mod is the author and mod2 the approver.
	created, err := f.svc.CreateFramework(ctx, f.mod, registry.CreateFrameworkRequest{
		ReferenceID: "self-published", Name: "Self Published", Region: "EU",
		Authority: "test", License: "public-domain",
	})
	if err != nil {
		t.Fatalf("create framework: %v", err)
	}
	addControl(t, f.svc, f.mod, "self-published", "c-1", "A control")

	if err := f.svc.Submit(ctx, f.mod, created.VersionID); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if err := f.svc.Approve(ctx, f.mod2, created.VersionID, "reviewed"); err != nil {
		t.Fatalf("approve by second moderator: %v", err)
	}

	// The author, despite holding the publish permission and despite a valid
	// approval existing, must not be the one to release it.
	if err := f.svc.Publish(ctx, f.mod, created.VersionID); !errors.Is(err, registry.ErrSeparationOfDuties) {
		t.Fatalf("expected author publish to be refused by separation of duties, got: %v", err)
	}

	// Somebody else may.
	if err := f.svc.Publish(ctx, f.mod2, created.VersionID); err != nil {
		t.Fatalf("non-author publish: %v", err)
	}
}

// TestUnpublishedWorkNeverReachesTheFeed: the moderation gate is what the whole
// distribution story rests on. A draft, a submission awaiting review and an
// approved-but-unpublished version must all be invisible to consumers.
func TestUnpublishedWorkNeverReachesTheFeed(t *testing.T) {
	f := newSyncFixture(t)
	ctx := context.Background()

	draft := f.authorDraft(t, "still-a-draft", "EU")
	inReview := f.authorDraft(t, "awaiting-review", "EU")
	approved := f.authorDraft(t, "approved-not-published", "EU")

	if err := f.svc.Submit(ctx, f.super, inReview); err != nil {
		t.Fatalf("submit in-review: %v", err)
	}
	if err := f.svc.Submit(ctx, f.super, approved); err != nil {
		t.Fatalf("submit approved: %v", err)
	}
	if err := f.svc.Approve(ctx, f.mod, approved, "ok"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	_ = draft

	tc := f.tokenFor(t, "grc-gate", []string{"EU"})
	feed, err := f.svc.Changes(ctx, tc, 0, 100)
	if err != nil {
		t.Fatalf("changes: %v", err)
	}
	if len(feed.Events) != 0 {
		t.Fatalf("unpublished work leaked to consumers: %+v", feed.Events)
	}
	if feed.HeadSeq != 0 {
		t.Fatalf("HeadSeq should be 0 when nothing is published, got %d", feed.HeadSeq)
	}

	// Publishing the approved one — and only that one — opens the gate.
	if err := f.svc.Publish(ctx, f.mod, approved); err != nil {
		t.Fatalf("publish: %v", err)
	}
	feed, err = f.svc.Changes(ctx, tc, 0, 100)
	if err != nil {
		t.Fatalf("changes after publish: %v", err)
	}
	if len(feed.Events) != 1 || feed.Events[0].Framework != "approved-not-published" {
		t.Fatalf("expected only the published framework, got %+v", feed.Events)
	}
}

// TestRejectReturnsVersionToDraft covers the transition that existed in the
// service but had no route, CLI command or button — a moderator could previously
// only approve or leave a submission pending forever.
func TestRejectReturnsVersionToDraft(t *testing.T) {
	f := newSyncFixture(t)
	ctx := context.Background()

	versionID := f.authorDraft(t, "needs-work", "EU")
	if err := f.svc.Submit(ctx, f.super, versionID); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if err := f.svc.Reject(ctx, f.mod, versionID, "citations missing"); err != nil {
		t.Fatalf("reject: %v", err)
	}

	// Back in DRAFT means editable again: a rejection must not be terminal.
	if _, err := f.svc.AddControl(ctx, f.super, registry.AddControlRequest{
		VersionID: versionID, RefID: "c-2", Name: "Added after rejection",
	}); err != nil {
		t.Fatalf("expected a rejected version to be editable again: %v", err)
	}

	// And nothing was announced to consumers.
	tc := f.tokenFor(t, "grc-reject", []string{"EU"})
	feed, err := f.svc.Changes(ctx, tc, 0, 100)
	if err != nil {
		t.Fatalf("changes: %v", err)
	}
	if len(feed.Events) != 0 {
		t.Fatalf("a rejected version must not appear in the feed, got %+v", feed.Events)
	}
}
