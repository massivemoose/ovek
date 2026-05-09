# TLS And Domain Automation Design

This note scopes the next stack after launch hardening. It does not implement TLS automation yet.

## Goal

Move the public VPS flow beyond SSH tunnels and `.localhost` hostnames while preserving Ovek's simple project routing model.

The target user experience should be:

1. point a domain or wildcard DNS record at the VPS
2. configure Ovek's public domain once
3. run a capsule
4. reach the app at a stable HTTPS project hostname

## Proposed Direction

Use Traefik's ACME support as the first TLS automation path. The initial domain model should be host-level and simple:

- one base domain per Ovek install, such as `apps.example.com`
- project hostnames under that base domain, such as `signup-demo.apps.example.com`
- Brain remains reachable at a reserved hostname, such as `brain.apps.example.com`

HTTP-01 should be the first certificate challenge to support because it keeps setup understandable for a single VPS with ports `80` and `443` open. DNS-01 and wildcard certificates can follow later if the project needs wildcard-only DNS or private-network issuance.

## Brain And CLI Shape

The next implementation should add one host-level domain configuration path rather than per-project domain management:

- installer/env config for the base domain and ACME email
- Traefik static config for HTTP-to-HTTPS redirect and ACME storage
- generated dynamic routers that use project hostnames under the configured base domain
- CLI/docs updates that show the public HTTPS URL after `ovek run`

The SSH tunnel trial should remain documented as the safe first dogfood path. TLS/domain automation should become the normal public sharing path after it is validated on a fresh VPS.

## Open Questions For Implementation

- Where should ACME storage live under `/var/lib/ovek`, and what permissions should the installer enforce?
- Should Brain reject domain automation if ports `80` or `443` are already occupied?
- Should the first implementation support custom per-project hostnames, or reserve that for a later domain-management stack?
- Should Ovek expose a read-only `ovek domains status` command, or keep domain state in install/preflight docs for v1?
