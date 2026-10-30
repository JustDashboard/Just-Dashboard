<div align="center">

<picture><source media="(prefers-color-scheme: dark)" srcset="frontend/public/brand-logo/just-dashboard-horizontal-blue.svg"><img src="frontend/public/brand-logo/just-dashboard-horizontal.svg" width="420" alt="Just Dashboard"></picture>

### Your whole Linux server, in one private control panel.

Deploy apps, run Docker, manage databases, shape the network, open a real shell and keep it all
backed up, behind one login that lives on your private network.

**Version 0.8.3** · Free and self-hosted · AGPL-3.0

[![Install](https://img.shields.io/badge/Install-CAE9FF?style=for-the-badge&logoColor=141414)](#install)
[![Features](https://img.shields.io/badge/Features-1f2328?style=for-the-badge)](FEATURES.md)
[![Docs](https://img.shields.io/badge/Docs-1f2328?style=for-the-badge)](#documentation)
[![Security](https://img.shields.io/badge/Security-1f2328?style=for-the-badge)](SECURITY.md)
[![Changelog](https://img.shields.io/badge/Changelog-1f2328?style=for-the-badge)](CHANGELOG.md)
[![Website](https://img.shields.io/badge/Website-1f2328?style=for-the-badge)](https://just-dashboard.com)

</div>

<a href="FEATURES.md"><img src="docs/readme/overview.png" alt="The server overview: the machine, shortcuts to every section, live resources, storage and the deployed projects"></a>

## What it does

Just Dashboard is one control panel for one Linux server. It deploys your apps, runs your containers and
databases, manages the network, proxy and certificates, and gives you a real terminal, files and Git in the
browser. It needs no fleet, no cloud account and no paid tier: **every feature is free and stays free.**
[See every feature →](FEATURES.md)

## Install

You need a Linux server you have root on.

```bash
git clone https://github.com/JustDashboard/Just-Dashboard.git
cd Just-Dashboard && sudo ./install.sh
```

The installer asks how you will reach the dashboard, then does the rest and prints how to get in:

- **Tailscale (default):** the machine stays invisible to the internet, and the dashboard answers at
  `https://your-box.tailnet-name.ts.net:8443` with a real certificate.
- **SSH tunnel:** the dashboard is served on loopback only.

Upgrade with **Update now** in the dashboard. [Everything about installing →](docs/guides/install.md)

## Documentation

| Guide | What it covers |
| --- | --- |
| [**Features**](FEATURES.md) | Every area of the dashboard, with screenshots |
| [**Install and upgrade**](docs/guides/install.md) | What the installer does, first sign-in and upgrading |
| [**Configuration**](docs/guides/configuration.md) | Every environment variable, and running it by hand |
| [**The `jd` command line**](docs/guides/cli.md) | Deploy from a terminal, CI or an AI client |
| [**Administration**](docs/guides/administration.md) | Account scripts, API keys, updates, backups and uninstalling |
| [**Linking and the usage count**](docs/guides/linking-and-usage.md) | The two optional things that talk to just-dashboard.com |
| [**Security**](SECURITY.md) | How the dashboard protects your server, and how to report a vulnerability |
| [**Contributing**](CONTRIBUTING.md) | How to work on the project |

## Who builds this

Just Dashboard is built by one person, [Wayy01](https://github.com/Wayy01), under the
[JustDashboard](https://github.com/JustDashboard) organisation.

If it has saved you an evening, you can buy me a coffee. It is entirely optional.

[![Buy Me a Coffee](https://img.shields.io/badge/Buy%20me%20a%20coffee-FFDD00?style=for-the-badge&logo=buymeacoffee&logoColor=000000)](https://buymeacoffee.com/ionmoisei72)

## Sponsors and investors

*None yet, so this space is open.* To sponsor or invest in the project, write to
[ionmoisei755@gmail.com](mailto:ionmoisei755@gmail.com). Sponsors are listed here with a logo and a link.

## Licence

[AGPL-3.0](LICENSE). Run it, change it and distribute it, but if you run a modified version as a network service,
publish your changes. The product logos bundled in `frontend/public/logos/` are their owners' trademarks and keep
their own licences, listed in that directory's `NOTICE`.
