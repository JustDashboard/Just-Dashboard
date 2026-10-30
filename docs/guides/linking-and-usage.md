# Linking and the usage count

[← README](../../README.md) · [Features](../../FEATURES.md) · [Install](install.md) · [Configuration](configuration.md) · [Command line](cli.md) · [Administration](administration.md) · [Linking & usage count](linking-and-usage.md) · [Security](../../SECURITY.md)

Two things that talk to just-dashboard.com. Both are optional, and neither sends anything until an administrator says so.

## Linking to just-dashboard.com

Optional, and off until you do it. A linked server shows up on your [just-dashboard.com](https://just-dashboard.com)
account with its health, version and the way to reach it, so you can find your dashboards in one
place. Link it from **Settings → Connect**, from the question in `install.sh`, or over SSH
with `sudo ./scripts/manage.sh link`; all three run the same code. The server asks the site for a short
code, you enter it on the site while signed in, and the server learns it was approved. Nothing secret is
typed or printed: the code only points at the request, and completing the link needs the key this
server keeps sealed in its own store.

You choose the **scope** when you link, and can change it any time in Settings:

| Scope | The site sees | The site can also |
| --- | --- | --- |
| `observe` (default) | Version and update state, OS, kernel, architecture, uptime, the address you reach the dashboard at (tailnet name or SSH tunnel port), CPU, memory, disk, load and network readings, container and backup counts, alerts firing | Ask for a fresh reading |
| `operate` | All of that, plus the names of containers, stacks, backup jobs and the last-deployed project | Start, stop or restart a container, restart a compose stack, run a backup job |
| `maintain` | Same as `operate` | Also install a dashboard update, set an automatic-update policy (evaluated here, in your server's time zone), restart the dashboard |

Never sent, at any scope: `.env`, the allowlist, accounts, API keys, file paths beyond mount points,
logs, connection strings, deployment variables or backup credentials. The Settings page shows the exact
body of the last report.

Everything is outbound over HTTPS: the dashboard is not made reachable and the site never connects to it.
A command from the site runs only if its signature verifies against the site key pinned when you linked,
it has not expired or been seen before, and it fits the scope you chose here; every one, run or refused,
is written to the audit log as the site account. Unlink from Settings, with
`sudo ./scripts/manage.sh unlink`, or by removing the server on the site. `JD_ACCOUNT_LINK=false` switches
the feature off entirely: no page controls, no installer question, no command, and a link that exists
goes quiet. `JD_ACCOUNT_URL` names the site, an origin only (`http://` just for `localhost`).

## The usage count

An administrator is asked once, on the Overview, whether this server may be counted in the public install
numbers at [just-dashboard.com/stats](https://just-dashboard.com/stats). **Nothing is sent until somebody says
yes.** You can change the answer at any time under **Settings → Configuration**, which also prints the report
exactly as it is sent.

A server that says yes sends one request a day, and this is all of it:

```json
{"version":"0.8.2","usedToday":true,"usedThisWeek":true}
```

The two flags say whether anybody signed in during the last 24 hours and the last 7 days. There is no ID and no
hostname, so a server cannot be followed from one day to the next, and the number counts servers rather than
people. The site sees the address a request comes from, as any server does; it adds the report to that day's
totals and keeps no address.

`JD_USAGE_PING=false` means nobody is asked and nothing is ever sent, and silences an earlier yes.
`JD_ACCOUNT_LINK=false` does the same.
