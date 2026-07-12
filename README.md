# Ovek

Ovek is a lightweight app capsule runtime for your VPS. Build an OCI image outside the server, publish it to a registry, then run it on your own machine with:

```text
ovek run <project> <capsule-ref>
```

Brain is the small control plane that runs on the VPS. It pulls capsule images through Podman, starts app containers, creates per-project PocketBase sidecars, manages routing through Traefik, injects project env/secrets, waits for readiness, streams logs, and cleans up managed runtime resources.

## Capsule Runtime Flow

1. Build and publish an OCI image from your laptop, CI, or a hosted builder.
2. Initialize any project sidecars or secrets, such as `ovek db init <project> --app-secrets`.
3. Run the capsule with `ovek run <project> <capsule-ref>`.
4. Ovek pulls the image, starts the app plus sidecars, waits for readiness, and routes `<project>.localhost` to the app.
5. Use `ovek status`, `ovek logs`, and `ovek db status` to inspect the running project.
6. Use `ovek stop`, `ovek start`, `ovek restart`, and `ovek rm` to manage the app runtime without deleting database data by default.

Capsule v1 is intentionally simple:

- listen on the injected `PORT`, currently `8080`
- use `POCKETBASE_URL` when the app needs the managed PocketBase sidecar
- read project env/secrets from normal environment variables
- become ready by accepting TCP connections on `PORT`

## Quickstart

For a real Ubuntu VPS trial over an SSH tunnel, use [docs/vps-trial.md](docs/vps-trial.md). The commands below are the local development path for macOS with Podman Machine or Linux.

Build the CLI:

1. `mkdir -p ./bin`
2. `go build -o ./bin/ovek ./cmd/ovek`

Start the Linux-first Podman stack on macOS with Podman Machine:

1. `make podman-machine-init`
2. `make podman-machine-rootful`
3. `make podman-vm-bootstrap-compose`
4. `make podman-vm-up`

Or start it on a local Linux machine:

1. `sudo systemctl start podman.socket`
2. `make podman-linux-up`

Authenticate the CLI against the local Brain route:

1. `./bin/ovek auth login --profile local --host http://brain.localhost --api-key dev-brain-key`
2. `./bin/ovek auth status`

Run the canonical signup capsule:

1. `./bin/ovek db init signup-demo --app-secrets`
2. `./bin/ovek run signup-demo ghcr.io/massivemoose/ovek-signup-example:latest`
3. `./bin/ovek status signup-demo`
4. `./bin/ovek logs signup-demo --no-follow`

Open the app:

```text
http://signup-demo.localhost/
```

See [docs/signup-example-quickstart.md](docs/signup-example-quickstart.md) for the full app capsule walkthrough.

For private capsule images, configure a registry pull credential first:

1. `printf '<registry-token>' | ./bin/ovek registry login ghcr.io --username <user> --password-stdin`
2. `./bin/ovek run <project> ghcr.io/<owner>/<image-name>:<tag>`

## Async Workflows

Register scheduled or manual OCI workflow jobs for a project:

1. `./bin/ovek workflow set <project> <name> --image <capsule-ref> --schedule '@hourly'`
2. `./bin/ovek workflow run <project> <name>`
3. `./bin/ovek workflow status <project> [<name>]`
4. `./bin/ovek workflow logs <project> <run-id> --no-follow`
5. `./bin/ovek workflow token create <project> <name> --label app`

Workflow containers get the same project env/secrets and managed PocketBase access as app capsules, plus `OVEK_PROJECT`, `OVEK_WORKFLOW`, `OVEK_WORKFLOW_RUN_ID`, and `OVEK_WORKFLOW_PAYLOAD_FILE`. App capsules can enqueue scoped workflow runs through trigger tokens without holding full Brain API keys. See [docs/async-workflows.md](docs/async-workflows.md) and [docs/workflow-triggers.md](docs/workflow-triggers.md).

## VPS Trial

The first real-server path is Ubuntu + Podman + SSH tunnel:

1. install Ovek on the VPS with `./scripts/ovek-vps-install.sh`
2. forward laptop port `8088` to VPS port `80`
3. bootstrap CLI auth with `ovek auth bootstrap`
4. run the public signup capsule with `ovek run`

See [docs/vps-trial.md](docs/vps-trial.md) for the command-by-command flow.

## Podman Development

The local development stack is Podman-first and Linux-first. On macOS, the helper targets run the same Linux stack inside `podman machine`.

Useful entrypoints:

- `make podman-vm-up`
- `make podman-vm-capsule-smoke`
- `make podman-vm-down`
- `make podman-linux-up`
- `make podman-linux-capsule-smoke`
- `make podman-linux-down`
- `make podman-linux-builder-up`
- `make podman-linux-builder-smoke`
- `./pm up`
- `./pm capsule-smoke`
- `./pm down`

The primary acceptance path is `scripts/podman-capsule-smoke.sh`. It runs the public signup capsule through `ovek run`, checks job logs, runtime logs, routed app reachability, database sidecar status, and cleanup.

Legacy source-build smoke runs only through the explicit builder stack.

See [docs/podman-testing.md](docs/podman-testing.md) for the full Mac VM and Linux validation flows.

## More Docs

- [Auth](docs/auth.md): bootstrap vs login, profiles, API keys, reauth, registry credentials, and tunnel/TLS guidance.
- [Capsule runs](docs/capsule-runs.md): capsule expectations, GHCR image publishing, and validation commands.
- [Async workflows](docs/async-workflows.md): scheduled/manual OCI workflows, queue behavior, env injection, logs, and validation.
- [Workflow triggers](docs/workflow-triggers.md): app/API-triggered workflows, trigger tokens, payloads, idempotency, and language snippets.
- [Project env and secrets](docs/project-env-secrets.md): project configuration captured by the next capsule run.
- [Async workflows design](docs/async-workflows-design.md): scheduled and triggered OCI jobs as the next project capability.
- [Podman testing](docs/podman-testing.md): local VM and Linux acceptance workflows.
- [Ubuntu VPS trial](docs/vps-trial.md): first real-server flow over an SSH tunnel.
- [MVP release checklist](docs/mvp-release-checklist.md): final public-feedback readiness checks.

## Current MVP Shape

Ovek's public path is app capsules on Podman. The MVP is intentionally focused on prebuilt OCI images plus a tiny VPS runtime.
