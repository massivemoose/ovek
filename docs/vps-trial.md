# Ubuntu VPS Trial

This is the canonical real-server trial for the public MVP. It installs the runtime-only Ovek stack on a fresh Ubuntu VPS, connects to Brain through an SSH tunnel, and runs the public signup capsule:

```text
ghcr.io/massivemoose/ovek-signup-example:latest
```

The VPS runs Brain, Traefik, app containers, and managed PocketBase sidecars through Podman. App builds happen elsewhere; this flow pulls the published Brain runtime image during install, then pulls and runs published OCI capsule images.

## 1. Prepare The VPS

On a fresh Ubuntu VPS:

1. `git clone https://github.com/massivemoose/ovek`
2. `cd ovek`
3. `./scripts/ovek-vps-install.sh`
4. `./scripts/ovek-vps-check.sh`

Expected: `ovek.service` is enabled and running, the runtime-only stack pulls and starts Brain and Traefik, and the read-only preflight check passes through Brain's public health endpoint. The default VPS stack does not build Brain locally or start server-side builder services.

By default, the installer pulls `ghcr.io/massivemoose/ovek-brain:<current-git-sha>`. To test a different published Brain image, set `OVEK_BRAIN_IMAGE` when running the installer.

On success, the installer prints the next preflight, SSH tunnel, and laptop auth bootstrap commands. Re-running the installer preserves existing `/etc/ovek/ovek.env` secrets and existing runtime data under `/var/lib/ovek`.

If the preflight reports that sudo cannot run non-interactively:

1. `sudo -v`
2. `./scripts/ovek-vps-check.sh`

For other preflight failures, follow the single `suggest:` command printed with the failure, then rerun:

1. `./scripts/ovek-vps-check.sh`

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

Expected: `http://brain.localhost:8088` reaches the VPS through the tunnel.

## 4. Bootstrap CLI Auth

From your laptop checkout, with the SSH tunnel open:

1. `./bin/ovek auth bootstrap --profile vps-trial --host http://brain.localhost:8088`
2. `./bin/ovek auth status`

The bootstrap command prompts for a username and password. Save the password somewhere safe for this trial; protected mutations use it for reauth.

Expected: the active profile points at `http://brain.localhost:8088`.

## 5. Initialize The Managed Database

1. `./bin/ovek db init signup-demo --app-secrets`
2. `./bin/ovek db status signup-demo`

The init command may prompt for your bootstrap password.

Expected: the managed PocketBase sidecar is running, initialized is `yes`, and app secrets are configured for the next capsule run.

## 6. Run The Signup Capsule

1. `./bin/ovek run signup-demo ghcr.io/massivemoose/ovek-signup-example:latest`

The run command may prompt for your bootstrap password.

Expected: the run succeeds after Podman pulls the public app image, starts the app and PocketBase sidecar, waits for readiness, and promotes the deployment.

Check status and logs:

1. `./bin/ovek status signup-demo`
2. `./bin/ovek logs signup-demo --no-follow`

Expected: status is `running`, the current runtime references the signup capsule image, and runtime logs return app output.

## 7. Open The App

From your laptop browser:

1. `open http://signup-demo.localhost:8088/`

Expected: the signup form loads. Submit an email address and confirm the app redirects to `/success`.

## 8. Validate Lifecycle Commands

Stop the app runtime without deleting the managed database:

1. `./bin/ovek stop signup-demo`
2. `./bin/ovek status signup-demo`

Expected: the app runtime is stopped and project status reports `stopped`.

Start it again:

1. `./bin/ovek start signup-demo`
2. `./bin/ovek status signup-demo`

Expected: the app runtime is running again.

Restart it:

1. `./bin/ovek restart signup-demo`
2. `./bin/ovek status signup-demo`

Expected: the app runtime remains healthy and status returns to `running`.

## 9. Inspect PocketBase

Ovek currently generates and stores app-facing PocketBase credentials without a reveal command. For dashboard inspection during this trial, create a separate dashboard-only PocketBase superuser on the VPS:

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

## 10. Clean Up The App Runtime

Remove only the app runtime and route:

1. `./bin/ovek rm signup-demo`
2. `./bin/ovek status signup-demo`
3. `./bin/ovek db status signup-demo`

Expected: the app runtime is removed, the project returns to `idle`, and the managed database is still present.

Remove the managed database when you are finished with the trial:

1. `./bin/ovek rm signup-demo --remove-database`
2. `./bin/ovek status signup-demo`

The removal command prompts you to type the project name.

Expected: app runtime, route, and managed database resources are cleaned up.

## Troubleshooting On The VPS

Use these commands on the VPS only when the public CLI flow does not behave as expected:

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
3. `./scripts/ovek-vps-check.sh`
