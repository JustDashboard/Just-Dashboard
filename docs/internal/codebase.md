# Internal codebase guide

The codebase reference is organized by subsystem so contributors can load the guidance relevant to a
change without navigating one monolithic file. Start at [`README.md`](README.md) for the complete map.

The main entry points are:

- [`overview.md`](overview.md) for repository purpose, commands, and test strategy.
- [`architecture/`](architecture/) for request handling, runtime composition, host execution, paths,
  authentication, secrets, and persistent state.
- [`backend/`](backend/) for every feature backend and cross-feature platform service.
- [`deployments/`](deployments/) for the checked-in deployment architecture and delivery limits.
  The historical `docs/plans/0.6.7-deployments/` directory is absent from this checkout.
- [`frontend/`](frontend/) for the application shell, design system, feature panels, terminal, data, and
  theming.
- [`security/invariants.md`](security/invariants.md) for the security and compatibility invariants that
  must not regress, including the typed-confirmation policy.
- [`contributing/conventions.md`](contributing/conventions.md) for code and commit conventions.

This file intentionally remains as a stable link target for older plans and contributor references. New
links should point to the specific document or to [`README.md`](README.md).
