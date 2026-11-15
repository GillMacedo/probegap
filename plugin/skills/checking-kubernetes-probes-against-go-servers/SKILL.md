---
name: checking-kubernetes-probes-against-go-servers
description: Checks the httpGet and tcpSocket liveness, readiness and startup probes in Kubernetes manifests (Deployment, StatefulSet, DaemonSet, Pod, Job, rendered Helm or Kustomize output) against the HTTP servers a Go program really starts, by running the program with the container's args and env without opening any port and sending the kubelet's probe request to its real handler. Reports probe paths that return 404, 405, 401 or 403, ports nothing listens on, servers that listen only on localhost or 127.0.0.1, HTTPS probes on plain HTTP servers and the reverse, httpGet probes on gRPC ports, named ports missing from the container, and handlers that time out, panic or return 5xx. Use when a Go service is in CrashLoopBackOff from failed liveness probes, a pod never becomes Ready, "Liveness probe failed" or "Readiness probe failed" shows in events, when reviewing a chart or manifest change that edits probes, ports or listen flags, or when renaming health routes or moving a server to another port.
---

# Checking Kubernetes probes against a Go program

probegap builds the Go program from source with two hooks in the standard library: `net.Listen` returns a listener that records its address and never accepts, and `http.Server.Serve` records the server and its handler. It runs the program with each container's arguments and environment, waits until the probes' ports are set up, then sends each probe's request to the recorded handler the way the kubelet does (GET, `User-Agent: kube-probe/...`, the probe's headers, same-host redirects followed, `timeoutSeconds`). It needs Go 1.22 or newer and the program's source with its dependencies (module cache, `vendor/` or network).

Tell the user before you run it: it compiles their program and runs it, in an empty temporary directory with only the container's environment, until its servers start. No port is opened, but code before the servers start runs for real and may try to reach a database. Only run it on code the user trusts, normally their own repository.

## Get the two inputs

1. **The program:** the directory of its `main` package, such as `./cmd/server`. If the image is built from another commit, check that commit out first (for example `git worktree add /tmp/v1.4.0 v1.4.0`) and point `--pkg` there.
2. **Rendered manifests:** never a chart or kustomization directory (probegap refuses those).
   - Helm: `{ helm template RELEASE CHART -n NS -f values.yaml; echo ---; } > rendered.yaml`. The final `---` marks the file as complete; without it, a file that ends inside a container is refused as possibly cut off.
   - Kustomize: `{ kustomize build DIR; echo ---; } > rendered.yaml`.
   - Plain files: make sure each file ends with a `---` line.

Check that the render command exited 0 before you go on. Include the ConfigMaps that feed env vars, so their values are used.

## Run it

Build it once, then run it:

    go build -o "${TMPDIR:-/tmp}/probegap" $(ls "${CLAUDE_PLUGIN_ROOT}"/scripts/*.go | grep -v _test.go)
    "${TMPDIR:-/tmp}/probegap" --pkg ./cmd/server --container server rendered.yaml

`--container` names the containers that run this program, comma-separated. It can be left out when only one container has probes. If the program stops at startup because a value from a Secret is missing, the message names it: pass a harmless stand-in with `--env NAME=value` (never ask for or use a real secret). If the image's ENTRYPOINT adds words such as a subcommand and the container sets no `command`, pass them with `--entrypoint 'serve'`. `--wait` (default 10s) is how long the program may take to start the servers. Add `--format json` for machine-readable output and `--fail-on warn` to fail on warnings too.

## Read the exit code first

- **0:** nothing at or above `--fail-on` (default `error`). Notes may still be printed; read them.
- **1:** findings at or above `--fail-on`.
- **2:** nothing was checked. The message says why: the manifest is empty, a failed command's output, unrendered, cut off, or has no probes probegap can check; or the program didn't build, isn't a main package, doesn't use net/http, or exited, crashed or started no listener before its probes could be checked (its output is shown). Fix the input and run again. Never describe exit 2 as a clean result.

## Check each finding

- **ERROR, the probe fails in the cluster:** `not-found` (404), `method-not-allowed` (405), `needs-auth` (401 or 403: the kubelet sends no credentials), `client-error` (other 4xx), `no-listener` (nothing on that port within `--wait`), `loopback-only` (the server listens on 127.0.0.1, ::1 or localhost), `not-http` (an httpGet probe on a port that isn't served by net/http, such as gRPC), `scheme-mismatch`, `named-port-missing`, `invalid-probe` (the API server rejects it), `probe-error` (for example a redirect loop).
- **WARN, the route exists but failed here:** `server-error` (5xx), `timeout`, `handler-panicked`. The program ran with no database or other dependencies, so these may pass in the cluster. Read the handler and say what it checks.
- **WARN:** `specific-address` (the server binds one IP that may not be the pod IP).
- **note:** `not-checked` (exec and gRPC probes, probes with `host`, containers without probes), `unknown-value` (env from Secrets or ConfigMaps not in the input), `redirect-off-host` (the kubelet counts it as success, so the probe checks nothing).

For `no-listener`, read the listen addresses in the summary line: often the probe points at the wrong port, or a flag or env var moved the server. If the program starts its server only after connecting to something, a larger `--wait` won't help without that dependency; say so instead of calling it a finding you confirmed.

## Report

1. Errors first: the workload, container and probe, what the handler answered, and the fix line. Point at the route registration or listen address in the code, and the manifest lines.
2. Then warnings, and whether each matters in the cluster.
3. Say what wasn't checked: notes, other containers, and that the check used the source at `--pkg`, not the image.

## Don't

Don't change the cluster or apply manifests. Don't ask for real secret values. Don't say the probes will pass because probegap found nothing: it checks routing, ports, binding and scheme; handlers that depend on a database or other services may behave differently in the cluster.
