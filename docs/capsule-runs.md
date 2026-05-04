# Capsule Runs

Ovek's primary runtime path is image-first: build an OCI image somewhere other than the tiny VPS, push it to a registry the VPS can pull from, then run it with `ovek run <project> <capsule-ref>`.

Capsule v1 is intentionally simple:

- listen on `PORT`, currently injected as `8080`
- use `POCKETBASE_URL` when the app needs the managed per-project PocketBase sidecar
- read project env/secrets from normal environment variables
- become ready by accepting TCP connections on `PORT`

## Publish A Capsule

The app repo can use any OCI-compatible build tool. A common path is a `Dockerfile` as the build recipe plus Podman, Buildah, Docker Buildx, or a GitHub Actions workflow as the producer.

For GHCR with Podman:

1. `podman login ghcr.io`
2. `podman build --format oci --platform linux/amd64 -t ghcr.io/<owner>/<image-name>:<tag> .`
3. `podman push ghcr.io/<owner>/<image-name>:<tag>`

Success: the image is visible in GHCR and the Ovek host can pull it by reference.

For public examples, make the GHCR package public so a fresh Ovek host can pull without registry credentials.

## Run A Capsule

1. `./bin/ovek run <project> ghcr.io/<owner>/<image-name>:<tag>`

Success: Ovek records the job source as `image`, stores the image ref as the deployment image, skips host-side app builds, pulls the image through Podman, provisions app and sidecar containers, waits for readiness, promotes the runtime, and routes the project hostname.

## Canonical Signup Example

The canonical public example is:

```text
https://github.com/massivemoose/ovek-signup-example
```

The published capsule is:

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
