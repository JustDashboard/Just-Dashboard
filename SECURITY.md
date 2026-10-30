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
- The session cookie stays the dashboard's. It is `__Host-jd_session` — HttpOnly, SameSite=Strict,
  Secure and sent to no other name — and the previews and tailnet-only apps the dashboard publishes on
  its own tailnet name, or a deployment on the dashboard's own domain, are never sent it: browsers send
  a cookie to every port of its name, so the dashboard's proxy takes it out before the app sees the
  request. Upgrading signs nobody out.
- Linking to a just-dashboard.com account is optional and outbound only. A command from the site runs
  only if its signature verifies against the key pinned at link time and its kind is within the scope
  you chose; linking is a browser-session action for an administrator, or root at the terminal.
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
- [What linking to just-dashboard.com sends](docs/guides/linking-and-usage.md)
