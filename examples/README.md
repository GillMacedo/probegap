# Examples

`shop/` is a made-up Go service that uses only the standard library. Its manifests:

- `broken.yaml`: a startup probe behind the auth middleware, a liveness probe on a path the server doesn't route, and a readiness probe on an admin server that listens on 127.0.0.1 only. probegap reports three errors and exits 1.
- `fixed.yaml`: the same Deployment with the probes fixed and `-admin-addr=:9090` in its args. It stays quiet and exits 0.
- `moved.yaml`: `fixed.yaml` with `-addr=:8000` added, so the probes on port 8080 hit nothing.

Run them with `make demo`, or see the README for the commands and their output.
