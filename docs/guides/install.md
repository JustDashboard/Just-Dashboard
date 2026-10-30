# Install

[← README](../../README.md) · [Features](../../FEATURES.md) · [Install](install.md) · [Configuration](configuration.md) · [Command line](cli.md) · [Administration](administration.md) · [Linking & usage count](linking-and-usage.md) · [Security](../../SECURITY.md)

Everything `install.sh` does, what happens at first sign-in, and how to upgrade.

```bash
git clone https://github.com/JustDashboard/Just-Dashboard.git
cd Just-Dashboard && sudo ./install.sh
```

The installer asks one question that matters — how you will reach the dashboard — then does the
rest in four labelled stages:

- **Tailscale (default):** the machine stays invisible to the internet and the dashboard answers at
  `https://your-box.tailnet-name.ts.net:8443` with a real certificate.
- **SSH tunnel (fallback):** the dashboard is served on loopback only.
- It generates the master key and a first password, builds the stack and prints how to get in.
- It installs the host tools the terminal and pages rely on — `gh`, `git-lfs`, `whois`,
  `traceroute` — where they are missing.

Use `./install.sh --help` to preview the steps, or `sudo NO_COLOR=1 ./install.sh` for plain output.
Everything it asked is editable afterwards under **Settings → Configuration**.

One of its questions is optional and defaults to **No**: whether to [link this server to your
just-dashboard.com account](linking-and-usage.md#linking-to-just-dashboardcom). For unattended runs, `--link` and
`--no-link` answer it and `--link-scope observe|operate|maintain` names what the site may do
(default `observe`). The install finishes whether or not the link is approved, and
`JD_ACCOUNT_LINK=false` removes the question altogether.

**First sign-in:** every new account is asked once what it will use the server for — shipping apps,
running databases, containers, the network, security, or working on the box — and its sidebar,
Overview and search's empty list keep only those sections. Hiding a section is about the sidebar,
not access: links from other pages still open it, search still finds it, and the role decides what
the account may do. **Account → Workspace** changes the answer or asks the question again. Accounts
that existed before 0.7.3 are not asked and keep every section.

**Upgrading:** use **Update now** in the dashboard, run `sudo ./install.sh` again, or `git pull`
followed by `docker compose up -d --build`. All three keep your `.env`, database, accounts and
sessions.
