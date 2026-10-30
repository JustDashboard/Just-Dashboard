# The usage count

An optional daily report that lets the project count how many servers run the dashboard and how many of
those are in use. It is off until an administrator says yes. Everything lives in `backend/internal/usageping`;
the routes are `api/handlers_usageping.go`.

## What it sends

One `POST {JD_ACCOUNT_URL}/api/usage-ping` per UTC day, with this body and nothing else:

```json
{"version":"0.8.2","usedToday":true,"usedThisWeek":true}
```

- `version` is the running build.
- `usedToday` and `usedThisWeek` say whether anybody was signed in within the last 24 hours and 7 days. They
  read the newest of `sessions.last_seen_at` and a successful `auth.login` or `auth.logout` in `audit_log`. The
  sessions table alone is not enough, because signing out or expiring deletes the row. A failed sign-in does
  not count, so a bot guessing passwords does not make a server look used.
- The only other headers are `Content-Type` and `User-Agent: just-dashboard`, plus the ones Go adds to any
  request. There are no cookies, and a redirect is never followed.

## Why there is no ID

Each server sends at most once per UTC day, and the day it last sent is stored in `settings`, so restarts do
not send again. That makes a day's reports a count of that day's servers without an identifier, and with
nothing to join on, nobody can follow one server from one day to the next. A day's reports give:

| Number | How it is counted |
| --- | --- |
| Servers running | All of that day's reports |
| Daily active | Reports with `usedToday` |
| Weekly active | Reports with `usedThisWeek` |

It counts servers, not people: a server with three administrators counts once. Counting people would need
per-person data, which is the thing this design avoids.

`TestReportIsExactlyThreeFields` fails on a new field. Adding one changes what the project has told people it
collects, and the site's receiver refuses anything else, so it is a change to both repositories.

## How it decides to send

| `settings['usage.ping.choice']` | Behaviour |
| --- | --- |
| Unset | Nothing is sent. Administrators are asked on the Overview until one answers. |
| `off` | Nothing is sent. Nobody is asked. |
| `on` | The first report goes when the person answers, then one per UTC day. |

The loop checks an hour after each check, starting a minute after boot. That spreads reports over the first
hour of the UTC day without a random delay, because each server's phase depends on when it started. It is also
the retry: a failed attempt is stored in `settings['usage.ping.last']`, shown under Settings → Configuration, and
tried again at the next check, so a site that is down hears from a server at most hourly.

A yes sends its first report inside the request, waiting at most five seconds, so the page can show the result
at once. A site that is unreachable does not fail the answer, which is stored either way. Switching off and on
again the same day does not send a second report.

## Switches

| Setting | Effect |
| --- | --- |
| `JD_USAGE_PING=false` | Nobody is asked, the loop does not start, and an earlier yes goes quiet. Answering is refused with `403 usage_ping_disabled`. |
| `JD_ACCOUNT_LINK=false` | The same. That switch tells operators the server never contacts the site, and this keeps that true. |
| `JD_ACCOUNT_URL` | Where reports go. A fork points this at its own site and counts its own servers. |

Agent mode has no count at all: the module is nil and the routes do not exist, as with the account link.

## Routes

Inside the admin-only `/dashboard` group:

| Route | Who | What |
| --- | --- | --- |
| `GET /dashboard/usage-ping` | Administrator, by session or key | The status, including `nextBody`: the exact bytes the next report would carry. |
| `PUT /dashboard/usage-ping` `{on}` | Administrator's session only; audited as `usage.ping.on` or `usage.ping.off` | Answers, and returns the new status. |

An API key cannot answer, for the reason it cannot link: whether this server reports anything is a person's
decision.

## The surfaces

- **The question** (`components/overview/usage-ping-question.tsx`) is a `Notice` on the Overview for administrators.
  It prints the report, and its two buttons both store an answer, so it is asked once.
- **The panel** (`components/config/usage-ping.tsx`) is under Settings → Configuration. It holds the switch, the
  last report or the next one as exact bytes, any failure, and a link to the totals. It sits outside the Settings
  panel because it applies immediately, with no Apply and no restart.

## The site's side

The receiver and the public page at `/stats` live in the site's repository. Its side of this contract is:

- Accept only this exact shape, and answer `204`.
- Keep daily totals per version and nothing per server.
- Keep no address. The site necessarily sees the address a request comes from, and a list of those would be a
  list of root-equivalent panels. That route stays out of access logs, and any rate limit on it lives in memory.
- Publish the totals and the receiver's source, because the site's repository is private and people could
  otherwise read what is sent but not what is kept.

The report goes to the site without authentication, so anyone can send a fake one. The totals are approximate,
and the public page says so.
