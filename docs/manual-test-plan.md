# Manual Test Plan

This plan validates the public Ovek MVP surface with public CLI commands. It uses the canonical signup capsule:

```text
ghcr.io/massivemoose/ovek-signup-example:latest
```

The setup commands can start local or VPS infrastructure, but the product validation path uses `ovek auth`, `ovek registry`, `ovek db`, `ovek run`, `ovek status`, `ovek logs`, lifecycle commands, and `ovek rm`.

## 1. Build The CLI

From the Ovek checkout:

1. `mkdir -p ./bin`
2. `go build -o ./bin/ovek ./cmd/ovek`

Expected: `./bin/ovek` exists.

## 2. Start A Local Runtime

On macOS with Podman Machine:

1. `make podman-machine-init`
2. `make podman-machine-rootful`
3. `make podman-vm-bootstrap-compose`
4. `make podman-vm-up`

On Linux with Podman:

1. `sudo systemctl start podman.socket`
2. `make podman-linux-up`

Expected: Brain and Traefik are running, and Brain can reach the rootful Podman socket.

## 3. Configure Hostnames

If your resolver does not already map `.localhost` names to loopback:

1. `echo '127.0.0.1 brain.localhost signup-demo.localhost' | sudo tee -a /etc/hosts`

Expected: `brain.localhost` and `signup-demo.localhost` resolve to `127.0.0.1`.

## 4. Authenticate

For the local development stack:

1. `./bin/ovek auth login --profile local-manual --host http://brain.localhost --api-key dev-brain-key`
2. `./bin/ovek auth status`

For a VPS trial over the documented SSH tunnel, use [vps-trial.md](vps-trial.md) and replace the host with `http://brain.localhost:8088`.

Expected: the active profile points at the Brain host you are testing.

For production auth mode, also validate owner key lifecycle:

1. `./bin/ovek auth key create --label manual-test`
2. `./bin/ovek auth keys`
3. `./bin/ovek auth key rm <KEY_ID>`

Expected: the created key is printed once, list output shows metadata only, and revocation succeeds after reauth.

## 5. Initialize The Managed Database

1. `./bin/ovek db init signup-demo --app-secrets`
2. `./bin/ovek db status signup-demo`

Expected: running is `yes`, initialized is `yes`, and app secrets are `yes`.

## 6. Run The Public Capsule

1. `./bin/ovek run signup-demo ghcr.io/massivemoose/ovek-signup-example:latest`

Expected: the command streams lifecycle logs and finishes with status `succeeded`.

Optional private-image check:

1. `printf '<registry-token>' | ./bin/ovek registry login ghcr.io --username <user> --password-stdin`
2. `./bin/ovek registry list`
3. `./bin/ovek run private-demo ghcr.io/<owner>/<image>:<tag>`

Expected: registry list output is redacted, and the private image pull succeeds through Podman.

## 7. Inspect Status And Logs

1. `./bin/ovek status signup-demo`
2. `./bin/ovek logs signup-demo --no-follow`

Expected: project status is `running`, the current runtime references the signup capsule image, and runtime logs return app output.

If you need job logs, copy the job ID from `ovek run` or `ovek status`:

1. `./bin/ovek logs --job <JOB_ID> --no-follow`

Expected: job logs include image-run lifecycle lines for pulling the prebuilt image, provisioning PocketBase, starting the app container, waiting for readiness, and promotion.

## 8. Open The App

For local testing:

1. `open http://signup-demo.localhost/`

For the VPS SSH tunnel:

1. `open http://signup-demo.localhost:8088/`

Expected: the signup form loads. Submit an email address and confirm the app redirects to `/success`.

## 9. Validate Lifecycle Commands

1. `./bin/ovek stop signup-demo`
2. `./bin/ovek status signup-demo`

Expected: project status is `stopped`.

1. `./bin/ovek start signup-demo`
2. `./bin/ovek status signup-demo`

Expected: project status is `running`.

1. `./bin/ovek restart signup-demo`
2. `./bin/ovek status signup-demo`

Expected: project status is `running`, and the app remains reachable.

## 10. Validate Database Tunnel

1. `./bin/ovek db tunnel signup-demo --listen 127.0.0.1:8091`

Keep the tunnel terminal open, then open:

1. `open http://127.0.0.1:8091/_/`

Expected: the PocketBase dashboard route is reachable. Dashboard login currently requires a separate superuser created through host-level troubleshooting commands, as shown in [vps-trial.md](vps-trial.md).

## 11. Clean Up

Remove the app runtime while preserving the managed database:

1. `./bin/ovek rm signup-demo`
2. `./bin/ovek status signup-demo`
3. `./bin/ovek db status signup-demo`

Expected: project status returns to `idle`, and the database still exists.

Remove the managed database when the trial is finished:

1. `./bin/ovek rm signup-demo --remove-database`
2. `./bin/ovek status signup-demo`

The removal command prompts you to type the exact project name.

Expected: app runtime, route, and managed database resources are cleaned up.

## Automated Acceptance Shortcuts

Use these when you need the scripted version of the same public capsule path.

On macOS with Podman Machine:

1. `make podman-vm-capsule-smoke`

On Linux with Podman:

1. `make podman-linux-capsule-smoke`

Expected: the smoke suite runs the public signup capsule, validates status/logs/routing/database state, and cleans up.
