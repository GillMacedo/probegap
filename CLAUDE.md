# probegap

Claude Code skill and Go CLI that checks the httpGet and tcpSocket probes in Kubernetes manifests against the servers a Go program really starts.

## Architecture
`plugin/scripts/probegap.go` holds the CLI, probe parsing (`probes`), the checks (`judge`) and the report. `plugin/scripts/hooks.go` holds the two hooks added to the standard library with `go build -overlay`: `netHook` makes `(*net.ListenConfig).Listen` return a fake listener that records its address, and `httpHook` records each `(*http.Server).Serve` and `ServeTLS` call and sends the probe requests to the recorded handler. `plugin/scripts/program.go` builds (`buildProgram`) and runs (`binary.run`) the program. `plugin/scripts/manifest.go` loads manifests, refuses input it can't trust, and builds each container's arguments and environment the way the kubelet does. `plugin/scripts/yaml_*.go` is go.yaml.in/yaml/v2 copied in, so the plugin builds with no go.mod. See `docs/architecture.md`.

## Commands
- `make test`: gofmt, vet and tests, including `tests/`, which run probegap on the test programs in `tests/testdata` and the examples
- `make lint`: plugin manifest validation (needs Claude Code)
- `make demo`: run on the examples

## Conventions
- Standard library only, plus the copied YAML parser. Never connect to a cluster. Never open a port in the checked program: every listen must go through the fake listener.
- Any input it can't fully read or trust exits 2 with nothing on stdout, whatever `--fail-on` says. Add a case to `badManifests` or `TestBadPrograms` in `tests/inputs_test.go` for each new kind of input.
- A program that exits, crashes or starts no listener is refused, never reported as clean.
- Tests must not assume the machine they run on: build for the host, and read `runtime` for anything platform-specific.
- Never print Secret values. Values from Secrets are left unset and listed by name.

## Do and don't
- Do run `make test` on Go 1.22 and on the current Go release before calling a change done; the hooks patch the standard library of whichever Go builds the program.
- Don't add `go.mod` requirements to the root module: the installed plugin has no module. Test programs that need a third-party router live in `tests/testdata/chimod` with their own `vendor/`.
- Don't print "No problems" for a run that checked nothing.

## Definition of done
`make test` and `make lint` pass, README output matches a real run, and `ROADMAP.md` and docs are updated when relevant.
