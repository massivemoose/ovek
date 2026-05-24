# Async Workflows

Async workflows are project-scoped one-shot OCI jobs managed by Brain. A workflow is registered like a small deploy: Brain pulls the image once, stores the source ref plus resolved image metadata, and future runs start from the cached runtime image ID.

## Commands

Register or update a workflow:

```text
ovek workflow set <project> <name> --image <capsule-ref> [--schedule '<cron>']
```

Run it manually:

```text
ovek workflow run <project> <name>
```

Inspect workflows and runs:

```text
ovek workflow list <project>
ovek workflow status <project> [<name>]
ovek workflow logs <project> <run-id> [--follow|--no-follow]
ovek workflow rm <project> <name>
```

## Runtime Model

- `workflow set` pulls the image immediately using Ovek registry credentials.
- Mutable tag updates are explicit: run `workflow set` again to refresh image metadata.
- Runs use the stored runtime image ID when available.
- Scheduled runs are evaluated by Brain in UTC.
- Scheduled overlap is skipped; manual/API overlap queues until the workflow queue cap.
- Default queue cap is `64` per workflow.
- Default execution timeout is 10 minutes. Brain admins can override the install-wide default with `OVEK_WORKFLOW_RUN_TIMEOUT`, using Go duration syntax such as `5m`, `30m`, or `1h`.
- Project removal deletes workflow definitions, workflow run rows, managed workflow logs, and outstanding workflow containers. It does not delete workflow images.

Workflow containers receive:

```text
PORT=8080
POCKETBASE_URL=http://db:8090
OVEK_PROJECT=<project>
OVEK_WORKFLOW=<workflow>
OVEK_WORKFLOW_RUN_ID=<run-id>
```

Project env and secrets from the captured project config revision are injected after those built-ins. Workflow readiness is exit-code based: exit `0` succeeds, non-zero fails.

## Manual Validation

1. `./bin/ovek db init workflow-demo --app-secrets`
2. `./bin/ovek workflow set workflow-demo digest --image <workflow-image> --schedule '@hourly'`
3. `./bin/ovek workflow list workflow-demo`
4. `./bin/ovek workflow run workflow-demo digest`
5. `./bin/ovek workflow status workflow-demo digest`
6. `./bin/ovek workflow logs workflow-demo <RUN_ID> --no-follow`
7. `./bin/ovek rm workflow-demo --database --delete-database-data`

Success looks like a queued workflow run that streams logs, reaches `succeeded`, and remains visible in `workflow status` after the container is removed.
