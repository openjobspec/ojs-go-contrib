# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.9.0] — 2026-02-20

Release candidate for v1.0.

### Stabilized

- **ojs-chi** — Chi router middleware and worker manager. API surface finalized.
- **ojs-gin** — Gin web framework middleware and worker manager. API surface finalized.
- **ojs-echo** — Echo web framework middleware. API surface finalized.
- **ojs-fiber** — Fiber web framework middleware. API surface finalized.
- **ojs-gorm** — GORM transactional enqueue plugin and outbox publisher. API surface finalized.
- **ojs-serverless** — AWS Lambda handler adapters (SQS, API Gateway, EventBridge). API surface finalized.

### Added

- **ojs-chi** — Chi router middleware and worker manager. Chi is the same router used by the official OJS backends, making this a natural fit for projects aligned with the OJS server stack.
