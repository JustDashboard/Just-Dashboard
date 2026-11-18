# Decisions: Workspace: account, settings, audit, files, git, terminal, logs

Why these pages look as they do. The stories were told in `design-system.md` §14 and §15 until 0.9.0
and are moved here verbatim, under a heading per page. They are history: a rule they state is in
[`../standard.md`](../standard.md) or the page it links to, and where a story and the standard
disagree, the standard wins.

## Files (from §14)

**A file is drawn as what it is, and a folder in the colour it was given.** The file manager drew
Material Design Icons' file family, a stencil per category in one flat tone: it told a config from a
certificate, but a directory of forty files was forty stencils and the format's own mark — the thing it
is recognised by — was nowhere. `components/file-icon.tsx` now draws its own two shapes, the ones every
desktop file manager has trained the eye on. A **folder** is two-toned, its tab behind its face, with
what it holds pressed into the face a step darker — a product's Simple Icons mark for `.git`, `.docker`
or `node_modules` (single-shape drawings, so a mask of one reads as the mark), a glyph from this
product's Heroicons vocabulary for `.ssh`, `etc` or `logs`. A **file** is a page with its corner folded,
the format's own logo on it in its own colours (devicon's language marks beside the product logos), and a
band along its foot in the format's colour carrying the extension where the icon is large enough to set
a word. The page is light — `--doc-page` stops short of white — because those logos were drawn for paper
and Rust's black gear or Markdown's ink is invisible on this ground; it is the one light surface in the
product and it is artwork, not a surface anything sits on. The band's hue is the format's (`--language-*`
where GitHub colours it, `--tag-*` otherwise), by the argument `LanguageMark` makes: a colour the reader
already knows the format by is a legend they do not have to learn.

A folder's colour is a **label**, and §3's tag argument is why it is the operator's: "the red one is
production" is a fact about this server, so it is stored there (`files.colours`) and drawn wherever the
folder is — the listing, the tiles, the sidebar, the inspector, the strip, the finder, the terminal's and
a checkout's tree, a volume's or a stack's browser, a deployment's storage. The shell provides the saved
labels to every page (`SavedFolderColours`, read again on each navigation); Files nests its own provider
over it so a colour it picks is drawn before the round trip lands.
The strip's compact folder button sets one colour for every folder, stores it as `files.defaultColour`,
and clears old individual labels. A folder can then be labelled on its own in its inspector or menu;
that label takes precedence until another global choice. Nine names (`--folder-*`, one value each;
the tab and the pressed mark are mixed from the face in `[data-folder]`), blue until somebody says
otherwise, graphite for build output and installed dependencies until a global colour is picked
because nothing in them is yours to edit. The picker is the folder drawn in each colour,
the chosen one `bg-accent` like every selection. The Files page is a workbench and, like the terminal
and a Git working copy, has no page header: its commands sit in the strip across the workbench, beside
the folder they act on.

Files and `/files/editor` take the **reading** register: their frames contain independently scrolling
workbench panes, while directory and selection readings live in the status strip. Tiles use fixed,
compact columns (80/104/128px) and tight gaps rather than stretching to fill empty space. A tile
contains its icon and name; metadata stays in details view and the inspector. Listing entries have
no overflow dots: their menus open through right-click, Shift+F10 or touch long-press. Selection and
clipboard commands float above the listing's foot, with a four-pixel arrival/exit and opacity over
160ms (instant with reduced motion), so selecting never inserts a row or shifts the workbench.
Selection washes and drag-source opacity ease; the marquee follows the pointer immediately.
The sidebar's active wash and
`aria-current` identify its folder without another dot. Its rows are one line — the folder drawn as
what it is and the name of what it holds (an account's name for a home, *File system* for `/`,
*Configuration* or *Logs* for the server's own folders, named by `files/places.go`) — under four
headings that fold and stay folded: Home, Starred, This server, Recent. A second line carrying each
place's path or a caption such as "Locally installed software" doubled the column and read as small
print; the path is the row's tooltip. Every control in the strip has a face and a 32px height, and the
faces come in groups rather than as a row of bare glyphs with three boxed buttons among them: the
sidebar toggle; Back, Forward, Parent and Refresh as one box (`StripGroup`, the Git workspace's fetch,
pull and push shape: the group draws the edge, each segment only its hover); where you are as a
field — the colour button, the crumbs and the star inside one `bg-input/30` edge, which is what it
becomes on Ctrl+L; Find and content search as a second field-shaped box; the view toggle and Arrange;
then the account, New, Upload and the details toggle. Upload keeps the brand face as the page's one
command. Name and content search share a compact keyboard palette with
file identities, paths and highlighted matching lines. Quick editors keep these same controls when
opened in the full workspace beside a collapsible tree.

Below `lg` Files and `/files/editor` show one pane at a time with a bar at the foot (`phone-panes.tsx`):
Places, Folder and Details, and Files and Editor. The strip is then one row and never wraps: Back, the
path (its crumbs scrolled to the open folder), Find, New, and a `MoreMenu` holding the rest of the wide
strip as words, drawn by the listing's own `VerbList`. The inspector says what a folder holds as a
sentence ("12 folders, 40 files, 1.2 GB", `files/counts.ts`), the size joining once it is measured,
instead of two boxed figures. The editor's commands (`editor-toolbar.tsx`; the two Files pages ask for
it with `grouped`, an editor embedded in another page keeps the row it had) are `StripGroup`s like the
strip's: find and replace, undo and redo, wrap and minimap, then format, review and discard, with text
size, indentation and Monaco's palette as words in a menu at the end. Review and Discard are drawn
disabled until there is a draft, so the first keystroke moves nothing. Below `lg` the row is the
language, find, undo, redo and the menu, which then also holds the groups it has no room to draw, and
the caret's position moves from the footer to the status line so the footer is one row too.

