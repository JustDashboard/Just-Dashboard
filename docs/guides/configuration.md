# Configuration

[← README](../../README.md) · [Features](../../FEATURES.md) · [Install](install.md) · [Configuration](configuration.md) · [Command line](cli.md) · [Administration](administration.md) · [Linking & usage count](linking-and-usage.md) · [Security](../../SECURITY.md)

The installer writes the settings that matter, and **Settings → Configuration** edits them afterwards. This is the full list, for tuning.

**Required**

| Variable | Description |
| --- | --- |
| `JD_MASTER_KEY` | 64 hex characters. Encrypts every stored secret. `openssl rand -hex 32`. |

**Network perimeter**

| Variable | Default | Description |
| --- | --- | --- |
| `JD_SITE` | `localhost` | The address the stack answers on and the name on its certificate. Your machine's MagicDNS name (`box.tailnet-name.ts.net`) is the recommended value. Loopback is bound alongside it either way, so an SSH tunnel always works. Never `0.0.0.0`. |
| `JD_BIND` | none | What the proxy listens on, when that is not the same string as `JD_SITE`. Blank uses `JD_SITE`. A Tailscale install answers for a MagicDNS name and binds the tailnet IP behind it, because the proxy container resolves names through Docker's resolver rather than the host's. |
| `JD_TLS` | `internal` | How the connection is trusted. `tailscale` serves the certificate `tailscale cert` issues for `JD_SITE` — publicly trusted, no browser warning, renewed automatically. `internal` is Caddy's own CA, which is what produces "not secure". `off` is plain HTTP and is refused on anything but loopback; the API also accepts same-origin HTTP WebSockets for live features in this mode. |
| `JD_ALLOWED_CIDRS` | `127.0.0.1/32,::1/128` | Who may reach the API at all, checked before authentication. Use `100.64.0.0/10,127.0.0.1/32,::1/128` for Tailscale, and keep loopback or you lose the tunnel. |
| `JD_TRUSTED_PROXIES` | none | Addresses allowed to set `X-Forwarded-For`. Without it a client could spoof its way past the allowlist. One hop is supported: the bundled Caddy replaces the header with the client's real address, so anything placed *in front* of Caddy becomes the client as far as the allowlist is concerned. |
| `JD_PORT` | `8443` | The port you connect to — the only one the proxy publishes. |
| `JD_BACKEND_PORT` | `8080` | The API's loopback port, behind the proxy. `install.sh` picks a random high port on a new install so nothing collides with it. |
| `JD_FRONTEND_PORT` | `3000` | The UI's loopback port, behind the proxy. Likewise randomised at install time. |
| `JD_ADDR` | `127.0.0.1:$JD_BACKEND_PORT` | Where the API binds. `docker-compose.yml` sets it from `JD_BACKEND_PORT` and does not read it from `.env`, so change the port there; it is an override only when you run the binary yourself. Leave it on loopback; the proxy is the entry point. |
| `JD_ALLOWED_ORIGINS` | none | Complete browser origins allowed to open WebSockets. Scheme, host, and port must match; only needed if the UI is served from a different origin. |

**Behaviour**

