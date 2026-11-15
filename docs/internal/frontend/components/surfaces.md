# Surfaces

Which surface a block is: `Panel`, `Pane`, `Well`, `Group`, and when a block is framed.

**Members:** `Page`, `Panel`, `PanelHeader`, `PanelToolbar`, `PanelBody`, `PanelFooter`, `Pane`,
`PaneHeader`, `PaneFooter`, `Well`, `Group`.

**Status: moved text.** WP-10 rewrites this page in wave 1 as purpose, anatomy, use when and when
not (and what instead), props, states, the phone, accessibility, an example call site, what enforces
it, and related families. Until then it holds the rules that were in `design-system.md`, below.

## 7. Which surface

| Surface | Is | Is not |
| --- | --- | --- |
| `Panel` | A block of content *on* the page: framed, header, hairline, body — or `plain`, the same anatomy with no frame | Not a working region |
| `RowList` / `Row` | A list of rows with hairlines between them: a leading mark, a title, a second line, a trailing state | Not a table — nothing lines up in columns |
| `Pane` | A sized region of a workspace that owns its own scrolling — session rail, file tree, log console. `flush` drops its frame for a pane that is one column of a workbench sharing a single frame, with a hairline between columns (the terminal page, the logs page's source rail beside its log workspace, the files page's sidebar, listing and inspector under the strip that holds the page's commands, and a deployment's Output inside its Logs page's pane) | Not a block in a page's flow |
| `Well` | Output you read: command output, a log tail, a diff, a stored secret | Not a fence around controls |
| `Group` | A fence around part of a body: a set of ports, one release task, a repeated form row | Not a `Panel` — no header, no lift |
| `StatTile` | One figure in a run of figures read as a table, in a `StatGrid`, with a sparkline or a strip and its tone (§15 pass 2) | Not a page's opening: no row of tiles opens a page (D4), and a project's Overview has none. A figure the reader came for is a fact on the identity line, a node of the picture or a `ReadingLine` |
| `ChoiceCard` | One thing you pick out of several, in a `ChoiceGrid` — a source, a template, a database engine | Not a `Panel`: a panel is content that happens to be framed, this is a button that happens to be large |
| `ChoiceRow` | One instance among many of one kind, in a `ChoiceList` — a repository, an image tag, an unfinished setup | Not a `Row`: a `Row` is read, a `ChoiceRow` is taken |
| `BarList` | A ranked list with the meter's track behind each name and the figure at the right — the top ten of something, with a signal segment for the share that is wrong | Not a chart, and not a `RowList`: nothing here has a second line worth a row. Not for a ranked list any more: that is a `RankList`. Every page left it in 0.8.6 — the Databases pages first, then a project's failure causes, the proxy's busiest sites, a stream's outcomes and clients, Suricata's signatures, the connection table's breakdowns and the Files inspector's largest entries. A query result's Chart view is the one importer left, because there the bars are the picture the reader asked for, and the lint rule `design-system/primitives` refuses the import anywhere else (the small size bars inside table cells are their own few lines, not a `BarList`) |
| `RankList` | A ranked list read rather than measured: name, figure and share in columns, with `SplitBar` for one strip of a whole over it, and `RankSkeleton` (rows of its own height) while it loads. Insights, every ranked list on the Databases pages and, since 0.8.6, every other ranked list in the product use it. The share is of a real whole — the schema's bytes, the keys walked, the runtime counted — and a list of the top few of a whole nobody read has no share column rather than a share of the largest | Not a `BarList`: no bar per row, so nothing grows from row to row |
| `BarReading` | One block of a band: a head, a bar, one line and one strip of marks, the same height in every state (§10) | Not a `BarList`: nothing is listed under the bar, and it is the only thing a band's bar is drawn with |
| `MissingThing` | A page whose subject is absent: the thing drawn as itself, one headline, the next step as a button ([`states.md`](states.md#a-thing-that-is-not-there-has-one-screen)). It is the proxy's `EngineSetup` made general | Not an `EmptyState` (an empty list) or an `ErrorState` (a failed read). A new page never copies `EngineSetup` |
| `EmptyState` | A list with nothing in it: what it would hold (`mark`), one sentence, the step that fills it. The proxy overview's `OverviewEmpty` is the same job inside one of its blocks, and stays its own | Not for an absent subject (`MissingThing`) or a failed read (`ErrorState`) |
| Rail | The list an item's page came from, beside it from `xl` (D9). One `Rail` lands in WP-07 ([`rail.md`](rail.md)); until then a page copies `procs/process-rail.tsx` | Not a sheet over the list, and not a second generic rail |

**Reach for `Panel`/`Pane`/`Page`, and add a variant there rather than a one-off in a feature page.**
Before these existed, fourteen pages read as fourteen products.

`Modal`, `PaletteModal` and `SidePanel` are the only assemblers of raw `Dialog` and `Sheet`, and
`no-restricted-imports` enforces it. A page never opens one itself.

**A conversation is a column of the workbench, not a window over it.** The SQL editor's assistant
(`database/query/query-assistant.tsx`) is the editor's third column, on its right with a hairline and a
resize handle, and lies over the editor like the rail where the frame cannot hold three columns. It is
not a second assistant: it draws `AgentChat` compact on the connection's own session, so a chat begun
there is the one in front on the connection's Assistant page, and the dock stays off both. What the tab
holds goes with a question only by the reader's press, as toggles over the box — *Statement*, *Result ·
3 rows*, *Error*, *Plan* (`query/attachments.ts`, a result as a small text table) — and the question
carries them as `Tag`s (*statement*, *result · 20 rows*), never their values. A statement in an answer is
a sunken block with its verbs in words — *Open in a tab*, *Insert*, *Copy* — drawn only once the answer
has finished, because a statement still arriving may not be whole; `AgentChat`'s `codeBlock` is where a
place gives a fenced block its verbs, rather than forking the chat. The line under the box says who it
goes to and what of the database goes with it, and turns amber while rows are attached.

**The deployment's assistant is the same conversation given a page, because it works.**
(`components/assistant/agent/`, drawn from AI Elements' registry components, `elements/NOTICE`.) A database's
is the same page (`agent/subject-page.tsx`, `SubjectAssistant`): each subject passes only its scope, its words,
its suggestions and what refuses it — an archived project, a protected connection, which says *Nothing about
<name> is sent to the assistant while it is protected* in place of the chat. What it did stands
in the answer as steps, each the product it touched on a tile (§14's argument for a logo: Docker's whale is read
before its label), with a word lit while it runs (`TextShimmer`) and a badge that turns from a spinner to a tick or a
cross as it ends. A route it called itself (`api_request`) is drawn as the request log draws one — the method on a
recessed chip, a read muted and a write in the method hue, and the path in the path hue (`MethodWord`,
`RequestPath`) — so `GET /databases/connections` reads as a request before it reads as words; looking routes up
(`find_routes`) is a muted line with no badge. A command it runs on the host (`run_command`) is drawn as the
command itself in mono, on the terminal's mark, with its exit beside it and what it printed in the step's well;
waiting on the person, the card shows it whole in a sunken mono block after a `$`, because the command, not a
sentence about it, is what a person reads to decide. A call into the chat's own terminal window — a terminal call, or a `run_command` the server typed into the window (a package change, a Docker restart, a `sudo`) — draws its command in mono like a shell line, and only the newest
one says what the window is doing under it when that is worth a line — *Still running in its terminal* or what it
is asking, on the header surface, with **Open Terminal →** — and, for a password, a card on the warning wash with
a password field and **Send**, saying the text goes straight to the window and never to the assistant; its tile
shows the waiting dot rather than a tick while it waits. What it changed is a card on the success wash with **Open Runtime →**
beside it, and what is different in mono, so a change is the loudest thing in the column. What it wants to do is a card
on the warning or danger wash with each call's own *Allow* and *Decline*, its values as `path value` lines rather than
JSON, since a person reads it to decide. Full access is amber on the toolbar for as long as it is on, and the mode
menu says what each mode does in plain words: full access runs everything without asking; asking before changes holds
every change. A question is right-aligned on `--surface-header`, the answer has the provider's real logo beside it,
and `BorderBeam` (§11: work is happening inside this frame) runs round the logo's tile and the composer while it works.

The chat is not in a frame. On a deployment's page, a connection's and `/assistant` it is the page's own ground from the
header's hairline to the window's foot (`ProjectShell` and the server page draw a `fill` page there, and a connection's
Assistant is a workbench section under its strip): the column of messages is
the one thing that scrolls, the composer sits at the foot with a small margin and nothing under it but the line that
says what is sent, and the rail is a column after a hairline, never a card. An empty chat centres its mark, title and
suggestions in the column. The composer draws no focus at all — no ring, no lit border, no glow — because it is the
page's one place to type and the blinking caret is the whole answer to "is it ready"; a press anywhere on its frame puts
the caret in it, and the buttons in its toolbar keep the ring for the keyboard. Its send is one round button
at the box's corner, as the chat apps people know draw it: an arrow up on the command's blue to ask, turning in
place into a square on the text's own colour to stop while it answers, its name the word since there is none. An image waiting in it, or asked with a
question, is a thumbnail that opens large on a press (`ImageLightbox`, `components/image-lightbox.tsx`, which README
and pull request pictures open in too): the picture as large as the window allows over a dark ground, room made for it
from its size with the thumbnail drawn there until it lands, so nothing jumps; arrows, keys or a swipe for the rest, a
click for pixel for pixel, Escape back to the thumbnail. A tile pops in when an image is pasted and out when removed.

The rail holds the chats and what the open one has done. **Chats** are the person's saved chats (`agent/chats.ts`,
`agent/chat-list.tsx`), newest first, each a row of one height in every state: its title, how long ago it moved, a
lit `signal` dot that breathes while a tab is answering it, and a warning `Tag` while calls in it wait on the person.
Rename turns the row's own line into the field; delete asks first through `useConfirm`; nothing unfolds under a row.
The deployment's page lists that deployment's chats and a connection's page that connection's; `/assistant` is a
chat about the whole server and lists every chat under what each is about, and a deployment's or a connection's chat
opens on its own page (`?chat=`). A server chat begun by **Ask** from another page carries that page
(`AgentTranscript.about`): a small square chip at the head of the column, *About shop · Logs*, is the way back to it,
the empty chat offers questions about the page rather than the server, and the server is told the page with the press
that begins the chat and never again. A chat opened
from the history is read back from the server and folded through the same reducer the stream feeds, so it looks
exactly as it did and can be carried on. One another tab is answering is followed and says so in a calm bar over the
box — a breathing dot, *This chat is running in another tab*, and **Take over** — and the tab it was taken from says
*Continued somewhere else* and stops. Below the chats, what it has changed with a shortcut to each, and what it has
read. On a phone the chat, the chats and what it did are one full-height pane at a time with a bar at the foot
(`phone-panes.tsx`, shared by every page that shows one pane at a time: `patterns/keyboard-and-place.md` lists them).

A shortcut's arrival scrolls to the section and lights it with `data-arrived`, the focus ring's colour as a transition
on an outline, not a fifth animation. The floating **Back to chat** is the same surface as Remote Desktop's dock, in
the same corner, and sits above it when both are up. `/assistant`'s second tab is the install's settings: who
answers, what a conversation starts on, and where it works.
