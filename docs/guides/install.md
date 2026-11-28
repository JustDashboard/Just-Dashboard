# Install

[← README](../../README.md) · [Features](../../FEATURES.md) · [Install](install.md) · [Configuration](configuration.md) · [Command line](cli.md) · [Administration](administration.md) · [Linking & usage count](linking-and-usage.md) · [GitHub](github.md) · [Security](../../SECURITY.md)

Everything `install.sh` does, what happens at first sign-in, and how to upgrade.

```bash
git clone --depth 1 --single-branch --branch main https://github.com/JustDashboard/Just-Dashboard.git
cd Just-Dashboard && sudo ./install.sh
```

The clone takes the one branch an install follows, without its history: about 150 MB to download where
a plain `git clone` takes about 470 MB, most of it screenshots from pull requests on branches of their
own and the evidence of past audits. Updates work from it as from a full clone, and fetch only that
branch and the release tag. To work on the code, clone without `--depth` and `--single-branch`: the
contributor checks compare a change with the branch it started from.

## Requirements

| | |
| --- | --- |
| Architecture | x86_64 (amd64) or arm64 (aarch64). Nothing else can build the images. |
| Init system | systemd. The dashboard manages services and timers through it and uses it to uninstall itself. |
| Distribution | One with systemd. The releases installed and used end to end are listed under [Supported distributions](../../README.md#supported-distributions); see [Docker on each distribution](#docker-on-each-distribution) for how setup gets Docker on them. Without systemd (Alpine, Devuan, Void) setup stops before changing anything. |
| Memory | 2 GB of memory with 2 GB of swap, or 3 GB of memory. |
| Disk | 10 GB free where Docker keeps its images (`/var/lib/docker`). |
| Access | root, and outbound HTTPS to GitHub, Docker Hub and your distribution's packages. |

The memory is for building, not for running: the running stack idles in a little over 100 MB, but setup
and every update compile the frontend on the server, which peaks between 2.5 and 3 GB. A 1 GB server can
run the dashboard and cannot install it. On a 2 GB server, add swap first:

```bash
sudo fallocate -l 2G /swapfile && sudo chmod 600 /swapfile
sudo mkswap /swapfile && sudo swapon /swapfile
echo '/swapfile none swap sw 0 0' | sudo tee -a /etc/fstab
```

`install.sh` checks these before it installs or writes anything. It **stops** on an architecture it
cannot build for and on a server without systemd. It **warns**, with the numbers it measured, when memory
plus swap is under about 3 GB or less than 10 GB is free, and goes on; if the build then fails it says so
again, because that is almost always the reason.

## What the installer does

The installer asks one question that matters — how you will reach the dashboard — then does the
rest in four labelled stages:

- **Tailscale (default):** the machine stays invisible to the internet and the dashboard answers at
  `https://your-box.tailnet-name.ts.net:8443` with a real certificate.
- **SSH tunnel (fallback):** the dashboard is served on loopback only.
- It generates the master key and a first password, builds the stack and prints how to get in.
  The password is removed from `.env` once the dashboard has answered, so a password you generated
  is shown only that once.
- It installs the host tools the terminal and pages rely on — `gh`, `git-lfs`, `whois`,
  `traceroute`, `ip`, `ss` and `tc`, `tcpdump`, `last` — and Docker, where they are missing.

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

### Docker on each distribution

When Docker is missing, setup asks before installing it and names where it will come from:

| Distribution | Docker comes from |
| --- | --- |
| Ubuntu, Debian, Raspberry Pi OS, Fedora, CentOS Stream, RHEL, Rocky, AlmaLinux, Oracle Linux | Docker's own install script, get.docker.com |
| Linux Mint, Pop!_OS and other Ubuntu-based releases | Docker's repository for the Ubuntu release it is built on |
| Parrot, deepin and other Debian-based releases | Docker's repository for the Debian release it is built on, or the distribution's own packages when Docker publishes none |
| Kali | Kali's own `docker.io`, `docker-cli`, `docker-compose` and `docker-buildx` |
| Arch, Manjaro, openSUSE Leap and Tumbleweed | The distribution's own `docker`, `docker-compose` and `docker-buildx` |
| Amazon Linux 2023 | Amazon's `docker`, with Compose and Buildx from Docker's releases (checksums checked) |

Whichever way, setup needs `docker compose` and a `docker buildx` of 0.17 or later, and fetches either
from Docker's releases when the distribution's is missing or older. Where certbot is kept in EPEL
(Rocky, AlmaLinux, CentOS Stream, Oracle Linux, RHEL), setup turns EPEL on first; on openSUSE it adds
certbot's renewal timer, which openSUSE packages separately.
