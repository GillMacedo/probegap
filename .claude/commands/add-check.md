Add a probe check to probegap for: $ARGUMENTS

1. Find the kubelet behavior it follows in `pkg/probe/http` or `pkg/probe/tcp` of kubernetes/kubernetes, or the Kubernetes API validation, and link it in the PR.
2. Add it to `judge` in `plugin/scripts/probegap.go`, with a code in kebab case, a message that says what the kubelet sees, and a fix line. If the hook must record something new, change `httpHook` or `netHook` in `plugin/scripts/hooks.go` and keep both working on Go 1.22 and the current release.
3. Add a route to `tests/testdata/cases/main.go` and a case to `probeCases` in `tests/cases_test.go`, plus a case that must stay quiet.
4. Add the code to the table in `README.md` and to the skill's list in `plugin/skills/checking-kubernetes-probes-against-go-servers/SKILL.md`.
5. Run `make test`.
