# Deployment guides

- [`Deployment creation audit`](../../audits/2026-09-22-deploy-new/README.md) — wizard state, template
  usability, encrypted draft inputs, database connections and local acceptance evidence.
- [`implementation.md`](implementation.md) — current implementation, invariants, feature joins, automation, and topology.
- [`preview-isolation.md`](preview-isolation.md) — exact-revision approval, previews tested from the dashboard, copied production variables, tailnet-only addresses, reconciliation with GitHub, storage, network and cleanup.
- [`backup-coverage.md`](backup-coverage.md) — immutable archive manifests and persistent-data coverage limitations.
- [`restore-verification.md`](restore-verification.md) — native SQLite snapshots and artifact-bound isolated application recovery checks.
- [`recipes.md`](recipes.md) — build variable delivery, the framework catalogue (JavaScript, Python, Go, Rust, Java, .NET, Deno, PHP), environment discovery, Procfiles, where a server listens and whom it trusts behind the proxy, repository shape and candidate selection, other platforms' deployment files, background processes, ecosystems without a recipe, submodules and LFS, the single-page fallback and toolchain selection.
- [`git-policy.md`](git-policy.md) — shared polling/hook policy, manual-only mode, complete path comparison and decision evidence.
- [`database-networks.md`](database-networks.md) — logical database URLs, owned networks, replacement reconciliation and cleanup.
- [`request-observability.md`](request-observability.md) — what a deployment served: ingress access
  logs, the request readings, container lifecycle events, and the three container-output fixes.
- [`notifications.md`](notifications.md) — run observers, Discord/Slack/Telegram/e-mail/webhook channels, delivery history, GitHub commit statuses and pull request comments.
- [`github-app.md`](github-app.md) — the dashboard's GitHub App: manifest flow, installation credentials for clones, App-delivered triggers, statuses and pull request comments.
- [`gap-closure-plan.md`](../../audits/2026-09-16-deployments/gap-closure-plan.md) — second-pass re-evaluation, defects D1–D7, competitive position and remaining phases.
- [`third-pass-plan.md`](../../audits/2026-09-16-deployments/third-pass-plan.md) — third-pass review: defects D10–D16, blueprint deployment, native database dumps, delivery insights, notification retries, and evidence.
- [`2026-09-16 deployment audit`](../../audits/2026-09-16-deployments/README.md) — point-in-time capability map, fresh test evidence, confirmed gaps, competitor comparison, and prioritized acceptance criteria.
- The historical `docs/plans/0.6.7-deployments/` directory is absent. Missing frozen contracts and checkpoint evidence have not been reconstructed or verified.
- [`permanent-deletion.md`](permanent-deletion.md) — archived record deletion and resource-retention contract.