The Files search palette reserves its viewport-bounded height before results arrive. The input is
borderless even while focused, with the caret and active result carrying its keyboard state; other
controls retain their focus rings. Only the results scroll. Its footer is one line on a wide screen and
two on a phone, the same lines in every state: where to search (*This folder*, *Home*, *Everywhere* —
a segmented choice, with two that are the same folder offered once), the folder that means, hidden
files, the count and the unreadable-entry warning as text inside the status cell. It used to reserve a
whole empty row under the rest for that warning, which read as a strip of dead space at the foot of
every search. A hit's second line is the folder it is in, said from the scope (`ubuntu/Downloads`),
never its own path again under its name. Header controls and footer cells are fixed so typing, loading
and partial results do not move the frame. Result arrivals and departures
fade, with departing rows immediately inert; reduced motion renders the next state immediately. The
search body stays mounted through the dialog's closing animation so dismissal does not collapse it.

## Git: the page with no tiles (§15 pass 2)

**And the one page with no tiles, which is the shape of the argument for dropping this pass.**
`/git` had four — repositories, uncommitted, behind, unpushed — and the operator asked for them to
go. Nothing was lost, because every one of those numbers already sat on a filter chip under them,
and a chip says what is waiting *and* narrows the list to it where a tile could only say it. What
the tiles also did — put the urgent thing first — is done by ordering the repository cards
worst-first on each shelf, and the shelf with something wrong on it first. A page may drop this pass when it can name where each
figure went and what now does the job the figures were doing; `app/(dashboard)/git/page.tsx`
carries that in its doc comment, the way a surviving frame carries its sentence in pass 1.

## Audit log (§15 pass 2)

The audit log took the exit in 0.8.7 too. Its four tiles (changes, failed, people, sign-ins)
are the state and the facts of an identity line, and the day is one strip under it, an hour to
a stretch in the colour of how its changes went (`components/audit/day-strip.tsx`, the
availability ribbon's four hues), which says *when* something was refused where a tile said
only how often. The trail stayed a framed table of readings and became one line to a row: the
second line of every cell (how long ago, the address, the method and path, the detail) either
joined the first or went to the entry the row's action opens in a popover.

## The dashboard's own pages (§15 pass 2)

The dashboard's own two pages took the same exit in 0.7.0, and the reason generalises: a figure
on a page you configure is best drawn beside the control that sets it. Version's Installed,
Latest and Checked became one identity line and the timeline's marks; Configuration's Answers
at, Certificate, Port and Two-factor went to the heads of the sections that set them and to
the proxy in the stack. Both pages' doc comments name where each went.

Configuration took a second pass in 0.7.1, because the operator found it the greyest page in
the product: a list of three services, a single 48rem column of fields and nothing on it a
reader could find without reading. It opens on the Version page's identity line (the address
it answers at as the name, each part of the URL in its own hue, the checkout as the facts), and
the stack is drawn as the path a request takes to reach it in the wiring vocabulary
(`components/config/request-path.tsx`): this browser as itself, the tailnet as Tailscale, Caddy
with its certificate's issuer, and Next.js and Go behind it with their state in the tile's
corner. Restart and Rebuild keep their cards and gain the Compose or Docker command each runs,
coloured by `ShellWords`. The settings are two columns of sections, the certificate across both
and first; the allowlist is a list of the networks it lets in, each drawn as where its addresses
are (`components/config/allowlist.tsx`); a section holding a change not yet applied carries
*Edited* in `--git-modified`, the colour §3 gives a pending change. In 0.8.7 the two command
cards became the identity line's `⋯`, each still with the command it runs, the settings one
centred column with the floating save bar (§7), and the Version page's two framed notices one
line each under its identity line.
The account's Security page took it for the same reason — the second factor's state and how many
sessions are signed in sit in the heads of the sections that change them — and Sessions opens on
the session it is read through, with the count of the rest on their header.

