# MVP Release Checklist

Use this checklist before asking public MVP users for feedback on Ovek's capsule-first VPS runtime.

## Public Capsule

- [ ] Confirm the canonical signup capsule is public in GHCR:
  `ghcr.io/massivemoose/ovek-signup-example:latest`
- [ ] Pull the image anonymously from a clean host:
  `podman pull ghcr.io/massivemoose/ovek-signup-example:latest`
- [ ] Confirm the image supports the VPS architecture being tested. The current public trial should prefer linux/amd64 until multi-arch publishing is verified.

## Automated Acceptance

- [ ] Run Linux capsule acceptance:
  `make podman-linux-up`
- [ ] Run Linux capsule smoke:
  `make podman-linux-capsule-smoke`
- [ ] Clean up the Linux runtime stack:
  `make podman-linux-down`
- [ ] Confirm CI uses the capsule smoke path as the MVP acceptance signal.

## Brain Runtime Image

- [ ] Confirm the Brain image workflow published the current commit:
  `ghcr.io/massivemoose/ovek-brain:<full-git-sha>`
- [ ] Pull the Brain image anonymously from a clean host:
  `podman pull ghcr.io/massivemoose/ovek-brain:<full-git-sha>`
- [ ] Confirm the GHCR Brain package is public before asking friends to run the VPS installer.

## Fresh VPS Trial

- [ ] Start from a fresh Ubuntu VPS.
- [ ] On hosts under 768 MiB RAM, configure a 1 GiB swapfile before capsule trials.
- [ ] Install Ovek:
  `./scripts/ovek-vps-install.sh`
- [ ] Confirm the installer pulls the pinned Brain image and Traefik instead of building Brain locally.
- [ ] Confirm the installer prints copy/pasteable preflight, SSH tunnel, and auth bootstrap next commands.
- [ ] Run the read-only preflight and follow any printed `suggest:` command before retrying:
  `./scripts/ovek-vps-check.sh`
- [ ] Confirm preflight does not warn about a tiny no-swap host, or document that the test intentionally uses a larger VPS.
- [ ] Confirm preflight reaches Brain through the public health endpoint before CLI auth bootstrap.
- [ ] Complete [vps-trial.md](vps-trial.md) over an SSH tunnel.
- [ ] Confirm `ovek run signup-demo ghcr.io/massivemoose/ovek-signup-example:latest` succeeds.
- [ ] Confirm the signup form is reachable through Traefik and writes records to the managed PocketBase sidecar.
- [ ] Reboot the VPS and confirm Brain, Traefik, the app container, and the PocketBase sidecar come back.
- [ ] Confirm `./scripts/ovek-vps-check.sh` reports `podman-restart.service` enabled after reboot.
- [ ] Confirm `ovek auth key create --label <label>`, `ovek auth keys`, and `ovek auth key rm <key-id>` work over the SSH tunnel.
- [ ] Confirm `ovek auth password` changes the owner password and the new password works for subsequent reauth prompts.

## Private Capsule Pulls

- [ ] Publish a private capsule image for the VPS architecture, preferably linux/amd64 for the current trial.
- [ ] Configure a pull-scoped registry token:
  `printf '<registry-token>' | ovek registry login ghcr.io --username <user> --password-stdin`
- [ ] Confirm `ovek registry list` shows only host, username, and timestamps.
- [ ] Run the private image:
  `ovek run private-demo ghcr.io/<owner>/<image>:<tag>`
- [ ] Remove the credential when the trial is done:
  `ovek registry rm ghcr.io`

## Lifecycle Cleanup

- [ ] Stop, start, and restart the app runtime:
  `ovek stop signup-demo`
- [ ] Confirm stopped status:
  `ovek status signup-demo`
- [ ] Restart the runtime:
  `ovek start signup-demo`
- [ ] Restart in place:
  `ovek restart signup-demo`
- [ ] Remove only the app runtime:
  `ovek rm signup-demo`
- [ ] Confirm the database remains:
  `ovek db status signup-demo`
- [ ] Remove the managed database explicitly:
  `ovek rm signup-demo --remove-database`
- [ ] Confirm repeated cleanup reports no app runtime to remove instead of a false success.

## Public CLI Surface

- [ ] `ovek --help` lists `auth`, `db`, `logs`, `registry`, `run`, `status`, lifecycle commands, `env`, and `secret`.
- [ ] `ovek --help` does not list legacy deployment commands.
- [ ] `ovek run --help`, `ovek db --help`, and lifecycle command help are command-specific.
- [ ] Public docs use `ovek db init/status/tunnel`, not removed PocketBase command names.
- [ ] Public docs use `ovek run <project> <capsule-ref>` for runtime activation.
- [ ] Public docs use `ovek registry login/list/rm` for private image pulls.
- [ ] Public docs use `ovek auth keys`, `ovek auth key create`, `ovek auth key rm`, and `ovek auth password` for owner auth lifecycle.

## Known Limitations

- TLS and real domain automation are not part of this MVP trial; the documented VPS path uses an SSH tunnel and `.localhost` hostnames.
- Automated backups and restore flows are not implemented yet.
- Private source repos are still handled outside Ovek; users build and push OCI images locally or in CI, then give Ovek image pull access.
- The legacy source-build path remains an internal regression surface, not public MVP guidance.
- The current PocketBase image is still `docker.io/elestio/pocketbase:latest`; an Ovek-owned pinned image remains follow-up work.
