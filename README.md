# probegap: check Kubernetes liveness and readiness probes against your Go server

[![CI](https://github.com/GillMacedo/probegap/actions/workflows/ci.yml/badge.svg)](https://github.com/GillMacedo/probegap/actions/workflows/ci.yml) [![MIT license](https://img.shields.io/badge/license-MIT-blue)](LICENSE)

**A Claude Code skill and Go CLI for teams that run Go services on Kubernetes. It checks the `httpGet` and `tcpSocket` liveness, readiness and startup probes in rendered Helm, Kustomize or plain manifests against the servers the Go program really starts, and reports probes that will fail: paths that return 404, health checks behind auth middleware, ports nothing listens on, servers bound to localhost, and HTTP and HTTPS mismatches.**

A probe and the code it checks drift apart. Someone renames `/healthz` to `/livez`, moves the admin server to another port with a flag, puts the health route behind the auth middleware, or binds the metrics server to `127.0.0.1`. The manifest still looks right, `kubeconform` and `helm lint` pass, and the pod goes into CrashLoopBackOff with `Liveness probe failed: HTTP probe failed with statuscode: 404`, or never becomes Ready. Linters such as kube-linter check that a probe's port is a declared container port. They don't look at what the program serves there.

probegap builds the program from source with two small hooks in Go's standard library, runs it with each container's `args` and `env` from the manifest, and records every listener and HTTP server it starts. No port is opened: `net.Listen` returns a listener that only records its address. Then it sends each probe's request, built the way the kubelet builds it, to the program's real handler, so it works with `net/http`'s `ServeMux`, chi, gorilla/mux, gin, echo and any other router built on `net/http`.

## What you can ask Claude

- "Our pods are in CrashLoopBackOff with Liveness probe failed 404. Is the probe path wrong?"
- "Will the probes in this Helm chart pass against the code in cmd/api?"
- "I moved the metrics server to its own port. Do the readiness probes still hit something?"
- "Why does this pod never become Ready?"

## Install

As a Claude Code plugin:

    claude plugin marketplace add GillMacedo/probegap
    claude plugin install probegap@probegap-marketplace

Or run it with Go 1.22 or newer, from the repository of the program you're checking. It has no dependencies outside this repository:

    { helm template my-release ./chart; echo ---; } | go run github.com/GillMacedo/probegap/plugin/scripts@v0.1.0 --pkg ./cmd/server --container server -

## Example

`examples/shop` is a made-up service. It serves its API on `-addr` (`:8080`), and an admin server with `/livez` and `/metrics` on `-admin-addr`, which defaults to `127.0.0.1:9090`. Its `/api/` routes need a token. `broken.yaml` deploys it with three common probe mistakes. `--fail-on never` keeps the exit code at 0 for this demo; without it, the run exits 1:

    $ go run ./plugin/scripts --pkg ./examples/shop --fail-on never examples/shop/broken.yaml
    ERROR needs-auth: Deployment shop/api, container api, startup httpGet /api/ping port http
      GET /api/ping on port 8080 returns 401, body "unauthorized". The kubelet sends no credentials, only the probe's httpHeaders. As a startup probe, it keeps the container from ever finishing its start, and gets it restarted
      fix: serve this path before the auth middleware, or add the header the server needs with httpHeaders
      in examples/shop/broken.yaml

    ERROR not-found: Deployment shop/api, container api, liveness httpGet /health port http
      GET /health on port 8080 returns 404, body "404 page not found". The kubelet counts any status of 400 or more as a failure. As a liveness probe, it gets the container restarted over and over
      fix: probe a path the server routes, or register this one
      in examples/shop/broken.yaml

    ERROR loopback-only: Deployment shop/api, container api, readiness httpGet /livez port admin
      port 9090 listens only on 127.0.0.1:9090, inside the pod. The kubelet connects to the pod IP, so the connection is refused and the probe fails
      fix: listen on :9090 (all addresses) instead of 127.0.0.1
      in examples/shop/broken.yaml

    note unknown-value: Deployment shop/api, container api
      ran without API_TOKEN, which comes from Secret shop-api; give it with --env KEY=VALUE if it changes where the program listens
      in examples/shop/broken.yaml

    3 errors, 0 warnings and 1 note in 3 checked probes in 1 container.
    Deployment shop/api, container api listened on 127.0.0.1:9090 (HTTP), :8080 (HTTP).

The same Deployment with the probes fixed, and `-admin-addr=:9090` in its args, stays quiet and exits 0:

    $ go run ./plugin/scripts --pkg ./examples/shop examples/shop/fixed.yaml
    note unknown-value: Deployment shop/api, container api
      ran without API_TOKEN, which comes from Secret shop-api; give it with --env KEY=VALUE if it changes where the program listens
      in examples/shop/fixed.yaml

    No problems in 3 checked probes in 1 container, and 1 note above.
    Deployment shop/api, container api listened on :8080 (HTTP), :9090 (HTTP).

`moved.yaml` adds `-addr=:8000` to the args and leaves the probes on port 8080. probegap runs the program with those args, so it sees where the server really went:

    $ go run ./plugin/scripts --pkg ./examples/shop --wait 2s --fail-on never examples/shop/moved.yaml
    ERROR no-listener: Deployment shop/api, container api, startup httpGet /healthz port http
      nothing listened on port 8080 within 2s of starting; the program listened on :8000 (HTTP), :9090 (HTTP). The kubelet's connection is refused and the probe fails
      fix: point the probe at the port the server uses, or change the program's listen address. If the server starts later, after a dependency, raise --wait
      in examples/shop/moved.yaml

    ERROR no-listener: Deployment shop/api, container api, readiness httpGet /readyz port http
      nothing listened on port 8080 within 2s of starting; the program listened on :8000 (HTTP), :9090 (HTTP). The kubelet's connection is refused and the probe fails
      fix: point the probe at the port the server uses, or change the program's listen address. If the server starts later, after a dependency, raise --wait
      in examples/shop/moved.yaml

    note unknown-value: Deployment shop/api, container api
      ran without API_TOKEN, which comes from Secret shop-api; give it with --env KEY=VALUE if it changes where the program listens
      in examples/shop/moved.yaml

    2 errors, 0 warnings and 1 note in 3 checked probes in 1 container.
    Deployment shop/api, container api listened on :8000 (HTTP), :9090 (HTTP).

Add `--format json` for machine-readable output.

## What it reports

| Code | Severity | Meaning |
|---|---|---|
| `not-found` | ERROR | The handler answers 404 |
| `method-not-allowed` | ERROR | The handler answers 405; the kubelet always sends GET |
| `needs-auth` | ERROR | The handler answers 401 or 403; the kubelet sends no credentials, only the probe's `httpHeaders` |
| `client-error` | ERROR | Any other 4xx answer |
| `no-listener` | ERROR | Nothing listened on the probe's port within `--wait`. The message lists the ports the program did use |
| `loopback-only` | ERROR | The port listens only on `127.0.0.1`, `::1` or `localhost`, which the kubelet can't reach |
| `not-http` | ERROR | An httpGet probe on a port that no `net/http` server serves, such as a gRPC port |
| `scheme-mismatch` | ERROR | `scheme: HTTPS` on a plain HTTP server, or HTTP on a TLS server |
| `named-port-missing` | ERROR | The probe names a port the container doesn't declare |
| `invalid-probe` | ERROR | A probe the API server rejects: a port out of range, a port name made only of digits, a scheme other than `HTTP` or `HTTPS` |
| `probe-error` | ERROR | The request can't complete, for example a redirect loop |
| `server-error` | WARN | The handler answers 5xx. The route exists; the program ran without its database or other dependencies, which may be why |
| `timeout` | WARN | No answer within `timeoutSeconds`, for the same reason |
| `handler-panicked` | WARN | The handler panicked, which closes the connection |
| `specific-address` | WARN | The port listens on one IP address that may not be the pod IP |
| `redirect-off-host` | note | The handler redirects to another host, which the kubelet counts as a success, so the probe checks nothing |
| `not-checked` | note | exec and gRPC probes, probes with `host` set, containers with no probes |
| `unknown-value` | note | An env var from a Secret or from a ConfigMap that isn't in the input, which the program ran without |

Answers from 200 to 399 pass, as in the kubelet, and same-host redirects are followed up to 10 times. The request has `Host` set to the pod IP and port (or the probe's `Host` header), `User-Agent: kube-probe/1.34`, `Accept: */*` and the probe's `httpHeaders`, and `timeoutSeconds` (default 1) cancels it. Arguments are built the way the kubelet builds them: `command[1:]` followed by `args`, with `$(VAR)` replaced from `env` and `envFrom` (ConfigMaps in the input are read). `fieldRef` values get stand-ins: `status.podIP` is `10.1.2.3`, `metadata.name` is the workload name plus `-0`.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | Nothing at or above `--fail-on`, which is `error` by default. Use `--fail-on warn` to fail on warnings too, or `never`. |
| 1 | Findings at or above `--fail-on`. |
| 2 | The input can't be trusted or the flags are wrong. Nothing was checked, and stdout is empty. |

## When it refuses

probegap exits 2 and prints only the reason when it can't trust what it read, whatever `--fail-on` says. For example, feeding it the error that `helm template` prints when a chart fails to render:

    printf 'Error: execution error at (shop/templates/deployment.yaml:31:20): image.tag is required\n' | go run ./plugin/scripts --pkg ./examples/shop -

prints this on stderr. The program exits 2 (under `go run`, Go adds a line `exit status 2` and itself exits 1):

    probegap: can't trust the input, nothing was checked: stdin, document 1: not valid YAML or JSON (yaml: mapping values are not allowed in this context); is it the output of a failed command?

On the manifest side it does the same for empty files, other failed command output (kubectl, kustomize, `go build`, `go test`), a values file, an API error, invalid or cut-off YAML or JSON, a file with no final line break, a template with `{{ }}` left in, a chart or kustomization directory, a workload without containers, a probe with no handler or no port, and a file whose last object ends inside a container. A single bad file among good ones fails the whole run. It also refuses a manifest with no httpGet or tcpSocket probe to check.

On the program side it refuses a package that doesn't build or isn't `main`, a program that doesn't link `net/http`, and a program that exits, crashes, or starts no listener within `--wait`. The program's output is shown, with the values probegap couldn't fill in:

    probegap: can't trust the input, nothing was checked: Deployment shop/api, container api: example.com/api exited (exit status 1) before probegap could check its servers. Its output:
      2026/11/15 12:07:23 connecting to database: DATABASE_URL is not set
    Values probegap couldn't fill in, which may be why: DATABASE_URL, which comes from Secret db. Give them with --env KEY=VALUE.

Pass a harmless stand-in such as `--env DATABASE_URL=postgres://localhost/x` if the program only needs the value to be set. Never pass real secrets.

A manifest cut on a line break can still parse. If the cut falls inside a container, its probes and ports after the cut are gone, so probegap refuses a file whose last object ends inside a container. A file that really ends there, as hand-written manifests and `helm template` output usually do, can be marked complete with a final `---` line, as in the install example. A file cut exactly between two objects is a valid shorter file, and no tool can tell. The summary line says how many probes were checked, so check that number.

## Limits

- **It runs your program.** Package `init` functions and `main` run until the servers start. No port is opened, but outgoing connections are real, so a program that connects to a database at startup will try to. Run it only on code you trust.
- **Dependencies.** A program that won't start without its database, a config file mounted from a ConfigMap volume, or a Kubernetes API is refused. Handlers that check a database get no database, so their 5xx answers and timeouts are warnings, not errors.
- **net/http only.** Servers on fasthttp (Fiber) aren't seen, and an httpGet probe on their port is reported as `no-listener` or `not-http`. gRPC probes aren't checked yet. tcpSocket probes are checked against every listener the program opens with `net.Listen`. `net.ListenTCP` and file-descriptor listeners are not faked and not seen.
- **Source, not the image.** probegap checks the code at `--pkg`. If the image was built from another commit, check that commit out first. It doesn't read images, so a container without `command` is assumed to run the program with its `args`; give ENTRYPOINT words such as a subcommand with `--entrypoint`.
- **Probe settings.** It doesn't judge `initialDelaySeconds`, `periodSeconds` or `failureThreshold`, or whether a liveness probe should check a database.

## FAQ

**Why does my pod say "Liveness probe failed: HTTP probe failed with statuscode: 404"?** The kubelet sent GET to the probe's path and port and the server has no route there. Often the path was renamed in code, or the probe points at another server's port. probegap shows the status the real handler returns, and the ports the program listens on.

**Why does my readiness probe fail with "connection refused" when the app is running?** Often the server listens on `127.0.0.1` or `localhost`, which works for `curl` inside the container and not for the kubelet, which connects to the pod IP. probegap reports this as `loopback-only`.

**Why does my probe get 401 Unauthorized?** The health route is behind auth middleware. The kubelet sends no credentials. Register health routes outside the middleware.

**Does it need a cluster or Docker?** No. It needs Go and the program's source with its dependencies (module cache, `vendor/` or network).

**Is it an MCP server?** No. It's a skill with one Go program, so `go run` or `go build` is all it needs.

## Tested on

- **Every finding:** `tests/cases_test.go` runs 41 probe cases against `tests/testdata/cases`, a program with a route or listener for each result: 13 that must stay quiet (including a same-host redirect, a required header given in `httpHeaders`, a Host-routed pattern, a route that needs `kube-probe/` as User-Agent, the server's `BaseContext`, HTTPS with certificate files that don't exist, a tcpSocket probe, and a listener bound to the pod IP from a `fieldRef`) and 28 that must report a given code and severity, with the exit code checked under each `--fail-on`. A server that starts after `--wait` is reported as `no-listener`.
- **A third-party router, offline:** `tests/testdata/chimod` uses go-chi/chi v5.2.3 (vendored) with a sub-router, a group behind basic auth and chi's own 404 handling. Its healthy probes stay quiet, and a wrong path, a probe without credentials and a wrong port are reported.
- **A real program, by hand:** podinfo 6.9.2 (stefanprodan/podinfo, gorilla/mux, plus a gRPC server). Its own `kustomize/deployment.yaml` uses exec probes, so probegap refuses it with exit 2 as having nothing to check. With those probes replaced by httpGet `/healthz` and `/readyz` on port `http` and a tcpSocket probe on `grpc`, using podinfo's real command and args, it stays quiet and lists `:9797`, `:9898` and `:9999 (not net/http)`. With a wrong path, `/readyz` on the metrics port and an httpGet probe on the gRPC port, it reports `not-found` twice and `not-http`. This needs network and isn't part of `make test`.
- **Input it can't trust:** tests pass 23 kinds of bad manifest alone, on stdin, before and after a real file, and in a directory with a real file. They also pass 18 kinds of bad program (an empty file, random text, binary, cut mid-line, cut on a line, build and test output, a type error, no `main` function, not a main package, no `net/http`, exits at once, panics, stops on a missing env var, hangs, fails to listen, an empty directory, a missing directory), each with a real manifest. Valid manifests with nothing to check (no probes, exec probes only, a Service only) are refused too. Every case runs under every `--fail-on` and with `--format json`, and must exit 2 with nothing on stdout. Another test cuts a two-document manifest at every line break and every fifth byte: mid-line cuts always exit 2, and line-break cuts exit 2 or give only findings of the whole file.
- **Go versions:** the tests pass on Go 1.22.12 and Go 1.27.3 on Linux arm64, offline. CI runs them on Linux amd64 with Go 1.22 and the current release.

Not tested: macOS and Windows; a real cluster (the kubelet's behavior was taken from `pkg/probe/http` in kubernetes/kubernetes, not observed); gin, echo and gorilla/mux in `make test` (gorilla/mux only through podinfo, by hand); a fresh Claude Code session using the skill. The `go run ...@v0.1.0` line works only once the repository is published.

## Contributing

Contributions are welcome, from people and from people working with AI agents. See [CONTRIBUTING.md](CONTRIBUTING.md). Good first issues are [listed here](https://github.com/GillMacedo/probegap/issues?q=is%3Aissue+is%3Aopen+label%3A%22good+first+issue%22).

### How pull requests work

1. **Open a PR.** Fork the repo, work on a branch, and keep it to one change. Write a title that says what changes for users, because it becomes the changelog line: `feat:` goes under Added and `fix:` under Fixed.
2. **Automatic check.** Code changes need tests, and `make test` must pass. Changes over 30 lines need a linked open issue (`Closes #<number>`), and a PR can have at most 800 changed lines. If something is missing, a comment tells you what to fix.
3. **Review.** Claude reviews each PR automatically. Code PRs also get a security review and are tested in a sandbox. The review then merges the PR or asks for changes, and a small problem may become a follow-up issue instead. A PR with no new commit 14 days after a change request is closed, and you can reopen it. PRs that change CI, agent settings, npm or Python dependencies, vendored code or build files can't be merged this way, and a comment says so. The same goes for anything the security review doesn't clear.
4. **Release.** A merge that changes what users install becomes a new version within about two hours, with a GitHub Release and, if the project has one, its npm package. Docs and CI changes wait for the next release.

## AI disclosure

This project was largely generated with Claude Code and reviewed by its maintainer.

## About me

I'm Giulliano Macedo, and I build developer tools at GoTech Solutions. Follow along or get in touch:

- GitHub: [GillMacedo](https://github.com/GillMacedo)
- LinkedIn: [giulliano](https://www.linkedin.com/in/giulliano/)
- X: [@giumacedo](https://x.com/giumacedo)

If `probegap` saved you some prompts, a star helps others find it.

---

Built by [Giulliano Macedo](https://github.com/GillMacedo), GoTech Solutions · [LinkedIn](https://www.linkedin.com/in/giulliano/) · [X](https://x.com/giumacedo)
