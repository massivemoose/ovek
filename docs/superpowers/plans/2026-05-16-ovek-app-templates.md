# Ovek App Templates Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Create a polished first Ovek app template repo that users can clone or use as a GitHub template to build, publish, and run an Ovek-ready capsule.

**Architecture:** Keep template repos separate from the main Ovek repo. Each template owns its app code, local development helpers, OCI image publishing workflow, and Ovek usage docs. The first template should prove the full Ovek contract with Go + PocketBase before broadening to other languages.

**Tech Stack:** Go, PocketBase, Dockerfile/OCI image builds, GHCR, GitHub Actions, Podman-compatible runtime validation, Ovek CLI.

---

## Summary

Create a small set of Ovek-ready app template repos that people can clone or use as GitHub templates. The first template should be excellent rather than broad: a Go + PocketBase web app that proves the full Ovek capsule path with minimal moving parts.

Distribution should use separate GitHub template repos as the source of truth. The Ovek site can later link to those repos and GitHub-generated zip/tar downloads, but should not maintain separate template archives at first.

## Initial Template

Start with a new public GitHub template repo:

```text
massivemoose/ovek-template-go-pocketbase
```

The template should include:

- A tiny signup app adapted from `ovek-signup-example`, trimmed into reusable starter shape.
- `main.go`, `config.go`, `handlers.go`, `pocketbase.go`, focused tests, and embedded HTML templates.
- `Dockerfile` for multi-arch OCI image builds.
- `.github/workflows/publish-image.yml` publishing to `ghcr.io/${{ github.repository }}` with `latest` and SHA tags.
- `.gitignore` and `.dockerignore` covering `.llms`, `.envrc`, `pocketbase`, `pb_data`, `pb_migrations`, build artifacts, and OS/editor noise.
- `.envrc.example`, not a committed `.envrc`.
- `scripts/fetch-pocketbase` that downloads pinned PocketBase `v0.38.1` by OS/arch and verifies checksums.
- `scripts/dev-pocketbase` and `scripts/dev-app` for local development.

The Ovek runtime contract should stay explicit:

- App listens on `PORT`, default `8080`.
- App reads `POCKETBASE_URL`, default local `http://127.0.0.1:8090`.
- App accepts `PB_SUPERUSER_TOKEN` or `PB_SUPERUSER_EMAIL` plus `PB_SUPERUSER_PASSWORD`.
- Ovek use requires `ovek db init <project> --app-secrets` before `ovek run`.
- Public docs must use `ovek db`, not the older `ovek pb` wording.

## Implementation Tasks

### Task 1: Create The Template Repo Skeleton

- [ ] Create a separate repository directory outside this Ovek checkout, named `ovek-template-go-pocketbase`.
- [ ] Initialize Git and mark the GitHub repo as a template repository after push.
- [ ] Add baseline files: `README.md`, `.gitignore`, `.dockerignore`, `.envrc.example`, `go.mod`, and `Dockerfile`.
- [ ] Use `module github.com/massivemoose/ovek-template-go-pocketbase` in `go.mod` until template users rename it.
- [ ] Keep local generated files out of Git: `.envrc`, `.llms`, `pocketbase`, `pb_data`, and `pb_migrations`.
- [ ] Commit as: `Add Go PocketBase template skeleton`.

### Task 2: Add The Go Signup Starter

- [ ] Add `config.go` with env loading for `PORT`, `POCKETBASE_URL`, `PB_SUPERUSER_TOKEN`, `PB_SUPERUSER_EMAIL`, and `PB_SUPERUSER_PASSWORD`.
- [ ] Add `pocketbase.go` with a small PocketBase client that authenticates, ensures a `signups` collection, and writes signup records.
- [ ] Add `handlers.go` and embedded templates for `/`, `/signup`, `/success`, and a simple health endpoint.
- [ ] Add `main.go` that loads config, ensures the PocketBase collection on startup, and serves on `:${PORT}`.
- [ ] Add focused tests for config defaults, signup validation, and handler behavior.
- [ ] Run `go test ./...`.
- [ ] Commit as: `Add Go signup starter app`.

### Task 3: Add Local PocketBase Helpers

- [ ] Add `scripts/fetch-pocketbase` to download pinned PocketBase `v0.38.1` for the current OS/arch.
- [ ] Fetch `checksums.txt` from the same release and verify the downloaded zip before extracting.
- [ ] Install the binary to the ignored `./pocketbase` path.
- [ ] Add `scripts/dev-pocketbase` to run `./pocketbase serve --http=127.0.0.1:8090 --dir=./pb_data`.
- [ ] Add `scripts/dev-app` to run the app with local defaults and clear instructions when PocketBase credentials are missing.
- [ ] Run `scripts/fetch-pocketbase`.
- [ ] Run `scripts/dev-pocketbase`.
- [ ] Run `scripts/dev-app`.
- [ ] Commit as: `Add local PocketBase development helpers`.

### Task 4: Add OCI Publishing

- [ ] Add a multi-stage `Dockerfile` based on the current signup example pattern: Go builder stage, static binary, `scratch` runtime, CA certificates copied in, `PORT=8080`, `EXPOSE 8080`, non-root user, and app entrypoint.
- [ ] Add `.github/workflows/publish-image.yml` that builds `linux/amd64` and `linux/arm64` images with Docker Buildx.
- [ ] Tag images as `ghcr.io/${{ github.repository }}:latest` and `ghcr.io/${{ github.repository }}:${{ github.sha }}`.
- [ ] Add OCI source labels that resolve to the template repo.
- [ ] Run `podman build --platform linux/amd64 -t ovek-template-go-pocketbase:local .`.
- [ ] Commit as: `Add GHCR image publishing workflow`.

