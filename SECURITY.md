# Security

## Reporting a vulnerability

Please do not open a public issue. Report it privately through
[**Security → Report a vulnerability**](https://github.com/JustDashboard/Just-Dashboard/security/advisories/new)
on this repository. Fixes ship in the newest release, which **Update now** in the dashboard installs.

## How the dashboard protects your server

**This dashboard is root-equivalent.** Anyone who reaches it with a valid session has root on the
machine, so it is built to sit behind a VPN or an SSH tunnel — and that is enforced:

- The backend refuses to start on a non-loopback address without a `JD_ALLOWED_CIDRS` allowlist,
  and the allowlist is checked **before** authentication.
- Two-factor is enforced for every account that has enrolled; `JD_REQUIRE_2FA` decides whether an
  account *must* enrol. A passkey replaces the password at sign-in, never the code.
- Repeated wrong passwords and repeated wrong two-factor codes each lock that part of an account's
  sign-in for fifteen minutes. Regenerating recovery codes and turning two-factor off take the current
  password.
- Capabilities are checked on every backend route, never in the UI alone.
- Destructive actions are confirmed and audited; deleting a project, database, stack, Git checkout or a
  host account's home, and uninstalling with a wipe, asks for a typed phrase checked on the server.
- Every state-changing request lands in the audit log.
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
  make an API key, uninstall the dashboard, attach to your own terminal windows or a stream, or read a database
  connection you marked protected. It asks before any change unless you give it full access,
  which runs everything your role allows without asking. Text it reads, such as a log line, can try to talk a
  model into something, so leave "Ask before changes" on where someone else's text can reach it. Its chats
  are yours alone, saved encrypted on the server.
- The session cookie stays the dashboard's. It is `__Host-jd_session` — HttpOnly, SameSite=Strict,
  Secure and sent to no other name — and the previews and tailnet-only apps the dashboard publishes on
  its own tailnet name, or a deployment on the dashboard's own domain, are never sent it: browsers send
  a cookie to every port of its name, so the dashboard's proxy takes it out before the app sees the
  request. Upgrading signs nobody out.
- Linking to a just-dashboard.com account is optional and outbound only. A command from the site runs
  only if its signature verifies against the key pinned at link time and its kind is within the scope
  you chose; linking is a browser-session action for an administrator, or root at the terminal.
- Connecting to a Just Hub is optional and outbound only, to one machine on the server's own tailnet,
  reached only over the tailnet. It has its own key, pinned hub key, counter and audit name, separate
  from the just-dashboard.com link, and a hub's command runs only under the same checks and the scope
  you chose. The hub's discovery probe is behind the network allowlist and answers nothing but whether
  the server can be connected. `JD_HUB_LINK=false` switches all of it off.
- [The usage count](docs/guides/linking-and-usage.md#the-usage-count) is off until an administrator says yes, and is three fields with no ID.

### Roles

| | `readonly` | `limited` | `admin` |
| --- | :---: | :---: | :---: |
| View everything | ✅ | ✅ | ✅ |
| Start / stop / restart, Git, edit files | | ✅ | ✅ |
| Terminal, delete, prune, restore, updates, accounts, firewall | | | ✅ |

`readonly` can read any file inside `JD_FILE_ROOTS`, so give it to someone you would let read the
disk.

## Read further

- [The complete security invariants](docs/internal/security/invariants.md), which every change has to keep
- [What a deploy key can and cannot do](docs/guides/cli.md)
- [What linking to just-dashboard.com, or connecting to Just Hub, sends](docs/guides/linking-and-usage.md)
