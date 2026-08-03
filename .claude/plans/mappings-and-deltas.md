# Plan — cross-mappings on the wire, and version deltas

> **BOTH LANDED 2026-07-21, verified against a live database.** Part B (version
> deltas) and Part A (signed mapping sets, publish lifecycle with the non-public
> target gate, distribution endpoints, and client consumption) are complete. The
> A×B interaction (flagging stale mapping sets on a requirement change) is the
> one deferred item. See the tail of this file for the original plan.



The two remaining ❌ in the sync story. They are independent: build either first.
Current state verified 2026-07-21 against the working tree.

---

# Part A — cross-mappings crossing the wire

## Where this already stands

The architectural decision was made and half-built: mappings become their own
signed, moderated artifact rather than living inside the framework bundle. The
reason is not preference — it is forced. A distributed bundle is re-assembled
from live tables on every request while the signature served with it is the
stored one, so a mapping edited after publication changes the bytes and every
consumer rejects the bundle as tampered. Mappings could never be both inline and
editable.

Already on `main`:

- `mapping_sets` table — code-addressed target `(target_framework_code,
  target_framework_version)`, moderation provenance columns, frozen counts,
  `signature`/`content_hash`/`key_id`. Unique per `(source version, target
  framework@version)`.
- Bundle format versioned to v3 (mappings removed from the framework document),
  v2 preserved for the one already-published framework.
- The feed migration allows `mapping_published` / `mapping_deprecated` kinds and
  carries `target_framework_ref` / `target_version` columns.

**Nothing reads or writes `mapping_sets`.** The table is inert. And the Go event
constants for the two mapping kinds do NOT exist yet — `distribution_event.go`
has only `published`/`deprecated`. So the feed cannot emit them even though the
column CHECK permits them.

## What a mapping set contains

Assembled from the authoring-side tables that already hold the work — 14
`requirement_cross_mappings` and 65 `control_cross_mappings` today, none of which
reach any consumer:

```
MappingSet {
  $schema:  "meizon-mappingset/v1"
  source:   { framework, version }
  target:   { framework, version }
  requirementMappings: [ { sourceRef, targetRef, relation, notes } ]
  controlMappings:     [ { sourceRef, targetRef, relation, notes } ]
  publishedAt, approvedBy, publishedBy
  signature: { keyId, contentHash, sig }   // ed25519 over canonical JSON
}
```

Addressed by code end to end, exactly as the stored mappings are, so a set can
be authored and published before the target framework is loaded on the consumer
— it resolves late, or dangles harmlessly.

## The security gate — decide before the first signature

**A signed artifact cannot be filtered per token.** A mapping naming
`iso-27001/A.9.2.3` discloses that framework's control codes to anyone who can
pull the set. For public standards this is nothing. For a **non-public tenant
framework it is a leak**, and signing makes it permanent.

Rule: **refuse to publish a mapping set whose target framework is not `Public`**,
unless an explicit override is set. Enforce at publish, in the same transaction
that signs — after signing it is irrevocable. This is the one decision that
cannot be walked back, so it goes in first, with a test that a non-public target
is rejected.

## Pieces

1. **`pkg/fwmap`** — the schema, canonical JSON, `Sign`/`Verify` (ed25519),
   mirroring `pkg/fwschema/sign.go` so the trust mechanism is identical and
   auditable the same way. Pure, fully unit-testable.

2. **coredata** — `MappingSet` CRUD on the existing table: `Insert`,
   `LoadByPair`, `LoadPublishedByTarget`, status transitions, and the two event
   constants (`DistributionEventMappingPublished` / `…Deprecated`) plus the
   `target_*` fields on `DistributionEvent.Append`.

3. **Service + lifecycle** — `AssembleMappingSet(sourceVersion, targetFramework)`
   reads the cross-mapping tables into a `fwmap.MappingSet`; a
   submit→approve→publish lifecycle mirroring the framework one (same moderator
   gate, same separation of duties), signing on publish, emitting
   `mapping_published`. The non-public-target refusal lives here.

4. **Distribution API** — `GET /mappings` (catalog of published sets visible to
   the token, region/copyright gated) and `GET
   /mappings/{source}/{sourceVer}/{target}/{targetVer}` returning the signed set.
   Same bearer + region gating as the framework routes.

5. **Client** — on a `mapping_published` event, fetch the set, `Verify` it,
   write `<source>__<target>.mzmap.json`, and resolve refs against frameworks
   already held (dangling where the target is absent). `mapping_deprecated`
   retires it, exactly as framework deprecation does now.

## Sequencing (A)

Security gate + `pkg/fwmap` sign/verify → coredata + event constants → service
lifecycle → distribution endpoints → client. The gate and the schema are pure
and land first; everything after needs a database to verify.

---

# Part B — version deltas (upgrade without high changes)

## The requirement

