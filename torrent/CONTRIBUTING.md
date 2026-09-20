# Contributing

## Setup

```sh
git clone https://github.com/natalie-o-perret/go-torrent.git
cd go-torrent
```

Requires Go 1.24+ and [golangci-lint](https://golangci-lint.run/welcome/install/).

## Workflow

```sh
go test -race ./...          # run tests
golangci-lint run ./...      # lint Go files
```

All checks run in CI on every PR. A PR must be green before merging.

## Commit messages

Commits must follow [Conventional Commits](https://www.conventionalcommits.org/):

```text
type(scope?): short description
```

Allowed types: `feat` `fix` `docs` `style` `refactor` `perf` `test` `build` `ci` `chore` `revert`

```text
feat: add UDP tracker support
fix(peer): handle keepalive messages
docs: document piece block size constant
refactor(bencode): simplify dict decoder loop
```

The PR title is linted in CI.

`BREAKING CHANGE:` in the commit footer signals a semver major bump.
