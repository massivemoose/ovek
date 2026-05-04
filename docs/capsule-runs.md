# Capsule Runs

Ovek's primary runtime path is image-first: build an OCI image somewhere that is not the tiny VPS, push it to a registry the VPS runtime can pull from, then run it with `ovek run <project> <capsule-ref>`.

Capsule v1 is intentionally simple. It is an OCI image that follows Ovek runtime expectations:

- listen on `PORT`, currently injected as `8080`
- use `POCKETBASE_URL` when the app needs the managed per-project PocketBase sidecar
- read project env/secrets from normal environment variables
- become ready by accepting TCP connections on `PORT`

`ovek deploy <project> <repoURL>` still exists, but it is the transitional source-build path. It clones the repo and runs Railpack/BuildKit on the Ovek host, so it is best treated as local dogfood or larger-host convenience rather than the tiny-VPS default.

## Local Image Build And Push

From an app repo that has a Dockerfile or another OCI-compatible build setup:

1. `docker build -t localhost:5001/<image-name>:<tag> .`
2. `docker push localhost:5001/<image-name>:<tag>`

Success: the image exists in a registry reachable by the runtime engine. In the local Docker and Podman scaffolds, `localhost:5001` is the host-published development registry.

Run it with Ovek:

1. `./bin/ovek run <project> localhost:5001/<image-name>:<tag>`

Success: the command streams job logs and exits after the image is pulled, the managed app/PocketBase runtime is provisioned, readiness passes, and the deployment is promoted.

## CI Image Build And Push

For a registry such as GHCR:

1. `docker login ghcr.io`
2. `docker buildx build --platform linux/amd64 -t ghcr.io/<owner>/<image-name>:<tag> --push .`

Run the pushed image:

1. `./bin/ovek run <project> ghcr.io/<owner>/<image-name>:<tag>`

Success: Ovek records the job source as `image`, stores the image ref as the deployment image, and skips git/Railpack/BuildKit entirely.

## Canonical Signup Example Capsule

The canonical public example is:

```text
https://github.com/massivemoose/ovek-signup-example
```

Publish it from the example repo with a public GHCR image tag:

1. `docker login ghcr.io`
2. `docker buildx build --platform linux/amd64 -t ghcr.io/massivemoose/ovek-signup-example:<tag> -t ghcr.io/massivemoose/ovek-signup-example:latest --push .`
3. `docker buildx imagetools inspect ghcr.io/massivemoose/ovek-signup-example:<tag>`

Success: the image is visible as a public GHCR package and can be pulled without registry credentials.

The capsule smoke and quickstart default to:

```text
ghcr.io/massivemoose/ovek-signup-example:latest
```

The signup example creates its PocketBase collection on app startup, so initialize app-facing PocketBase secrets before the first run:

1. `./bin/ovek pb init signup-demo --app-secrets`
2. `./bin/ovek run signup-demo ghcr.io/massivemoose/ovek-signup-example:latest`

Success: the image is pulled from GHCR, Ovek injects the captured PocketBase app credentials, and the app becomes reachable at `signup-demo.localhost`.

## Validate A Run

Check status:

1. `./bin/ovek status <project>`

Success: the project status becomes `running`, the recent job source shows the image ref, and the current runtime app reports `running=true`.

Read runtime logs:

1. `./bin/ovek logs <project> --no-follow`

Success: app container logs are returned.

Read job logs:

1. `./bin/ovek logs --job <JOB_ID> --no-follow`

Success: image runs show lifecycle lines such as `using prebuilt image`, `provisioning PocketBase`, `starting app container`, `waiting for app readiness`, and `deployment promoted`.

Check routing:

1. `curl -si -H 'Host: <project>.localhost' http://127.0.0.1/`

Success: the managed app responds through Traefik.

Check PocketBase:

1. `./bin/ovek pb status <project>`

Success: the PocketBase sidecar is running. If the app needs PocketBase credentials in its environment, run `./bin/ovek pb init <project> --app-secrets` before the next `ovek run`.

Open a PocketBase tunnel for dashboard/API inspection:

1. `./bin/ovek pb tunnel <project> --listen 127.0.0.1:8091`

Success: the tunnel stays open and forwards to the managed PocketBase sidecar.

## Transitional Source Build Path

Use source deploy when you intentionally want Ovek to build on the host:

1. `./bin/ovek deploy <project> <repoURL>`

Success: Brain clones the repo, prepares a Railpack plan, builds with BuildKit, pushes to the local managed registry, then runs the resulting image through the same provisioning, readiness, promotion, routing, status, logs, and cleanup path.
