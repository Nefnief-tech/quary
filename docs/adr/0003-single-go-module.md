# ADR-0003: Single Go Module

Date: 2026-10-04
Status: Accepted

## Context

The repo contains two Go binaries (panel, daemon) plus generated protobuf code. Options: one Go module at the repo root, or a `go.work` workspace with separate modules per component and a shared proto module.

## Decision

One Go module, `quarry.dev/quarry`, at the repo root. `/panel` and `/daemon` are directories within it; generated proto Go code lives under `/proto/gen`.

## Consequences

- Shared packages (`/internal/…`: path sanitization, SSRF guard, sealing) are importable by both binaries with no publish gymnastics; `go.work` sync issues disappear.
- Generated proto import paths resolve inside the module; no replace directives.
- Versioning is repo-wide rather than per-component — acceptable while panel and daemon ship in lockstep (they must agree on the gRPC protocol anyway). Revisit if components ever need independent release cadences.
