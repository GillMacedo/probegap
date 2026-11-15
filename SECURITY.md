# Security

Report vulnerabilities through [GitHub's private vulnerability reporting](https://github.com/GillMacedo/probegap/security/advisories/new), not in a public issue.

`probegap` compiles the Go program you point it at and runs it until its servers start: package `init` functions and `main` up to that point run on your machine, in an empty temporary directory with the container's environment plus `PATH`, `HOME` and `TMPDIR`, and are stopped after `--wait` and the probes' timeouts. Listens through `net.Listen` are faked, so no port is opened, but outgoing connections the program makes at startup are real. Run it only on code you trust, as you would `go test`. It never connects to a cluster. Values from Secrets are never read; they are left unset and named in notes.

A missed or wrong finding is a normal bug. If `probegap` opens a port, changes a file outside its temporary directory, prints a Secret value, or reports a clean result for input it couldn't read, report it privately. `probegap` is a diagnostic aid and doesn't enforce anything.
