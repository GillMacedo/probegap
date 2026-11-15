# probegap plugin

This directory is the Claude Code plugin for probegap, which checks the httpGet and tcpSocket liveness, readiness and startup probes in Kubernetes manifests against the servers a Go program really starts. `skills/checking-kubernetes-probes-against-go-servers/SKILL.md` is the skill, and `scripts/` is the Go program it builds. See the [repository README](https://github.com/GillMacedo/probegap#readme) for examples and limits.

Install it with:

    /plugin install probegap --marketplace GillMacedo/probegap
