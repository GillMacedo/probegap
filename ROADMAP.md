# Roadmap

## Next
- gRPC probes: check that the port serves `grpc.health.v1.Health` and that the probe's `service` name is registered
- Read probes from docker-compose `healthcheck` lines that call curl or wget
- Report a liveness probe that calls the same handler as readiness, and handlers that check a database on liveness

## Later
- Servers that don't use net/http, such as fasthttp and Fiber
- A GitHub Action that renders a chart and comments the findings on a pull request
- `--image-ref`: check out the commit an image was built from before building