### Task 5: Document Clone-To-Ovek Flow

- [ ] Write the README around three paths: local app development, image publishing, and Ovek runtime.
- [ ] Include command-by-command local setup:
  - `scripts/fetch-pocketbase`
  - `scripts/dev-pocketbase`
  - `PB_SUPERUSER_EMAIL=<email> PB_SUPERUSER_PASSWORD=<password> scripts/dev-app`
- [ ] Include command-by-command image checks:
  - `podman build --platform linux/amd64 -t ovek-template-go-pocketbase:local .`
  - `podman run --rm -p 8080:8080 -e PORT=8080 -e POCKETBASE_URL=http://host.containers.internal:8090 -e PB_SUPERUSER_EMAIL=<email> -e PB_SUPERUSER_PASSWORD=<password> ovek-template-go-pocketbase:local`
- [ ] Include command-by-command Ovek deployment:
  - `ovek db init template-demo --app-secrets`
  - `ovek run template-demo ghcr.io/<owner>/ovek-template-go-pocketbase:<tag>`
  - `ovek status template-demo`
  - `ovek logs template-demo --no-follow`
  - `ovek db tunnel template-demo --listen 127.0.0.1:8091`
- [ ] Explain that private GHCR packages require `ovek registry login ghcr.io --username <user> --password-stdin`.
- [ ] Commit as: `Document Ovek template workflow`.

### Task 6: Link From Ovek Docs

- [ ] In the main Ovek repo, add a short templates section to `README.md` or `docs/capsule-runs.md` after the first template repo exists.
- [ ] Link to `https://github.com/massivemoose/ovek-template-go-pocketbase`.
- [ ] Mention that templates are regular OCI app repos and Ovek only needs the published image ref.
- [ ] Fix any stale example references from `ovek pb` to `ovek db`.
- [ ] Run a doc drift search:

```sh
rg -n "ovek pb|pb init|pb status|pb tunnel" README.md docs
```

- [ ] Commit as: `Document Ovek app templates`.

## Template Roadmap

After the Go + PocketBase template proves the pattern, add templates in this order:

1. **Go Minimal HTTP Service**
   No PocketBase. Just `PORT`, `/healthz`, `/`, Dockerfile, GHCR workflow, and `ovek run` docs.

2. **Python FastAPI + PocketBase**
   Same Ovek contract as the Go template, with `app/main.py`, `config.py`, `pocketbase.py`, `pyproject.toml`, Dockerfile, GHCR workflow, and local PocketBase helpers.

3. **React + Tiny API Server**
   Prefer Vite React plus Hono or Express in one container. The server listens on `PORT`, serves built frontend assets, and owns `/api/*` PocketBase calls. Avoid a pure static React template for privileged PocketBase usage because browser code must not receive `PB_SUPERUSER_*` secrets.

4. **Next.js App**
   Useful for people wanting a familiar modern full-stack web app in one container. Use standalone/container-friendly output and the same Ovek env contract.

5. **Webhook Receiver Example**
   GitHub/Stripe/Linear-style webhook endpoint, signature validation, event storage in PocketBase, and a small recent-events UI.

6. **Admin Dashboard / CRUD Back Office**
   A private operational app over PocketBase records, useful for tiny VPS-hosted internal tools.

## Future Async Templates

Once Ovek has first-class async/scheduled workflows, add examples that show app + managed data + background work:

- Scheduled digest app: web UI plus daily email/report job.
- Webhook-to-worker pipeline: accept events immediately, process later, expose status.
- Data sync worker: pull from GitHub, Stripe, RSS, or another API into PocketBase.
- AI document summarizer: submit URL/file, process in background, poll result.
- Personal automation bot: scheduled checks plus notification delivery.

## Test Plan

For the first template:

1. `go test ./...`
2. `scripts/fetch-pocketbase`
3. `scripts/dev-pocketbase`
4. `PB_SUPERUSER_EMAIL=<email> PB_SUPERUSER_PASSWORD=<password> go run ./...`
5. `podman build --platform linux/amd64 -t ovek-template-go-pocketbase:local .`
6. `podman run --rm -p 8080:8080 -e PORT=8080 -e POCKETBASE_URL=http://host.containers.internal:8090 -e PB_SUPERUSER_EMAIL=<email> -e PB_SUPERUSER_PASSWORD=<password> ovek-template-go-pocketbase:local`
7. `ovek db init template-demo --app-secrets`
8. `ovek run template-demo ghcr.io/<owner>/ovek-template-go-pocketbase:<tag>`
9. `ovek status template-demo`
10. `ovek logs template-demo --no-follow`
11. `ovek db tunnel template-demo --listen 127.0.0.1:8091`

Success: the local app runs against local PocketBase, the image builds for `linux/amd64`, Ovek pulls and runs the published image, and submitted signup records appear through the PocketBase tunnel.

## Assumptions

- First batch is intentionally one template: Go + PocketBase + tiny signup.
- GitHub template repos are the canonical distribution strategy.
- The Ovek site should link to template repos/downloads later, not own separate archives initially.
- No Ovek runtime/API changes are required for the first template.
- The next implementation agent may build the actual template repo in a separate directory outside this checkout.
