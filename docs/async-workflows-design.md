# Async Workflows Design

This design starts Ovek's async capability with generic scheduled and triggered OCI workflow runs. The goal is to make background work feel like the existing capsule model while avoiding wasteful image pulls and unsafe queue pressure on tiny VPSes.

V1 should treat workflow registration like a small deploy: Brain pulls the workflow image, resolves and stores an immutable local image reference, then later workflow runs start from the cached image. One-shot containers remain the v1 execution primitive for scheduled, manual, and low-frequency background work. A long-running Bun/TypeScript hot-worker sidecar is the v2 path for high-frequency or latency-sensitive app-triggered work.

## Product Shape

Async v1 is project-scoped and image-first. A project may have a web capsule plus one or more workflow definitions. Each workflow definition owns a capsule image, trigger configuration, queue policy, and cached image metadata.

The first public capability should be scheduled OCI jobs with manual runs for validation. API-triggered runs can use the same run creation path once the queue and backpressure model is in place. Higher-level Bun/TypeScript workflow sidecars should build on the generic workflow/run model rather than replace it.

## Image Lifecycle

Workflow registration pulls once:

1. User runs `ovek workflow set <project> <name> --image <capsule-ref> --schedule '<cron>'`.
2. Brain pulls the image immediately using existing registry credentials.
3. Brain stores the original image ref and the resolved immutable image digest or runtime image ID.
4. Workflow runs start from the cached immutable image reference.
5. If the local image is missing at run time, Brain re-pulls before execution and updates cached image metadata.

Mutable tag updates are explicit. If a user wants `ghcr.io/me/worker:latest` to resolve to a newer image, they run `workflow set` again. Ovek should not check mutable tags on every workflow run.

Private workflow images reuse the existing registry credential store. Missing private-image credentials should fail at registration time when possible, not at every run.

Image cleanup is deferred until Ovek has a safe image-retention policy. Removing a workflow should remove its definitions and runtime artifacts, but it should not aggressively prune shared images in v1.

## Resources And Interfaces

Brain should add project-scoped workflow definitions and workflow run records. Workflow run records should be separate from deployment jobs so app deploy history stays readable.

A workflow definition records:

- project name
- workflow name
- source image ref
- resolved image digest or runtime image ID
- trigger type and schedule expression
- enabled state
- overlap policy
- queue cap
- created and updated timestamps

A workflow run records:

- project name and workflow name
- trigger type: `manual`, `schedule`, or `api`
- idempotency key for API-triggered runs when provided
- captured project config revision
- status, timestamps, exit code, error message, and log path
- image ref and resolved image digest or runtime image ID used for the run

Suggested API shape:

```text
GET    /v1/projects/{project}/workflows
PUT    /v1/projects/{project}/workflows/{workflow}
GET    /v1/projects/{project}/workflows/{workflow}
DELETE /v1/projects/{project}/workflows/{workflow}
POST   /v1/projects/{project}/workflows/{workflow}/runs
GET    /v1/projects/{project}/workflow-runs?limit=20
GET    /v1/projects/{project}/workflow-runs/{runID}
GET    /v1/projects/{project}/workflow-runs/{runID}/logs
GET    /v1/projects/{project}/workflow-runs/{runID}/logs/stream
```

Suggested CLI shape:

```text
ovek workflow set <project> <name> --image <capsule-ref> --schedule '<cron>'
ovek workflow run <project> <name>
ovek workflow list <project>
ovek workflow status <project> [<name>]
ovek workflow logs <project> <run-id> [--follow|--no-follow]
ovek workflow rm <project> <name>
```

Protected mutations should follow the existing production reauth pattern.

## Queue Ownership

Brain owns the workflow queue. Apps should enqueue workflow runs through Brain rather than maintaining a hidden project-local queue.

SQLite is the source of truth for workflow definitions and workflow runs. In-memory timers and channels are only wake-up mechanisms. On Brain restart:

- queued runs remain queued
- running runs are marked failed with an interrupted-by-restart message
- schedules are rebuilt from persisted workflow definitions
- the next future scheduled tick is used; v1 does not need missed-run catch-up

Queue caps prevent unbounded growth. If an API/manual trigger would exceed the cap, Brain returns a queue-full error so callers can surface backpressure. Scheduled triggers should record or emit skipped ticks instead of growing the queue forever.