When `iso-27001@2023` supersedes `@2022`, a consuming GRC has evidence,
assessments and internal-control links attached to each requirement. A full
re-import orphans all of it. The upgrade must tell the consumer **what actually
changed** so it keeps everything attached to requirements that did not.

Requirement **codes** are the stable identity — already how cross-mappings
address things — so a delta keyed on codes lets the consumer preserve work on
unchanged requirements and touch only the rest.

## The decision: compute the delta client-side, do NOT distribute a signed one

The tempting shape is a signed delta artifact served by the registry. **Reject
it.** A delta is fully derivable from two bundles the consumer can already fetch
and verify, so:

- A separately-signed delta is a third thing to keep consistent with the two
  bundles, and a new trust surface, for zero information gain.
- The consumer already holds the old version — the client writes `<id>.mzfw.json`
  on every sync. It fetches the new bundle, verifies it, and diffs against the
  held one. **The delta inherits the trust of two independently-verified
  signatures**; nothing new needs signing.
- A crash or partial state degrades to "re-fetch and re-diff", never to a delta
  that disagrees with the bundles it describes.

So version deltas are a **client capability**, not a distribution artifact. The
registry side needs no new endpoint. (An optional server `/diff` endpoint could
save the consumer holding the old bundle, but it is advisory and strictly a
later optimization — the authoritative path is local.)

## What exists to reuse

`diffFlatAgainstBaseline` (`version_service.go:107`) already classifies
requirements as `added` / `modified` / `removed` / `unchanged`, keyed by code —
but it is server-side and operates on `fwflat` against stored structure. The
client operates on `fwschema` bundles. The logic ports directly; the types
differ.

## Pieces

1. **`distclient.DiffBundles(old, new *fwschema.Framework) VersionDelta`** — a
   pure function over two verified bundles. Walks requirements by code
   (`WalkAssessable`), classifies each `added|removed|modified|unchanged`,
   compares title+description for `modified`. Fully unit-testable, no I/O.

   ```
   VersionDelta {
     framework, fromVersion, toVersion
     added:     [ ref ]
     removed:   [ ref ]
     modified:  [ { ref, fields: [title|description|...] } ]
     unchanged: count
   }
   ```

2. **Wire into sync** — in `pull`, when a `published` event arrives for a
   framework already held at a different version: read the held `.mzfw.json`,
   fetch+verify the new bundle, `DiffBundles`, and write `<id>.delta.json`
   alongside the updated seed and bundle. The consumer's importer reads the delta
   and applies surgically. First-ever sight of a framework has no baseline, so no
   delta — the full seed is the delta.

3. **Report deltas** — `SyncedFramework` gains `Added/Removed/Modified` counts so
   the CLI prints `iso-27001 2022→2023  +3 ~5 -1` instead of a bare "verified".
   That line is the feature made visible: an operator sees the upgrade was small.

## Why this satisfies "without high changes"

The consumer applies only `added` (new requirements), `removed` (retire, with
its attached work flagged for review — never silently dropped), and `modified`
(re-review those specific requirements). Everything `unchanged` keeps its
evidence and assessments untouched, identified by a code that did not move. A
104-requirement framework with a 3-requirement revision costs the consumer three
re-reviews, not 104.

## Sequencing (B)

`DiffBundles` + its tests (pure, lands immediately) → wire into `pull` with a
delta file → CLI reporting. All of it is client-side and testable against the
same in-memory signed-bundle harness the sync tests already use — no database.

---

## Interaction between A and B

A framework upgrade (B) can strand mappings (A): a set published against
`@2022` may point at requirement codes that `@2023` renamed or removed. When
both ship, a mapping set carries the target version it was authored against, and
the client marks a set stale when it holds a newer target than the set names —
the `needs_review` state the authoring side already tracks internally
(`MarkReviewState`). Note it in B's delta output: a `removed` or `modified`
requirement should flag any held mapping set that references it. Not required for
either to ship alone; wire it once both exist.

## What ships first

B is smaller, entirely client-side, needs no database to verify, and directly
answers the stated ask. A is larger, needs the security decision locked and a
database to exercise the lifecycle. **Recommend B first** — it is a complete,
testable, user-visible win on its own, and it de-risks A's staleness handling by
having the delta machinery already in place.

## Verification (both, DB-free where possible)

- `pkg/fwmap`: sign→verify round trip; tamper rejection; canonical-JSON
  stability (same set → same hash).
- Non-public target refused at publish (needs DB — the one part that does).
- `DiffBundles`: added/removed/modified/unchanged by code; a renamed code reads
  as remove+add, not modify (codes are identity); title-only change is modified.
- Sync integration: publish v1, sync; publish v2 with one changed requirement,
  sync; assert the delta file lists exactly that one and the seed updated. Reuses
  the existing `fakeRegistry` harness.
- Mapping client: `mapping_published` fetched+verified+written;
  `mapping_deprecated` retires it; a set targeting an unheld framework dangles
  without error.
