# Deployment guides

- [`Deployment creation audit`](../../audits/2026-09-22-deploy-new/README.md) — wizard state, template
  usability, encrypted draft inputs, database connections and local acceptance evidence.
- [`implementation.md`](implementation.md) — current implementation, invariants, feature joins, automation, and topology.
- [`game-servers.md`](game-servers.md) — the Minecraft templates as releases: install-time files and pinned artifacts written into a managed volume by a helper container before start, UDP published on the plan's bind address (loopback until the operator opens it), the `game_handshake` and `udp_probe` readiness checks, the reach readout and the Console, Players and Server settings pages.
- [`tailnet-apps.md`](tailnet-apps.md) — environments published on the tailnet alone, and access per person from Tailscale identity: the plan's list of logins and tags, the address from the previews' range, the gate (nginx or the dashboard's loopback Caddy) that asks the whois route about every request, the route itself, audit, and what the pages say.
- [`edge-rules.md`](edge-rules.md) — what a deployment's route decides at the proxy before the application sees a request: an ordered list of edge rules (path, method, user agent and address matches; block, status, allow or redirect), a rate limit per client address (nginx's 429 with Retry-After; host-level on the Docker Caddy ingress), CrowdSec's bans refused at the route, and the per-rule hits read from the request record.
- [`preview-isolation.md`](preview-isolation.md) — exact-revision approval, previews tested from the dashboard, Compose previews with every service isolated, copied production variables, database branches (a preview's own masked copy of a linked database), tailnet-only addresses, reconciliation with GitHub, storage, network and cleanup.
- [`backup-coverage.md`](backup-coverage.md) — immutable archive manifests and persistent-data coverage limitations.
- [`restore-verification.md`](restore-verification.md) — native SQLite snapshots and artifact-bound isolated application recovery checks.
- [`recipes.md`](recipes.md) — build variable delivery, the framework catalogue (JavaScript, Python, Go, Rust, Java, .NET, Deno, PHP), environment discovery, Procfiles, where a server listens and whom it trusts behind the proxy, repository shape and candidate selection, other platforms' deployment files, background processes, ecosystems without a recipe, submodules and LFS, the single-page fallback and toolchain selection.
- [`git-policy.md`](git-policy.md) — shared polling/hook policy, manual-only mode, promotion-only production, complete path comparison and decision evidence.
- [`staging-promotion.md`](staging-promotion.md) — the staging environment (what it copies from production and why), promoting a staging release's exact image into production with no build — and a pull request preview's approved image into staging — promotion-only production, retention of a promotion's image, removal, routes, UI and the decision record.
- [`database-networks.md`](database-networks.md) — logical database URLs, owned networks, replacement reconciliation and cleanup.
- [`data-aware-rollback.md`](data-aware-rollback.md) — where each linked database stood when a release went live, a side copy of it restored on the deployment's network for a bounded time, the comparison with the live database, the restore-in-place hand-off and the sweeps.
- [`request-observability.md`](request-observability.md) — what a deployment served: ingress access
  logs, the request readings, container lifecycle events, and the three container-output fixes.
- [`right-sizing.md`](right-sizing.md) — the memory and CPU limits each environment should have, read
  from its retained container samples (percentiles, headroom, throttling and OOM kills, verdicts, the
  apply through the configuration PUT), and the capacity forecast: days until each disk is full and the
  fastest-growing deployment volumes.
- [`log-drains.md`](log-drains.md) — copying deployments' container output and requests to OpenTelemetry
  (OTLP), Loki or a JSON-lines endpoint, and the requests as OTLP trace spans: formats, severity,
  traces, sealed headers, delivery, retries and status.
- [`secret-stores.md`](secret-stores.md) — Vault, OpenBao, Infisical, Doppler, AWS Secrets Manager and Azure
  Key Vault connections, the `${{store.NAME/path#KEY}}` reference and each kind's shape of it, when a run
  reads it, what a rollback reads, previews, routes and audit.
- [`notifications.md`](notifications.md) — run observers, chat (Discord, Slack, Teams, Mattermost, Telegram), push (ntfy, Gotify, Pushover), e-mail and webhook channels, delivery history, commit statuses and preview comments on GitHub, GitLab, Gitea and Forgejo, and what Settings and Credentials say about them.
- [`github-app.md`](github-app.md) — the dashboard's GitHub App: manifest flow, installation credentials for clones, App-delivered triggers, statuses and pull request comments.
- [`gap-closure-plan.md`](../../audits/2026-09-16-deployments/gap-closure-plan.md) — second-pass re-evaluation, defects D1–D7, competitive position and remaining phases.
- [`third-pass-plan.md`](../../audits/2026-09-16-deployments/third-pass-plan.md) — third-pass review: defects D10–D16, blueprint deployment, native database dumps, delivery insights, notification retries, and evidence.
- [`2026-09-16 deployment audit`](../../audits/2026-09-16-deployments/README.md) — point-in-time capability map, fresh test evidence, confirmed gaps, competitor comparison, and prioritized acceptance criteria.
- [`permanent-deletion.md`](permanent-deletion.md) — archived record deletion and resource-retention contract.
- [`../backend/deploy-api.md`](../backend/deploy-api.md) — the OpenAPI 3.1 description of every deployment route: how it is generated, served and kept honest, and how to regenerate the committed copy.
