# Workbench

One frame held to the window, where the reader works for minutes rather than reads. Reference: a
database's Data, `/databases/[id]/data`, and `/terminal`.

**Reference:** `components/database/workbench/chrome.tsx` (`WorkbenchFrame`, the head every
database workbench shares) and `app/(dashboard)/terminal/page.tsx`. The shot is
[`docs/readme/terminal.png`](../../../readme/terminal.png). Its siblings are a database's Query,
Schema and Diagram, Files and its editor, a Git repository, Logs, a board, the two maps, a Remote
Desktop session and the assistant.

**Components:** `Page fill="workspace"` (`components/page.tsx`); `WorkbenchHeader`; `Pane`,
`PaneHeader`, `PaneFooter`, `PaneSection`; `PaneBar`, `PaneBack`, `paneShown`; `ResizeHandle`;
`Workspace`; `XtermPane`, `CodeEditor`, `CanvasZoom`. All of them are in
[`../components/workbench.md`](../components/workbench.md).

## The regions, in order

1. **The top strip**: the way back (always an arrow), the mark, what is open with its state beside
   it, the workbench's own tools, one command and a `⋯`. A workbench that opens one thing (a board,
   a repository, a file) draws it with `WorkbenchHeader`. A database's workbenches draw the
   database's line and the open thing's verbs in `WorkbenchFrame`'s bar. A workbench that is a
   whole section (the terminal, Files, Logs) has no strip above its panes and says its name in a
   screen-reader `h1` (`PageContext`).
2. **One frame**: a single rounded border from `lg`, the screen's edge below it. Inside it the
   panes are `Pane flush` with a hairline between them, and their top strips are one height so the
   hairlines meet as one rule.
3. **The side column** (a rail, a tree, a list of sessions): beside the work while the work keeps
   its room, over it otherwise. The reader can widen it (`ResizeHandle`), and the page keeps the
   width.
4. **The work**: the grid, the editor, the terminal, the canvas. It owns its scrolling and its
   keys.
5. **The inspector**, where a picked thing has details: a second side column, its groups
   `PaneSection`s.
6. **The pane bar**, below the break only: one stop a pane, at the foot.

A workbench has no picture, no readings and no sections. Of the standard it is judged on D3, D5,
D11, D12, D13 and D14 ([`../standard.md`](../standard.md)).

## Skeleton

```tsx
type Stop = "list" | "work" | "details"

export function ThingWorkbench() {
  const [shown, setShown] = useState<Stop>("work")
  const side = useSideWidth("things.side") // the reader's width, kept
  return (
    <Workspace name="Things" stateKey="things.workbench" refresh={reload}>
      <Page fill="workspace">
        <WorkbenchHeader
          back={{ href: "/things", label: "Things" }}
          heading={`Thing: ${name}`}
          mark={<ProductLogo id="postgres" size="sm" />}
          name={name}
          state={<Status tone="success" label="Saved" />}
          command={<Button size="sm">Run</Button>}
          menu={<VerbMenu verbs={verbs} label={`More actions for ${name}`} />}
        />
        <div className="flex min-h-0 flex-1 overflow-hidden lg:rounded-xl lg:border">
          <Pane flush className={paneShown(shown === "list")} style={{ width: side.width }}>
            <PaneHeader>Tables</PaneHeader>
            <ThingList onOpen={() => setShown("work")} />
          </Pane>
          <ResizeHandle side="left" label="Resize the list" {...side.handle} />
          <Pane flush className={cn("min-w-0 flex-1", paneShown(shown === "work"))}>
            <ThingWork />
          </Pane>
          <Pane flush className={paneShown(shown === "details")}>
            <ThingDetails />
          </Pane>
        </div>
        <PaneBar label="Thing" stops={STOPS} active={shown} onPick={setShown} />
      </Page>
    </Workspace>
  )
}
```

The width hook and the frame's classes are the page's own here; a database workbench takes both
from `WorkbenchFrame`, `WorkbenchSide` and `useSideColumn`, and a new one under a database uses
those rather than this skeleton.

## The four states

| State | What the page draws |
| --- | --- |
| Loading | The frame, the strip and every pane's head at once, at their final sizes; each pane waits as its own skeleton (the rail's rows, the grid's lines). The frame never changes size when a read lands. |
| Empty | With nothing open, the work pane says what goes there and how to open one (`EmptyState`, or the landing a schema or a folder has). An empty rail says so in one line (`EmptyNote dense`). |
| Unavailable | The thing the workbench works on is not there (no terminal backend, a database that is unreachable, a file that is gone): `MissingThing` in the frame, with the next step and the way back. Protected or read-only: the work is shown and the write controls are not drawn. |
| Failed | A pane's failed read is its own `ErrorState`, or `Stale form="strip"` across its top when it still shows the last reading. The other panes keep working. |

## The phone

One pane at a time, the whole height, over a `PaneBar` at the foot: never a short box inside a
scrolling page. Each pane takes `paneShown`, the page keeps which one is shown, and a pane opened
from a row has `PaneBack` in its head. The frame is the screen, edge to edge. The top strip keeps
the back arrow, the name and the command as an icon; the tools move behind the `⋯`. A canvas pans
with one finger and pinches to zoom. The pages on the bar and their stops are listed in
[`../patterns/keyboard-and-place.md`](../patterns/keyboard-and-place.md).

## Checklist

- [ ] `Page fill="workspace"`: the frame is the window's height and nothing scrolls but the panes.
- [ ] One frame, hairlines between panes, strips of one height. No framed panes with gutters.
- [ ] The top strip is `WorkbenchHeader` (or the database head), not a strip drawn by hand: an
      arrow back, one command, the rest behind `⋯`. One `h1`.
- [ ] The side column resizes with a `ResizeHandle`, and its width is kept.
- [ ] The page's keys go through `Workspace` or `useShortcuts`; the editor, the terminal and the
      canvas own theirs; `?` opens the help.
- [ ] A canvas spreads `CANVAS_MOVE`, calls `useCanvasPinch` and has `CanvasZoom`.
- [ ] Each pane has its loading, empty and failed form. A failure in one pane leaves the rest.
- [ ] Below the break: one pane, a `PaneBar`, `PaneBack` where a row opened a pane. A dock or a
      save bar rises above the bar.
- [ ] Unsaved work is guarded: leaving asks, and the state beside the name says saved or not.

## The spec to add

1. At 1440 the shell does not scroll (`scrollHeight` equals `clientHeight`), and each pane does.
2. The strip is found by its name, its back control leads to the list, and its command is there.
3. A drag on the handle changes the column's width, arrows nudge it, and a reload keeps it.
4. A failed read in one pane shows its error or its strip while the others still answer.
5. At 390 one pane is shown; a stop on the bar (`getByRole("navigation", { name })`) shows another
   and takes `aria-current="page"`; the document is no wider than the window.

`tests/browser/database-data.spec.ts`, `terminal-ui.spec.ts`, `git-ui.spec.ts` and `files-ui.spec.ts`
are the models. `phone-panes.test.js` fails a foot bar drawn by hand.
