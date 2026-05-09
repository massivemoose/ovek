# Auth

Ovek's launch auth model is single-owner Brain auth. One admin user owns the Brain install, and the CLI authenticates with Brain API keys stored in local profiles.

## Bootstrap Vs Login

Bootstrap is only for the first admin on a production Brain install:

1. `./bin/ovek auth bootstrap --profile vps-trial --host http://brain.localhost:8088`

The command prompts for a username and password, creates the first admin, receives the first API key, and stores that API key in the named local profile. The API key is not printed.

Login is for adding an existing Brain API key to a local profile:

1. `./bin/ovek auth login --profile vps-trial --host http://brain.localhost:8088 --api-key <api-key>`

Use profiles when you work with more than one Brain install:

1. `./bin/ovek auth profiles`
2. `./bin/ovek auth use <profile>`
3. `./bin/ovek auth status`

## API Keys

Create a labeled API key when you want a separate credential for another laptop, CI job, or short-lived test:

1. `./bin/ovek auth key create --label <label>`

The new API key is shown once. Store it somewhere safe before closing the terminal.

List key metadata:

1. `./bin/ovek auth keys`

Revoke a key:

1. `./bin/ovek auth key rm <key-id>`

Revoked keys stop working immediately. List responses show metadata only; Brain never returns existing API key secrets.

## Reauth

In production auth mode, critical mutations require a short-lived reauth token. The CLI handles this by prompting for the Brain owner password when Brain returns `reauth_required`.

Examples that may prompt for reauth:

1. `./bin/ovek run <project> <capsule-ref>`
2. `./bin/ovek db init <project> --app-secrets`
3. `./bin/ovek registry login <host> --username <user> --password-stdin`
4. `./bin/ovek auth key create --label <label>`
5. `./bin/ovek auth key rm <key-id>`

Change the owner password:

1. `./bin/ovek auth password`

Password changes require the current password and a new password. Outstanding reauth tokens are revoked after the password changes.

## Registry Credentials

Private capsule images use Brain-managed registry credentials keyed by registry host:

1. `printf '<registry-token>' | ./bin/ovek registry login ghcr.io --username <user> --password-stdin`
2. `./bin/ovek registry list`
3. `./bin/ovek registry rm ghcr.io`

Credentials are encrypted at rest in Brain's data directory. API and CLI list output never includes the token/password. Use the least-privileged pull token your registry supports.

## Tunnel And TLS

The current VPS trial reaches Brain through an SSH tunnel, for example `http://brain.localhost:8088`. Keep that tunnel private to your machine and treat local CLI profiles as secrets because they contain Brain API keys.

Do not expose Brain directly on the public internet without TLS and a deliberate domain configuration. The next TLS/domain stack is scoped in [tls-domain-design.md](tls-domain-design.md).
