# Releasing pkg/kit and pkg/llm

`pkg/kit` and `pkg/llm` are Go modules of their own inside this repository.
The application builds against the working copy through the root `go.work`;
everything outside it builds against the version the root `go.mod` pins. That
includes `go install github.com/imyousuf/CodeEagle/cmd/codeeagle@latest`, which
ignores `go.work`, refuses `replace` directives, and (the root having no semver
tags) builds main's HEAD.

**There are no tags until the owner decides on them.** Consumers read the
default branch, so `@latest` is the pseudo-version of main's HEAD, and the
application pins a pseudo-version too. Consumers pin exact versions before
they ship; that is their decision, not a release here.

## Pinning, for every change to pkg/kit or pkg/llm

1. **Before a module's first `go mod tidy`,** give it the application's exact
   versions of anything it shares, or tidy resolves them at @latest and minimal
   version selection raises the application to match. The simplest way is to
   copy the root's `require` lines in and let tidy drop what is unused:
   `GOWORK=off go mod tidy` inside the module. `scripts/check-root-deps.sh`
   lists any third-party requirement of the application that moved.
2. **Pin last,** once the branch is otherwise done and pushed:
   - if pkg/kit's own requirement on pkg/llm must move, inside `pkg/kit`:
     `GOWORK=off GOFLAGS=-mod=mod go get github.com/imyousuf/CodeEagle/pkg/llm@<sha>`,
     then commit and push;
   - at the root: `GOWORK=off GOFLAGS=-mod=mod go get github.com/imyousuf/CodeEagle/pkg/kit@<sha> github.com/imyousuf/CodeEagle/pkg/llm@<sha>`,
     then `GOWORK=off go mod tidy`, and commit ("Pin the application to pkg/kit <sha12>").
   Each `<sha>` is a pushed commit whose copy of the module is the final one.
3. **Bring main into the branch by merging, never by rebasing,** and merge the
   pull request with a merge commit (the repository allows nothing else). A
   rebase or squash leaves the pinned commit outside main's history.
4. **`make modules-check`** (also run by CI) builds each module as a consumer
   does, with `GOWORK=off` and `CGO_ENABLED=0`, rejects imports of application
   code and cgo modules, and runs `scripts/check-module-pins.sh`, which fails
   when a pinned commit is not in the history or the module has changed since
   it. Until the pin step, the out-of-workspace build of the application fails
   for a module the root does not require yet; that is expected on a branch
   and fixed by step 2.
5. **After merging,** check what consumers resolve:
   `GOPROXY=direct go list -m github.com/imyousuf/CodeEagle/pkg/kit@main`.
   The public proxy caches `@latest` for a while, so consumers wanting the
   newest code use `@main` or the SHA.

## What belongs in the kit

Generic pieces another program can reuse through interfaces it implements:
the graph types and a conformance suite for stores, vector search over a
persistence seam, hybrid search, bounded judgments, Markdown sections and list
items, topics and relations. CodeEagle's own policy stays in the application:
which node types are embedded and how, its Badger stores, its prompts, its
meeting-specific profiles.

The kit must build with `CGO_ENABLED=0` and must never require the application
module.
