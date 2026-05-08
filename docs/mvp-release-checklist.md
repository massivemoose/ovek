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

## Fresh VPS Trial

- [ ] Start from a fresh Ubuntu VPS.
- [ ] Install Ovek:
  `./scripts/ovek-vps-install.sh`
- [ ] Confirm the installer prints copy/pasteable preflight, SSH tunnel, and auth bootstrap next commands.
- [ ] Run the read-only preflight and follow any printed `suggest:` command before retrying:
  `./scripts/ovek-vps-check.sh`
- [ ] Complete [vps-trial.md](vps-trial.md) over an SSH tunnel.
- [ ] Confirm `ovek run signup-demo ghcr.io/massivemoose/ovek-signup-example:latest` succeeds.
- [ ] Confirm the signup form is reachable through Traefik and writes records to the managed PocketBase sidecar.

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

- [ ] `ovek --help` lists `auth`, `db`, `logs`, `run`, `status`, lifecycle commands, `env`, and `secret`.
- [ ] `ovek --help` does not list legacy deployment commands.
- [ ] `ovek run --help`, `ovek db --help`, and lifecycle command help are command-specific.
- [ ] Public docs use `ovek db init/status/tunnel`, not removed PocketBase command names.
- [ ] Public docs use `ovek run <project> <capsule-ref>` for runtime activation.

## Known Limitations

- Private capsule pulls and private registry credentials are not first-class yet.
- TLS and real domain automation are not part of this MVP trial; the documented VPS path uses an SSH tunnel and `.localhost` hostnames.
- Automated backups and restore flows are not implemented yet.
- Auth hardening beyond the current bootstrap/API-key/reauth shape remains follow-up work.
- The legacy source-build path and BuildKit-based builder stack remain internal regression surfaces, not public MVP guidance.
- The current PocketBase image is still `docker.io/elestio/pocketbase:latest`; an Ovek-owned pinned image remains follow-up work.
