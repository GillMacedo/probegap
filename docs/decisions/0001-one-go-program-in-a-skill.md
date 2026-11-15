# 1. One Go program in a skill

Date: 2026-11-15

## Decision
probegap is a Claude Code skill with one Go program, built with `go build` from the plugin's `scripts/` directory. It isn't an MCP server.

## Why
The work is one command over files and a source tree, with a text or JSON report. A skill with a script gives Claude everything it needs. The program uses only the standard library and a copied YAML parser, so it builds with no go.mod and no network.
