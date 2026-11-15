# 3. Refuse input it can't trust

Date: 2026-11-15

## Decision
probegap exits 2, with nothing on stdout, when it can't read all of its input or the program didn't get far enough to check: empty files, failed command output, unrendered templates, files cut inside a container, probes with no handler or port, programs that don't build, exit, crash or start no listener, and runs with no probe to check. `--fail-on` doesn't change this.

## Why
A tool that prints a clean result for a manifest it couldn't read, or a program that never started, is worse than none. Exit 2 can't be mistaken for success in CI or by an agent.
