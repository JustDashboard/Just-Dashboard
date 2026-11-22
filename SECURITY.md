# Security

## Reporting a vulnerability

Please do not open a public issue. Report it privately through
[**Security → Report a vulnerability**](https://github.com/JustDashboard/Just-Dashboard/security/advisories/new)
on this repository. Fixes ship in the newest release, which **Update now** in the dashboard installs.
Only the newest release is supported: there are no fixes for older versions, so the way to get one is to
update.

## How updates are verified

An update builds and runs code as root, so this is what it relies on. The dashboard reads the changelog
and the release tag over HTTPS from the project's repository on GitHub and from nowhere else; no other
host can be configured, and `JD_UPDATE_REPO` only names a different repository there. The code itself
is fetched from the `origin` of the checkout you installed from. A version is
offered and installed only once it has a release tag (`v0.8.6`), which the project's release job makes
after the checks pass on `main`, and the update installs exactly the tagged commit by fast-forwarding
your checkout, never by resetting it. Release tags are **not signed yet**: the trust is in GitHub and
in who can push to the repository. The updater can already require a tag signed by a release key built
into the running version; it is switched off until that key is published, and this section will say so
when it is on. Before it builds, an update keeps the running images and copies the dashboard's database,
and it goes back by itself when the new version does not answer; see
[If an update fails](docs/guides/administration.md#if-an-update-fails).

## How the dashboard protects your server

**This dashboard is root-equivalent.** Anyone who reaches it with a valid session has root on the
machine, so it is built to sit behind a VPN or an SSH tunnel — and that is enforced:

- The backend refuses to start on a non-loopback address without a `JD_ALLOWED_CIDRS` allowlist,
  and the allowlist is checked **before** authentication.
- Two-factor is enforced for every account that has enrolled, except accounts that opted in to hub
  sign-in on a server whose operator turned it on (`JD_HUB_SIGNIN=true`, off by default; see below);
  `JD_REQUIRE_2FA` decides whether an account *must* enrol. A passkey replaces the password at
  sign-in, never the code.
- Repeated wrong passwords and repeated wrong two-factor codes each lock that part of an account's
  sign-in for fifteen minutes. Regenerating recovery codes and turning two-factor off take the current
  password.
- Capabilities are checked on every backend route, never in the UI alone.
- Destructive actions are confirmed and audited; deleting a project, database, stack, Git checkout, a
  host account's home or a repository on GitHub, and uninstalling with a wipe, asks for a typed phrase
  checked on the server.
- The GitHub page acts on GitHub only as the person who connected their own GitHub account, through
  this dashboard's own GitHub App (no personal access tokens). A connection is used only by the
  dashboard account that made it, administrators included. The App's client secret and private key
  and each connection's access and refresh tokens are sealed under the master key, never sent to a
  browser, a log or the audit trail. A sign-in carries a single-use state bound to the account that
  started it, and PKCE. A private clone gets a one-repository, read-only token through git's
  environment, never its arguments or the checkout's configuration, and the token is revoked when the
  clone ends. See [Connecting GitHub](docs/guides/github.md).
- Every state-changing request lands in the audit log.
- The Files page keeps the earlier contents of each file it saves over, so a save can be undone. They
  are copies of whatever those files held, `.env` files and keys included, kept under the data
  directory (`file-versions/`, folders 0700 and files 0600, root's alone), at most 50 per file and
  512 MiB in all by default (`JD_FILE_VERSIONS`, `JD_FILE_VERSIONS_MB`; `0` turns it off). They are
  served only to whoever may read the file itself, through the same roots, and a picture's version
  through the same closed list of types as the file. A backup run's copy of a file is listed beside
  them, and its content is read out of the archive only for an administrator, as downloading the
  archive is. A secret you rotate out of a file is still in its
  earlier versions until they age out.
- A delete on the Files page goes to a Trash instead of being final, and Find answers from an index of
  file names. Both live outside the folders you browse and are root's alone: the Trash is
  `.jd-trash` under the data directory (`/var/lib/just-dashboard`) for what shares its filesystem, and
  at the top of each other mount (folders 0700, records 0600), each item kept with its own owner and
  mode; the index is `/var/lib/just-dashboard/files-index/` (0700, its file 0600), because it lists
  every path on the server. The Trash is bounded by time (`JD_FILES_TRASH_DAYS`, 30 by default, every
  removal audited) and by Empty Trash; the index by the names on disk, one line each, rebuilt in place.
  Deleting from the Trash, emptying it and a permanent delete need the destructive capability; a
  restore is a file write and lands only inside `JD_FILE_ROOTS`. A Trash folder is opened once per
  operation and used only through that open handle, checked to be the dashboard's own and closed to
  everyone else, because the top of a mount may belong to another account that could otherwise swap
  the folder for one of its own and choose where root moves a restored file.
- The assistant, when you set one up, sends your question and what it reads to the model you chose, with
  every secret value masked first: the model's provider never sees one. An image you paste into its chat is
  the exception: it is sent as it is, because masking reads text, so a secret in a screenshot reaches the
  model, as the box says while one waits to be sent. It acts through the dashboard's own
  routes as you, with your whole role — whatever you could do in the dashboard, including what runs commands
  on the host — and every change is in the audit log under your name, marked as the assistant's. If your role
  has the terminal, it can also run shell commands on the server as the terminal's account, in a terminal window
  of its own that you can open and watch on the Terminal page; a command that is not a plain read, and anything
  it types into a running program, waits for your Allow unless you gave it full access, and each one is in the
  audit log. When a command asks for a password, you type it into a field in the chat that goes straight to the
  terminal: the assistant and its model never see it, and it is not saved or logged. It cannot sign in, change your password, second factor, passkeys or sessions,
  let Just Hub sign you in (or stop it), make an API key, uninstall the dashboard, attach to your own terminal windows or a stream, or read a database
  connection you marked protected. It asks before any change unless you give it full access,
  which runs everything your role allows without asking; for an administrator that includes what outlasts the
  chat, such as a new account, an SSH key, a sudo rule or a VPN peer. Text it reads, such as a log line, can try to talk a
  model into something, so leave "Ask before changes" on where someone else's text can reach it. In that mode
  it also asks before a read that is sent to another machine, such as a DNS lookup, a certificate scan or a
  ping, because a name to look up can carry what it has read off the server; what it reads still goes to the
  model you chose. Its chats
  are yours alone, saved encrypted on the server.
- The session cookie stays the dashboard's. It is `__Host-jd_session` — HttpOnly, SameSite=Strict,
  Secure and sent to no other name — and the previews and tailnet-only apps the dashboard publishes on
  its own tailnet name, or a deployment on the dashboard's own domain, are never sent it: browsers send
  a cookie to every port of its name, so the dashboard's proxy takes it out before the app sees the
  request. Upgrading signs nobody out.
- Linking to a just-dashboard.com account is optional and outbound only. A command from the site runs
  only if its signature verifies against the key pinned at link time and its kind is within the scope
  you chose; linking is a browser-session action for an administrator, or root at the terminal.
- A just-dashboard.com address (app domains) is optional and held through the same link. Claiming a handle
  **publishes this server's public IPv4 address in public DNS** under just-dashboard.com, so claiming and
  releasing are browser-session actions for an administrator, never an API key or the assistant, and
  releasing is in the destructive budget. A handle can also be chosen by the account's owner on the site,
  on the link's approval page or the server's page; the server then claims that one by itself, with the
  same signed request from its own address, and `JD_APP_DOMAINS=false` ignores it. The new outbound calls are signed requests on the link
  (`GET/PUT/DELETE /domain`, the challenge values); the site keeps its DNS token and changes only this
  server's three names. The wildcard's certbot hook reaches the backend only on loopback with a credential
  the backend writes 0600 in its data directory, and can only publish a challenge for this server's own
  handle. No certificate is ever ordered for a single app name under the zone.
- Connecting to a Just Hub is optional and outbound only, to one machine on the server's own tailnet,
  reached only over the tailnet. It has its own key, pinned hub key, counter and audit name, separate
  from the just-dashboard.com link, and a hub's command runs only under the same checks and the scope
  you chose. The hub's key is pinned when you approve and nothing the hub sends later can change it; the
  hub's tailnet machine is pinned too, so if another machine takes the hub's name the server sends it
  nothing and the connection goes quiet until you reconnect. What the hub sends to show (notices, server
  names, its messages) is shown as the hub's, never as the dashboard's own prompt. The one-time token the
  hub gives for the terminal is read only from standard input, never from the command line. The hub's
  discovery probe is behind the network allowlist and answers nothing but whether the server can be
  connected. `JD_HUB_LINK=false` switches all of it off.
- Hub sign-in is the one thing that comes in the other way, and it is off unless the operator sets
  `JD_HUB_SIGNIN=true`. Then the hub may open this server for an account that chose "Let Just Hub sign
  me in" (with its password and code), through one credential: a single-use ticket the hub signs with
  its pinned key, valid for about a minute, for one account, one hub user and the device that asked,
  redeemed at `POST /api/v1/auth/hub/enter`. The hub's own authenticator code stands in for this
  server's. Anyone who takes over the hub can then sign in as those accounts, so a session opened this
  way lasts at most two hours, is refused by the dashboard's own forms that make, change or reveal keys,
  accounts, passwords, authenticators, passkeys, Linux or database users, database passwords, secret
  variables, VPN peers, saved credentials or links, or change the dashboard's own settings, and ends as
  soon as its grant, the link or the switch does, or the link goes quiet, with every terminal or other
  live connection it opened. Everything it does is in the audit log as done through Just Hub, with the
  hub user and the session. What it does not stop: the Files page reads every file on the server as
  root for every role, so a hub session can read every credential on disk; with permission to change
  files it writes any file, and an administrator's hub session has the terminal, so for those accounts
  it is as good as root. The full list and its limits are in
  [the invariants](docs/internal/security/invariants.md#the-exception-hub-sign-in), and the security
  review the owner signs before turning it on is
  [docs/audits/2026-11-05-hub-signin/security-review.md](docs/audits/2026-11-05-hub-signin/security-review.md).
- The Files page can fetch a link onto the server (New → From a link), for accounts that may change
  files. The download is made by the server as the dashboard, so it can reach whatever the server can:
  the internet, and also the loopback, the private network, other containers and a cloud provider's
  metadata address. That is no more than such an account already has, since writing any file as root
  is root, but it means a link pasted from somewhere else is fetched from inside your network. Only
  `http` and `https` links are followed, through at most ten redirects that stay on those schemes; the
  request carries the link and a user agent and nothing else, no cookie and no header of the
  dashboard's; the audit log records the link without its query string, where signed download links
  keep their secret. A fetched file and an upload in pieces are staged root-only (0600) beside where
  they go, never visible under their name until whole, and never replace a file unless asked. An upload
  in pieces is recorded in the dashboard's data folder (`file-uploads`, 0700, one 0600 record each, at
  most 512), and one left untouched for a day is removed with its bytes. Every transfer stops, with a
  sentence, before it would leave the disk with less than two per cent free (between 64 MiB and 1 GiB).
- [The usage count](docs/guides/linking-and-usage.md#the-usage-count) is off until an administrator says yes, and is three fields with no ID.
- The Files page keeps small copies of pictures so a folder of photographs lists quickly. They are
  made only of a file the person could already open in Files, kept in `thumbnails` under the data
  directory (readable by root alone, at most 256 MiB, the least recently used removed first), and a
  picture claiming more than 50 megapixels is never decoded. A Markdown file is shown rendered but
  never as HTML: its script, styles and `javascript:` links are shown as text, and a picture from
  another site in it is not fetched.

### Roles

| | `readonly` | `limited` | `admin` |
| --- | :---: | :---: | :---: |
| View everything, and read any file as root | ✅ | ✅ | ✅ |
| Start / stop / restart, Git, write any file as root | | ✅ | ✅ |
| Terminal, delete, prune, restore, updates, accounts, firewall | | | ✅ |
| **Can become root on the server** | reads what root reads | **yes** | **yes** |

**A role decides which buttons an account has, not how far it can be trusted.** There is no role for
someone you would not trust with the server:

- **`admin` is root.** It has the terminal.
- **`limited` is root too.** The Files page writes as root, anywhere inside `JD_FILE_ROOTS`, which is
  the whole server unless you narrowed it. A `limited` account can therefore add its own key to
  `/root/.ssh/authorized_keys`, or write a sudo rule or a cron job, and from there do anything an
  administrator can. What it lacks is the terminal, the delete buttons and the accounts page, not the
  power. Give it only to someone you would give `admin`.
- **`readonly` changes nothing and reads everything root can read**: `/etc/shadow`, SSH private keys,
  every project's `.env`, and the dashboard's own `.env` and database (`/var/lib/just-dashboard`).
  Those two together hold `JD_MASTER_KEY` and everything sealed with it, so a `readonly` account can
  open every password, token and key the dashboard stores and every account's two-factor secret, and
  it has every account's password hash to guess at. The pages mask secrets; the Files page reads the
  files they are kept in. Give `readonly` only to someone you would let read the whole disk as root.

An API key made with one of these roles reaches what the role reaches.

This is how the dashboard is built, not something it overlooked, and it is the owner's decision to keep:
it is one panel for the people who run one server. `JD_FILE_ROOTS` narrows the Files page, for every
role at once, administrators included. Keeping the dashboard's own files and the paths that grant
root away from the roles below `admin` is not built;
[what that would take](docs/internal/security/invariants.md#what-a-role-is-and-what-it-is-not) is
written down with the decision.

### When someone who had an account should no longer be trusted

Anyone who had an account, of any role, could read `JD_MASTER_KEY` and the database, and so everything
stored in the dashboard, along with every file on the server. **`JD_MASTER_KEY` cannot be changed yet
without losing what is sealed with it**: replacing it in `.env` makes every stored secret unreadable,
and there is no command that seals them again under a new key. Removing the account is therefore not
enough, and a new key would not be either, because what they could read was the secrets themselves.
Change those: the passwords, tokens and keys saved in the dashboard (database connections, Git and DNS
credentials, backup destinations, deployment secrets), the server's SSH keys and account passwords,
and every dashboard account's password and authenticator. Backups of the dashboard made while they
had access open with the same key. What changing the key safely has to do before it can ship is
written down in [the master key](docs/internal/security/master-key.md).

## Read further

- [The complete security invariants](docs/internal/security/invariants.md), which every change has to keep
- [What a deploy key can and cannot do](docs/guides/cli.md)
- [What linking to just-dashboard.com, or connecting to Just Hub, sends](docs/guides/linking-and-usage.md)
