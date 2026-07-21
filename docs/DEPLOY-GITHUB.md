# Deploying from GitHub

How a change gets from a branch to a running server. For the server-side
specifics — secrets, sizing, reverse proxy, first-run governance — see
[INSTALL-PRODUCTION.md](INSTALL-PRODUCTION.md); this document only covers the
GitHub half.

```
PR ──► CI (test + build, no publish)
        │
        ▼
      merge to main
        │
     git tag v0.2.0
        │
        ▼
   Release ──► ghcr.io/shkhomidov/meizon-registry:0.2.0   (private)
                    │
                    ▼
              server pulls and restarts
```

## The two workflows

| | Runs on | Does | Publishes |
|---|---|---|---|
| `.github/workflows/ci.yml` | every PR, and pushes to `main` | gofmt, vet, tests against a Postgres service, console build, binary build, image build | **no** |
| `.github/workflows/release.yml` | tags matching `v*` | builds and pushes the image to GHCR | yes |

Publishing is tag-driven rather than branch-driven on purpose: an image reaching
the registry should be a deliberate act with a version attached, not a side
effect of merging a PR.

CI builds the image but never pushes it, so a broken Dockerfile fails on the PR
that broke it rather than at release time.

### Two things to know about CI

**The test database is called `registryd_test`, and that matters.** The
database-backed harnesses `TRUNCATE`, and they refuse any database whose name
does not end in `_test`. That guard exists because a test run once wiped a
working `registryd` database — five frameworks and a signed published version,
unrecoverable. Do not "fix" a connection error by pointing CI at a database
named anything else.

**One test is skipped**: `TestNextVersionJob` makes a real outbound call to
`api.openai.com` with a placeholder key, so it fails everywhere, not just in CI.
It is excluded by name with `-skip` so the rest of the suite gives an honest
signal. Point it at a local stub and remove the flag — do not let the skip list
grow quietly.

## Cutting a release

```sh
git checkout main && git pull
git tag v0.2.0 -m "Change feed for consumers"
git push origin v0.2.0
```

Watch it: `gh run watch` — or `gh run list --workflow=release.yml`.

The image lands at `ghcr.io/shkhomidov/meizon-registry`, tagged `0.2.0`, `0.2`,
and `sha-<commit>`. **The package inherits the repository's visibility**, so a
private repo produces a private image. Verify after the first release rather
than assuming:

```sh
gh api user/packages/container/meizon-registry --jq .visibility
```

No registry credential is stored anywhere: the workflow authenticates with the
`GITHUB_TOKEN` minted for that run and scoped by its `permissions:` block.

## Pulling on the server

The image is private, so the server needs a read-only credential. Create a
**fine-grained personal access token** with only `read:packages`, or better, a
GitHub App installation token if you have one.

Do this on the server, not in CI, and not in this repo:

```sh
# On the server. Paste the token at the prompt — do not put it in the command,
# where it would land in shell history.
docker login ghcr.io -u <your-github-username>
```

Then, with `compose.prod.yaml`:

```sh
export REGISTRYD_IMAGE=ghcr.io/shkhomidov/meizon-registry:0.2.0
docker compose -f compose.prod.yaml pull
docker compose -f compose.prod.yaml up -d
```

Migrations apply on boot; there is no separate migrate step.

For Kubernetes, create the pull secret once and reference it from the chart:

```sh
kubectl create secret docker-registry ghcr \
  --docker-server=ghcr.io --docker-username=<user> --docker-password=<token>

helm upgrade registry ./helm/registryd \
  --set image.repository=ghcr.io/shkhomidov/meizon-registry \
  --set image.tag=0.2.0 \
  --set imagePullSecrets[0].name=ghcr
```

## Automating the deploy step (optional)

Everything above stops at "an image exists". Pushing it onto a server from
GitHub means giving GitHub a way in, so weigh it deliberately:

- **Self-hosted runner on the server** — no inbound SSH, no long-lived key in
  GitHub. The runner pulls work. Best option if you control the host.
- **SSH from a hosted runner** — needs a private key in GitHub Secrets. That key
  is a standing grant of server access to anyone who can modify a workflow file.
  If you take this route, restrict the key to a single command with
  `command=` in `authorized_keys`, and protect `main`.
- **Pull-based** — a `systemd` timer on the server that checks for a new tag.
  Slowest to react, smallest attack surface, nothing to leak.

Whichever you pick, gate it on a GitHub **Environment** with required reviewers
so a deploy is an approval, not a merge.

## Secrets: what goes where

`REGISTRYD_ENCRYPTION_KEY`, `REGISTRYD_AUTH_PASSWORD_PEPPER`,
`REGISTRYD_AUTH_COOKIE_SECRET`, `REGISTRYD_PG_PASSWORD` and
`REGISTRYD_ADMIN_TOKEN` belong **on the server or in its secret manager** — not
in GitHub Secrets, and not in the repo. CI never needs them: it runs tests
against a throwaway database with throwaway values.

Put something in GitHub Secrets only if a workflow genuinely needs it, which
today means nothing at all.

The **LLM and OCR API keys are not deployment configuration.** A superadmin
enters them in the console's Settings page; they are stored AES-256-GCM
encrypted and never returned by the API. Do not add them as environment
variables or GitHub Secrets — that would put them somewhere they can be read
back.

Rotating `REGISTRYD_ENCRYPTION_KEY` is not a config change: it decrypts stored
signing-key material. It needs a migration plan, not a redeploy.

## Recommended repository settings

Worth doing once, in **Settings → Branches**:

- Require CI to pass before merging to `main`.
- Require a pull request. This mirrors the moderation model the registry itself
  enforces — an author does not publish their own work unreviewed.
- Restrict who can push tags, since a tag is what ships an image.

## Rollback

Images are immutable and every release keeps its tag, so rolling back is
re-pinning:

```sh
export REGISTRYD_IMAGE=ghcr.io/shkhomidov/meizon-registry:0.1.0
docker compose -f compose.prod.yaml up -d
```

**The database does not roll back with it.** Migrations apply on boot and are
not reversed, so an older image may meet a newer schema. Check whether the
release you are rolling back over added a migration before assuming this is
safe — back up Postgres first regardless. All state lives there: frameworks,
signing keys, tokens, audit, translations, cross-mappings, and the uploaded
source documents.
