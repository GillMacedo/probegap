# Changelog

All notable changes are documented here, in [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) format. This project uses [Semantic Versioning](https://semver.org/).

## [Unreleased]

## [0.1.0] - 2026-11-15

### Added
- Checks the httpGet and tcpSocket liveness, readiness and startup probes of containers in rendered Kubernetes manifests against the servers a Go program starts.
- Builds the program with `go build -overlay` hooks in `net.Listen` and `http.Server.Serve`, runs it with the container's args and env without opening any port, and sends each probe's request to the real handler the way the kubelet does.
- Reports 404, 405, 401 and 403 and other 4xx answers, ports nothing listens on, servers on localhost only, HTTP and HTTPS mismatches, httpGet probes on non-HTTP ports, missing named ports and probes the API server rejects; warns on 5xx, timeouts and panics.
- Exit 2 with no result for input it can't trust: failed builds, programs that exit, crash or start no listener, empty or failed-command manifests, unrendered templates and files cut inside a container.
- Text and JSON output, `--fail-on error|warn|never`, `--container`, `--env`, `--entrypoint` and `--wait`.
- `checking-kubernetes-probes-against-go-servers` skill and plugin manifest.
