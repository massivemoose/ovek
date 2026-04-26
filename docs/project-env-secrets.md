# Project Environment And Secrets

Ovek stores project environment configuration in immutable revisions. A deploy captures the latest revision when the deployment job is created, and the managed app container receives that captured revision when it starts.

Changes made with `ovek env` or `ovek secret` affect the next deployment only. They do not mutate or restart the currently running app.

## CLI

```sh
ovek env set <project> KEY=value
ovek env list <project>
ovek env unset <project> KEY
ovek secret set <project> KEY
ovek secret unset <project> KEY
```

Secret values are masked in CLI and API list output.

## Runtime Injection

Ovek always injects these built-in values first:

```text
PORT=8080
POCKETBASE_URL=http://db:8090
```

Project-defined values are injected after the built-ins. The following names are reserved and cannot be configured by projects:

```text
PORT
POCKETBASE_URL
OVEK_*
```

## Storage

Regular environment values are stored in Brain's SQLite database as plaintext. Secret values are encrypted at rest with AES-256-GCM.

Brain loads the secret encryption key in this order:

1. `OVEK_SECRETS_KEY`, if set.
2. A generated local key at `/var/lib/ovek/secrets/master.key`.

The generated key file is written with `0600` permissions inside a `0700` directory.

## Key Rotation

Automatic re-encryption is not part of the v1 implementation. For now, key rotation is manual:

1. Keep the old key available.
2. Export or otherwise recover the existing secret values.
3. Start Brain with the new `OVEK_SECRETS_KEY`.
4. Re-set the project secrets so they are encrypted under the new key.
5. Redeploy affected projects.

Future work should add a first-class re-encryption command.

## Log Redaction

Brain redacts exact secret values from deployment job logs before writing command output and lifecycle/error lines to persisted job logs. Redaction is best effort and ignores secrets shorter than four characters to avoid noisy accidental replacements.