## The account pages (§15 pass 2)

The account pages took the exit together in 0.7.5, at the operator's request — "no metric cards,
make them feel alive, real logos and colouring" — with the deploy pages and Configuration as the
reference: every figure the tiles gave became a wire, a chip or a verdict, and each page gained a
picture in the wiring vocabulary. The profile's four (two-factor, sessions, keys, users) are the
identity line's verdict and the shield on the account's face, the two sides of *Signed in as you*
(`components/account/access-picture.tsx`: the sessions as their browsers wired into the face, the
account's own keys as the products holding them wired out of it, each side counted and a way to
its page) and, for an administrator, the accounts' faces among the line's facts. The keys page's
four (in use, used this week, never used, expiring soon) are the list's count and three chips that
count and narrow, and wires in `key-map.tsx`, where each key reaches the API through the role it
carries: called today pulses, never called is dashed, about to stop is amber. The users page's four
(accounts, administrators, two-factor, signed in this week) are an identity line whose verdict is
how many accounts a password alone opens — a press narrowing the cards to exactly those — the
chips over the cards, and each card's own readings: its role, what guards it, when it last signed
in. A band of three blocks stood between the line and the cards for a while — last sign-ins as dots
on an axis ending now, two-factor and roles as share bars — and went at the operator's request ("I
don't need the metrics"): every figure in it was already on the cards under it. Sessions had no
tiles, and gained the path each session takes (`session-map.tsx`: browser, network, server). Its
rows carried each session's life as a bar on an axis every row shared, and that went too, at the
operator's request: a dot on a dashed line said less than the *seen 5h ago* beside it. Security gained
what a sign-in passes (`sign-in-path.tsx`), where an account with no second factor is a dashed ring
with an amber line bowed past it from the password to the server, and its sections went two to a
row as Configuration's are. The capability rows became a ladder of the three roles
(`role-ladder.tsx`), lit in each rung's hue up to the account's own, so the powers past it say why
a page is missing from the sidebar. A role's hue (`ROLE_HUE`: cyan, green, violet — none red or
amber) is the same on the ladder, the keys' role nodes and every role tag.
The three fan pictures — the profile's, Sessions' and the keys' — draw every node at Configuration's
scale, which the operator named as the picture to match: a full-size (`lg`) tile, a word over its
name in the eyebrow and a line of detail under it, with addresses in mono. A session's eyebrow is
what it is doing (*this device*, green while open; *seen 5h ago*), its detail its address and what
let it in; a key's eyebrow is its role; a network's is what it is to the server (*Tailnet*,
*Network*, *Loopback*); a role's is *Role*. The shared pieces are `SessionState` and `keyHint` in
`components/account/marks.tsx`. On a phone the fans keep only their marks, one size up from before.
The reads behind every wire, chip and verdict are `components/account/access.ts` (unit-tested).

## Connect and Hub (§15 pass 2)

