# Contributing to OtterMQ

Thanks for your interest in OtterMQ! It's a message broker written in Go that speaks AMQP 0-9-1 and aims to work with existing RabbitMQ clients. Contributions of any size are welcome: a one-line bug fix, a test, a docs correction or a new feature.

## Finding something to work on

- **[Good first issues](https://github.com/ottermq/ottermq/issues?q=is%3Aissue+is%3Aopen+label%3A%22good+first+issue%22)** are small and self-contained, and they already point at the code that needs to change. Most come with a suggested fix and a test idea.
- **[Help wanted](https://github.com/ottermq/ottermq/issues?q=is%3Aissue+is%3Aopen+label%3A%22help+wanted%22)** issues are bigger, but specified well enough that you don't need to know the whole codebase.
- The **[production readiness roadmap (#271)](https://github.com/ottermq/ottermq/issues/271)** is the big picture. It groups all open work into themed epics, has a map of the codebase, and shows how a message flows through the broker. The **Phase 0** and **Phase 1** milestones show what's most urgent.

Before you start, leave a comment on the issue saying you're working on it, so two people don't fix the same thing. If an issue looks wrong or out of date, say so on the issue. That's a useful contribution too.

## Setting up

You'll need:

- **Go** at the version in [`go.mod`](go.mod) or newer.
- **A C compiler** (`gcc` or `clang`). The user database uses `mattn/go-sqlite3`, which needs cgo.
- **Node.js 22+**, only if you work on the management UI.
- `make` and `git`.

```sh
git clone https://github.com/ottermq/ottermq.git
cd ottermq
make build          # builds bin/ottermq
make run            # runs the broker: AMQP on :5672, web API on :3000
```

Configuration comes from environment variables or a `.env` file; see [`example.env`](example.env) and [docs/configuration.md](docs/configuration.md). By default, the broker creates a `guest`/`guest` administrator. That's fine locally, but never expose a default install to a network.

## Running the tests

```sh
# Unit tests, which is what most changes need (about a minute):
go test -race ./internal/... ./pkg/... ./web/... ./config/...

# One package while you iterate:
go test -race ./internal/core/amqp/...

# End-to-end tests with the official Go client against an in-process broker (several minutes):
go test ./tests/e2e/
```

The e2e suite starts its broker on `localhost:5672`. If something already listens there, like a local RabbitMQ or another OtterMQ, the tests silently use it instead (#257), so stop it first.

Before opening a pull request, run `make test-race`. It runs the unit and e2e tests with the race detector, as CI does, and CI fails on any race report.

Please add a test for every bug fix, and check that it fails without your fix. Pure parsing and encoding bugs are easiest to cover with a table-driven unit test next to the code (see `internal/core/amqp/decode_test.go`). Protocol behaviour is clearest as an e2e test in `tests/e2e/`.

## Working on the management UI

The UI is a SvelteKit app in [`web/ui`](web/ui); its [README](web/ui/README.md) has the details.

```sh
cd web/ui
npm install
npm run dev          # http://localhost:5173, proxies /api to the broker on :3000
npm run check        # type check
npm run lint         # prettier + eslint
npm run format       # apply prettier
```

## Code style

- **Go:** `gofmt` your changes and keep `go vet ./...` clean. Follow the style of the surrounding code. Return errors rather than calling `log.Fatal` or `panic` in library code.
- **UI:** Prettier and ESLint; `npm run lint` must pass.
- Keep changes focused. Avoid reformatting or refactoring code your change doesn't need; open a separate PR for that.

## Commits and pull requests

- **Branch from `dev` and open your pull request against `dev`**, the default branch. `main` only changes when a release is made (see [Releases](#releases)). Use a short descriptive branch name, e.g. `fix/182-decode-table-error`.
- **Commit messages** follow [Conventional Commits](https://www.conventionalcommits.org/): `type(scope): summary`, for example `fix(amqp): reject truncated field values`. Common types are `fix`, `feat`, `refactor`, `test`, `docs`, `build` and `ci`. Common scopes are `amqp`, `broker`, `api`, `ui`, `cli`, `docker` and `ci`.
- **One topic per pull request.** Mention the issue it closes (`Closes #182`) so GitHub links and closes it.
- **In the PR description,** explain what was wrong, how you fixed it, and how you tested it. A short "before and after" (a failing test, then a passing one) makes review fast.
- If the change is visible to users, add a line under `[Unreleased]` in [CHANGELOG.md](CHANGELOG.md). If you're unsure how to word it, or forget, a maintainer adds it when the PR is merged: every merged change ends up under `[Unreleased]`.
- **CI** builds and tests every pull request, and also builds the Docker image. For first-time contributors, GitHub waits for a maintainer to approve the workflow run, so don't worry if checks don't start right away.

### Releases

All work is merged into `dev`. When `dev` is ready for a release, a maintainer opens a pull request from `dev` to `main` that turns the `[Unreleased]` section of the CHANGELOG into the new version's section. It is merged with a merge commit (not squashed or rebased, so `dev` and `main` keep the same history), and the merge commit on `main` is tagged `vX.Y.Z`. The tag publishes the versioned Docker images. `main` therefore always matches the latest release.

### Using AI assistants

Contributions prepared with AI coding assistants are welcome. You're responsible for the result: understand the change, run the tests yourself, and say in the PR description that an assistant was involved.

## Reporting bugs and requesting features

Use the [issue forms](https://github.com/ottermq/ottermq/issues/new/choose). For bugs, include the OtterMQ version (`git describe --tags` or the image tag), the client library you used, and the smallest steps that reproduce the problem. Logs with `LOG_LEVEL=debug` help, but **remove passwords and tokens first**.

## Security issues

Please **don't open a public issue for a new vulnerability.** Report it privately through the repository's **Security** tab ("Report a vulnerability"). Known weaknesses that are already public are tracked in the roadmap (#271) and the Phase 0 milestone.

## License

By contributing, you agree that your contributions are licensed under the project's [MIT License](LICENSE).
