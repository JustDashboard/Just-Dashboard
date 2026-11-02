# The deployment assistant

A project's **Assistant** page (`/deploy/{id}/assistant`) is the dashboard's assistant asked about one
environment of one deployment, and the server's own chat (`POST /assistant/chat`) is the same assistant asked
about the whole server. It answers anything and does what it is asked: it reads logs, requests, metrics and a
database's rows, sets variables, domains and limits, runs a backup, deploys and rolls back, and reaches the rest
of the dashboard — databases, backups, Docker, files, the firewall, the proxy, users — through the dashboard's
own API. **Diagnose** on the overview opens the deployment's page and asks the diagnostic question. A new
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
  | `/auth/*`, `/account/password`, `/account/2fa`, `/account/recovery-codes`, `/account/passkeys`, `/account/sessions` | A person's own credentials |
  | `POST /tokens`, `/tokens/deploy`, `/tokens/mcp` | A new key's value is shown once, and would be masked from the model and the page alike |
  | `POST /dashboard/uninstall`, `/dashboard/link` writes, `PUT /dashboard/usage-ping` | The person's own press ([invariants 12 and 16](../security/invariants.md)) |
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
| Ask before changes (`changes`, the default) | Reads, including a SQL statement that only reads | Every change |
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

## The routes

`POST /deploy/{id}/assistant[?environment=]`, `POST /deploy/drafts/{draft}/assistant` and `POST /assistant/chat`
(`api/handlers_assistant_turn.go`) take a question or the answers to the calls the last answer is waiting on,
and stream the answer back as one JSON object a line (`application/x-ndjson`):

| Body | What it does |
| --- | --- |
| `{"conversation": "", "question": "…", "mode": "…"}` | Begins a chat |
| `{"conversation": "<id>", "question": "…"}` | Asks again in it; calls still waiting are declined in the same message |
| `{"conversation": "<id>", "decisions": {"<call id>": true, …}}` | Answers the waiting calls: `true` runs one, `false` declines it, and a call left out is declined |
| `…, "takeOver": true` | Carries on a chat a press is running elsewhere: that press stops, and this one goes on with the chat as it stood |

