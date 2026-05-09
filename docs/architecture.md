# Ovek Architecture

Ovek is a lightweight VPS capsule runtime. The public path is image-first: users build an OCI image outside the VPS, publish it to a registry the VPS can reach, then activate it with:

```text
ovek run <project> <capsule-ref>
```

## Runtime Model

Brain is the control plane that runs on the VPS. It receives CLI/API requests, stores project state, pulls capsule images through Podman, creates app containers, creates managed per-project PocketBase sidecars, writes Traefik routing config, injects env/secrets, waits for readiness, streams logs, and cleans up managed resources.

The VPS runtime is Podman-first. Ovek uses Podman as the supported runtime path for the public MVP. Docker can still be used by users or CI systems to build OCI images before publishing them, but Docker is not a supported Ovek runtime.

App containers are expected to:

- listen on `PORT=8080`
- accept configuration from environment variables
- use `POCKETBASE_URL=http://db:8090` when they need the managed PocketBase sidecar
- become ready by accepting TCP connections on port `8080`

## Control Plane Shape

Brain owns a small set of durable resources:

- projects: app identity and current control-plane status
- deployments: promoted runtime versions for a project
- jobs: async run/build work and associated logs
- runtime: currently managed app, database sidecar, and network state
- project config: environment variables and encrypted secrets captured by config revision
- registry credentials: encrypted host-level credentials used to pull private capsule images
- owner auth: single-admin password auth, API key metadata, reauth tokens, and audit logs

Public CLI commands should favor the capsule-first surface: `auth`, `db`, `env`, `secret`, `registry`, `run`, `status`, `logs`, lifecycle commands, and `rm`.

## Auth Model

The launch auth model is single-owner. Production installs bootstrap one admin user, then use local CLI profiles that hold Brain API keys. API keys can be listed, created with labels, and revoked. New API keys are shown once; list responses never return key secrets.

Critical mutations require reauth with the owner password. That includes capsule runs, managed database initialization, registry credential mutations, API key lifecycle mutations, and password changes. HTTP auth failures stay generic to clients, while server logs and audit events retain operational reasons such as missing, malformed, revoked, or reauth-rejected credentials.

## Managed Data

PocketBase is managed per project for the MVP. `ovek db init <project> --app-secrets` creates or verifies the sidecar, initializes app-facing credentials, and stores those credentials as normal project env/secrets for the next capsule run.

Shared or external databases remain future capabilities. PostgreSQL can be added later as a separate managed database mode.

## Internal Builder Path

The legacy source-build path remains an internal regression surface. It uses Railpack/BuildKit/local-registry plumbing for local dogfood and larger-host experiments, but it is not public MVP guidance and should not shape the tiny-VPS install path.

Build helpers should eventually live in local CLI workflows, CI actions, or a hosted builder that produces OCI capsule refs for `ovek run`. Heavy builds should not run on the tiny VPS by default.
