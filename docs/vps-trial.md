# Ubuntu VPS Trial

This trial installs the runtime-only Ovek stack on an Ubuntu VPS, connects to it through an SSH tunnel, and runs the public signup capsule.

The stack runs Brain and Traefik on the VPS. App builds happen elsewhere; this flow pulls and runs a published capsule image with Podman.

## 1. Prepare The VPS

On the VPS:

1. `git clone https://github.com/massivemoose/ovek`
2. `cd ovek`
3. `./scripts/ovek-vps-install.sh`

Expected: `ovek.service` is enabled and running.

Check the service:

1. `sudo systemctl status ovek.service`
2. `sudo podman ps`

Expected: `brain` and `traefik` are running. The default stack does not start BuildKit or a local registry.

## 2. Build The CLI On Your Laptop

From a local checkout on your laptop:

1. `git clone https://github.com/massivemoose/ovek`
2. `cd ovek`
3. `mkdir -p ./bin`
4. `go build -o ./bin/ovek ./cmd/ovek`

Expected: `./bin/ovek` exists.

## 3. Open The SSH Tunnel

From your laptop:

1. `ssh -N -L 8088:127.0.0.1:80 <user>@<vps-host>`

Keep this terminal open while you use Ovek from your laptop.

If your machine does not already resolve `.localhost` hostnames to loopback, add local host entries:

1. `echo '127.0.0.1 brain.localhost signup-demo.localhost' | sudo tee -a /etc/hosts`

## 4. Bootstrap CLI Auth

From your laptop checkout, with the SSH tunnel open:

1. `./bin/ovek auth bootstrap --profile vps-trial --host http://brain.localhost:8088`
2. `./bin/ovek auth status`

The bootstrap command prompts for a username and password. Save the password somewhere safe for this trial; protected mutations use it for reauth.

Expected: the active profile points at `http://brain.localhost:8088`.

## 5. Initialize PocketBase

1. `./bin/ovek db init signup-demo --app-secrets`
2. `./bin/ovek db status signup-demo`

The init command may prompt for your bootstrap password.

Expected: PocketBase is running, initialized is `yes`, and app secrets are configured.

## 6. Run The Signup Capsule

1. `./bin/ovek run signup-demo ghcr.io/massivemoose/ovek-signup-example:latest`

The run command may prompt for your bootstrap password.

Expected: the run succeeds after Podman pulls the public app image, starts the app and PocketBase sidecar, waits for readiness, and promotes the deployment.

Check status and logs:

1. `./bin/ovek status signup-demo`
2. `./bin/ovek logs signup-demo --no-follow`

## 7. Open The App

From your laptop browser:

1. `open http://signup-demo.localhost:8088/`

Expected: the signup form loads. Submit an email address and confirm the app redirects to `/success`.

## 8. Inspect PocketBase

Create a dashboard-only PocketBase superuser on the VPS:

1. `sudo podman exec ovek-signup-demo-pb pocketbase --dir=/pb_data superuser upsert local-admin@signup-demo.ovek.local local-dev-password-please-change`

Start a PocketBase tunnel from your laptop checkout:

1. `./bin/ovek db tunnel signup-demo --listen 127.0.0.1:8091`

Open the dashboard from your laptop:

1. `open http://127.0.0.1:8091/_/`

Log in with:

```text
local-admin@signup-demo.ovek.local
local-dev-password-please-change
```

Expected: the `signups` collection exists, and submitted emails appear as records.

## 9. Clean Up The Project

From your laptop checkout:

1. `./bin/ovek rm signup-demo --remove-database`
2. `./bin/ovek status signup-demo`

Expected: project runtime is cleaned up and the project returns to `idle`.

## Useful VPS Commands

1. `sudo systemctl status ovek.service`
2. `sudo journalctl -u ovek.service -n 100 --no-pager`
3. `sudo podman ps -a`
4. `sudo podman logs brain`
5. `sudo podman logs traefik`
6. `sudo podman logs ovek-signup-demo-app`
7. `sudo podman logs ovek-signup-demo-pb`

To restart Ovek after pulling new repo changes on the VPS:

1. `cd ovek`
2. `./scripts/ovek-vps-install.sh`