Each is **session-only** and asks `service.control` (a deployment's sits in its group; the draft's in the draft
group's `system.admin`): there is nobody to ask behind a key. A chat a press is running answers `409 chat_busy`
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

A press that fails before it ran what it answered leaves those calls waiting, and the page asks again. One
whose provider fails partway keeps what already ran in the conversation (`c.Mark` is taken after
each step), because what ran has run and the next question must be asked knowing that.

Each press is audited as `deploy.assistant.ask`/`.tool`, `deploy.draft.assistant.ask`/`.tool` or
`assistant.chat.ask`/`.tool`, with the provider, the model, the mode, the chat's first eight characters, the
tokens, what ran, what was refused, the calls that could not run (`malformed`), what was allowed and declined by
name, and `takeOver`. What was read is not written into the audit log. Every change it makes is also the route's
own entry, as the person, with actor `assistant`.

## Saved chats

A chat outlives the press, the process and the browser (`api/handlers_assistant_chats.go`). A press saves its
chat after every step and as soon as the page has its first line (`saveChat`), so a crash or a take-over loses
nothing, and a chat that is not in memory is loaded back (`loadChat`, `assistant.Store.TakeChat`) exactly as the
provider was last sent it. A chat carried on by another model goes on without the thinking the first one gave
back, which the next would refuse (`agentCarryOver`). `assistant_chats` keeps one row per chat — owner, what it
is about, the model, when, its steps and changes, whether calls wait, and the conversation and its title sealed
with the master key, as the keyring is — and `assistant_chat_lines` what the page was shown, a sealed batch per
save. A chat goes with its owner and with the deployment it is about (`ON DELETE CASCADE`). Each person keeps
their newest 200.

| Route | Does |
| --- | --- |
| `GET /assistant/chats[?deployment=&draft=&scope=server]` | The person's chats, the newest first, at most 100: id, title, scope, times, provider, model, `running`, `waiting`, steps, changes |
| `GET /assistant/chats/{chat}` | One chat and its `lines`: each question and each Allow/Decline press, and every line the stream sent, as sent, with `at`; a run of text is kept as one line |
| `PATCH /assistant/chats/{chat}` | Renames it (`{"title"}`) |
| `DELETE /assistant/chats/{chat}` | Deletes it; `409 chat_busy` while a press runs it |

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
tool takes, drawn as a failed step, and the turn goes on. Only an answer whose calls cannot be told apart (a
call with no id, two under one) is refused, as `assistant.ErrUnreadableAnswer`.

| Tool | Offered to | Does | Kind |
| --- | --- | --- | --- |
| `recent_runs`, `run_transcript`, `container_logs`, `release_metrics`, `lifecycle_events` | Deployment | The reads shaped and bounded for a model (`assistant/operator.go`, `api/handlers_deploy_operator.go`): 20 runs, 300 lines, 168 hours | `read` |
| `host_alerts`, `firewall_rules`, `security_updates` | Deployment, server | The host's own reads; `host_alerts` asks `system.admin`, and the server's `firewall_rules` reads every rule | `read` |
| `describe_operation` | Deployment, draft | One operation's parameters and JSON body | `free` |
| `call_operation` | Deployment, draft | Runs one operation under `/deploy` by name. `id` and `draft` are filled in, and another deployment's id is pointed at `api_request`; `env` defaults to the conversation's | by the route |
| `wait_for_run` | Deployment | Waits for a run to end, or for up to 120 seconds | `read` |
| `find_routes` | Every scope | Looks the API up by words or an area, with each route's method, path, words and kind, and the matching routes it cannot make with the reason; an empty query lists the areas | `free` |
| `api_request` | Every scope | Makes any route of `/api/v1` as the person: `method`, `path`, `query`, `body`, `reason` | by the route |
| `open_page` | Deployment, server | Offers a shortcut to a page from a closed list (`AgentPages`, `ServerPages`) | `free` |

An answer to the model is its JSON, masked and cut at 24,000 bytes.

## What it begins knowing

`assistant.AgentSystemPrompt` is told what the overview already shows anybody who may read the deployment
— the server's hostname, the deployment and environment, the source, the build, the address, the health,
the live release, the run in progress and the last run, masked — and how to work, and it lists the
deployment's operations, grouped by what they do. `assistant.ServerSystemPrompt` is told the server's hostname
and its deployments by id, name and last run. Every prompt (`writeAPI`) tells the model it has the whole
dashboard through `api_request` and `find_routes`, lists the API's areas — each first path segment with its
count of routes, a sentence (`agentAreaBlurbs`) and a few reads, built from `chi.Walk` when the catalogue is —
says how to learn a body's shape (GET the resource first, or read the 400 the route answers), how to query a
database, what it cannot reach, and that secret values reach it masked.

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
  fields said, arguments and whole inputs written as text read, a call that cannot run answered with why, only an
  unreadable answer refused; a Claude answer streamed with parallel calls, one bad, round-tripped through
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
  schedule that runs a command asked for, not refused; a Claude answer with a bad call going on; variables read
  through `api_request` with the secret masked; backups listed and one started, held in "changes" and run in
  full; a read-only SQL statement run unasked and a writing one held; excluded routes refused with their reason;
  the server's chat masking secret-named fields, key material and every deployment's values; a configuration's
  diff and shortcut; one deployment's reach for `call_operation`; the step bound; a press stopping with its
  session; a draft configured with Deploy waiting; chats saved, listed, read back, renamed, deleted and carried on
  after a restart; a chat its owner's alone; a take-over stopping the press; retention; a deployment's chats going
  with it. `api/handlers_assistant_routes_test.go`: every streaming handler excluded, read off the source;
  `find_routes`; every page with an address.
- `store/schema_assistant_chats_test.go`: the tables on a fresh and an upgraded install, and a chat going with its
  owner and its deployment.
- `tests/browser/assistant.spec.ts`: the page, Diagnose, a card's Allow and Decline, the mode, a shortcut and the
  dock, a conversation going on behind another page, the model picker, the provider page, a new deployment handed
  over, a role without the capability, the assistant off, and the phone's one pane.