| Variable | Default | Description |
| --- | --- | --- |
| `JD_REQUIRE_2FA` | `false` | Whether an account with no authenticator may sign in. An account that *has* enrolled is always asked for its code, whatever this says; the one exception is [hub sign-in](linking-and-usage.md#signing-in-from-just-hub), where `JD_HUB_SIGNIN` is on and the account opted in. |
| `JD_TERMINAL_ENABLED` | `true` | The web terminal. |
| `JD_TERMINAL_SHELL` | account's shell | The shell the web terminal opens. Empty honours `chsh`. `install.sh` sets it to the host's zsh, which the bundled startup gives inline history suggestions and command colouring; ssh is unaffected. The account's own login shell still reads its profile first, so a window finds the commands ssh finds. |
| `JD_TERMINAL_USER` | lowest regular account | Host account a terminal session logs in as. |
| `JD_REMOTE_DESKTOP_BRIDGE` | none | A Moonlight Web stream bridge on another tailnet machine, as `host:port`, used in place of the container this server would run. Remote Desktop's picture always passes through the bridge; one near you and your computers spares it the trip to this server and back. The bridge must trust the dashboard's account header from this server only. |
| `JD_SESSION_TTL` | `12h` | Absolute session lifetime. |
| `JD_SESSION_IDLE_TTL` | `60m` | Idle timeout. |
| `JD_DOCKER_HOST` | `unix:///var/run/docker.sock` | Docker Engine endpoint. The containers that update, restart and uninstall the dashboard are started on it too, and those operations are refused, with nothing changed, while it does not answer. |
| `JD_DEPLOY_HEAVY_SLOTS` | `1` | Concurrent deployment build/activation workers. Valid range: 1–8. |
| `JD_DEPLOY_LIGHT_SLOTS` | `2` | Concurrent validation/metadata deployment workers. Valid range: 1–8. |
| `JD_DEPLOY_LEASE_TTL` | `30s` | Time before a silent deployment worker claim is reconciled after a crash. Valid range: 5s–5m. |
| `JD_DEPLOY_HISTORY_RUNS` | `200` | How many of each environment's newest runs keep everything they printed. An older run keeps the last 200 lines of its output and all the rest of its record — its steps, its diagnosis, its variables, its release — so history, a retry and a rollback read as before; only the bulk of an old build log is removed. The run that built what is live, the runs behind pinned releases and the runs behind the releases a rollback can still reach keep their whole output whatever their age. Applied a minute after the dashboard starts and every hour. `0` keeps every run whole. An install updated from a version without this setting has its older runs cut by the first pass, and removed output cannot be brought back: to keep it, set `0` before updating. The database file does not shrink when output is removed; the space is used again by what is written next. Valid range: 0–100000. |
| `JD_METRICS_INTERVAL` | `15s` | How often the backend samples the host, and every running container, into its own history. Clamped to 5s and 5m. |
| `JD_METRICS_RETENTION` | `7d` | How long that history is kept — and with it the sizes of the volumes deployments own, sampled every 15 minutes for the capacity forecast. Right-sizing needs a day of it before it suggests a limit. Accepts days. `0` records nothing and leaves only the live feed. A deployment's container events (exits, OOM kills, restarts, health changes) are kept for 30 days or this, whichever is longer, whatever this is set to. |
| `JD_UPDATE_CHECK` | `true` | Whether the dashboard may ask GitHub whether a newer version of *itself* exists — one unauthenticated GET of one file, every two hours and at most every five minutes while pages are being opened, carrying nothing but a version number. `false` turns it off; the release notes for the version you run stay readable, since they are compiled in. |
| `JD_ACME_DIRECTORY` | Let's Encrypt | Another ACME directory to order deployment certificates from: Let's Encrypt's staging endpoint for a rehearsal that spends no rate limit, or a private authority on a network that never sees the internet. Both the managed Caddy and certbot follow it — the Certificates page's orders and a deployment run's, over HTTP-01 and DNS-01 alike — and certbot registers an account with that directory rather than reusing one it has with another. |
| `JD_ACME_CA_ROOT` | system roots | A PEM bundle to trust when that authority signs with its own roots — its issuing roots, and the root behind its directory's TLS listener if that is private too. Caddy is given it as its trusted roots and certbot as `REQUESTS_CA_BUNDLE`, so keep it where both read it (under `/etc`, `/srv`, `/opt` or the data directory, which the stack mounts at the same paths). With it set, the public-DNS check before an order is skipped, because a private authority validates however it was set up to. |
| `JD_UPDATE_REPO` | `JustDashboard/Just-Dashboard` | The repository releases are read from. With the default, a version is offered and installed only once it has a release tag `v<version>` there, which the project makes after the version's checks pass. The code itself comes from the `origin` of your checkout, so if you cloned a fork, name the fork here; left on the default, the update refuses a version whose tag the fork does not have. Change it to follow a fork: a fork's tagged versions are installed exactly too, and a version it has not tagged is installed from the head of `JD_UPDATE_BRANCH`, which the Version page says before you install. |
| `JD_UPDATE_BRANCH` | `main` | The branch whose changelog says which versions exist. Only a fork is ever updated to this branch's head; see `JD_UPDATE_REPO`. |
| `JD_ACCOUNT_LINK` | `true` | Whether this server may be linked to a just-dashboard.com account. Linking is always something an administrator does; `false` removes the Settings page's controls, the installer's question and the terminal command, and silences a link that already exists, so nothing is ever sent. |
| `JD_ACCOUNT_URL` | `https://just-dashboard.com` | The site this install may link to: an origin only, with no path, query or credentials. `https://` everywhere; `http://` only for `localhost`, `127.0.0.1` or `[::1]`, to develop the site beside the dashboard. Anything else refuses to start. |
| `JD_HUB_LINK` | `true` | Whether this server may be [connected to a Just Hub](linking-and-usage.md#connecting-to-just-hub) on its own tailnet. Connecting is always something an administrator does; `false` removes the hub's discovery answer, the Settings page's controls and the terminal command, and silences a connection that already exists, so nothing is ever sent. Independent of `JD_ACCOUNT_LINK`. |
| `JD_HUB_SIGNIN` | `false` | Whether the Just Hub this server is connected to may [sign people in](linking-and-usage.md#signing-in-from-just-hub): open this server for an account that chose "Let Just Hub sign me in" in **Settings → Hub**, without this server asking for its own code. Anyone who takes over the hub could then sign in as those accounts, with what each may do (the Files page and, for an administrator, the terminal included), so a session opened this way lasts at most two hours, the dashboard's own forms refuse it keys, accounts, passwords, revealed secrets, links and **Settings → Configuration**, and the audit log marks what it does. Counts as off while `JD_HUB_LINK` is `false`; found off at a start, every such choice and session is ended before anything is served. |
| `JD_USAGE_PING` | `true` | Whether an administrator may be asked to [count this server](linking-and-usage.md#the-usage-count) in the public install numbers. Nothing is sent until one says yes. `false` means nobody is asked and nothing is ever sent; `JD_ACCOUNT_LINK=false` does the same. |
| `JD_UPDATE_DIR` | discovered | The directory you cloned into. Normally empty: the dashboard asks Docker where its own stack was deployed from. Set it only if the Updates page says that failed. |
| `JD_AGENT_MODE` | `false` | Run as an agent managed by a hub: no login, mutual TLS only. Not useful on its own yet. |
| `JD_DEV` | `false` | Development only. Drops `Secure` from the session cookie, which is then `jd_session` rather than `__Host-jd_session`, so the UI works over plain HTTP. Never set it on a real host. |
| `JD_LOG_LEVEL` | `info` | `debug` for verbose logs. |

**Where it looks**

| Variable | Default | Description |
| --- | --- | --- |
| `JD_FILE_ROOTS` | `/` | Directories the file manager may reach. |
| `JD_LOG_ROOTS` | `/var/log` | Directories the log viewer may read. |
| `JD_COMPOSE_ROOTS` | `/opt,/srv,/home` | Where compose stacks are discovered. |
| `JD_GIT_ROOTS` | `/opt,/srv,/home,/root` | Where the Git page looks for repositories. |
| `JD_DEPLOY_ROOTS` | `/opt,/srv,/home,/root` | Where a deploy project's repository may live. A project outside these is refused. |
| `JD_NGINX_DIR` | `/etc/nginx` | nginx configuration root. |
| `JD_CADDYFILE` | `/etc/caddy/Caddyfile` | Caddy configuration file. |
| `JD_INGRESS_CONTAINER` | — | Name of the Caddy container deployment domains are served through, when it is not one publishing 80 and 443 on every address (discovered otherwise). |
| `JD_BACKUP_DIR` | `/var/backups/just-dashboard` | Local backup destination and staging. |
| `JD_DB_UPLOAD_MAX_MB` | `2048` | Largest file one database import or uploaded dump may be, in MiB. |
| `JD_DATA_DIR` | `/var/lib/just-dashboard` | The dashboard's own database. **Back this up.** |

**First run only**

| Variable | Default | Description |
| --- | --- | --- |
| `JD_BOOTSTRAP_USER` | `admin` | Account created on an empty database. |
| `JD_BOOTSTRAP_PASSWORD` | none | Leave empty for a generated one, logged once and replaced at first sign-in. A password set here is kept as is. `install.sh` removes the one it writes from `.env` once the dashboard has answered; one you set yourself stays until you remove it. Neither is read again once an account exists. |

Durations take a unit (`12h`, `60m`, and the metrics settings also accept `7d`); booleans
take `true` or `false`. A value that cannot be parsed stops the dashboard at startup rather
than falling back to the default, so a typo is visible instead of silently in effect.

## Running it locally, or setting it up by hand

```bash
cd backend && go run ./cmd/server      # API on :8080
cd frontend && bun install && bun dev  # UI on :3000
```

Set `NEXT_PUBLIC_WS_BASE=http://localhost:8080` for WebSockets and `JD_ALLOWED_ORIGINS=http://localhost:3000`
on the backend.

Without the installer: `cp .env.example .env`, set `JD_MASTER_KEY` (`openssl rand -hex 32`),
`JD_SITE` and `JD_TLS`, then `docker compose up -d --build`. The generated admin password is
printed once in `docker compose logs backend`.

| Script | What it is for |
| --- | --- |
| `scripts/test-changed.sh` | Runs exactly the checks, Go tests and browser specs your diff can reach — run it before a pull request |
| `scripts/go-test-race.sh` | Runs Go packages under the race detector, split across processes |
| `scripts/release.sh <version>` | Cuts a release: bumps the version everywhere and regenerates the changelog from `backend/internal/selfupdate/changelog.json` |

See [CONTRIBUTING.md](../../CONTRIBUTING.md) before opening a pull request.