## Concurrency Defaults

V1 optimizes for small VPS safety over maximum throughput.

Defaults:

- global workflow concurrency: `1`
- per-project workflow concurrency: `1`
- per-workflow concurrency: `1`
- scheduled overlap policy: `skip`
- manual/API-triggered overlap policy: `queue`

These defaults protect the primary app and managed PocketBase sidecar from workflow bursts. Higher limits can come later once Ovek has clearer host sizing guidance, observability, and resource controls.

## Runtime Behavior

Workflow containers are one-shot containers. They join the project network and receive the same built-ins as app capsules:

```text
PORT=8080
POCKETBASE_URL=http://db:8090
```

`PORT` remains present for compatibility, but workflow readiness is not TCP-based. A workflow run completes when the process exits. Exit code `0` means succeeded; non-zero means failed.

Workflow runs also receive metadata:

```text
OVEK_PROJECT=<project>
OVEK_WORKFLOW=<workflow>
OVEK_WORKFLOW_RUN_ID=<run-id>
```

Project env/secrets are captured at workflow run creation time using the same revision model as capsule runs. Logs should reuse the existing persisted log and secret-redaction behavior.

Terminal workflow containers are removed after logs and status are persisted. Project removal should delete workflow definitions, outstanding workflow containers, workflow run records, and workflow run logs for that project.

## Statuses And Triggers

Run statuses:

```text
queued
preparing
running
succeeded
failed
skipped
canceled
timed_out
```

Trigger types:

```text
manual
schedule
api
```

Scheduling rules:

- cron expressions are evaluated by Brain in UTC
- no missed-run catch-up in v1
- scheduled overlaps use `skip` by default
- manual/API triggers queue behind active runs until the queue cap is reached
- optional idempotency keys prevent duplicate API-triggered run creation for retrying callers

## Hot Worker Sidecars

One-shot OCI workflow runs are a good v1 primitive for scheduled work, manual jobs, low/medium-frequency tasks, language-neutral examples, and simple operational automation. They are not ideal for frequent app-triggered work where every event starts a new container.

V2 should add a hot-worker mode. A long-running worker sidecar stays warm, receives tasks from Brain, and executes many workflow actions without per-run container startup overhead. Bun/TypeScript should be the first opinionated hot-worker system because it can provide a strong developer experience, but it should sit on top of the generic workflow queue/run model.

The generic OCI workflow model remains useful even after hot workers exist:

- it works with any language
- it is easier to reason about operationally
- it keeps scheduled jobs simple
- it gives Bun workers a durable Brain-owned queue to build on

## Failure Modes

Registration failures:

- invalid workflow name or image ref
- missing project
- missing registry credential for private image
- image pull failure
- digest/runtime image ID resolution failure

Run failures:

- cached image missing and re-pull fails
- container create/start failure
- non-zero workflow exit
- run timeout
- log capture failure
- Brain restart while running

Failures should surface in the run error message and persisted logs. Secret redaction should reuse the existing job log scrubber behavior.

## Canonical Example

The first async example should be an app + PocketBase + scheduled digest workflow:

- web app accepts signup or event records
- managed PocketBase stores records
- workflow image runs daily, reads PocketBase through `POCKETBASE_URL`, computes a summary, and logs or sends a digest
- manual `ovek workflow run` validates the workflow before enabling schedule trust

This example demonstrates app + managed data + background work without requiring a framework-specific workflow engine.

## Test Plan

Registration:

- `workflow set` pulls the image once, stores source ref and resolved digest/image ID, and fails cleanly on missing private registry credentials.
- Re-running `workflow set` with the same name updates image/schedule and re-pulls.

Run execution:

- Manual run uses cached image without pulling on every run.
- If the cached image is missing, Brain re-pulls before execution.
- Exit code `0` marks `succeeded`; non-zero marks `failed`.
- Logs stream and persist like deployment job logs.

Queue behavior:

- With default concurrency, only one workflow run executes at a time.
- Scheduled overlaps become `skipped`.
- Manual/API triggers queue until cap; over-cap triggers return queue-full.
- Brain restart recovers queued/running state predictably.

Integration:

- A scheduled digest example runs against Ovek-managed PocketBase.
- Manual `ovek workflow run` validates the same workflow before enabling schedule trust.
- Existing app capsule runtime remains reachable while a workflow is running.
