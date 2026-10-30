# Overview & monitoring

See what the server is doing right now, what it did this week, and what is about to run out.

<a href="../readme/overview.png"><img src="../readme/overview.png" alt="The server overview: the machine, shortcuts to every section, live resources and the deployed projects"></a>

<p align="center"><sub><b>Overview</b> — the machine, its readings, and a shortcut to every section</sub></p>

## The whole machine at a glance

- A live overview of the machine: CPU, memory, load, network, storage and disk I/O
- Shortcuts to every section, with the open shells, the drawings edited last, the databases and every backup job one press away
- Notable moments, top processes and the day's activity at a glance

## A map of the server

- The server in the middle and everything it runs branching off it — projects, containers, databases, domains, backups, shells, accounts — wired together where the server knows they are
- Arranged however you like, and remembered

<a href="../readme/map.png"><img src="../readme/map.png" alt="The server map with a project selected, its containers, domains and databases wired together"></a>

<p align="center"><sub><b>Map</b> — everything the server runs, and what it is wired to</sub></p>

## History, alerts and forecasts

- Metrics with seven days of history the backend records itself, and CSV export
- Alerts when the CPU or memory stays high, a disk fills, a container goes down, a backup fails or a deployment's image has a fixable vulnerability a newer base would bring
- A capacity forecast: when each disk fills at the rate it has been growing, and whose volumes grow fastest

## Processes and logs

- Processes, PM2 apps, systemd services and scheduled jobs, each with its verbs
- One log viewer for files, containers, stacks, PM2 and the journal, with insights
- Every service shows its own logs on its own page

<table>
<tr>
<td width="50%" valign="top">
<a href="../readme/metrics.png"><img src="../readme/metrics.png" alt="Metrics with live readings, notable moments and top processes"></a>
<p align="center"><b>Metrics</b><br><sub>A week of history, recorded by the server</sub></p>
</td>
<td width="50%" valign="top">
<a href="../readme/logs.png"><img src="../readme/logs.png" alt="The log viewer reading a PostgreSQL log with a deadlock called out"></a>
<p align="center"><b>Logs</b><br><sub>Every source in one viewer, with insights</sub></p>
</td>
</tr>
</table>

<a href="../readme/processes.png"><img src="../readme/processes.png" alt="Scheduled work drawn over the next day, with cron jobs and systemd timers below"></a>

<p align="center"><sub><b>Scheduled jobs</b> — the next 24 hours of cron and timers, with their verbs</sub></p>

---

<p align="center"><a href="everywhere.md">← Everywhere</a> &nbsp;·&nbsp; <a href="../../FEATURES.md"><b>All features</b></a> &nbsp;·&nbsp; <a href="deployments.md">Deployments →</a></p>
