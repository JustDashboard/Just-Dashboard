# Databases

A control center for every database on the server, even the ones nothing has connected to yet.

<a href="../readme/databases.png"><img src="../readme/databases.png" alt="The database control center with storage, sessions, backups and every database as a card"></a>

<p align="center"><sub><b>Control center</b> — every engine on the server, each as itself</sub></p>

## Every engine

- PostgreSQL, MySQL, MariaDB, SQLite, SQL Server, ClickHouse, Oracle, MongoDB, Redis
- Key browser and console for Redis, documents and pipelines for MongoDB
- Protected connections, dump backups, PostgreSQL major-version upgrades with a way back, accounts, grants and a live map
- Import from another server, kept current by logical replication on PostgreSQL until you cut over
- Alerts when a database goes down, fills its disk, runs low on connections, lags or misses a backup

## Working in the data

- Table editor with staged, reviewed edits and a SQL editor with plans and stored programs (DELIMITER, GO and PL/SQL scripts)
- An optional SQL assistant on your own Anthropic key or local model, told the schema and never the rows
- Schema browser, diagram, code generation, mock seed data written parents first, performance

<table>
<tr>
<td width="50%" valign="top">
<a href="../readme/database-assistant.png"><img src="../readme/database-assistant.png" alt="The SQL editor with the assistant beside it, answering with statements to open or insert"></a>
<p align="center"><b>SQL assistant</b><br><sub>Ask for a statement, an error or a plan explained</sub></p>
</td>
<td width="50%" valign="top">
<a href="../readme/database-explain.png"><img src="../readme/database-explain.png" alt="A measured plan with a hash join and a sequential scan flagged as misjudged"></a>
<p align="center"><b>Measured plans</b><br><sub>Where the time went, and what the planner misjudged</sub></p>
</td>
</tr>
</table>

<table>
<tr>
<td width="50%" valign="top">
<a href="../readme/database-rls.png"><img src="../readme/database-rls.png" alt="A table's row-level security policies, tried as a role"></a>
<p align="center"><b>Row-level security</b><br><sub>Write the policies, then try them as a role</sub></p>
</td>
<td width="50%" valign="top">
<a href="../readme/database-seed.png"><img src="../readme/database-seed.png" alt="The Generate page previewing believable rows for a table"></a>
<p align="center"><b>Seed data</b><br><sub>Believable rows, written parents first</sub></p>
</td>
</tr>
</table>

## Recovery

- Point-in-time recovery for PostgreSQL: the write-ahead log archived with WAL-G, recovered to any second

## AI clients

- An MCP server for AI clients such as Claude Code, Cursor and VS Code: keys that reach only the databases you choose, read-only unless you allow changes, never a protected one, and the deployments only if you allow it — every one or the ones you pick — through 113 tools in all, 100 tools generated from the deployment API; every call audited

<table>
<tr>
<td width="50%" valign="top">
<a href="../readme/database-pitr.png"><img src="../readme/database-pitr.png" alt="Point-in-time recovery for PostgreSQL with its dumps, recoverable window and archive"></a>
<p align="center"><b>Point-in-time recovery</b><br><sub>Any second, not only the last dump</sub></p>
</td>
<td width="50%" valign="top">
<a href="../readme/database-mcp.png"><img src="../readme/database-mcp.png" alt="The MCP server dialog with its endpoint and a key"></a>
<p align="center"><b>MCP server</b><br><sub>The databases you choose, for your AI clients</sub></p>
</td>
</tr>
</table>

---

<p align="center"><a href="docker.md">← Docker</a> &nbsp;·&nbsp; <a href="../../FEATURES.md"><b>All features</b></a> &nbsp;·&nbsp; <a href="workspace.md">Workspace →</a></p>
