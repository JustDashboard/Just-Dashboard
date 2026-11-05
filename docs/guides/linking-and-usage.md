# Linking and the usage count

[← README](../../README.md) · [Features](../../FEATURES.md) · [Install](install.md) · [Configuration](configuration.md) · [Command line](cli.md) · [Administration](administration.md) · [Linking & usage count](linking-and-usage.md) · [Security](../../SECURITY.md)

Two things that talk to just-dashboard.com, and one that talks to a Just Hub on your own tailnet. All are
optional, and none sends anything until an administrator says so.

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

## Connecting to Just Hub

A Just Hub is a separate app that runs on its own machine on your tailnet and shows many dashboards in one
place: their health and version, a switcher between them in each dashboard, updates across them. Connecting a
server is optional and off until you do it, and it is a separate thing from linking to just-dashboard.com: a
server can be linked to both, and the two never share a key, a counter, a setting or a log name.

Connect from the dashboard's Settings, or over SSH:

```bash
sudo ./scripts/manage.sh hub connect hub.example.ts.net            # prints a code to approve in the hub
sudo ./scripts/manage.sh hub connect hub.example.ts.net --scope operate
sudo ./scripts/manage.sh hub status
sudo ./scripts/manage.sh hub disconnect
```

`hub connect` prints the hub's machine and its owner (or "not checked" when Tailscale could not be asked), a code
and this server's key fingerprint, and waits (15 minutes by default, `--wait` up to an hour) while the hub's owner
types that code into the hub. Once connected it prints the hub's key fingerprint; compare it with the one on the
hub's Settings page. The hub's own installer and its "Add by command" screen give a one-time token instead, which
approves the request in advance. A token is only ever given on standard input, with `--token-stdin`: at a terminal
the command asks `Link token:` and does not echo what you paste, and from a pipe it reads one line, so the token
is in no process list and no shell history. There is no option that takes it as an argument. The command exits 0
once connected, 2 if the request was declined, not approved in time or interrupted, and 1 for anything else.

**The hub must be on this server's tailnet.** The address you give must be one machine directly under this
server's own MagicDNS name (`hub.example.ts.net` beside `server.example.ts.net`), never this server's own name,
and the dashboard reaches it only over the tailnet: it never dials a hub at an address outside Tailscale's
range. If tailscaled can be asked, the dashboard also checks that the address belongs to the machine of that
name, shows that machine and its owner before you connect, and remembers it: from then on it sends nothing to
another machine that takes the hub's name (a hub machine deleted and its name reused), and the connection goes
quiet until you disconnect and connect again. If the server later moves to another tailnet, the connection
likewise stays stored but goes quiet, and Settings says why; disconnect and connect to a hub on the new
tailnet.

The scope works as for just-dashboard.com, with the table above, except that the default is `maintain`: a hub
is yours and on your own network, and keeping servers updated is what it is for. Change it any time in
Settings. Every command from the hub, run or refused, is written to the audit log as `hub:<hub name>`, and its
destructive actions draw on their own budget. The hub never opens a connection to the dashboard, with one
exception before you connect: it may ask `GET /api/v1/hub/hello` to find dashboards on the tailnet, which is
behind your network allowlist and answers only whether this server can be connected, never its version or
name.

Disconnect from Settings, with `sudo ./scripts/manage.sh hub disconnect`, or by removing the server in the hub.
`JD_HUB_LINK=false` switches the feature off entirely: no discovery answer, no page controls, no command, and a
connection that exists goes quiet. The Settings page shows the exact body of the last report the hub was sent.

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
