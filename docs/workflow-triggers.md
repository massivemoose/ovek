# Workflow Triggers

Workflow trigger tokens let an app enqueue one registered workflow without holding a full Brain API key.

## Setup

1. `ovek workflow token create <project> <workflow> --label app`
2. Store the one-time token as a project secret, for example `DIGEST_WORKFLOW_TOKEN`.
3. Re-run the app capsule so the secret is injected.

App capsules receive `OVEK_BRAIN_URL`, defaulting to `http://brain:8081`. Trigger requests use the existing workflow run path:

```text
POST ${OVEK_BRAIN_URL}/v1/projects/<project>/workflows/<workflow>/runs
X-Ovek-Workflow-Token: <token>
Idempotency-Key: <optional-stable-key>
Content-Type: application/json
```

The request body may include a JSON payload:

```json
{
  "payload": {
    "signupId": "rec_123"
  }
}
```

Workflow containers receive the payload as a read-only file at `OVEK_WORKFLOW_PAYLOAD_FILE`.

## Responses

- `202 Accepted`: the workflow run was queued. The response body includes the run ID and log links.
- `401 Unauthorized`: the trigger token is missing, invalid, revoked, or scoped to another workflow.
- `404 Not Found`: the project or workflow does not exist for authenticated control-plane callers.
- `429 workflow_queue_full`: the workflow already has too many queued/running runs. Retry later or surface backpressure to the user.

Use an `Idempotency-Key` for user actions that may be retried. Reusing the same key for the same project/workflow returns the existing run instead of enqueueing a duplicate.

## Go

```go
func triggerWorkflow(ctx context.Context, project, workflow, tokenEnv, idempotencyKey string, payload map[string]any) (string, error) {
	brainURL := strings.TrimRight(os.Getenv("OVEK_BRAIN_URL"), "/")
	token := os.Getenv(tokenEnv)
	if brainURL == "" || token == "" {
		return "", errors.New("workflow trigger is not configured")
	}

	body, err := json.Marshal(map[string]any{"payload": payload})
	if err != nil {
		return "", err
	}
	endpoint := fmt.Sprintf("%s/v1/projects/%s/workflows/%s/runs", brainURL, url.PathEscape(project), url.PathEscape(workflow))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Ovek-Workflow-Token", token)
	if idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("workflow trigger failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}

	var run struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&run); err != nil {
		return "", err
	}
	return run.ID, nil
}
```

## Python

```python
import json
import os
import urllib.error
import urllib.parse
import urllib.request


def trigger_workflow(project, workflow, token_env, payload, idempotency_key=None):
    brain_url = os.environ["OVEK_BRAIN_URL"].rstrip("/")
    token = os.environ[token_env]
    endpoint = f"{brain_url}/v1/projects/{urllib.parse.quote(project)}/workflows/{urllib.parse.quote(workflow)}/runs"
    body = json.dumps({"payload": payload}).encode("utf-8")
    headers = {
        "Content-Type": "application/json",
        "X-Ovek-Workflow-Token": token,
    }
    if idempotency_key:
        headers["Idempotency-Key"] = idempotency_key

    request = urllib.request.Request(endpoint, data=body, headers=headers, method="POST")
    try:
        with urllib.request.urlopen(request, timeout=5) as response:
            return json.load(response)["id"]
    except urllib.error.HTTPError as exc:
        detail = exc.read().decode("utf-8", errors="replace")
        raise RuntimeError(f"workflow trigger failed: status={exc.code} body={detail}") from exc
```

## JavaScript / TypeScript

```ts
export async function triggerWorkflow(
  project: string,
  workflow: string,
  tokenEnv: string,
  payload: unknown,
  idempotencyKey?: string,
): Promise<string> {
  const brainURL = process.env.OVEK_BRAIN_URL?.replace(/\/+$/, "");
  const token = process.env[tokenEnv];
  if (!brainURL || !token) throw new Error("workflow trigger is not configured");

  const endpoint = `${brainURL}/v1/projects/${encodeURIComponent(project)}/workflows/${encodeURIComponent(workflow)}/runs`;
  const headers: Record<string, string> = {
    "Content-Type": "application/json",
    "X-Ovek-Workflow-Token": token,
  };
  if (idempotencyKey) headers["Idempotency-Key"] = idempotencyKey;

  const response = await fetch(endpoint, {
    method: "POST",
    headers,
    body: JSON.stringify({ payload }),
  });
  if (response.status !== 202) {
    throw new Error(`workflow trigger failed: status=${response.status} body=${await response.text()}`);
  }
  const run = (await response.json()) as { id: string };
  return run.id;
}
```
