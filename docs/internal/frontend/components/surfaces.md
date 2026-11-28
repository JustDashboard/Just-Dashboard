# Surfaces

Which surface a block is (`Panel`, `Pane`, `Well`, `Group`), and when a block is framed.

**Members:** `Panel`, `PanelHeader`, `PanelToolbar`, `PanelBody`, `PanelFooter`, `Pane`,
`PaneHeader`, `PaneSection`, `PaneFooter`, `Well`, `Group` (all in `components/panel.tsx`). `Page`
is in [`page-header.md`](page-header.md). The head of a panel has its own page,
[`section-head.md`](section-head.md). Grounds, elevation and radius are in
[`../foundations/surfaces.md`](../foundations/surfaces.md).

## Panel

The one content block. A page is a stack of them: an optional head, an optional toolbar, a body and
an optional footer. The props decide what a panel holds, not how it is drawn.

**Anatomy.**

```
Panel plain                      Panel framed
Title                  4  Verb   ┌──────────────────────────────┐
───────────────────────────────  │ Title               4  Verb  │
[toolbar]                        ├──────────────────────────────┤
───────────────────────────────  │ [toolbar]                    │
body                             ├──────────────────────────────┤
───────────────────────────────  │ body                         │
footer                           ├──────────────────────────────┤
                                 │ footer                       │
                                 └──────────────────────────────┘
```

A plain panel's head sits on the page's own edge, and the hairline is the only rule under it. A
framed panel is the same anatomy inside a border on the card ground (`rounded-xl`, 20px gutters).
Nothing lifts, and neither has a shadow.

**Plain or framed.** A block on a page is plain unless it can say why it needs an edge (D6). A
table can say why: its container scrolls, and without an edge a row cut off at the right reads as
a row with nothing more. A danger zone can say why too. A panel that is a destination (`interactive`)
is a card. Everything else is plain.

| Prop | What it does |
| --- | --- |
| `plain` | No frame: the title, the hairline, the content. 400 of 424 panels pass it. |
| `framed` | The frame, said out loud. Framed is still the default, so a panel without `plain` is framed. A framed caller passes `framed` with the reason in a comment; once every one does, the default flips to plain and `plain` retires (WP-39, PRM-08). `framed={false}` is plain. |
| `interactive` | The panel is a link or a button. Its border steps up under the pointer, and nothing else moves. |
| `…section props` | `aria-label`, `id`, `className` and the rest of a `<section>`'s props. |

`PanelBody` takes `flush` for a body that is one full-bleed table or list. A plain panel keeps 8px
under the hairline even when flush, so a row's hover wash does not butt into the rule. It takes
`scroll` for a body that owns its scrolling. `PanelToolbar` is the row of filters under the head on
the panel's own ground, with a hairline under it. In it, a list's search, sort and chips are a
`ListToolbar` ([`search-toolbar.md`](search-toolbar.md)). `PanelFooter` closes the panel, and it
holds a count, a retention note or one verb.

```tsx
{/* Framed: the table owns a scroll region, and the edge is what says so. */}
<Panel framed>
  <PanelHeader title="Services" count={installed.length} />
  <PanelToolbar>…</PanelToolbar>
  <PanelBody flush>
    <Table>…</Table>
  </PanelBody>
</Panel>
```

**The phone.** A framed panel inside a `Page` runs to the screen's edges below `sm`. It drops its
side borders and radius and keeps its ground and its top and bottom hairlines. Its own 20px
padding is then the page's gutter, so its rows line up with everything above them instead of
sitting 20px of gutter, a border and 16px of padding in from the edge (318 of 390px, MOB-19). An
`interactive` panel keeps its card on a phone, because the card is what you press.

**States.** A panel is drawn with the page, its head first. Its body holds the loading, empty and
error states ([`states.md`](states.md)), in the box the content will fill.

**Accessibility.** A panel is a `<section>`. Give it an `aria-label` when it has no visible head.
Otherwise its head names it. The head's `level` keeps the outline in order where a panel sits under
a `Section` ([`section-head.md`](section-head.md)).

## Pane

A sized region of a workspace that owns its own scrolling: the terminal's session rail, a file
tree, a log console, a diff. Structurally it matches a framed `Panel`. The difference is in how it
is composed: a panel is a block stacked in a page's flow, and a pane is a region of a workbench.
`flush` drops its frame for a pane that is one column of a workbench sharing a single frame, with a
hairline between columns.

`PaneHeader` is the pane's chrome strip: `min-h-9`, `px-2.5 py-1.5`, on `--surface-header`. It is
tighter than `PanelHeader` on purpose, and in a split view the strips are what line the regions up.
`PaneFooter` is the closing strip.

### PaneSection

A titled group inside a pane beside the work: the files inspector's Details and Contents, the map
inspector's Ports, a remote session's Clipboard, the assistant's activity. Four inspector columns
used to draw this four ways: two eyebrow heads, a semibold h2 and an unruled eyebrow, with the rule
above in one column and below in the others.

```
PORTS  3                      [Copy]
rows…
────────────────────────────────────
CLIPBOARD
…
```

