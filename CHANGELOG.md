# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Initial project scaffold: monorepo structure, Go module, licensing
- Provider interface (`internal/providers`) with contract test suite
- Mock provider implementation for unit testing
- CLI skeleton with cobra commands: `login`, `request`, `status`, `approve`, `deny`, `exec`, `shell`, `revoke`, `audit`, `policy`, `server`
- Control plane skeleton with gRPC + REST API (grpc-gateway)
- Protobuf API definitions (`api/proto/jitsudo/v1alpha1/`)
- Docker Compose local development environment (jitsudod + PostgreSQL + dex)
- Architecture Decision Records (ADR-001 through ADR-010)

[Unreleased]: https://github.com/jitsudo-dev/jitsudo/compare/HEAD
