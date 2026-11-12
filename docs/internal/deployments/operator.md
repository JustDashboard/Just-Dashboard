# The deployment assistant

A project's **Assistant** page (`/deploy/{id}/assistant`) is the dashboard's assistant asked about one
environment of one deployment, a database connection's **Assistant** page (`/databases/{id}/assistant`, and the SQL
editor's column) is the same assistant asked about one connection, and the server's own chat
(`POST /assistant/chat`) is the same assistant asked about the whole server — reached from `/assistant`, and from
**Ask** in the top bar of every page, which opens a chat that knows the page it was opened from. It answers anything and does what it is asked: it reads a project's code, logs,
requests, metrics and a database's rows, sets variables, domains and limits, runs a backup, deploys and rolls
back, and reaches the rest of the dashboard — databases, backups, Docker, files, the firewall, the proxy, users —
through the dashboard's own API, and the host itself: a shell command run to its end (`run_command`), or a
real terminal window of the chat's own that it types into and reads, for what runs long, asks a question or
wants a password ([the terminal](#the-terminal)). It is meant to be talked to as any general assistant is, and to look at the
server only when the question is about it ([how it answers](#how-it-answers)). **Diagnose** on the overview opens the deployment's page and asks the diagnostic question. A new
deployment can be handed to it too: it configures the draft, and the person presses Deploy (below).

It asks the provider the install set up once ([the assistant's providers](#providers)), the same one the
SQL editor, a drawing's Describe and a failed run's Ask why ask, on the same budget of model calls and
four answers at once.

Two contracts changed in 0.8.5, and both were security properties, so they are said plainly. Until the first
release of 0.8.5 the operator ran nothing: every action was a card only a person's press could run. Then it
could act on its own as a deploy key would, narrowed to one deployment. **Now it acts as the person, with their
whole role**: nothing they may do is refused it, and what the old narrowing refused waits for their Allow
instead, unless they chose full access. What still holds it is below, and
[invariant 13](../security/invariants.md) states it.

## What holds it

- **It acts as the person, through the router.** A call is a request through the dashboard's own router from
  its root (`s.assistantDispatch`) under the person's own session principal, carried in the request's context
  (`httpx.WithAssistant`) and read again for the call. Every group's authentication takes it in place of a
  credential and marks it `Principal.Assistant`, so the route's capability check, handler rules, rate limit
  (`ByPrincipal`, the person's own budget) and audit entry apply exactly as to them; it sends the CSRF header a
  page sends. It cannot do what their role cannot, and a `readonly` person's assistant changes nothing. The
  audit entry is the person's, with actor `assistant`, so what it did reads apart from what they did.
- **Its reach is the API, less a closed list.** `api/handlers_assistant_routes.go` reads every route under
  `/api/v1` off the router (`chi.Walk`) once per router. The model makes one with `api_request` and looks them
  up with `find_routes`; a deployment's routes are also `call_operation`'s by name, with their OpenAPI schemas
  (`api/handlers_assistant_catalogue.go`, every route under `/deploy` the person could call). What stays out of
  reach (`agentExclusion`), in every mode and whatever the role, and why:

  | Not reachable | Why |
  | --- | --- |
  | `/assistant/*`, `/databases/assistant`, `/databases/{id}/assistant`, `POST /deploy/{id}/assistant`, `POST /deploy/drafts/{draft}/assistant` | Its own routes: a press inside a press, and the provider's keys |
  | `POST /deploy/{id}/runs/{run}/diagnosis`, `POST /boards/diagram` | They ask the model themselves; it reads the run, or describes the drawing in words |
  | `/auth/*`, `/account/password`, `/account/2fa`, `/account/recovery-codes`, `/account/passkeys`, `/account/sessions`, `/account/hub-signin` | A person's own credentials, and whether Just Hub may sign the account in (the exception to [invariant 2](../security/invariants.md#the-exception-hub-sign-in)) |
  | `POST /tokens`, `/tokens/deploy`, `/tokens/mcp` | A new key's value is shown once, and would be masked from the model and the page alike |
  | `POST /dashboard/uninstall`, `/dashboard/link` and `/dashboard/hub` writes (an administrator removing an account's hub sign-in among them), `PUT /dashboard/usage-ping` | The person's own press ([invariants 12, 16 and 17](../security/invariants.md)) |
  | WebSockets and streams: a terminal's attach, a container's exec and stats, log, event, run, request and lifecycle streams, image pull and build, a stack's run, Redis monitor, Remote Desktop (`agentStreamHandlers`) | A recorded request cannot hold one; `TestEveryStreamingHandlerIsKeptFromTheAssistant` reads the handlers' source for an upgrade |
  | `/hooks/*`, `POST /deploy/{id}/hooks/{triggerID}`, `/mcp`, `/tailnet/*`, `/probe-agent/*` | Other callers' routes, authenticated by a signature, a key or a certificate |

  `find_routes` lists a matching excluded route with its reason, and `api_request` answers one with it, so the
  model is told rather than left to guess.
- **What used to be refused is asked.** The deploy-key narrowing, `assistantRoutes`, `agentClosedRoutes` and the
  handler guards `assistantDraftGuard` and `refuseAssistantSchedule` are gone. A schedule that runs a command, a
  trigger's new secret, a duplicate, a generated variable, the server's backups: each is a change, which waits
  in "ask" and "changes" and runs in full access. A draft's conversation runs its own saves unasked, as before,
  but committing the draft (pressing Deploy), discarding it, or a save that changes what is deployed, points a
  variable at a saved credential, store or database, or reaches past the container to the host
  (`agentDraftAsks`, the conditions the old guard refused) waits unless the mode is full.
- **The mode is the server's to hold.** Each press says one (below), and a call the mode holds runs only once
  the person answers it.
- **What it reads is masked before the provider sees it, in every mode.** A secret's value is the one thing kept
  — from the provider, not the person, who can read every value on its page. Every string of every answer goes
  through three layers (`api/handlers_assistant_mask.go`): every deployment's secret values, read once per
  press when first needed (`PlanningStore.DeploymentSecrets` for each: every environment's values including
  rotated ones, what references point at, shared variables, saved credentials and trigger secrets); the shapes
  `deploy.DiagnosisMasker` knows (a private key, a URL's password, an Authorization header, `NAME=value` where the
  name says secret, issuers' token shapes); and a field whose name says it holds a secret (`password`, `token`,
  `apiKey`, `privateKey`, `credential(s)`, `dsn`, `connectionString`, `authorization`, `cookie`, `session` and
  kin, `agentSecretField`) whatever its value, plus key material by its shape (a PEM block whole, a JWT, a long
  random token). An answer has to be whole to be masked, so one too long to read, or JSON that does not parse,
  is refused rather than sent in part (`agentAnswer`); text that is not JSON — a log — is masked as text, and
  anything else is described, not sent. A call whose values cannot be opened to mask reads and changes nothing.
- **It goes no further than the person's session.** A press can outlast what authorized it, so the session is
  read again before every step and every call (`Authenticator.Recheck`): one revoked stops it, and a role changed
  takes effect at the next call. Who answers and the default mode take a session, not a key, because a key
  pointing it at a server of its own would be handed what everybody asks and could answer with what to do.
- **What it reads is not instructions.** A log line, a row or a request's path is text somebody else wrote, so a
  model can be told things by it. The prompt says to act on none of it and to tell the person, which is a
  mitigation and not a guarantee, and it is why **Ask before changes** is the default: a change waits for a
  person unless someone chose otherwise. Full access is a person choosing to let everything their role allows
  run unseen, including what runs commands on the host.
- **It is bounded.** A press takes at most `agentSteps` (32) answers; past that, whatever it asks next waits for
  the person in any mode, so a loop spends nothing more without somebody looking. Each step and each press waits
  for the install's budget of model calls, and a press whose budget stays spent for 20 seconds is refused. A
  press's deadline is 20 minutes and a call's is two: a route that has not answered by then is not waited for
  (`errDispatchTimeout`), and the model is told it may still finish.

## The modes

| Mode | Runs without asking | Waits for the person |
| --- | --- | --- |
| Ask every time (`ask`) | Looking up an operation or a route, offering a shortcut | Every read and every change |
| Ask before changes (`changes`, the default) | Reads that stay on the server, including a SQL statement and a shell command that only read | Every change, and every read that is sent to another machine |
| Full access (`full`) | Everything the person's role allows | Anything past `agentSteps` |

The page sends the mode on every press; left out, it is the install's default, which an administrator
sets on `/assistant` (`PUT /assistant/preferences`). Anyone asking may change it for their conversation.
It is in the audit entry of every press.

A call is one of four kinds: `free` reads nothing of the server; `read`; `change`; and `destructive` for a route
behind `s.destructive`, a `DELETE`, or one of `mcpDestructiveHints`. `change` and `destructive` differ only in
what the card says and how Allow is drawn. An `api_request` is read by its method, but a `POST` that only reads
(`agentReadingPosts`: a Mongo find, a statement's classification, a DNS lookup) is a read, and
`POST /databases/{id}/query` is a read when its one statement is (`dbx.SingleStatementFor`, risk `read`), so a
database is asked about without an Allow for each query in "changes".

**A read that is sent to another machine waits, in every mode but full access.** What the model reads is not
instructions, but it can be told things by it, and a read that sends what it is given to a machine of its
choosing is the way out for everything else it has read: a row, a file or a host name can be put into a name
to look up or a path to fetch, and whoever answers for that name is sent it. Masking keeps secrets' values from
the provider, not ordinary data from a third machine. So:

- A route that reads by reaching a host, a domain or an address its caller names is listed in
  `agentReachingReads` (`api/handlers_assistant_routes.go`) and its call carries `asks`, which `agentHolds`
  holds like a change though the card and the answer stay a read's: `GET /certificates/check`, `/scan`,
  `/scan/deep` and `/dns`, `GET /remote-desktop/probe`, `GET /deploy/hostname` when it is given a `hostname`
  (a name suggested for a deployment is under the server's own domain), `POST /databases/test` and
  `POST /databases/{id}/imports/inspect`, which connect to the address they are given,
  `POST /network/dns/lookup` and `/dnssec-chain`, and `POST /docker/stacks/{name}/validate`, since Compose
  resolves what a file includes. A scan of one of the server's own sites waits as any other does: the route is
  listed, not the name. A draft's chat, whose own reads never wait, waits for these too. A read is not
  listed when it reaches only where the server already points: a saved database connection, a deployment's own
  domain or repository, GitHub, an image's registry, a configured resolver, the distribution's package index.
  A new route that takes a host from its caller and reads by reaching it joins the list, which
  `TestTheCatalogueIsTheRoutesItSaysItIs` holds to the router.
- A ClickHouse `SELECT` from a table function that reads another machine or runs a program (`url`, `remote`,
  `s3`, `mysql`, `executable` and kin, `clickHouseReaching`) is not a statement that runs unasked, though the
  statement's own classifier reads it as a read. The name is looked for anywhere in the statement, quoted or
  with a comment before its bracket, so a column of that name called like a function costs an Allow.
- A shell line that reaches another machine is a `change`, as `curl` always was: `ping`, `dig`, `host`,
  `nslookup` and `traceroute` are not readers, nor are `git ls-remote`, `git remote show`, `openssl s_client`,
  `npm view` and `npm outdated`; `getent hosts name`, `lsof -i @host`, an `ss` filter that names a host,
  `openssl x509 -in http://…` and bash's `< /dev/tcp/host/port` ask as well.

A `run_command` line is read by `assistant.ClassifyCommand`, conservatively, so that a misreading costs an Allow
rather than a write nobody saw. It keeps that by listing what a read may be made of and asking about everything
else, never by listing what to refuse. A line is a `read` only when all of this holds:

- **It is plain** (`parseLine`): nothing written by a redirection except to `/dev/null` or to another stream
  (`2>&1`, `>&2`, `>&-`, each a whole word: `>&file` writes a file in bash and zsh, and `/dev/null.txt` is one),
  no `$( )`, backticks, `${ }`, `$'…'`, subshell, group, here-document, background job or comment, and no `!`
  that a terminal window's history would expand. Where bash and zsh, run with `-c` or typed at a prompt, read a
  line differently, the reading that asks is taken.
- **Every command in it is a listed reader** (`readers`, `assistant/command_readers.go`), named bare or under a
  system directory (`/usr/bin/ls`, never `./ls` or `/srv/app/bin/cat`). A few can only read whatever they are
  given (`ls`, `cat`, `grep`, `ps`, `jq` and the like) and their arguments are not looked at. Every other is a
  reader only with the flags listed for it and the subcommands that only read: `find` with its tests and prints
  but not `-exec`, `-delete` or `-fprint`; `sort` without `-o` or `--compress-program`; `git log`, `status`,
  `diff`, `show`, `branch` and `tag` listing, without `-c`, `-C`, `--output` or a remote; `docker ps`, `logs`,
  `inspect` and `compose ps` without `-H`, `--config` or `-f`; `openssl x509` without `-out` or `-engine`;
  `systemctl status`, `journalctl -u`, `crontab -l`, `iptables -L`, `nft list`, `nginx -t`. **A flag the table has
  not heard of makes the line a change**, so a new flag, an abbreviated one or a letter in a run of short ones
  costs an Allow rather than slipping through; a value is taken from the word after its flag only where the
  program certainly takes it from there, and is read as an operand otherwise.
- **Its arguments are what was typed**, wherever they decide what the program does: a variable (`$X`, `"$X"`)
  or a pattern the shell matches file names against can turn out to be a flag or several words, so a reader
  other than the first kind is not read past one, and neither is a wrapper.
- **It runs under nothing but** `sudo`, `nice`, `ionice`, `stdbuf`, `timeout`, `time`, `command` or `builtin`
  with the flags listed for them (`readWrappers`), and a variable set in front of it only when it is one of the
  locale's, the zone's, the screen's width or a pager set to `cat` (`harmlessVariables`): any other is the
  program's to obey (`GIT_SSH_COMMAND`, `LD_PRELOAD`, `PATH`), and a line that only sets one sets it for what
  follows.

It is `destructive` when any part removes, stops, kills, wipes or reboots (`rm`, `dd`, `mkfs.*`, `kill`, `reboot`,
`systemctl stop`, `docker rm`, `apt purge`, `git reset`, `iptables -F`, `crontab -r`, `loginctl terminate-session`,
`ss -K`), and a `change` otherwise — `sed`, `curl`, an interpreter, `cd`, an unknown program, anything not plain.
`TestNothingThatWritesOrRunsIsReadAsARead` holds the lines that were once read as reads; a new reader or flag
goes into the table with the lines that must still ask.

It reads what a line says, not where it runs or what it opens. In the terminal window a read runs in the
directory an earlier `cd` left, which the person allowed, and a program that takes configuration from its
directory (git, npm, Docker Compose) takes it there; and reading a file is a read whatever the file is.

## The routes

`POST /deploy/{id}/assistant[?environment=]`, `POST /deploy/drafts/{draft}/assistant`, `POST /databases/{id}/assistant`
and `POST /assistant/chat` (`api/handlers_assistant_turn.go`) take a question or the answers to the calls the last answer is waiting on,
and stream the answer back as one JSON object a line (`application/x-ndjson`):

| Body | What it does |
| --- | --- |
| `{"conversation": "", "question": "…", "mode": "…"}` | Begins a chat |
| `{"conversation": "<id>", "question": "…"}` | Asks again in it; calls still waiting are declined in the same message |
| `{"conversation": "<id>", "decisions": {"<call id>": true, …}}` | Answers the waiting calls: `true` runs one, `false` declines it, and a call left out is declined |
| `…, "takeOver": true` | Carries on a chat a press is running elsewhere: that press stops, and this one goes on with the chat as it stood |
| `…, "attached": [{"label": "Statement", "text": "…"}]` | Hands over up to four things with a question — the SQL editor's statement, last result, error or plan — each a one-line label of at most 60 characters and at most 20,000 characters of text. The model reads them after the question; the question's line, the saved chat and the audit entry keep the labels alone |
| `{"conversation": "", "question": "…", "about": {"path": "/docker/stacks/web", "title": "web"}}` | The server's chat only, on the press that begins it: the page it was opened from (a dashboard path of at most 512 characters and a title of 120), which the prompt names and the chat keeps, sealed |

Each is **session-only** and asks `service.control` (a deployment's sits in its group; the draft's in the draft
group's `system.admin`; a database's behind the guard that refuses a protected connection with
`409 connection_read_only`): there is nobody to ask behind a key. A chat a press is running answers `409 chat_busy`
(JSON, not a stream) unless `takeOver` is set; the press it stops sends
`{"type":"error","error":{"code":"taken_over",…}}` and ends.

| Line | Carries |
| --- | --- |
| `start` | The chat's id (`conversation`), its `title`, the provider, the model and its label, whether it was offered tools, and the mode |
| `thinking`, `text` | The answer as it is written |
| `call` | A call as it starts: its id, the tool and operation, the route (`METHOD /path` as called, for `api_request`), the label, the kind, the model's `reason`, and its input |
| `called` | What became of it: `ok`, `declined`, a few words of what it came to (`said`), the first 4 KB of its answer (`output`), and for a change a `change` with the page's `label`, its `href`, and for a configuration a `diff` |
| `link` | A shortcut it offers (`open_page`): a label, an address and a note |
| `done` | Why it stopped, the tokens, whether the turn was kept, `pending` (the calls waiting, each as `call` carries them), and `paused` when `agentSteps` is why |
| `error` | Why there is no answer (`assistant_failed`, `conversation_full`, `taken_over`) |
| `ping` | Nothing: written when the stream has been quiet for 15 seconds — a command running, a terminal's wait, a model thinking — because a proxy in front of the dashboard closes a connection that carries nothing for a minute (nginx) or a hundred seconds (Cloudflare), which the page could only read as the answer failing, and which stopped the press and the command it was running. The page passes over it, and the saved chat does not keep it (`assistantStream.keepAlive`) |

A press that fails before it ran what it answered leaves those calls waiting, and the page asks again. One
whose provider fails partway keeps what already ran in the conversation (`c.Mark` is taken after
each step), because what ran has run and the next question must be asked knowing that.

Each press is audited as `deploy.assistant.ask`/`.tool`, `deploy.draft.assistant.ask`/`.tool`,
`database.assistant.ask`/`.tool` or `assistant.chat.ask`/`.tool`, with the provider, the model, the mode, the
chat's first eight characters, the tokens, what ran, what was refused, the calls that could not run (`malformed`),
what was allowed and declined by name, `takeOver`, the labels of what was attached, the page a server chat was
opened from (`about`), and each shell line it ran (`commands`, masked and cut at 300 characters). What was read is not written into the audit log. Every change it makes is also the route's
own entry, as the person, with actor `assistant`.

## Saved chats

A chat outlives the press, the process and the browser (`api/handlers_assistant_chats.go`). A press saves its
chat after every step and as soon as the page has its first line (`saveChat`), so a crash or a take-over loses
nothing, and a chat that is not in memory is loaded back (`loadChat`, `assistant.Store.TakeChat`) exactly as the
provider was last sent it. A chat carried on by another model goes on without the thinking the first one gave
back, which the next would refuse (`agentCarryOver`). `assistant_chats` keeps one row per chat — owner, what it
is about, the model, when, its steps and changes, whether calls wait, and the conversation and its title sealed
with the master key, as the keyring is — and `assistant_chat_lines` what the page was shown, a sealed batch per
save. A chat goes with its owner and with the deployment it is about (`ON DELETE CASCADE`), and a database's
with its connection (`forgetConnection`); `database_id` and the sealed `about_enc` were added to the table for
those two scopes. Each person keeps
their newest 200. `assistant_chat_images` keeps each image pasted into a chat whole, sealed, for the page to open
large; it goes with its chat (see Pasted images).

| Route | Does |
| --- | --- |
| `GET /assistant/chats[?deployment=&draft=&database=&scope=]` | The person's chats, the newest first, at most 100: id, title, scope (with `database` for a connection's and `about` for a server chat opened from a page), times, provider, model, `running`, `waiting`, steps, changes. `scope` is `server`, `deployment`, `draft` or `database` |
| `GET /assistant/chats/{chat}` | One chat and its `lines`: each question and each Allow/Decline press, and every line the stream sent, as sent, with `at`; a run of text is kept as one line |
| `GET /assistant/chats/{chat}/images/{image}` | An image pasted into it, whole, as its bytes (`private, immutable`, `nosniff`, a sandboxing CSP); `404 not_found` once only its thumbnail is kept |
| `PATCH /assistant/chats/{chat}` | Renames it (`{"title"}`) |
| `DELETE /assistant/chats/{chat}` | Deletes it; `409 chat_busy` while a press runs it |
| `POST /assistant/chats/{chat}/terminal` | Types the person's own `text` (and Enter unless `enter` is false) into the chat's terminal window: a password a command asked for, or any answer. Terminal capability, a session; `404 no_terminal` when the chat has none open; audited as `assistant.terminal.input` with the byte count only ([the terminal](#the-terminal)) |

A chat a press is running answers a second press `409 chat_busy`. `takeOver: true` cancels the running press
(its stream ends with a `taken_over` line) and goes on once it has let go: with the person's question, or with
none, carrying the chat on from where the stopped press put it down. A press stopped before a step of its own
was kept puts the chat down with its question asked, so the press that takes over answers it; one with nothing
left to answer, or waiting on the person, sends its waiting calls again and ends.

They are the owner's alone, by user id, by session or the same user's key; anybody else's is a 404. A long chat
leaves out the text of its oldest tool results (`Conversation.Shrink`) rather than ending, and ends only when
that is not enough.

## The tools

`assistant.CheckCalls` reads each call on its own (`assistant/calls.go`). A model writes calls the way it writes
anything, so a number past its bound is held to the bound, a field the tool lacks is left out, an object or a
whole input written as the text of one is read as the object, a number written as text (`"#12"`) is read, a page
name is read loosely, and an argument put beside `arguments` is read as one of them — and the model is told
each in a note before the call's result. A call that cannot run at all — no such tool, a required field
missing, input that is not JSON — is answered with an `is_error` result saying what was wrong and what the
tool takes, and the turn goes on. It is the model's slip, not something done on the server, so the page is not
sent it: it used to be drawn as a failed step, and a made-up `deploy_get` read "There is no tool called…" to the
person, words meant for the model. The audit entry still lists it under `malformed`. Only an answer whose calls cannot be told apart (a
call with no id, two under one) is refused, as `assistant.ErrUnreadableAnswer`.

| Tool | Offered to | Does | Kind |
| --- | --- | --- | --- |
| `recent_runs`, `run_transcript`, `container_logs`, `release_metrics`, `lifecycle_events` | Deployment | The reads shaped and bounded for a model (`assistant/operator.go`, `api/handlers_deploy_operator.go`): 20 runs, 300 lines, 168 hours | `read` |
| `host_alerts`, `firewall_rules`, `security_updates` | Deployment, server | The host's own reads; `host_alerts` asks `system.admin`, and the server's `firewall_rules` reads every rule | `read` |
| `read_source` | Deployment, server | A deployment's code at the commit a deployment would build now (`deploy.DeploymentChecker.ReadSource`, through the private, temporary checkout detection reads): up to 8 `paths`, each folder listed and each file read as text, 16 KB a file and 18 KB a read, binary files named without their bytes, `.git` and links out of the checkout refused. With no paths it lists the top folder and reads the README. Masked with the deployment's secret values. A deployment's chat reads its own unless it names another `deployment`; the server's must name one | `read` |
| `describe_operation` | Deployment, draft | One operation's parameters and JSON body | `free` |
| `call_operation` | Deployment, draft | Runs one operation under `/deploy` by name. `id` and `draft` are filled in, and another deployment's id is pointed at `api_request`; `env` defaults to the conversation's | by the route |
| `wait_for_run` | Deployment | Waits for a run to end, or for up to 120 seconds | `read` |
| `find_routes` | Every scope | Looks the API up by words or an area, with each route's method, path, words and kind, and the matching routes it cannot make with the reason; an empty query lists the areas | `free` |
| `api_request` | Every scope | Makes any route of `/api/v1` as the person: `method`, `path`, `query`, `body`, `reason`. A route into a protected database connection is answered with why and reads nothing (`agentProtected`) | by the route |
| `run_command` | Deployment, server, database | Runs one shell line on the host as the terminal's account (`term.Manager.RunCommand`): its login shell with `-l -c`, through `su` for an account that is not the dashboard's own, in the account's home, with a built environment (never the dashboard's, which holds its secrets: `PATH`, `HOME`, `USER`, `SHELL`, `TERM=dumb`, `DEBIAN_FRONTEND=noninteractive`, pagers off), nothing on standard input, and a deadline of 60 seconds or the call's `timeout` up to 300, past which its whole process group is stopped. It takes the terminal capability, checked on every call, and the terminal turned on. Its answer is the exit code, the account, how long it took and the last 24,000 bytes of what it wrote, masked; the page is sent its last 4 KB | by the line |
| `terminal_run` | Deployment, server, database | Types one command line and Enter into the chat's own terminal window, opening it the first time, and answers with what it printed once it is back at the prompt, quiet on a question or a password, or after `wait` seconds (20 by default, at most 120), when it goes on running ([the terminal](#the-terminal)) | by the line |
| `terminal_send` | Deployment, server, database | Types `text` (with Enter unless `enter` is false) and presses `keys` by name (`ctrl-c`, `up`, `enter`…) into the chat's window, and answers as `terminal_run` does. A masked value is refused | `change` |
| `terminal_read` | Deployment, server, database | Waits up to `wait` seconds (30 by default, at most 300) and reads what the window printed since the last call | `read` |
| `open_page` | Deployment, server, database | Offers a shortcut to a page from a closed list (`AgentPages`, `ServerPages`, `DatabasePages` under `/databases/{id}`) | `free` |

An answer to the model is its JSON, masked and cut at 24,000 bytes.

## The terminal

`run_command` runs a line to its end with nobody at the keyboard, which is what most questions want and is its
cleanest answer. What it cannot do — a command that runs longer than five minutes, asks a question, wants a
password, or that the person wants to watch — is the terminal's (`assistant/terminal.go`,
`api/handlers_assistant_terminal.go`).

`run_command` hands some lines to the terminal itself (`commandForTerminal`), wherever windows are held: a line
run on its own is a child of the dashboard's process, in its container, and a package upgrade that upgrades
Docker restarts every container — the dashboard's with it — which stopped the upgrade halfway, dpkg and all, and
cut the press off. So a line that changes packages (`apt`/`apt-get`/`aptitude`/`dnf`/`yum`/`zypper`/`apk`/`snap`
installs, upgrades and removals, `dpkg -i`/`--configure`, `pacman -S`/`-U`/`-R`, `unattended-upgrade`,
`do-release-upgrade`) or restarts Docker (`systemctl restart|stop … docker|containerd`, `service docker
restart`) is typed into the chat's window as `terminal_run` would (`assistant.CommandForTerminal`), and so is a
`sudo` without `-n` or `-S` when the terminal's account is not root (`assistant.CommandAsksSudo`), since only a
window can take its password. It waits up to the command's own time, at most `terminal_run`'s two minutes, and
answers as `terminal_run` does, first saying why it went to the window. A line of several lines, or a host whose
windows are not held, runs on its own as before.

- **One window per chat, a real one.** The chat's first `terminal_run` opens a window of the Terminal page's
  (`term.Manager.Create`) as the person: owned by them, titled for the chat and named
  `assistant-<chat id>`, held on the host like any other, so it outlives the press, the page and a restart —
  after which it is found again by its name. The person sees it on the Terminal page
  (`/terminal?session=<workspace>`, which the page links from the call) and may type into it there. It keeps its
  directory and environment between commands, as a shell does. It takes the terminal capability and the
  terminal turned on, on every call, and a host that cannot hold a window (no systemd) refuses it with why,
  pointing at `run_command`.
- **What it printed, all of it.** A subscriber gathers what the window prints as it arrives, so nothing between
  two calls is missed, and each call answers with what arrived since the last (the end of it past 256 KiB, said),
  cleaned of the terminal's own codes — colours, titles, cursor moves; a line as a carriage return last left it,
  so a progress bar is its final state; backspaces applied (`terminalText`) — and masked like any answer. The
  model is sent its last 16,000 bytes and the page its last 4 KB.
- **When a call answers.** It waits until what runs is back at the prompt — the kernel's foreground group on the
  window's terminal (`term.Session.Foreground`), so a builtin is never mistaken for a job and a prompt drawn by
  anything is never mistaken for the shell's — or has gone quiet on a password prompt (1.2 seconds) or on a
  question (`[y/N]`, a line ending in `?`, `:`, `>` or `]`, 2.5 seconds), or until its `wait` runs out, when it
  says the command is still running and goes on in the window. Its answer says which, and the page is sent the
  same as `terminal` on the call: the window's workspace, the state (`finished`, `running`, `password`,
  `input`, `closed`) and what it asked.
- **A password is the person's.** On a password prompt the model is told not to type it or ask for it, but to
  say in a line that it is waiting and read on with a long wait. The page shows the person a field under the call
  that types what they write straight into the window (`POST /assistant/chats/{chat}/terminal`, `{"text", "enter"}`,
  the chat's owner with the terminal capability, in a session): it is in no request to the provider, no saved
  line, and the audit entry `assistant.terminal.input` records only how many bytes. A program that reads a
  password turns the terminal's echo off, so it is not in what the window prints either; one that does not would
  print it, and the field says so. `terminal_send` refuses a masked value outright, so the model cannot send a
  `[REDACTED]` back as if it were the secret.
- **Held like the shell.** A command line is read as `run_command`'s is (`ClassifyCommand`); whatever is typed
  into a running program is a change, since an answer to a question is a decision — a `terminal_run` line
  included when a program holds the window in front of its shell (`terminalBusy`), because that program reads
  it, not the shell; and a read is a read. The
  press's audit entry lists each line typed (`terminal: …`) and each answer (`typed: …`, keys by name), masked.
- **What it changed is said as a change.** A shell line read as a change that succeeded — `run_command` with exit
  0, a terminal call once its window is back at the prompt — carries a `change` like a route's (`commandChange`),
  so it is in the chat's list of what it did: a terminal's goes to its window, and a line run to its end to the
  audit log, which lists it.

## What it begins knowing

`assistant.AgentSystemPrompt` is told what the overview already shows anybody who may read the deployment
— the server's hostname, the deployment and environment, the source, the build, the address, the health,
the live release, the run in progress and the last run, masked — and how to work, and it lists the
deployment's operations, grouped by what they do. `assistant.ServerSystemPrompt` is told the server's hostname
and its deployments by id, name and last run, and, for a chat opened with **Ask** on a page, that page's title and
address, with "this" most likely meaning what it shows. `assistant.DatabaseSystemPrompt` is told the connection —
its name and id, engine and version, host, database, what it serves and the operator's notes — and, for a SQL
engine that answers within eight seconds, the structure of its schema in focus, never a row
([a connection's assistant](../backend/databases-proxy-platform.md#a-connections-assistant)). Every prompt (`writeAPI`) tells the model it has the whole
dashboard through `api_request` and `find_routes`, lists the API's areas — each first path segment with its
count of routes, a sentence (`agentAreaBlurbs`) and a few reads, built from `chi.Walk` when the catalogue is —
says how to learn a body's shape (GET the resource first, or read the 400 the route answers), how to query a
database, what it cannot reach, and that secret values reach it masked.

## <a id="how-it-answers"></a>How it answers

The deployment's, the database's and the server's prompts open with who it is to the person (`writeCharacter`): the assistant
they would talk to anywhere, naturally and about anything, that can also see and work on their server. "How to
answer" (`writeWork`) then says to answer the question asked and only that; to look something up only when the
answer depends on it, and where the answer is (what a project is or does from its code with `read_source`, a
failure from its runs, logs, events and metrics, the rest through the API; a general question with no tools); to
write as a person talks, without a status report or a list of other things it could read; to back what it says
with what it read and say when it is inferring; and to leave its own failed calls out of the answer. A deployment's
chat is told that "this project" and "the app" mean that deployment.

This replaced an incident responder's brief ("find out what is wrong", "be brief: what you found, the evidence,
and what happens next"), with only runs, logs and metrics to read. Asked "what is this project about?", it read
the last runs and the sizing verdict and reported them, then guessed the purpose from the configuration because
nothing it had could open the README. The draft's assistant keeps its own brief: it has one task.

## Pasted images

A deployment's, a new deployment's and the server's chat take images with a question: pasted into the box,
dropped on its frame or picked with the toolbar's image button (`elements/prompt.tsx`, `acceptImages`). An image
alone is a question; a chat that begins with one is titled "A pasted image". The page makes each small before it
is sent (`agent/images.ts`): its longest side is held to 1568 pixels, what the models read at full detail, and it
is a PNG, or a JPEG when the PNG passes a megabyte, shrinking further if a photograph needs it. A 320-pixel JPEG
thumbnail goes with it.

The turn takes `images: [{mediaType, data, thumb}]`, at most 4 (`assistant.MaxImages`), and the route reads a
body up to about 9.5 MB for them (`httpx.DecodeJSONWithin`); every other JSON body stays at 4 MB.
`assistant.CheckImage` refuses a format other than PNG, JPEG, GIF or WebP, base64 that does not decode, bytes
that are not the format they claim (`http.DetectContentType`), and an image past 1.5 MB or a thumbnail past 96 KB,
each with a 400 that says which. The images go into the conversation before the question's words, as an
Anthropic `image` block or an OpenAI-compatible `image_url` part with a data address. A model that reads no
images answers with its provider's error. The saved chat's question line keeps the thumbnails, so a chat read
back draws them, and names each image by an id with its size (`{mediaType, data, id, width, height}`; the size is
read from the image's header, `assistant.ImageSize`). The image itself is kept whole beside the chat in
`assistant_chat_images`, sealed, and read back from `GET /assistant/chats/{chat}/images/{image}`, the owner's
alone. Each person keeps their newest 60 (`chatImageRetention`, some 30 MB of screenshots and at most about
120); an older one opens as its thumbnail, and the viewer says only a small copy is kept.

A thumbnail opens the image large (`components/image-lightbox.tsx`, which the README and pull request pictures
use too): in the box before it is sent, from the data the page made, and in a question after, from this tab's copy
or the server's. The viewer makes room for the image from its size and draws the thumbnail there while the image
arrives, so nothing jumps; arrows, the arrow keys or a swipe walk a question's images, a click on one larger than
the window shows it pixel for pixel, and Escape gives focus back to the thumbnail.

The conversation keeps the images too, sealed with the rest of it, for the model. To keep a chat of
many screenshots a size worth sending and saving after every step, it keeps the newest 8 and at most 6 MB of them
whole, the newest question's always, and the older ones read as "(an image the person pasted earlier, left out to
keep the chat short)". An image counts for about 1,600 tokens against the chat's length, not for its bytes. The
audit entry says how many images a question carried.

**An image is not masked.** Masking reads text, so a secret in a screenshot reaches the model. The box says so
while an image waits, and `SECURITY.md` says it too.

## A shortcut to what it changed

`called` names where a change is shown. `agentPages` maps a route under `/deploy` to its page and section;
`TestTheCatalogueIsTheRoutesItSaysItIs` fails for a write with no line. A change through `api_request` elsewhere
names its page by the start of its path (`agentAPIPages`: a database, a backup job, a container, a site), when the
dashboard has one. A run names the run. A configuration
is read each side of the save (`agentConfiguration`), so the card can say what is different and point at
the part that changed (`agentConfigurationPages`, `runtime.port` to Runtime's *Where it listens*).

The page draws it as **Open Runtime →**, and pressing it scrolls to the section and lights it for a moment
(`assistant/agent/arrival.ts`). The conversation does not stop: it is held outside any page
(`assistant/agent/sessions.ts`) and goes on streaming, and the dock in the shell
(`assistant/agent/dock.tsx`) says what it is doing, lights while it works, turns amber when it waits on the
person, and goes **Back to chat**. Remote Desktop's sessions outlive their page the same way.

## A new deployment

`POST /deploy/drafts/{draft}/assistant` is the same route about a draft, under the draft group's
`system.admin` and session. `assistant.SetupSystemPrompt` tells it what the dashboard read of the source,
and it is offered the draft's routes, the reads under `/deploy` they are chosen from, and the API. Its own
saves and those reads never wait for leave in any mode (`agentHolds`), because a draft runs nothing until it is
committed. What a deploy key's commit would be refused is asked instead, because text in the repository
reaches the model through what detection read (`agentDraftAsks`): committing the draft — **Deploy is the
person's press unless they chose full access** — discarding it, and a save that changes what is being deployed,
points a variable at a saved credential, store or database, or has host authority (a release task on the host,
a privileged container, a host path) waits for the person in "ask" and "changes".

`/deploy/new` offers it twice: the switch above the sources, which carries through once a source is chosen,
and an offer at the head of the plan once one is. It sets up in a column beside the form, the whole screen on
a phone. What it saves is read back into the form (`SetupAssistant`), and Deploy waits while it works, because
the page's revision is behind the server's until it stops.

## <a id="providers"></a>The assistant's providers

Set up once, used everywhere. `/assistant` is the page (`components/assistant/assistant-page.tsx`), and
`api/handlers_assistant.go` the routes, `system.admin` but for the read:

| Route | Does |
| --- | --- |
| `GET /assistant` | The active provider, every saved one, the presets and the default mode. No key is ever in it |
| `PUT /assistant/providers/{preset}` | Saves one, once it answers a check, and makes it active if asked or if nothing else is |
| `POST /assistant/providers/{preset}/activate` | Switches every page to a saved one, and to another of its models |
| `DELETE /assistant/providers/{preset}` | Forgets one and its key; the next saved one takes over |
| `POST /assistant/models` | Lists what a provider can answer with: the saved key, or one typed in the form, used for that request and kept nowhere |
| `PUT /assistant/preferences` | The mode a conversation starts in |

`assistant.Presets` is the closed list: Anthropic, OpenAI, OpenRouter, Google Gemini, Groq, Mistral, DeepSeek,
xAI, Ollama, LM Studio and another server. All but Anthropic speak OpenAI's chat completions, so a preset is a
logo, an address and whether it needs a key. A hosted preset's address is its own whatever the form sends, so
a key typed for it cannot be pointed elsewhere; a saved key stays with its own provider and address. The
keyring (`assistant.Keyring`) is one row of `settings`, `assistant.keyring`, sealed whole. An install that
saved a provider before there were several reads as a keyring of that one (`RingOf`) and moves on its next
save. `/databases/assistant` still answers, on the same keyring.

An OpenAI-compatible provider is offered tools only when its setup says it takes them (`Settings.ToolCalls`):
always for a hosted preset, the person's choice for a local one, and never for a setup saved before there was
the choice, because plenty of local models refuse a request that carries them. Without tools it answers from
what the page shows, and the page says so.

Every place that uses it shares `components/assistant/`: `settings.ts` (`useAssistantSettings`, which every
page's reading follows when one changes it), `settings-sheet.tsx`, `model-picker.tsx`, `mark.tsx`, `off.tsx`.
A place added later reads these and has the provider.

## The page

`components/assistant/agent/`: the conversation as data in `model.ts` (unit-tested in `model.test.js`), held
outside any page in `sessions.ts`, in memory for the tab and never in Web Storage because an answer quotes the
application's own log lines; drawn by `chat.tsx`, `step.tsx`, `mode-menu.tsx` and `dock.tsx`, laid out by
`workspace.tsx` (the chat as the page itself, a rail after a hairline, one pane at a time on a phone), with the
person's chats listed by `chat-list.tsx` from `chats.ts`. What lasts is the
server's: a chat is read back from `GET /assistant/chats/{chat}` and its lines replayed through the same model, so
it opens again as it looked in another tab, another browser or after a restart (see [saved chats](#saved-chats)).
The usage line under an answer and in the rail counts every token the model read (`inputTokens`): Anthropic
counts what it read from and wrote to its cache apart from `input`, so a long chat once read "6 tokens in".
`components/assistant/elements/` is AI Elements (vercel/ai-elements, Apache-2.0, see its `NOTICE`), adapted to
this product's marks. `components/deploy/project-assistant.tsx` is the project's page: the conversation, and
beside it its chats and what it did with a shortcut to each change; `?chat=<id>` opens a saved chat there.
`components/assistant/assistant-page.tsx` is `/assistant`: a chat over the whole server, every chat of the
person's listed beside it (a deployment's opens on that deployment's page), and the settings under
`?view=settings`. A chat another tab is answering offers Take over, which stops that press and carries the
chat on here — with the person's question, or with none, from where the other press put it down. On a phone
each pane is one at a time with a bar to switch. `new-project/setup-assistant.tsx` and `assist-switch.tsx`
are the new deployment's.

## Tests

- `assistant/agent_test.go`, `assistant/calls_test.go`: calls read to their tools, bounds held and said, unknown
  fields said, arguments and whole inputs written as text read, `read_source`'s paths however they are written, a
  call that cannot run answered with why, only an unreadable answer refused; the prompts telling it to answer
  what was asked, as a general assistant, from the code for what a project is; a Claude answer streamed with parallel calls, one bad, round-tripped through
  Anthropic with its thinking and every call answered; the same through an OpenAI-compatible server; the prompts;
  a saved conversation loaded as it was, a long chat shrunk, and a chat loaded, owned, busy and taken over.
- `assistant/keyring_test.go`: an earlier setup as a keyring of one, a hosted preset keeping its address, a key
  staying with its address, and no key in any view. `assistant/openai_test.go`: tools sent only when set up,
  streamed calls gathered whole, results answering each by id.
- `httpx/delegate_test.go`: the assistant admitted as the person by every group, marked for the trail, and a key's
  principal not carried.
- `api/handlers_assistant_turn_test.go` and `api/handlers_assistant_chats_test.go`, against the fake provider with
  the whole router under it: a read and a change held by the mode; a change run through its route on Allow,
  audited as the person with actor `assistant`; full access running it unasked and held to the person's role; a
  schedule that runs a command asked for, not refused; a Claude answer with a bad call going on, the bad call
  not shown on the page; another deployment's README read through `read_source`, its secret masked
  (`api/handlers_assistant_source_test.go`); a pasted screenshot reaching the model before the words, kept as a
  thumbnail in the saved chat, an image alone asked, a body past 4 MB taken, and what is not an image refused
  (`api/handlers_assistant_images_test.go`); variables read
  through `api_request` with the secret masked; backups listed and one started, held in "changes" and run in
  full; a read-only SQL statement run unasked and a writing one held; excluded routes refused with their reason;
  the server's chat masking secret-named fields, key material and every deployment's values; a configuration's
  diff and shortcut; one deployment's reach for `call_operation`; the step bound; a press stopping with its
  session; a draft configured with Deploy waiting; chats saved, listed, read back, renamed, deleted and carried on
  after a restart; a chat its owner's alone; a take-over stopping the press; retention; a deployment's chats going
  with it. `api/handlers_assistant_routes_test.go`: every streaming handler excluded, read off the source;
  `find_routes`; every page with an address.
- `assistant/image_test.go`: an image read for what it is; images before the words, an image alone, the newest
  kept and the older left out, a saved chat keeping them, and each provider's shape.
  `components/assistant/agent/images.test.js`: the size held to 1568 pixels, only images taken, at most four,
  and a saved question drawn with its thumbnails.
- `assistant/command_test.go`: what a shell line is read as — reads, changes and destructive lines, a redirection,
  a substitution, a subshell or a background job never a read — a `run_command` call checked like the others,
  and every chat but a new deployment's offered the shell.
- `api/handlers_assistant_terminal_test.go`, with real held windows: a password prompt answered by the person
  through the chat's route and never sent to the provider, the saved chat or the audit entry; a question
  answered, a long command followed and stopped with Ctrl-C, what was printed read without its codes, a window
  found again after it was forgotten; and the capability, a chat with no window, a masked value and the mode
  held. `assistant/command_test.go` checks the terminal's calls and keys.
- `api/handlers_assistant_database_test.go`: a connection's chat beginning with its structure and no row, offered
  the dashboard and the shell but not a deployment's tools, listed under the connection and carried on only as
  its own; a protected connection refused its chat and kept from the server's; what the editor attaches sent
  with the question and kept by label alone; a server chat saying the page it was opened from; a connection's
  chats going with it; a shell line that reads run unasked and masked, ones that write or destroy waiting, a slow
  one stopped, and the terminal's capability and switch held; a database's shortcuts its pages.
- `deploy/source_reading_test.go`: the README of the commit that would be built, not the working tree, the top
  folder folders first, a path not in the commit said; a way up read from the top, a link out, `.git` and a
  binary file refused or named, and the read held to its size.
- `store/schema_assistant_chats_test.go`: the tables on a fresh and an upgraded install, and a chat going with its
  owner and its deployment.
- `tests/browser/assistant.spec.ts`: the page, Diagnose, a card's Allow and Decline, the mode, a screenshot pasted
  and sent at 1568 pixels, one taken out and an image alone asked, a shortcut and the
  dock, a conversation going on behind another page, the model picker, the provider page, a new deployment handed
  over, a role without the capability, the assistant off, and the phone's one pane.