| Prop | What it does |
| --- | --- |
| `title` | The group's name, drawn as the `eyebrow` (10px caps, 600). It also names the `<section>` for a screen reader. |
| `count` | How many the group holds, beside the name, tabular in the hint size. |
| `aside` | One control at the head's end: a copy, a toggle, a small verb. |
| `level` | The heading element: 3 by default. Use 4 under a pane whose own head is an h3, or 2 at the top of a page. |

A `PaneSection` is `px-4 py-3.5`, with the head 24px tall and 10px over the body. A hairline runs
under each group except the last. It is not for a page's sections: those are `PanelHeader` heads.

## Well and Group

`Well` is a recessed region for output you read: command output, a log tail, a diff, a stored
secret. It is mono by default, and `plain` gives the same ground for prose or controls. `Group` is
a fence around part of a body: a set of ports, one release task, a repeated form row. It has one
radius and one padding, with a `tone` for a group that holds an error or a warning, and `tinted`
for a region that should read as distinct. A `Group` of related inputs takes `role="group"` with an
`aria-labelledby`.

## What enforces it

- `src/components/panel.test.js`: `framed` and `plain`, the phone rule's classes, and
  `PaneSection`'s anatomy.
- The `no-restricted-imports` rule keeps raw `Dialog` and `Sheet` inside `Modal`, `PaletteModal`
  and `SidePanel`.
- `tests/browser/design-system.spec.ts` walks every page at desktop and phone widths for the shared rules (centred rows, no filled pill, names on icon controls).

## Related

[`section-head.md`](section-head.md), [`search-toolbar.md`](search-toolbar.md),
[`list-row.md`](list-row.md), [`table.md`](table.md), [`workbench.md`](workbench.md),
[`../foundations/surfaces.md`](../foundations/surfaces.md).

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
| `EmptyNote` | "Nothing here" inside something already framed: a rail, a picker, a panel under a filter. One line at the row's own size (`dense` in a rail), with `action` for the way out | Not an `EmptyState`, which would be a frame in a frame |
| `Rail` | The list an item's page came from, beside it from `xl` (D9): `Rail` and `RailRow` in `components/rail.tsx`, inside `DetailPage` ([`rail.md`](rail.md)) | Not a sheet over the list, and not a rail of your own: the areas' copies are gone or deprecated |

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
place into a square on the text's own colour to stop while it answers, its name the word since there is none.
While it answers the box still takes a question: Enter, or a second round arrow that joins Stop on its left
(*Ask when it finishes*; Stop keeps its corner), sends it to wait its turn (`AgentSession.queue` in
`agent/sessions.ts`, the same for every chat: a deployment's, a connection's, the server's and a draft's). Each one
waits under the answer as a dashed row of one height — *Waiting*, the question cut to the line, what goes with it in
words (*with 2 images, statement*), and **Edit**, which puts it back in the box with its images and what was pressed
for it or sent with it that the page still offers (a failed statement's *Ask the assistant* carries its error), and **Remove** — and nothing opens under a row. What decides when it goes: an answer that ends as an answer
(done, cut at its length, or declined by the model) asks the first one, and the next after that answer, in order.
An answer that waits on an Allow keeps them waiting (the box says *What waits is asked once you answer what it is
waiting on*), and the answer after the Allow or Decline asks the first; with nothing waiting, a question sent then
still declines what waits, as before. A password the chat's terminal asks for holds them too: typing it carries the
chat on first (*I've typed the password.*), and what waits goes after that answer. Stop, an answer that fails, and
one carried on in another tab (`taken_over`) send nothing more: every waiting question goes back into the box,
unsent, to send again or change. A question refused because another tab is answering the chat (`chat_busy`,
`conversation_busy`) is held for **Take over** as before, and the rest wait behind it; Take over sends the held
one, and what waits goes after its answer. Each one carries the images and attachments it was sent with. A page
can hold the next turn while it writes what the chat works on (`holdAgentTurns`; the new-deployment page does while
it saves the person's edits to the draft): what is sent then waits the same way, and the box says *What waits is
asked once the page has saved your changes*. An image waiting in it, or asked with a
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
*Continued somewhere else* and stops. Below the chats, what it has read, once it has read something, and the
tokens it took; a change is not listed again there, since its shortcut stands under its call in the conversation.
On a phone the chat, the chats and what it read are one full-height pane at a time with a bar at the foot
(`phone-panes.tsx`, shared by every page that shows one pane at a time: `patterns/keyboard-and-place.md` lists them);
the bar has no *What it read* stop until the chat has read something or spent a token, since a fresh chat's pane
would be its status line alone. A step's well is named for what it holds: what a command printed, git's answer to a
push, the workspace it opened, or what it read.

A shortcut's arrival scrolls to the section and lights it with `data-arrived`, the focus ring's colour as a transition
on an outline, not a fifth animation. The floating **Back to chat** is the same surface as Remote Desktop's dock, in
the same corner, and sits above it when both are up. `/assistant`'s second tab is the install's settings: who
answers, what a conversation starts on, and where it works.
