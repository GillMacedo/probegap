# Architecture

probegap is one Go program in `plugin/scripts`, plus a skill that tells Claude when and how to run it.

## Flow

1. **Load manifests** (`load`, `parseFile`, `containers` in `manifest.go`). This happens first, so a bad file is refused before any build. Each argument is a file, a directory or `-`. A file is refused when it is empty, has no final line break, contains NUL bytes or `{{`, ends on a key with no value, or holds a document that isn't a Kubernetes object. Directories are walked for `.yaml`, `.yml` and `.json`, and refused when they hold a `Chart.yaml` or a kustomization. A file whose last object ends inside a container is refused unless it ends with a `---` or `...` line.
2. **Read probes** (`probes` in `probegap.go`). Liveness, readiness and startup probes of containers and of sidecar init containers (`restartPolicy: Always`). A probe that isn't an object, has no handler, has no port or has a bad `timeoutSeconds` is refused as cut off. Named ports are resolved against the container's `ports`. exec, gRPC and probes with `host` become notes. `pick` selects the containers named with `--container`, or the only container with probes.
3. **Build** (`buildProgram` in `program.go`). `go list -deps -json` finds the main package and refuses a package that isn't `main` or doesn't link `net/http`. `patchDial` and `patchServer` (`hooks.go`) add a call at the top of `(*net.ListenConfig).Listen` in `net/dial.go` and of `(*http.Server).Serve` and `ServeTLS` in `net/http/server.go`, and new files with the hooks are added to both packages, all with `go build -overlay`. Your files and the Go installation are never changed.
4. **Run** (`binary.run`). The program runs with the container's arguments (built like the kubelet: `command[1:]` and `args` with `$(VAR)` expanded, init wrappers unwrapped) and environment (`env`, `envFrom`, ConfigMaps in the input, stand-ins for `fieldRef` values such as `status.podIP`, plus `--env`), in an empty temporary directory. `PROBEGAP_PLAN` names a file with the probes. In the program:
   - Every TCP or Unix `Listen` returns a fake listener whose `Accept` blocks, and records the requested host and port. No port is opened, so two programs or a busy port never conflict, and nothing outside can connect.
   - `Serve` and `ServeTLS` record the server and listener, then block. `ServeTLS` never loads the certificate files, which usually come from a Secret.
   - A goroutine started in `net/http`'s `init` waits until every probe's port has a server (or, for tcpSocket, a listener). When every port has at least a listener it waits one more quiet second, and it gives up at `--wait`. Then it builds each httpGet request as raw HTTP/1.1 text (GET, `Host` of the pod IP and port or the probe's `Host` header, `User-Agent: kube-probe/1.34`, `Accept: */*`, the probe's headers), parses it with `http.ReadRequest`, adds `RemoteAddr`, TLS state, the server's `BaseContext` and the server context keys, and calls the server's own `serverHandler`, the same path a real connection takes. Same-host redirects are followed up to 10 times, as the kubelet's client does. `timeoutSeconds` cancels the request context. The results are written to `PROBEGAP_OUT` and the program exits.
   A program that exits, crashes, or reports no listener at all is refused with its output, and the values probegap couldn't fill in (Secrets) are named.
5. **Judge** (`judge`). Status 200 to 399 passes, as in the kubelet. 4xx answers, missing listeners, loopback-only listeners, non-HTTP ports and scheme mismatches are errors. 5xx, timeouts and panics are warnings, because the program ran without its dependencies.
6. **Report** (`printText`, JSON). Findings are sorted by severity. The summary says how many probes were checked and what each container listened on.

## Why run the program

Routes can't be read reliably from source. Routers are built at run time, mounted under prefixes, wrapped in middleware that rejects requests without credentials, and listen addresses come from flags, env vars and config. Running the program with the container's own arguments and calling its real handler answers the question the kubelet asks, for any router built on net/http.

## Exit codes

0 when nothing reaches `--fail-on`, 1 when something does, 2 when the input can't be trusted or the flags are wrong. Exit 2 prints only to stderr.
