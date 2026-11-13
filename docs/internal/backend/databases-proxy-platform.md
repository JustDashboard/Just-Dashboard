# Databases, proxy, and platform services

This document was split into three on 2026-11-13; it had grown to 6,500 lines under five headings.
Nothing was rewritten: each part holds the text it held here.

- [`databases.md`](databases.md) — the Databases section: the inventory of what is on the machine,
  connections and protected connections, the capability table, the SQL workbench, schema and
  operations, the Redis and MongoDB surfaces, code generation, transfer, the MCP server, the complete
  `/databases` route table, database provisioning for deployments and the SQL handoff at tab capacity.
- [`proxy.md`](proxy.md) — nginx sites, certificates and TLS, DNS, streams and ports.
- [`platform.md`](platform.md) — streaming, jobs, secrets and agent mode; configuration, version,
  cutting a release, which build this is, signed releases, self-update, the dashboard's own settings
  (restart, rebuild, rollback, Tailscale certificates) and uninstalling it.
