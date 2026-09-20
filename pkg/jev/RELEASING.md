# Releasing pkg/jev

`pkg/jev` is its own Go module inside this repository. The application builds
against the working copy through the root `go.work`; everything outside the
repository — including `go install github.com/imyousuf/CodeEagle/cmd/codeeagle@latest`
and `make tidy` — resolves it through a tag instead, so a release is a tag
followed by a bump.

1. At the commit to release: `cd pkg/jev && go test ./...` and `make lint` pass, and if `DefaultModel` changed, `make jev-record` was run first (`TestCorpusProvenance` refuses a corpus answered by another model).
2. Tag the nested module — the tag name carries the directory: `git tag pkg/jev/v0.1.0 && git push origin pkg/jev/v0.1.0`.
3. Pin the application to it, outside the workspace so the tag is what resolves: `GOWORK=off go get github.com/imyousuf/CodeEagle/pkg/jev@v0.1.0 && GOWORK=off go mod tidy`.
4. Commit the root `go.mod`/`go.sum` change ("Require pkg/jev v0.1.0"). Until this has happened once, `go install …@latest` and `make tidy` cannot find the module — do it before the branch reaches main.
5. Version by semver while v0: a changed `DefaultModel` or a renamed identifier is a minor bump and a release note, because confidence thresholds tuned on the old pin drift on the new one.
6. The import path may move once, to `github.com/imyousuf/jev`, when a second external consumer appears or at v1.0.0 — whichever comes first. Say so in the release note that does it.
