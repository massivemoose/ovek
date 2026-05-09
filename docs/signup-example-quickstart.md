# Signup Example Quickstart

This quickstart runs the canonical PocketBase-backed signup app as an Ovek capsule. The app image is built outside the Ovek runtime host, published to GHCR, and activated with `ovek run`.

For a real Ubuntu VPS trial, use [vps-trial.md](vps-trial.md). This quickstart is the local development path for macOS with Podman Machine or a local Linux host.

Example app:

```text
https://github.com/massivemoose/ovek-signup-example
```

Published capsule:

```text
ghcr.io/massivemoose/ovek-signup-example:latest
```

The app writes email signups to the per-project PocketBase sidecar that Ovek manages.

## Build Ovek

1. `git clone https://github.com/massivemoose/ovek`
2. `cd ovek`
3. `mkdir -p ./bin`
4. `go build -o ./bin/ovek ./cmd/ovek`

Expected: `./bin/ovek` exists.

## Start Ovek With Podman

On macOS with Podman Machine:

1. `make podman-machine-init`
2. `make podman-machine-rootful`
3. `make podman-vm-bootstrap-compose`
4. `make podman-vm-up`

On a local Linux host with Podman:

1. `sudo systemctl start podman.socket`
2. `make podman-linux-up`

Expected: Brain and Traefik are running, and Brain can reach the rootful Podman socket.

## Configure Local Hostnames

For same-machine browser testing:

1. `echo '127.0.0.1 brain.localhost signup-demo.localhost' | sudo tee -a /etc/hosts`

## Authenticate The CLI

From the machine running Ovek:

1. `./bin/ovek auth login --profile local --host http://brain.localhost --api-key dev-brain-key`
2. `./bin/ovek auth status`

Expected: the active profile points at `http://brain.localhost`.

## Initialize Database

1. `./bin/ovek db init signup-demo --app-secrets`
2. `./bin/ovek db status signup-demo`

Expected: PocketBase is running, initialized is `yes`, and app secrets are configured. Ovek stores the generated PocketBase app credentials as project env/secrets for the next capsule run.

## Run The Capsule

1. `./bin/ovek run signup-demo ghcr.io/massivemoose/ovek-signup-example:latest`

Expected: the command streams run logs and exits successfully after the capsule image is pulled, the app and PocketBase sidecar are connected, readiness passes, and the deployment is promoted.

The first run can take a little while while Podman pulls the public app image and the PocketBase image. During that time, `ovek status signup-demo` should show the active run phase.

## Open The App

For same-machine browser testing:

1. `open http://signup-demo.localhost/`

Expected: the signup form loads. Submit an email address and confirm the app redirects to `/success`.

## Inspect Database

Ovek currently generates and stores the app-facing PocketBase credentials without a reveal command. For dashboard inspection during local trials, create a separate dashboard superuser.

On macOS with the Podman Machine helper:

1. `./pm podman exec ovek-signup-demo-pb pocketbase --dir=/pb_data superuser upsert local-admin@signup-demo.ovek.local local-dev-password-please-change`

On Linux:

1. `sudo podman exec ovek-signup-demo-pb pocketbase --dir=/pb_data superuser upsert local-admin@signup-demo.ovek.local local-dev-password-please-change`

Start a local PocketBase tunnel:

1. `./bin/ovek db tunnel signup-demo --listen 127.0.0.1:8091`

Open the PocketBase dashboard:

1. `open http://127.0.0.1:8091/_/`

Log in with:

```text
local-admin@signup-demo.ovek.local
local-dev-password-please-change
```

Expected: the `signups` collection exists after the app starts, and submitted emails appear as records.

## Useful Commands

1. `./bin/ovek status signup-demo`
2. `./bin/ovek db status signup-demo`
3. `./bin/ovek logs signup-demo`
4. `./bin/ovek logs signup-demo --no-follow`
5. `./bin/ovek stop signup-demo`
6. `./bin/ovek start signup-demo`
7. `./bin/ovek restart signup-demo`
8. `./bin/ovek rm signup-demo`
9. `./pm ps -a`
10. `./pm compose logs brain`
11. `./pm logs ovek-signup-demo-pb`
12. `./pm app signup-demo /`

If `ovek db tunnel` reports that port `8090` is already in use:

1. `./bin/ovek db tunnel signup-demo --listen 127.0.0.1:8091`

If a run appears stuck, rerun:

1. `./bin/ovek status signup-demo`

Success condition: the recent job has a phase, such as `preparing image` or `waiting for readiness`, or it eventually moves to `succeeded` or `failed`.
