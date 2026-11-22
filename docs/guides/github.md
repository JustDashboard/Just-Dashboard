# Connecting GitHub

[← README](../../README.md) · [Features](../../FEATURES.md) · [Install](install.md) · [Configuration](configuration.md) · [Command line](cli.md) · [Administration](administration.md) · [Linking & usage count](linking-and-usage.md) · [GitHub](github.md) · [Security](../../SECURITY.md)

The **GitHub** page manages repositories on GitHub: it lists what your GitHub accounts can reach,
creates repositories, and works with their branches, commits, pull requests, issues, Actions,
collaborators and settings. **Local Git** is the other page, for the checkouts on this server; it
does not need GitHub to be connected, and nothing here changes it. **Clone to server** joins the
two: it copies a GitHub repository into one of the Git roots, where Local Git finds it.

## How it signs in

The dashboard talks to GitHub through a **GitHub App of its own**, created from the GitHub page in
a few clicks. Everything the page does on GitHub is done *as you*: each person connects their own
GitHub account, and GitHub decides what every request may do from three things at once — what the
App was granted, which repositories its installation covers, and your own role on the repository.
Having an account on the dashboard gives nobody any power on GitHub, and a connection is only ever
used by the dashboard account that made it, administrators included.

No personal access token is needed. No webhook is set up, so the dashboard does not need a public
address for GitHub to reach.

This App is separate from the one **Deployments** can create under Credentials (which builds and
reports on what deploys here). Setting up one does not set up the other.

## Setting it up (an administrator, once)

1. Open **GitHub** in the sidebar and press **Set up**.
2. Choose who owns the App: your own GitHub account, or an organisation you administer. Leave
   *Let organisations and other accounts install it* on if anyone will connect repositories that are
   not the owner's; turning it off keeps the App installable only on the account that owns it.
3. Press **Continue on GitHub**. GitHub shows the App it is about to create; press **Create**.
   GitHub sends you back to the dashboard, which stores the App's credentials sealed under the
   master key.

The App asks every account that installs it for: Administration, Contents, Issues, Pull requests
and Actions (write); Checks, Commit statuses and Metadata (read). It never asks to edit workflow
files. Each installation can still be limited to selected repositories.

## Connecting your account

Press **Connect GitHub**, sign in on GitHub if asked, and approve. Then **install** the App on each
account or organisation whose repositories you want here (**Install on another account** in the
page's menu); choose *All repositories* or pick some. The page says, per installation, which it is,
with a **Change** link to its settings on GitHub. After changing them, press **Refresh access**.

To connect a second GitHub account, choose **Connect another account**: GitHub lets you pick which
account to sign in with.

### When the dashboard is reached at more than one address

GitHub only sends a sign-in back to the addresses the App was created with (`<address>/github/callback`).
The App remembers the address you set it up from. If people open the dashboard at another address
too — an IP, a tailnet name, a second domain behind your reverse proxy — either:

- **add that address on GitHub**: the App's settings (**App settings** in the page's menu), under
  *Callback URL*, add `https://<that address>/github/callback`, save, then press **Add this address**
  on the dashboard so it offers the normal sign-in there; or
- **sign in with a code** instead, which works from any address: tick **Enable Device Flow** in the
  App's settings on GitHub once, then choose **Sign in with a code** and type the code it shows at
  github.com/login/device.

The redirect is followed by your own browser, so the address does not have to be reachable from the
internet. Whether GitHub accepts a plain `http://` address other than `localhost` as a callback is
GitHub's decision; if it refuses one, use an HTTPS address or sign in with a code.

## Cloning onto the server

**Clone** on a repository asks for one of the Git roots (`JD_GIT_ROOTS`) and a folder name that does
not exist yet, then shows git's progress. A private repository is fetched with a one-time,
read-only token for that repository alone, which is passed to git through its environment and
revoked when the clone ends; it is never written into the checkout. Later pulls and pushes from
Local Git use this server's own git sign-in (Local Git's **GitHub** sign-in, or SSH keys), exactly
as for any other checkout.

## What each dashboard role can do here

| Role | On the GitHub page |
| --- | --- |
| `readonly` | See the App's state, its own connections and what they reach; connect nothing |
| `limited` | Also connect and disconnect its own accounts, create repositories, change settings, branches, issues, pull requests, workflows and collaborators, archive, clone onto the server |
| `admin` | Also delete branches and repositories, remove collaborators, change visibility, and set up or remove the App |

GitHub applies its own rules on top: a control the page cannot use says why — your role on the
repository, or a permission the installation was not granted.

## When something goes wrong

- **"GitHub no longer accepts the sign-in"**: the authorisation expired or was revoked on GitHub.
  Press **Reconnect**.
- **A permission the App was not granted**: an installation was made before the App asked for it, or
  its owner declined it. Accept it on GitHub (the installation's settings), then **Refresh access**.
- **Rate limited**: GitHub allows each account a budget of requests an hour; the page says when it
  lifts.
- **A new repository does not appear**: an installation limited to selected repositories does not
  include it until it is added there on GitHub.
- **Removing the setup**: **Remove GitHub setup** forgets the App and every connection on this
  dashboard. The App stays on GitHub until its owner deletes it in its settings.
