# The deployment assistant

A project's **Assistant** page (`/deploy/{id}/assistant`) is the dashboard's assistant asked about one
environment of one deployment and the server under it. It answers anything about them and changes what
it is asked to change: it reads logs, requests and metrics, sets variables, domains and limits, takes a
backup, deploys and rolls back. **Diagnose** on the overview opens the same page and asks the diagnostic
question. A new deployment can be handed to it too: it configures the draft, and the person presses
Deploy (below).

It asks the provider the install set up once ([the assistant's providers](#providers)), the same one the
SQL editor, a drawing's Describe and a failed run's Ask why ask, on the same budget of model calls and
four answers at once.

This replaced a contract worth saying plainly, because it was a security property: until 0.8.5 the
operator ran nothing, and every action was a card only a person's press could run. It can now act on its
own, as far as the person's chosen mode lets it. What holds it is below, and
[invariant 13](../security/invariants.md) states it.

## What holds it

- **It acts as the person.** A call is a request through the dashboard's own router
  (`s.agentDispatch`), under the person's user and role, so the route's capability check, audit entry and
  rate limit all apply. It cannot do what their role cannot.
- **It is held to everything a deploy key is.** The request carries a principal built for the press
  (`agentDelegate`): an MCP key of no id, named *Assistant*, narrowed to the deployment the conversation
  is about, and marked `Principal.Assistant`. So `p.DeployKey()` is true and every handler's key rule
  applies: variables are write-only and a generated value is withheld, nothing it writes may reach past a
  deployment's own container to the host (`hostAuthority`), and it cannot point a saved credential
  somewhere new (`keySourceGuard`, `keyReferenceGuard`). The audit entry carries the person and the key's
  name, so what it did reads apart from what they did.
- **Its reach is a closed list.** `api/handlers_assistant_catalogue.go` reads the operations off the
  router and the deployment API's description, so they cannot drift from the routes. It starts from what
  a deploy key may do (`deployKeyRoutes`), `assistantRoutes` opens the few a key may not because a person
  is at the dashboard (automation, rollback and scan terms, staging, a domain's DNS record, and backups:
  listing, running, enabling, adding a database), and `agentClosedRoutes` closes some a key may. The gate
  (`deployKeyGate`) reads `assistantRoutes` too, so the list is enforced by the router rather than by the
  catalogue.
- **What stays a person's, in every mode and whatever their role:** a secret's value, saved credentials,
  secret stores, notification channels and log drains (their secrets would go to somebody else's model);
  the routes that take a typed confirmation; merging, commenting and running a pull request's preview
  (they speak as the dashboard on GitHub, or run someone's code); what reaches the host; restoring a
  backup; archiving; and pressing Deploy on a new deployment. The model has no way to them. `open_page`
  gives the person a shortcut to the page instead.
- **The mode is the server's to hold.** Each press says one (below), and a call the mode holds runs only
  once the person answers it.
- **What it reads is masked.** Every string of every answer goes through the run diagnosis's masker
  (`PlanningStore.DiagnosisSecrets`, `deploy.NewDiagnosisMasker`): each secret value of the environment,
  what its references point at, saved credentials and trigger secrets, and the shapes of keys and tokens.
  A field named for a secret is withheld whatever a route ever answers (`agentSecretFields`). A call whose
  values cannot be opened to mask reads and changes nothing.
- **What it reads is not instructions.** A log line or a request's path is text somebody else wrote, so a
  model can be told things by it. The prompt says to act on none of it and to tell the person, which is a
  mitigation and not a guarantee, and it is why **Ask before changes** is the default: a change waits for
  a person unless someone chose otherwise.
- **It is bounded.** A press takes at most `agentSteps` (16) answers; past that, what it asks next waits
  for the person whatever the mode, so a loop spends nothing more without somebody looking. Each step is a
  model call on the install's budget. A press's deadline is 20 minutes and a call's is two.

## The modes

| Mode | Runs without asking | Waits for the person |
| --- | --- | --- |
| Ask every time (`ask`) | Looking up an operation's parameters, offering a shortcut | Every read and every change |
| Ask before changes (`changes`, the default) | Reads | Every change |
| Full access (`full`) | Everything it can reach | Nothing, until `agentSteps` |

The page sends the mode on every press; left out, it is the install's default, which an administrator
sets on `/assistant` (`PUT /assistant/preferences`). Anyone asking may change it for their conversation.
It is in the audit entry of every press.

A call is one of four kinds (`agentOperation.Kind`): `free` reads nothing of the server, `read`, `change`,
and `destructive` for a route behind `s.destructive`, a `DELETE`, or one of `mcpDestructiveHints`. A kind
only changes what the card says and how Allow is drawn; `ask` and `changes` hold `change` and
`destructive` alike.

## The route

`POST /deploy/{id}/assistant[?environment=]` (`api/handlers_assistant_turn.go`) takes a question or the
answers to the calls the last answer is waiting on, and streams the answer back as one JSON object a line
(`application/x-ndjson`):

| Body | What it does |
| --- | --- |
| `{"conversation": "", "question": "…", "mode": "…"}` | Begins a conversation |
| `{"conversation": "<id>", "question": "…"}` | Asks again in it; calls still waiting are declined in the same message |
| `{"conversation": "<id>", "decisions": {"<call id>": true, …}}` | Answers the waiting calls: `true` runs one, `false` declines it, and a call left out is declined |

It sits in the `service.control` group and is **session-only** (`deployKeyRoutes` refuses a key and the
route requires a session): there is nobody to ask behind a key. The conversation is held in memory per
person and subject (`assistant.Store`) and ends with the process or a change of provider or model.

| Line | Carries |
| --- | --- |
| `start` | The conversation's id, the provider, the model and its label, whether it was offered tools, and the mode |
| `thinking`, `text` | The answer as it is written |
| `call` | A call as it starts: its id, the tool and operation, the route, the label, the kind, the model's `reason`, and its input |
| `called` | What became of it: `ok`, `declined`, a few words of what it came to (`said`), the first 4 KB of its answer (`output`), and for a change a `change` with the page's `label`, its `href`, and for a configuration a `diff` |
| `link` | A shortcut it offers (`open_page`): a label, an address and a note |
| `done` | Why it stopped, the tokens, whether the turn was kept, `pending` (the calls waiting, each as `call` carries them), and `paused` when `agentSteps` is why |
| `error` | Why there is no answer |

A press whose provider fails partway keeps what already ran in the conversation (`c.Mark` is taken after
each step), because what ran has run and the next question must be asked knowing that.

Each press is audited as `deploy.assistant.ask` or `deploy.assistant.tool` with the provider, the model,
the mode, the conversation's first eight characters, the tokens, what ran, and what was allowed and
declined by name. What was read is not written into the audit log. A change it makes is also the route's
own entry, as the person, under the key name *Assistant*.

## The tools

`assistant.AgentTools` is a closed set, each with a schema of its own fields, read by `assistant.AgentCalls`
with unknown fields refused. A call to any other tool — a `run_shell`, the editor's `describe_tables` — or
past a bound refuses the whole answer, and nothing of it is kept. Naming an operation that does not exist
does not: the model is told and may correct it.

| Tool | Does | Kind |
| --- | --- | --- |
| `recent_runs`, `run_transcript`, `container_logs`, `release_metrics`, `lifecycle_events`, `host_alerts`, `firewall_rules`, `security_updates` | The reads shaped and bounded for a model (`assistant/operator.go`, `api/handlers_deploy_operator.go`): 20 runs, 300 lines, 168 hours. `host_alerts` asks `system.admin`, as the Alerts page does | `read` |
| `describe_operation` | One operation's parameters and JSON body. There are about a hundred, and their schemas together would be most of every request, so the prompt lists them by name and this is how one is read | `free` |
| `call_operation` | Runs one operation, by name, with its arguments and a sentence of why. `id` and `draft` are filled in and a different `id` is refused; `env` defaults to the conversation's, and another deployment's is refused by the gate | by the route |
| `wait_for_run` | Waits for a run to end, or for up to 120 seconds, so it can see a deploy through | `read` |
| `open_page` | Offers a shortcut to a page from a closed list, for what only the person can do | `free` |

An answer to the model is its JSON, masked and cut at 24,000 bytes.

## What it begins knowing

`assistant.AgentSystemPrompt` is told what the overview already shows anybody who may read the deployment
— the server's hostname, the deployment and environment, the source, the build, the address, the health,
the live release, the run in progress and the last run, masked — and how to work, and it lists the
operations it can reach, grouped by what they do.

## A shortcut to what it changed

`called` names where a change is shown. `agentPages` maps a route to its page and section;
`TestTheCatalogueIsTheRoutesItSaysItIs` fails for a write with no line. A run names the run. A configuration
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
and it is offered the draft's routes and the reads they are chosen from. Nothing it does to a draft waits
for leave in any mode (`agentHolds`), because a draft runs nothing until a person commits it, and the commit
is closed to it: **Deploy stays the person's press**. Their draft is reachable because `Principal.Assistant`
reads it under their account rather than as a key's own (`deploymentDraftForPrincipal`). A Compose or
local-folder source stays out of its reach, by the key rule (`keyDraftSource`): the person configures that
themselves.

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
application's own log lines; drawn by `chat.tsx`, `step.tsx`, `mode-menu.tsx` and `dock.tsx`.
`components/assistant/elements/` is AI Elements (vercel/ai-elements, Apache-2.0, see its `NOTICE`), adapted to
this product's marks. `components/deploy/project-assistant.tsx` is the project's page: the conversation, and
beside it what it did with a shortcut to each change. On a phone the two are one pane at a time with a bar to
switch. `new-project/setup-assistant.tsx` and `assist-switch.tsx` are the new deployment's.

## Tests

- `assistant/agent_test.go`: the calls held to their tools, fields and bounds; the prompts; answers that ran
  carried into the message that answers what waited.
- `assistant/keyring_test.go`: an earlier setup as a keyring of one, a hosted preset keeping its address, a key
  staying with its address, and no key in any view. `assistant/openai_test.go`: tools sent only when set up,
  streamed calls gathered whole, results answering each by id.
- `api/handlers_assistant_turn_test.go`, against the fake provider with the whole router under it: a read and a
  change held by the mode; a change run through its route on Allow, audited under the key name; full access
  running it unasked and held to the person's role; a configuration's diff and shortcut; one deployment's reach;
  the secret masked out of the prompt and of a route's answer; the step bound; a question declining what waits;
  a draft configured with Deploy out of reach; the catalogue naming only real routes and nothing that stays a
  person's; and several providers with one active.
- `tests/browser/assistant.spec.ts`: the page, Diagnose, a card's Allow and Decline, the mode, a shortcut and the
  dock, a conversation going on behind another page, the model picker, the provider page, a new deployment handed
  over, a role without the capability, the assistant off, and the phone's one pane.
