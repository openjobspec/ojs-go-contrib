# Contributing to OJS Go Contrib

Thank you for your interest in contributing to OJS Go Contrib!

## Adding a New Integration Package

1. **Create a directory** named `ojs-{framework}/` at the repository root.

2. **Initialize the Go module:**
   ```bash
   cd ojs-{framework}
   go mod init github.com/openjobspec/ojs-go-contrib/ojs-{framework}
   ```

3. **Required files:**
   - `README.md` — Package name, installation, quick usage, API summary, link to examples
   - `go.mod` — Module declaration with `github.com/openjobspec/ojs-go-sdk` dependency
   - `{framework}.go` — Main integration code
   - `{framework}_test.go` — Unit tests (use httptest, no real OJS backend needed)
   - `examples/` — Complete working example with Docker Compose

4. **Add the module to `go.work`** at the repository root.

5. **Update the root `README.md`** status table with your new integration.

6. **Update the CI matrix** in `.github/workflows/ci.yml`.

## Package Guidelines

- Keep dependencies minimal: only the framework + OJS SDK.
- Use idiomatic patterns for the target framework.
- Provide middleware that injects an OJS client into the framework's request context.
- Include an `Enqueue` helper that retrieves the client from context and enqueues a job.
- Support graceful shutdown by integrating with the framework's shutdown hooks.
- Tests should use the framework's test utilities — no real OJS backend required.

## Example Guidelines

Each example should include:
- `docker-compose.yml` with `ojs-backend-redis` and Redis for integration demos
- `go.mod` with a `replace` directive pointing to the parent package
- `main.go` — HTTP server that enqueues jobs
- `worker.go` — Worker that processes jobs
- `README.md` — Prerequisites, setup, and run instructions

## Code Style

- Follow standard Go conventions (`gofmt`, `go vet`).
- Keep exported APIs small and focused.
- Document all exported types and functions.

## Pull Request Process

1. Fork the repository and create a feature branch.
2. Ensure all tests pass: `make test-all`
3. Ensure linting passes: `make lint`
4. Submit a pull request with a clear description.