Settings → Connect (`/dashboard/link`, named after the site's domain until 0.8.2) took the exit
on 2026-10-26, at the operator's request, because it read as a form with no life: three grey
tiles over three cards each holding two paragraphs. When the link was made and by whom is the
identity line's; the heartbeat and the last command are an Activity block of two rows, each with
its `Status`, the command in words and drawn as what it acted on. What took the tiles' place is
the deploy overview's wiring turned to face out (`account-link/connect-picture.tsx`): what the
heartbeat reads — the distribution's logo, Readings, Docker, Backups — wired into this server
and out to the site's J and the account, every line pulsing while heartbeats go out, amber and
still when the link stopped reaching the site, dashed while nothing is linked. The scope cards
lost their *Shares* paragraphs for a mark and one line, because what a scope shares is the list
under them, which follows the card picked; that list and the snapshot's groups are drawn with
their marks, a kind no product names taking its lane hue as the Schedule band's kinds do.

It took a second pass the same day, because the operator found it oversized once linked: on a
server with two dozen containers the Containers group wrapped every record over two or three
lines (an image digest is 71 characters), and the groups sat in a grid whose rows were as tall
as their tallest, so the short ones stood beside a screen of nothing. The groups are set in
columns now, so a short group sits under the one above it; each list of records — the
containers, and the stacks and alerts firing, which are groups of their own rather than lines
stretching *Heartbeat* — is a line per record led by its name, the rest truncated on the same
line with the whole record as its title, folded after five behind *Show all*. Values are body
text in tabular figures, mono kept for the fingerprints compared character by character, and
the sections stand at the deploy settings pages' `py-8`. At 1440 the linked page went from
3,387px to 2,112px.

Settings → Hub (`/dashboard/hub`, 0.8.5) is Connect pointed at a Just Hub and takes the same
exit for the same reason, with Connect as its reference rather than a new design. Its register
was the one real question: the hub's Connect button lands on it with `?connect=<hub host>`, which
is a sequence with an outcome, and the §16 test asks what the reader came to do. It stays in the
reading register because the sequence is one section of a page that reads the link before and
after it — exactly Connect's shape — and a page that flips register when the link changes state
would be half in each. So the connect form is a section under a hairline, not a `FlowPanel`;
**Connect** is its one brand face; the scopes are Connect's lit cards, none of them chosen when
a link from the hub filled the address in; the hub's machine and owner read under the address
before Connect; the code and this server's fingerprint sit side by side because the code is
what the owner types into the hub and the fingerprint is what its approval screen shows. The picture is Connect's (`LinkPicture`, which both pages now draw through)
with the hub's J and address at its end. Hub notices on the page are `Notice`s in the notice's
own tone, because each is a sentence the hub chose to say, not a reading, and each is headed
*Just Hub (hub) says* so it never reads as the dashboard's own. A link this server has stopped
using is the one state that is a verdict rather than a reading: *Idle* in red at the identity
line's far end, and the reason once, as a danger `Notice` above the picture, not again as the
Activity section's failed exchange; Sync now leaves the identity line, since it would be refused,
and the hub's address stops being a link anywhere, the title included, since it may now answer on
the machine this server refused. The linked page's *Sign in from Just Hub* section keeps Connect
the page's one brand face: **Let Just Hub sign me in** is a choice with a cost, so it is an outline
button, as stopping is, and every account's choice is a `RowList`, not a framed list.

## The logs page (§15)

**The logs page took a deployment's Logs page in 0.7.1**, at the operator's request: its one strip
held the source's name, its facts, an outlined Export box and the view tabs, the box and the
underline meeting edge to edge at the right end of a 40px row, over a stray line holding nothing but
the shortcuts button; and nothing on the page counted anything until Insights was opened. It now
opens on the chosen source's identity line (`SourceIdentity`, the Overview's `HostIdentity` with its
rule a step closer, since the console under it needs the height) — the source drawn as its product,
its kind, state, path, size and rotated set as facts, Export and the shortcuts key at its end — and
under it the lens's readings as `StatButton` tiles, each with its window's shape, counting up as it
lands and narrowing the lines to what it counts. They hold still across Live, History and Insights,
so Insights no longer draws its own over the window it picked. The strip is the rail toggle and the
views from its leading edge, as a deployment's pane is. The tiles stand only where five fit across
and the live tail keeps its height under them — a window of at least 1280 by 800, where five broke
three and two at the old 200px tile floor, now 180 — and on a smaller window the same figures are
the counts on the lens row's chips, the database workbench's answer.

**Later in 0.7.1 the tiles went**, at the operator's request: three cards over the frame said again
what the lens row's chips count, and took the live tail's height. The readings are the chips' counts
at every width. The same pass fixed every service page's pane (`ServiceLogs` — the engine log on
`/proxy`, a site's, a container's, a database's, a deployment's output, Security's): its Export was an
outlined box between the source's facts and the view tabs, splitting the strip and reading as one
more tab. The strip is the name and facts, the views, then the page's commands as glyphs at its end,
Export an `IconAction`.
