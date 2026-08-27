# Documentation

Public documentation for freebuff-proxy. This folder is committed to the
repository; local-only development study (reverse-engineering notes, plans,
research) lives in the ignored `devdocs/` directory and is not part of the
public repo.

## Reference Documents (Project Root)

These top-level documents serve as the canonical design and specification references:

- [Architecture](../ARCHITECTURE.md) — System architecture, components, data flow, operating modes, invariants
- [Specification](../SPECIFICATION.md) — Full API surface, request/response contracts, error handling, behavioral rules
- [Roadmap](../ROADMAP.md) — Feature checklist, known limitations, in-flight work, future plans

## Guides

| Guide | What it covers |
|---|---|
| [Getting Started](getting-started.md) | 5-minute onboarding: get a FreeBuff token, install, pooled vs bridge mode, verify the proxy, connect your AI client, troubleshoot common 403/502 errors |
| [Client Integration](client-integration.md) | Config snippets for OpenAI-compatible clients: opencode, pi, Python/Node SDKs, Cursor, VS Code extensions, chat UIs, API routers |
| [9router Integration](9router-integration.md) | Wire the proxy into 9router as a custom OpenAI-compatible provider |
| [Dashboard Guide](dashboard.md) | The embedded admin web UI: access, pages, Docker caveats, hardening |
| [Manual Testing](testing.md) | Step-by-step verification runbook (Linux and Windows), mirroring the CI checks |
| [Bridge Mode](bridge-mode.md) | Bridge-mode architecture, invariants B1–B8, security considerations, error surfaces, hardening checklist |
| [Circuit Breaker Observability](circuit-breaker-observability.md) | `/healthz` fields, Prometheus metrics, dashboard surfacing, tuning, troubleshooting |
| [Ban-Avoidance & Signature Research](ban-avoidance.md) | Upstream detection landscape, proxy countermeasures, risk engine, operator hygiene rules, research plan |
| [Upstream Drift Tracking](upstream-drift-tracking.md) | Pinned registry snapshots, drift detection/sync scripts, CI integration, response playbook, runtime freshness |
| [Automated Token Rotation](automated-token-rotation.md) | Token lifecycle management, health scoring, exhaustion prediction, anti-ban rotation rules, implementation plan |

## Related

- [README](../README.md): overview, quick start, full config reference
- [DESIGN](../DESIGN.md): dashboard design system (typography, layout, components)
- [CONTRIBUTING](../CONTRIBUTING.md): how to contribute to this repository