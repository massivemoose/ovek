# Signup Example Quickstart

This is the image-first trial path for running a small Go app capsule with Ovek. The app image should be built outside the Ovek runtime host, published to GHCR, and run with `ovek run`.

The example app writes email signups to the per-project PocketBase sidecar that Ovek manages.

Example repo:

```text
https://github.com/massivemoose/ovek-signup-example
```

Default public capsule image:

```text
ghcr.io/massivemoose/ovek-signup-example:latest
```

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

On Linux or a VPS with Podman:

1. `sudo systemctl start podman.socket`
2. `make podman-linux-up`

Expected: Brain, Traefik, BuildKit, and the local registry are running. The image-first path does not use BuildKit for the app run, but the local stack still includes it for the transitional source-build path.

## Configure Local Browser Hostnames

For same-machine browser testing:

1. `echo '127.0.0.1 brain.localhost signup-demo.localhost' | sudo tee -a /etc/hosts`

For VPS browser testing from your laptop, forward the VPS port 80 first:

1. `ssh -N -L 8088:127.0.0.1:80 <user>@<vps-host>`
2. `echo '127.0.0.1 brain.localhost signup-demo.localhost' | sudo tee -a /etc/hosts`

With the SSH tunnel open, use `http://signup-demo.localhost:8088/` from your laptop browser.

## Authenticate The CLI

From the machine running Ovek:

1. `./bin/ovek auth login --profile local --host http://brain.localhost --api-key dev-brain-key`
2. `./bin/ovek auth status`

Expected: the active profile points at `http://brain.localhost`.

If you are driving a VPS through the SSH tunnel from your laptop instead, use:

1. `./bin/ovek auth login --profile vps-trial --host http://brain.localhost:8088 --api-key dev-brain-key`
2. `./bin/ovek auth status`

## Initialize PocketBase

1. `./bin/ovek pb init signup-demo --app-secrets`
2. `./bin/ovek pb status signup-demo`

Expected: PocketBase is running, initialized is `yes`, and app secrets are configured. Ovek stores the generated PocketBase app credentials as project env/secrets for the next deploy.

## Run The Published Capsule

1. `./bin/ovek run signup-demo ghcr.io/massivemoose/ovek-signup-example:latest`

Expected: the command streams run logs and exits successfully after the capsule image is pulled, promoted, and ready.

The first run can take a while while Podman pulls the public image and the PocketBase image. During that time, `ovek status signup-demo` should show the active run phase.

## Transitional Source Build

If you intentionally want Ovek to build the app on the runtime host, use the transitional source-build path:

1. `./bin/ovek deploy signup-demo https://github.com/massivemoose/ovek-signup-example`

Expected: Brain clones the repo, builds it with Railpack/BuildKit, pushes the result to the local registry, then runs the built image through the same runtime path.

## Open The App

For same-machine browser testing:

1. `open http://signup-demo.localhost/`

For VPS browser testing through the SSH tunnel:

1. `open http://signup-demo.localhost:8088/`

Expected: the signup form loads. Submit an email address and confirm the app redirects to `/success`.

## Inspect PocketBase

Ovek currently generates and stores the app-facing PocketBase credentials without a reveal command. For dashboard inspection during local trials, create a separate dashboard superuser.

On macOS with the Podman Machine helper:

1. `./pm podman exec ovek-signup-demo-pb pocketbase --dir=/pb_data superuser upsert local-admin@signup-demo.ovek.local local-dev-password-please-change`

On Linux or a VPS:

1. `sudo podman exec ovek-signup-demo-pb pocketbase --dir=/pb_data superuser upsert local-admin@signup-demo.ovek.local local-dev-password-please-change`

Start a local PocketBase tunnel:

1. `./bin/ovek pb tunnel signup-demo --listen 127.0.0.1:8091`

If the tunnel is running on a VPS and you want to inspect from your laptop:

1. `ssh -N -L 8091:127.0.0.1:8091 <user>@<vps-host>`

Open the PocketBase dashboard:

1. `open http://127.0.0.1:8091/_/`

Log in with:

```text
local-admin@signup-demo.ovek.local
local-dev-password-please-change
```

Expected: the `signups` collection exists after the app starts, and submitted emails appear as records.

## Useful Debugging Commands

Check project status:

1. `./bin/ovek status signup-demo`

Check PocketBase status:

1. `./bin/ovek pb status signup-demo`

Follow runtime logs:

1. `./bin/ovek logs signup-demo`

Show runtime logs without following:

1. `./bin/ovek logs signup-demo --no-follow`

Inspect the Podman VM stack from macOS:

1. `./pm ps -a`
2. `./pm compose logs brain`
3. `./pm logs ovek-signup-demo-pb`
4. `./pm app signup-demo /`

Inspect the Linux/VPS stack:

1. `sudo podman compose -f podman-compose.yml ps`
2. `sudo podman logs brain`
3. `sudo podman logs ovek-signup-demo-pb`

If `ovek pb tunnel` reports that port `8090` is already in use:

1. `./bin/ovek pb tunnel signup-demo --listen 127.0.0.1:8091`

If a run or deploy appears stuck, rerun:

1. `./bin/ovek status signup-demo`

Success condition: the recent job has a phase, such as `preparing image`, `building image`, or `waiting for readiness`, or it eventually moves to `succeeded` or `failed`.
