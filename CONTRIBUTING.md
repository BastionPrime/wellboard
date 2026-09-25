# Contributing to WellBoard

## Workflow

1. Every change starts with a ticket in the project tracker.
2. The team lead breaks it down and assigns an engineer.
3. The engineer implements in a separate branch, with tests.
4. A reviewer reads the diff against `main` and either approves or returns
   it with concrete findings.
5. The release engineer merges (`git merge --no-ff`) and pushes `main` and
   release tags to origin.

## Build & test

```sh
make build   # docker: golang:1.23-alpine, CGO disabled
make test    # unit tests
make lint
```

E2E tests (need a real mihomo binary): `go test -tags e2e ./test/e2e/`.

One branch — one change. `make test` and `make lint` must pass before
handover.

## What stays internal (the "don't leak" list)

This repository is published as open source. Never put into files, commit
messages, branch names, or PR descriptions:

- internal ticket/tracker numbers or links to internal chats and directives;
- internal hostnames, IP addresses, or file paths of internal machines;
- names of internal teams, agents, or role handles; private names of
  people behind the project.

Reference work via GitHub-native Issues/PRs (`#123`). New commit messages
reference GitHub issues, not internal task IDs.

## Git remotes

- Origin is GitHub (`https://github.com/BastionPrime/wellboard.git`),
  authentication via managed credentials (profile token + `~/.git-credentials`);
  never print or commit the token.
- The local bare repository is remote `mirror` — fallback and clone seed.
- Every merge to `main` and every release tag is pushed to origin immediately,
  in the same session where it was made. "Done" without a push to origin is
  not done.
- Force-push is forbidden; history is never rewritten. If internal data leaks
  into a pushed tree, fix the HEAD with a new commit and report to the owner.

## Definition of done

- `make test` / `make lint` green;
- for packaging changes: the OpenWrt package builds and installs on the
  supported releases (see `docs/COMPATIBILITY.md`);
- the change description says what problem it solves and how it was verified
  (command + output);
- no secrets and no internal-only data in the diff (see the list above);
- the merge reached origin (GitHub) — a live `git ls-remote` confirms it.
