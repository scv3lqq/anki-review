**E1.** (lines 122-124)

````md E1-old
A failing test stops the run: show it, don't build around it. When this checkout's
`bin/anki-review` already serves the port, skip `make build` if `build` says up to date,
otherwise ask: it replaces the binary under the running server.
````

````md E1-new
A failing test stops the run: show it, don't build around it. When this checkout's
`bin/anki-review` already serves the port, skip `make build` if `build` says up to date,
otherwise ask: it replaces the binary under the running server.

When changing deploy scripts, find the Go tests that read or extract them, as well as the
shell/Python fixtures. Include those Go packages in scoped gates alongside `make check-deploy`;
for example, `internal/provisioner/deployment_test.go` checks installer binary layouts.
Keep extraction boundaries explicit and checked so unrelated preflight code stays outside them.
````
