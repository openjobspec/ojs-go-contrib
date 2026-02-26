# Open Job Spec — Go Contrib

[![CI](https://github.com/openjobspec/ojs-go-contrib/actions/workflows/ci.yml/badge.svg)](https://github.com/openjobspec/ojs-go-contrib/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/openjobspec/ojs-go-contrib.svg)](https://pkg.go.dev/github.com/openjobspec/ojs-go-contrib)

Community framework integrations for the [OJS Go SDK](https://github.com/openjobspec/ojs-go-sdk).

## Provided Integrations

| Status | Integration | Description |
|--------|-------------|-------------|
| alpha  | [Chi](./ojs-chi/README.md) | Chi router middleware — the same router used by OJS backends |
| alpha  | [Gin](./ojs-gin/README.md) | Gin web framework middleware and request-scoped OJS client |
| alpha  | [Echo](./ojs-echo/README.md) | Echo web framework middleware and context integration |
| alpha  | [Fiber](./ojs-fiber/README.md) | Fiber web framework middleware and Locals-based client access |
| alpha  | [GORM](./ojs-gorm/README.md) | Transactional job enqueue via GORM after-commit hooks |
| alpha  | [Serverless](./ojs-serverless/README.md) | AWS Lambda handler adapter for SQS-based job processing |

Status definitions: `alpha` (API may change), `beta` (API stable, not battle-tested), `stable` (production-ready).

## Getting Started

Install any integration package:

```bash
go get github.com/openjobspec/ojs-go-contrib/ojs-gin
```

Each package includes an `examples/` directory with a complete working demo using Docker Compose.

## Contributing

See [CONTRIBUTING.md](./CONTRIBUTING.md) for guidelines on adding new contrib packages.

## License

Apache 2.0 — see [LICENSE](./LICENSE).

