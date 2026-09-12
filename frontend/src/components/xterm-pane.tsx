"use client"

import { useCallback, useEffect, useRef, useState } from "react"
import type { IDisposable, Terminal } from "@xterm/xterm"
import type { SearchAddon } from "@xterm/addon-search"
import {
  ArrowDown,
  ArrowUp,
  ChevronDoubleDown,
  Command,
  Copy,
  Cross,
  Download,
  FolderOpen,
  Fullscreen,
  FullscreenClose,
  Lightning,
  MagnifyingGlass,
  MoreHorizontal,
  RotateClockwise,
  SettingsSliders,
  SlashForward,
  TextTitle,
  TextUppercase,
  Trash,
} from "@/components/icons"
import { notify } from "@/lib/toast"
import { wsUrl } from "@/lib/api"
import { cn } from "@/lib/utils"
import { actionFor, formatChord, useKeymap } from "@/lib/terminal-keymap"
import {
  chooseDroppedImage,
  formatUploadSize,
  insertTerminalPath,
  interceptClipboardImagePaste,
  uploadTerminalImage,
} from "@/lib/terminal-upload"
import {
  setTerminalSettings,
  terminalSettings,
  useSnippets,
  useTerminalSettings,
} from "@/lib/terminal-settings"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Switch } from "@/components/ui/switch"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { Slider } from "@/components/ui/slider"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import { ShortcutsDialog } from "@/components/terminal/shortcuts-dialog"
import { Modal } from "@/components/modal"
import { Pane } from "@/components/panel"
import { copyText, copyTextQuietly } from "@/lib/clipboard"

type Query = Record<string, string | number | boolean | undefined | null>

type XtermTheme = NonNullable<Terminal["options"]["theme"]>

/**
 * The neutral ANSI ramp (colours 0, 8, 7, 15) as a percentage of ink mixed into
 * paper. Colour 8 (`brightBlack`) is the most-used of the four — git hashes,
 * vim comments, `ls -l` metadata — so it is lifted far enough off the ground to
 * stay clearly legible rather than merely present.
 */
const NEUTRAL_INK = { black: 18, brightBlack: 44, white: 74 } as const

/**
 * The last resort when a canvas 2D context is unavailable (a headless or
 * locked-down browser). The runtime resolver below is what actually runs.
 */
const TERMINAL_FALLBACK: XtermTheme = {
  background: "#141414",
  foreground: "#fafafa",
  cursor: "#fafafa",
  selectionBackground: "rgba(200,160,60,0.3)",
  black: "#333333",
  red: "#e5484d",
  green: "#46a758",
  yellow: "#d9a441",
  blue: "#5b7fdb",
  magenta: "#8e6fd6",
  cyan: "#5ec9c3",
  white: "#c2c2c6",
  brightBlack: "#6b6b6b",
  brightWhite: "#fafafa",
}

/**
 * xterm parses colours itself and understands neither `oklch()` nor `var()`,
 * so the palette can't be handed the theme tokens directly. Instead a hidden
 * probe borrows each token the way any element would — `color: var(--x)` — and
 * its resolved computed colour is normalised through a canvas, which accepts
 * every form `getComputedStyle` returns and hands back `#rrggbb`. The result
 * tracks the active theme in both modes: the background and foreground are the
 * app's surfaces, the accents are the status and chart colours the rest of the
 * UI uses, and the neutral ramp is mixed from foreground and background so it
 * moves with the palette rather than being a second set of hexes to maintain.
 */
function resolveTerminalTheme(): XtermTheme {
  if (typeof document === "undefined") return TERMINAL_FALLBACK
  const ctx = document.createElement("canvas").getContext("2d")
  if (!ctx) return TERMINAL_FALLBACK

  const probe = document.createElement("span")
  probe.style.cssText = "position:absolute;visibility:hidden;pointer-events:none"
  document.body.appendChild(probe)

  const read = (expr: string, fallback: string) => {
    // A sentinel first: an expression the browser rejects outright (a typo in
    // a function name) leaves `color` at the sentinel rather than erroring, so
    // that is the signal to fall back.
    probe.style.color = "rgb(1, 2, 3)"
    probe.style.color = expr
    const raw = getComputedStyle(probe).color
    if (!raw || raw === "rgb(1, 2, 3)") return fallback
    try {
      ctx.fillStyle = "#000"
      ctx.fillStyle = raw
      return ctx.fillStyle
    } catch {
      return fallback
    }
  }
  const token = (name: string, fallback: string) => read(`var(${name})`, fallback)
  const mix = (a: string, pct: number, b: string, fallback: string) =>
    read(`color-mix(in oklab, var(${a}) ${pct}%, var(${b}))`, fallback)
  const withAlpha = (hex: string, a: number) =>
    /^#[0-9a-f]{6}$/i.test(hex)
      ? hex +
        Math.round(a * 255)
          .toString(16)
          .padStart(2, "0")
      : hex

  const fb = TERMINAL_FALLBACK
  const n = NEUTRAL_INK
  const fg = token("--foreground", fb.foreground!)
  const bg = token("--surface-sunken", token("--background", fb.background!))
  const red = token("--destructive", fb.red!)
  const green = token("--success", fb.green!)
  const yellow = token("--warning", fb.yellow!)
  const blue = token("--chart-1", fb.blue!)
  const magenta = token("--chart-4", fb.magenta!)
  const cyan = token("--chart-5", fb.cyan!)

  const theme = {
    background: bg,
    foreground: fg,
    cursor: token("--primary", fg),
    cursorAccent: bg,
    selectionBackground: withAlpha(token("--ring", fb.blue!), 0.3),
    black: mix("--foreground", n.black, "--background", fb.black!),
    red,
    green,
    yellow,
    blue,
    magenta,
    cyan,
    white: mix("--foreground", n.white, "--background", fb.white!),
    brightBlack: mix("--foreground", n.brightBlack, "--background", fb.brightBlack!),
    brightRed: red,
    brightGreen: green,
    brightYellow: yellow,
    brightBlue: blue,
    brightMagenta: magenta,
    brightCyan: cyan,
    brightWhite: fg,
  }
  probe.remove()
  return theme
}

/** The control keys a strip can send. Bytes, not tmux key names: these go
 *  straight down the same socket a keypress would, so the shell cannot tell
 *  the difference — which is the point, since Ctrl+C has to interrupt rather
 *  than be delivered as text. */
const CONTROL_KEYS = [
  { label: "Tab", hint: "Autocomplete command or path", bytes: "\t" },
  { label: "Ctrl+C", hint: "Interrupt what is running", bytes: "\u0003" },
  { label: "Ctrl+D", hint: "End of input — logs a shell out", bytes: "\u0004" },
  { label: "Ctrl+Z", hint: "Suspend to the background", bytes: "\u001a" },
  { label: "Ctrl+L", hint: "Clear the screen, keeping scrollback", bytes: "\u000c" },
  { label: "Esc", hint: "Escape", bytes: "\u001b" },
  { label: "Ctrl+\\", hint: "Quit — stronger than Ctrl+C", bytes: "\u001c" },
] as const

/**
 * An xterm.js terminal wired to a PTY over a WebSocket.
 *
 * Binary frames carry raw terminal bytes in both directions; JSON frames carry
 * control messages, of which resize is the one that matters — without it the
 * remote shell keeps drawing for an 80x24 window no matter how large the pane.
 */
export function XtermPane({
  path,
  query,
  className,
  onExit,
  subtitle,
  headerContent,
  cwd,
  onOpenFiles,
  onCellClick,
  focusRef,
  copyMode,
  onToggleFullscreen,
  fullscreenActive,
  terminalSessionId,
}: {
  path: string
  query?: Query
  className?: string
  onExit?: () => void
  /** Shown in the pane header instead of the socket path — e.g. who you are. */
  subtitle?: React.ReactNode
  /** Session/window controls embedded in the terminal title bar. */
  headerContent?: React.ReactNode
  /** Where the shell currently is, for the actions that act on that directory. */
  cwd?: string
  onOpenFiles?: (path: string) => void
  /**
   * The cell a click landed on, so the caller can work out which tmux pane was
   * under it. The browser sees one terminal however many panes tmux has
   * composed into it, so the column and row are the only thing this component
   * can honestly report.
   */
  onCellClick?: (cell: { col: number; row: number }) => void
  /**
   * Handed a function that puts the keyboard back into this pane.
   *
   * The page calls it after every switch it offers — session, window, pane —
   * because clicking a chip leaves the focus on the chip, and the next
   * keystroke then goes to a button instead of to the shell that was just
   * chosen. A ref rather than an imperative handle: the terminal is built
   * inside an effect, and nothing here needs to re-render to hand it over.
   */
  focusRef?: React.RefObject<(() => void) | null>
  /**
   * Whether the socket on the other end is a tmux-backed session that
   * understands the `exit-copy` and `sync-copy` control frames.
   *
   * It is off by default, and that default is load-bearing rather than
   * cautious. The same component drives `docker exec`, whose handler writes
   * **every frame that is not a resize straight into the container's stdin** —
   * so a copy-mode question asked of a socket that has never heard of copy
   * mode does not fail, it types `{"type":"sync-copy"}` at somebody's prompt.
   * Only the terminal page, whose sessions are tmux's, turns it on.
   */
  copyMode?: boolean
  /**
   * When set, the fullscreen button and shortcut hand off to the caller instead
   * of fullscreening this pane alone. The terminal page uses it to take the
   * *whole* workspace fullscreen — rail, strips and tools with it — which is
   * the only way the file tree and git panel stay reachable while maximised.
   * The compose runner leaves it unset and keeps the pane-only behaviour.
   */
  onToggleFullscreen?: () => void
  fullscreenActive?: boolean
  /** Enables session-scoped image paste/drop on the real terminal page only. */
  terminalSessionId?: string
}) {
  const frameRef = useRef<HTMLDivElement>(null)
  const hostRef = useRef<HTMLDivElement>(null)
  const [state, setState] = useState<"connecting" | "open" | "closed">("connecting")
  const [error, setError] = useState<string>()
  const [fullscreen, setFullscreen] = useState(false)
  const [searching, setSearching] = useState(false)
  const [needle, setNeedle] = useState("")
  const [matches, setMatches] = useState<{ index: number; count: number }>({ index: -1, count: 0 })
  const [findOptions, setFindOptions] = useState({
    caseSensitive: false,
    regex: false,
    word: false,
  })
  const [atBottom, setAtBottom] = useState(true)
  // Whether tmux has this pane scrolled back through its history — **as tmux
  // reports it**, never as the browser guesses.
  //
  // It cannot be read from xterm. Under tmux the emulator's own scrollback
  // stays empty — tmux holds the alternate screen and repaints the viewport
  // rather than emitting lines — so the history belongs to tmux, and a wheel
  // tick is forwarded to it as a mouse report. What tmux *does* with that
  // report is not the browser's to know, and assuming it was a scroll is the
  // bug this flag used to have: tmux hands the wheel straight to the program
  // in the pane whenever that program has asked for the mouse — tmux's own
  // `mouse_any_flag`, which is on for every full-screen TUI, an editor, a
  // pager, Claude Code. Nothing enters copy mode there and there is nothing to
  // jump back from, so the affordance the browser drew from its own guess was
  // a button offering to return a terminal that had never moved. The wheel
  // only *asks* now, and `#{pane_in_mode}` coming back over the socket is what
  // puts the button on screen.
  const [scroll, setScroll] = useState({ offset: 0, history: 0, height: 0 })
  const draggingScroll = useRef(false)
  const [scrolledBack, setScrolledBack] = useState(false)
  const scrolledBackRef = useRef(false)
  // A wheel tick has gone out and tmux has not yet said what became of it.
  //
  // The answer arrives a couple of hundred milliseconds later, and the first
  // keystroke after a scroll routinely beats it. Copy mode is a mode in the vi
  // sense — keys become copy commands and the shell stops hearing them — so
  // that keystroke has to be preceded by the cancel or it is eaten, which
  // reads as the terminal having frozen. Cancelling a pane that is not in a
  // mode costs one `tmux send-keys -X cancel` that reports "not in a mode" and
  // writes nothing to the program, so guessing wrong here is free and waiting
  // is not.
  const wheeledRef = useRef(false)
  const [shortcuts, setShortcuts] = useState(false)
  // What a guarded paste is holding: the bytes to send if it is confirmed,
  // and the readable version to show. They differ whenever the shell has
  // bracketed paste on, where the text arrives wrapped in escape sequences
  // that must be forwarded intact and must not be put on screen.
  const [pendingPaste, setPendingPaste] = useState<{
    raw: string
    text: string
  } | null>(null)
  const [bell, setBell] = useState(false)
  const [imageDrag, setImageDrag] = useState(false)
  // The title the shell sets through OSC 0/2 — which for anybody with a
  // configured prompt is the command that is running. It is the one label the
  // pane can carry that says what this terminal is *doing* rather than what it
  // was called when it was opened.
  const [shellTitle, setShellTitle] = useState("")
  // Connection generation. Reconnecting is a deliberate act here — see the
  // socket effect — so it is a counter the effect depends on rather than
  // anything automatic.
  const [generation, setGeneration] = useState(0)
  const settings = useTerminalSettings()
  const snippets = useSnippets()
  const map = useKeymap()

  // The live terminal, kept so settings can be applied to it instead of
  // tearing down the PTY session behind it.
  const termRef = useRef<Terminal | null>(null)
  const searchRef = useRef<SearchAddon | null>(null)
  const fitRef = useRef<{ fit: () => void } | null>(null)
  const socketRef = useRef<WebSocket | null>(null)
  // The upload path uses this exact input writer after its HTTP request
  // finishes. It is assigned by the live socket effect so a returned path
  // travels through the same transport and copy-mode handling as typing.
  const inputRef = useRef<((data: string) => boolean) | null>(null)
  // Settings are read inside the connect effect, which must not re-run when
  // one changes: rebuilding the terminal would drop the scrollback and, on a
  // non-tmux host, the session with it. The effects below apply them to the
  // live terminal instead.
  const settingsRef = useRef(settings)
  useEffect(() => {
    settingsRef.current = settings
  }, [settings])
  // Read inside the connect effect, which must not re-run when it changes,
  // for the same reason the settings are.
  const copyModeRef = useRef(copyMode)
  useEffect(() => {
    copyModeRef.current = copyMode
  }, [copyMode])
  // Same reason as the settings ref: rebinding a shortcut must not tear down
  // the terminal and the PTY behind it.
  const keymapRef = useRef(map)
  useEffect(() => {
    keymapRef.current = map
  }, [map])
  // The click handler is a native listener installed once with the terminal —
  // see the mousedown listener below for why it cannot be a React prop — so
  // the caller's callback reaches it through a ref rather than a closure.
  const onCellClickRef = useRef(onCellClick)
  useEffect(() => {
    onCellClickRef.current = onCellClick
  }, [onCellClick])
  // The key handler is installed once, before toggleFullscreen is defined.
  const fullscreenRef = useRef<(() => Promise<void>) | null>(null)
  // The caller's fullscreen override, mirrored into a ref for the same reason:
  // the key handler closes over it once and must not go stale.
  const onToggleFullscreenRef = useRef(onToggleFullscreen)
  useEffect(() => {
    onToggleFullscreenRef.current = onToggleFullscreen
  }, [onToggleFullscreen])

  useEffect(() => {
    const host = hostRef.current
    if (!host) return

    let disposed = false
    let cleanup = () => {}

    // xterm touches `window` at import time, so it is loaded in the effect
    // rather than at module scope where the server render would break.
    ;(async () => {
      const [
        { Terminal },
        { FitAddon },
        { WebLinksAddon },
        { SearchAddon },
        { Unicode11Addon },
        { WebglAddon },
      ] = await Promise.all([
        import("@xterm/xterm"),
        import("@xterm/addon-fit"),
        import("@xterm/addon-web-links"),
        import("@xterm/addon-search"),
        import("@xterm/addon-unicode11"),
        import("@xterm/addon-webgl"),
      ])
      await import("@xterm/xterm/css/xterm.css")
      if (disposed) return

      const s = settingsRef.current
      const term = new Terminal({
        // Keep cell metrics deterministic. Browser defaults can resolve to a
        // proportional fallback for symbols even when ordinary ASCII looks
        // monospace, which leaves a TUI's logical cursor and painted glyphs
        // disagreeing. xterm clips fallbacks to the cell; customGlyphs draws
        // box and block structure to the complete cell boundary.
        fontFamily:
          'ui-monospace, "SFMono-Regular", "Cascadia Mono", "Liberation Mono", Menlo, Monaco, Consolas, monospace',
        fontSize: 14,
        lineHeight: 1,
        letterSpacing: 0,
        customGlyphs: true,
        // `convertEol` is deliberately **off**, and turning it back on breaks
        // the terminal in a way that takes a day to trace.
        //
        // It makes a bare LF also return the carriage — which is right for
        // feeding a string of plain text into an emulator, and wrong for a
        // PTY. The kernel's line discipline already turns a program's `\n`
        // into `\r\n` (ONLCR), so nothing needs the help; what it does
        // instead is corrupt every redraw that uses LF to step down a row
        // while *keeping* the column, which is exactly how tmux paints the
        // column of separators between two panes and the rows either side of
        // it. With it on, each of those steps also snapped the cursor to
        // column 1, so the next erase cleared the wrong span and the next
        // write landed in the wrong place: a prompt with its first thirty
        // characters missing, a line with its first letter eaten, residue that
        // `clear` could not remove because it was outside the pane that ran
        // it. Splitting a pane was reliably enough to produce it.
        scrollback: s.scrollback,
        // A right-click should offer the page's paste, not xterm's selection
        // handling, which is what makes copy/paste behave like a normal app.
        rightClickSelectsWord: false,
        macOptionIsMeta: true,
        // The default separators break a path or a URL into pieces on
        // double-click, which is the one thing anybody double-clicks a
        // terminal for.
        wordSeparator: " ()[]{}',\"`",
        // xterm mutes a colour that would be unreadable against the
        // background. Left off, a script that prints dark grey on the dark
        // theme prints nothing at all.
        minimumContrastRatio: 1.6,
        // The search addon's match count and highlight-all live behind
        // xterm's "proposed API" flag — the decoration API they are built on
        // is not frozen yet. Without it `findNext` throws where it would have
        // decorated, `onDidChangeResults` never fires, and the counter reads
        // "none" over a scrollback full of matches, which is worse than not
        // showing a count at all.
        allowProposedApi: true,
        theme: resolveTerminalTheme(),
      })
      termRef.current = term
      const fit = new FitAddon()
      const search = new SearchAddon()
      fitRef.current = fit
      searchRef.current = search
      term.loadAddon(fit)
      term.loadAddon(search)
      term.loadAddon(new WebLinksAddon())
      term.loadAddon(new Unicode11Addon())
      term.unicode.activeVersion = "11"
      host.dataset.terminalUnicode = term.unicode.activeVersion
      term.open(host)
      // A plain drag selects, the way it does in every other application.
      //
      // tmux's mouse mode is on — that is what makes the wheel scroll the
      // session's history — and with it on xterm hands every button and drag
      // to tmux instead of selecting. tmux then draws its own selection, copies
      // it into its own buffer and clears it on mouse-up, so from the page's
      // side the text highlighted and unhighlighted inside one gesture and
      // `getSelection()` stayed empty: the Copy button and the copy shortcut
      // both said nothing was selected, because as far as the browser was
      // concerned nothing was.
      forcePointerToSelect(term)

      const disposables: IDisposable[] = []

      // xterm 6's default DOM renderer deliberately does not implement custom
      // glyphs. That leaves box-drawing and block-element characters to font
      // fallback, where their edges do not fill the cell and TUI borders/logo
      // art develop visible gaps. The matching WebGL addon renders those
      // structural characters itself. Keep DOM as a context-loss fallback and
      // as an explicit diagnostic A/B override.
      host.dataset.terminalRenderer = "dom"
      if (window.localStorage.getItem("jd.terminal.renderer") !== "dom") {
        try {
          const webgl = new WebglAddon()
          disposables.push(
            webgl.onContextLoss(() => {
              webgl.dispose()
              host.dataset.terminalRenderer = "dom"
              window.requestAnimationFrame(() => {
                if (!disposed && term.rows > 0) term.refresh(0, term.rows - 1)
              })
            }),
          )
          term.loadAddon(webgl)
          host.dataset.terminalRenderer = "webgl"

          // Alternate-buffer switches replace the complete rendered surface.
          // Force one coherent frame so an atlas update cannot leave rows from
          // the former buffer stale or blank.
          disposables.push(
            term.buffer.onBufferChange(() => {
              window.requestAnimationFrame(() => {
                if (!disposed && term.rows > 0) term.refresh(0, term.rows - 1)
              })
            }),
          )
        } catch {
          // Software-only browsers remain usable through xterm's DOM renderer.
          host.dataset.terminalRenderer = "dom"
        }
      }

      const fitTerminal = () => {
        if (host.clientWidth <= 0 || host.clientHeight <= 0) return false
        fit.fit()
        return term.rows > 0 && term.cols > 0
      }
      fitTerminal()

      // The size the server was last told. Both xterm's resize event and the
      // host observer converge here, so a fit cannot emit the same control
      // message twice.
      let sent = { rows: 0, cols: 0 }
      const terminalDebug = window.localStorage.getItem("jd.terminal.debug") === "1"
      const syncPtySize = (socket: WebSocket) => {
        if (socket.readyState !== WebSocket.OPEN || term.rows <= 0 || term.cols <= 0) return
        if (term.rows === sent.rows && term.cols === sent.cols) return
        sent = { rows: term.rows, cols: term.cols }
        host.dataset.terminalRows = String(term.rows)
        host.dataset.terminalCols = String(term.cols)
        socket.send(JSON.stringify({ type: "resize", rows: term.rows, cols: term.cols }))
        if (terminalDebug) console.debug(`frontend terminal = ${term.rows}x${term.cols}`)
      }

      disposables.push(
        search.onDidChangeResults((r) =>
          setMatches({ index: r?.resultIndex ?? -1, count: r?.resultCount ?? 0 }),
        ),
      )
      disposables.push(term.onTitleChange((title) => setShellTitle(title)))
      disposables.push(
        term.onBell(() => {
          const now = settingsRef.current
          if (now.visualBell) {
            setBell(true)
            window.setTimeout(() => setBell(false), 250)
          }
          if (now.notifyOnBell && typeof Notification !== "undefined") {
            if (Notification.permission === "granted") {
              new Notification("Terminal", { body: "The shell rang the bell." })
            }
          }
        }),
      )
      // "Am I looking at the end of the output" — the difference between a
      // terminal that has gone quiet and one you have scrolled away from.
      disposables.push(
        term.onScroll(() => {
          const buffer = term.buffer.active
          setAtBottom(buffer.viewportY >= buffer.baseY)
        }),
      )
      disposables.push(
        term.onSelectionChange(() => {
          if (!settingsRef.current.copyOnSelect) return
          const selection = term.getSelection()
          if (selection) void copyTextQuietly(selection)
        }),
      )
      // The geometry itself is what the PTY has to be told about, so the send
      // hangs off the change rather than off each caller that might cause one
      // (a container resize or a fit after mounting). An
      // Ink-based TUI redraws entirely from the size it was last given, so a
      // fit that nobody forwarded is a full-screen app painting for the wrong
      // window — a blank or garbled first row.
      disposables.push(
        term.onResize(() => {
          const socket = socketRef.current
          if (socket) syncPtySize(socket)
        }),
      )

      const socket = new WebSocket(wsUrl(path, { ...query, rows: term.rows, cols: term.cols }))
      socket.binaryType = "arraybuffer"
      socketRef.current = socket

      // True while the scrollback is being replayed into the terminal.
      //
      // A terminal answers some of what is written to it — `CSI c` and the
      // other device queries are the shell asking the terminal a question,
      // and xterm replies down the same channel a keystroke uses. Replaying a
      // buffer that contains one makes it answer again, at whatever prompt
      // exists now: reopening a tab typed `1;2c0;276` into the shell and left
      // a column of "command not found" behind it. The replies are dropped
      // for the length of the replay, which is the only window in which they
      // can only be about the past.
      let replaying = false

      /**
       * Tell the PTY how big the screen is — **immediately**, never on a
       * timer.
       *
       * The temptation is to debounce this, since a drag produces a frame a
       * millisecond. Doing so corrupts the screen, and this is the exact
       * mechanism: splitting a pane brings up the pane bar, which takes two
       * rows off this box, at the same moment tmux repaints for the split.
       * Delay the resize and tmux paints those two rows into an emulator that
       * no longer has them; the rows are lost, and tmux never sends them again
       * because as far as it is concerned they arrived. What is left is a
       * prompt stuck where it used to be that survives `clear`.
       *
       * So the size goes out the moment it changes, and the *repair* is on the
       * server: it asks tmux to repaint the whole screen once the size stops
       * changing (term.Manager.Redraw). Frequency is not what hurts here —
       * tmux handles a dragged window all day — lag is.
       */
      const sendResize = () => {
        if (!fitTerminal()) return
        syncPtySize(socket)
      }

      socket.onopen = () => {
        setState("open")
        setError(undefined)
        sendResize()
        if (terminalDebug) console.debug("terminal WebSocket connected")
        term.focus()
        // The session may have been left scrolled back by whoever was here
        // before — copy mode outlives the socket the way everything else in a
        // tmux session does — and a pane that is in a mode reads as a pane
        // that has frozen. One question on the way in is what makes the
        // affordance appear for a state this tab did not create.
        if (copyModeRef.current) socket.send(JSON.stringify({ type: "sync-copy" }))
      }
      socket.onmessage = (event) => {
        if (typeof event.data === "string") {
          // Only control frames arrive as text; an error is the one that
          // matters to the reader, and the scrollback marker says that the
          // next binary frame is the past rather than the present.
          try {
            const msg = JSON.parse(event.data)
            if (msg.type === "error") {
              setError(msg.error)
              term.writeln(`\r\n\x1b[31m${msg.error}\x1b[0m`)
            } else if (msg.type === "scrollback") {
              replaying = true
            } else if (msg.type === "copy-mode") {
              // The server is the only authority on whether tmux is still
              // scrolled away from the prompt — the browser forwards the wheel
              // tick but never learns what tmux did with it. This frame comes
              // back after an `exit-copy` and after a `sync-copy` (sent once
              // the wheel settles, whichever way it went), so the jump-to-end
              // affordance tracks the real mode rather than a guess that could
              // stick on forever — or appear over a pane that never scrolled.
              const active = Boolean(msg.data?.active)
              // tmux is at the prompt, so there is nothing left for the next
              // keystroke to cancel either.
              if (!active) wheeledRef.current = false
              scrolledBackRef.current = active
              setScrolledBack(active)
              if (!draggingScroll.current)
                setScroll({
                  offset: Math.max(0, Number(msg.data?.offset) || 0),
                  history: Math.max(0, Number(msg.data?.history) || 0),
                  height: Math.max(0, Number(msg.data?.height) || 0),
                })
            }
          } catch {
            term.write(event.data)
          }
          return
        }
        // The callback fires once xterm has parsed everything written, which
        // is what makes "the replay is over" a fact rather than a guess at a
        // timeout.
        term.write(new Uint8Array(event.data as ArrayBuffer), () => {
          replaying = false
          scheduleCopySync()
        })
      }
      socket.onclose = () => {
        setState("closed")
        if (terminalDebug) console.debug("terminal WebSocket disconnected")
        term.writeln("\r\n\x1b[90m— disconnected —\x1b[0m")
        onExit?.()
      }
      socket.onerror = () => setState("closed")

      /**
       * Puts the pane back at the prompt before a keystroke is delivered.
       *
       * Sent on tmux's confirmed copy mode *or* on a wheel tick whose answer
       * has not come back yet: the operator typing is the commonest way out of
       * a scrolled-back terminal, and it must not have to wait for a round
       * trip to work. See `wheeledRef` for why guessing wrong is free.
       */
      const leaveCopyMode = () => {
        if (!scrolledBackRef.current && !wheeledRef.current) return
        wheeledRef.current = false
        scrolledBackRef.current = false
        setScrolledBack(false)
        if (copyModeRef.current && socket.readyState === WebSocket.OPEN) {
          socket.send(JSON.stringify({ type: "exit-copy" }))
        }
      }

      const insertInput = (data: string) => {
        if (socket.readyState !== WebSocket.OPEN || replaying) return false
        leaveCopyMode()
        sendTerminalInput(socket, data)
        term.focus()
        return true
      }
      inputRef.current = insertInput

      disposables.push(
        term.onData((data) => {
          if (socket.readyState !== WebSocket.OPEN) return
          if (replaying) return
          // Typing is how everybody leaves a scrolled-back terminal — no
          // other emulator asks you to scroll back down first — so the ask
          // goes out ahead of the keystroke, on the same socket, which is
          // what keeps the two in order.
          //
          // A mouse report is not typing. With tmux's mouse mode on, every
          // wheel tick arrives here as a report sequence, so treating this
          // callback as "the operator typed something" cancelled copy mode
          // on the very tick that entered it — scrolling up moved the view
          // and then snapped it straight back to the bottom.
          if (!isMouseReport(data)) leaveCopyMode()
          // The guard has to live here and not only on the paste shortcut.
          // Ctrl+V, the right-click menu and the X11 middle click all reach
          // xterm as a browser paste event, which becomes one onData call
          // carrying the whole block — so intercepting only the shortcut
          // guarded the one route nobody uses. A single Enter is one
          // character and is not a paste; anything longer that carries a line
          // ending is.
          if (settingsRef.current.confirmMultilinePaste && isMultilinePaste(data)) {
            setPendingPaste({ raw: data, text: readablePaste(data) })
            return
          }
          sendTerminalInput(socket, data)
        }),
      )

      // The shortcuts the emulator owns, resolved through the keymap so every
      // one of them is the operator's to change. Ctrl+Shift is the default
      // family precisely because Ctrl+C and Ctrl+V belong to the shell —
      // intercepting those would break SIGINT, the most used key in any
      // terminal.
      //
      // Returning false tells xterm not to handle the key; it does *not* stop
      // the browser. Without preventDefault, Ctrl+Shift+V opened the paste
      // confirmation and let Chromium paste into xterm's textarea at the same
      // time — the guarded route and the unguarded one, at once.
      term.attachCustomKeyEventHandler((event) => {
        if (event.type !== "keydown") return true
        const action = actionFor(event, "terminal", keymapRef.current)
        if (!action) return clipboardKey(event, term)
        event.preventDefault()
        event.stopPropagation()
        switch (action) {
          case "terminal.copy":
            void copySelection(term)
            break
          case "terminal.paste":
            void requestPaste(socket, setPendingPaste)
            break
          case "terminal.search":
            setSearching(true)
            break
          case "terminal.clear":
            term.clear()
            break
          case "terminal.fullscreen":
            if (onToggleFullscreenRef.current) onToggleFullscreenRef.current()
            else void fullscreenRef.current?.()
            break
          case "terminal.shortcuts":
            setShortcuts(true)
            break
        }
        return false
      })

      // What the wheel did is tmux's to say, in both directions. Scrolling up
      // moves the history only when the program in the pane has *not* asked
      // for the mouse; scrolling down leaves copy mode only once it reaches
      // the very bottom, which the browser cannot see either. So each gesture
      // updates one throttled query, including during a continuous scroll — and the `copy-mode` frame carrying
      // `#{pane_in_mode}` is the answer that drives the affordance.
      let syncTimer: ReturnType<typeof setTimeout> | undefined
      const scheduleCopySync = () => {
        if (!copyModeRef.current) return
        if (syncTimer) return
        syncTimer = setTimeout(() => {
          syncTimer = undefined
          if (socket.readyState === WebSocket.OPEN) {
            socket.send(JSON.stringify({ type: "sync-copy" }))
          }
        }, 180)
      }

      const onWheel = (event: WheelEvent) => {
        // The tick itself is xterm's to forward — it goes out as a mouse
        // report and tmux decides what it means. This only notes that one
        // went, so the next keystroke can cancel a mode that may now be on,
        // and asks what actually happened once the wheel stops.
        if (event.deltaY === 0) return
        wheeledRef.current = true
        scheduleCopySync()
      }
      host.addEventListener("wheel", onWheel, { passive: false, capture: true })

      // Clicking inside a pane focuses it, which is what clicking inside
      // anything does. tmux composes every pane into one screen before the PTY
      // ever sees it, so there is no element to hang a handler on — only a
      // cell. xterm publishes no pixel-to-cell mapping and does not need to:
      // the grid is uniform, so the screen's box divided by the terminal's own
      // rows and columns is exact.
      //
      // **Capture, and a native listener rather than a React prop.** With
      // tmux's mouse mode on, xterm's selection service is disabled and its
      // mousedown handler on `.xterm` calls `stopPropagation()` on exactly the
      // clicks this pane takes back for the browser (see
      // `forcePointerToSelect`). React binds at the root container, so a
      // bubbling `onMouseDown` never ran and clicking the other half of a
      // split silently did nothing — the pane bar was the only way to move the
      // focus. The capture phase is above the element that stops the event.
      const onMouseDownCapture = (event: MouseEvent) => {
        const report = onCellClickRef.current
        if (!report || event.button !== 0) return
        const screen = host.querySelector(".xterm-screen")
        if (!screen) return
        const box = screen.getBoundingClientRect()
        if (box.width === 0 || box.height === 0) return
        const col = Math.floor(((event.clientX - box.left) / box.width) * term.cols)
        const row = Math.floor(((event.clientY - box.top) / box.height) * term.rows)
        if (col < 0 || row < 0 || col >= term.cols || row >= term.rows) return
        report({ col, row })
      }
      host.addEventListener("mousedown", onMouseDownCapture, { capture: true })

      let resizeFrame = 0
      const scheduleResize = () => {
        if (resizeFrame) return
        resizeFrame = requestAnimationFrame(() => {
          resizeFrame = 0
          sendResize()
        })
      }
      const observer = new ResizeObserver(scheduleResize)
      observer.observe(host)
      const onVisibility = () => {
        if (document.visibilityState === "visible") scheduleResize()
      }
      document.addEventListener("visibilitychange", onVisibility)
      void document.fonts?.ready.then(scheduleResize)

      cleanup = () => {
        observer.disconnect()
        cancelAnimationFrame(resizeFrame)
        document.removeEventListener("visibilitychange", onVisibility)
        clearTimeout(syncTimer)
        host.removeEventListener("wheel", onWheel, { capture: true })
        host.removeEventListener("mousedown", onMouseDownCapture, { capture: true })
        for (const d of disposables) d.dispose()
        socket.close()
        term.dispose()
        termRef.current = null
        searchRef.current = null
        fitRef.current = null
        socketRef.current = null
        if (inputRef.current === insertInput) inputRef.current = null
      }
    })()

    return () => {
      disposed = true
      cleanup()
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [path, generation, JSON.stringify(query ?? {})])

  useEffect(() => {
    scrolledBackRef.current = scrolledBack
  }, [scrolledBack])

  // The caller's way back to the keyboard. Read through `termRef` at call
  // time, so it keeps working across a reconnect rather than capturing the
  // terminal that existed when the pane mounted.
  useEffect(() => {
    if (!focusRef) return
    focusRef.current = () => termRef.current?.focus()
    return () => {
      focusRef.current = null
    }
  }, [focusRef])

  // Clipboard images and dragged images take an authenticated HTTP path to
  // the server, then only the returned filename goes through the PTY socket.
  // A native capture listener is deliberate: xterm owns the hidden textarea
  // that receives paste, so the terminal container must see an image before
  // xterm can mistake it for ordinary paste data. Text-only events return
  // without even calling preventDefault and keep xterm's existing behaviour.
  useEffect(() => {
    const host = hostRef.current
    if (!host || !terminalSessionId) return

    const controller = new AbortController()
    let dragDepth = 0

    const unsupported = (mime: string) =>
      notify.error(
        "Image type not supported",
        new Error(`${mime || "This file type"} cannot be pasted. Use PNG, JPEG or WebP.`),
      )

    const upload = async (file: File, action: "pasted" | "dropped") => {
      const toast = notify.loading(action === "pasted" ? "Pasting image…" : "Uploading image…")
      try {
        const result = await uploadTerminalImage(terminalSessionId, file, controller.signal)
        let inserted = false
        insertTerminalPath(result.path, (path) => {
          inserted = inputRef.current?.(path) ?? false
        })
        if (!inserted) {
          throw new Error(
            `The image was saved at ${result.path}, but the terminal is no longer connected.`,
          )
        }
        notify.dismiss(toast)
        notify.success(`Image ${action} • ${result.name} • ${formatUploadSize(result.size)}`, {
          description: result.path,
        })
      } catch (err) {
        notify.dismiss(toast)
        if (err instanceof DOMException && err.name === "AbortError") return
        notify.error(`Could not ${action === "pasted" ? "paste" : "upload"} image`, err)
      }
    }

    const onPaste = (event: ClipboardEvent) => {
      const intercepted = interceptClipboardImagePaste(
        event,
        (file) => void upload(file, "pasted"),
        unsupported,
      )
      if (intercepted) event.stopPropagation()
    }

    const carriesFiles = (event: DragEvent) =>
      Array.from(event.dataTransfer?.types ?? []).includes("Files")

    const onDragEnter = (event: DragEvent) => {
      if (!carriesFiles(event)) return
      event.preventDefault()
      event.stopPropagation()
      dragDepth++
      setImageDrag(true)
    }
    const onDragOver = (event: DragEvent) => {
      if (!carriesFiles(event)) return
      event.preventDefault()
      event.stopPropagation()
      if (event.dataTransfer) event.dataTransfer.dropEffect = "copy"
    }
    const onDragLeave = (event: DragEvent) => {
      if (!carriesFiles(event)) return
      event.preventDefault()
      event.stopPropagation()
      dragDepth = Math.max(0, dragDepth - 1)
      if (dragDepth === 0) setImageDrag(false)
    }
    const onDrop = (event: DragEvent) => {
      if (!carriesFiles(event)) return
      event.preventDefault()
      event.stopPropagation()
      dragDepth = 0
      setImageDrag(false)
      const choice = chooseDroppedImage(event.dataTransfer?.files ?? [])
      if (choice.kind === "image") void upload(choice.file, "dropped")
      else if (choice.kind === "unsupported") unsupported(choice.mime)
    }

    host.addEventListener("paste", onPaste, { capture: true })
    host.addEventListener("dragenter", onDragEnter, { capture: true })
    host.addEventListener("dragover", onDragOver, { capture: true })
    host.addEventListener("dragleave", onDragLeave, { capture: true })
    host.addEventListener("drop", onDrop, { capture: true })
    return () => {
      controller.abort()
      setImageDrag(false)
      host.removeEventListener("paste", onPaste, { capture: true })
      host.removeEventListener("dragenter", onDragEnter, { capture: true })
      host.removeEventListener("dragover", onDragOver, { capture: true })
      host.removeEventListener("dragleave", onDragLeave, { capture: true })
      host.removeEventListener("drop", onDrop, { capture: true })
    }
  }, [terminalSessionId])

  // Anything that changes the cell size changes the geometry, so the PTY has
  // to be told — otherwise the remote shell keeps wrapping for the old one.
  useEffect(() => {
    const term = termRef.current
    if (!term) return
    term.options.scrollback = settings.scrollback
    fitRef.current?.fit()
    if (term.rows > 0) term.refresh(0, term.rows - 1)
    const socket = socketRef.current
    if (socket?.readyState === WebSocket.OPEN) {
      socket.send(JSON.stringify({ type: "resize", rows: term.rows, cols: term.cols }))
    }
  }, [settings])

  // Fullscreen is the browser's, not a CSS class: only the real thing escapes
  // the window chrome, and only it survives the operator pressing Escape,
  // which they will expect to work.
  useEffect(() => {
    const onChange = () => setFullscreen(document.fullscreenElement === frameRef.current)
    document.addEventListener("fullscreenchange", onChange)
    return () => document.removeEventListener("fullscreenchange", onChange)
  }, [])

  const toggleFullscreen = useCallback(async () => {
    const frame = frameRef.current
    if (!frame) return
    try {
      if (document.fullscreenElement === frame) await document.exitFullscreen()
      else await frame.requestFullscreen()
    } catch {
      notify.error("Fullscreen was refused by the browser")
    }
    // The pane's box changes after the transition, and the ResizeObserver
    // fires before the browser has finished laying it out.
    setTimeout(() => {
      fitRef.current?.fit()
      termRef.current?.focus()
    }, 120)
  }, [])

  useEffect(() => {
    fullscreenRef.current = toggleFullscreen
  }, [toggleFullscreen])

  const runSearch = useCallback(
    (direction: "next" | "previous") => {
      const search = searchRef.current
      if (!search || !needle) return
      const options = {
        caseSensitive: findOptions.caseSensitive,
        regex: findOptions.regex,
        wholeWord: findOptions.word,
        // Highlighting every match is what makes a count meaningful: "3 of 47"
        // with only one of them drawn is a number with nothing behind it.
        decorations: {
          matchBackground: "#a16207",
          matchOverviewRuler: "#a16207",
          activeMatchBackground: "#f59e0b",
          activeMatchColorOverviewRuler: "#f59e0b",
        },
      }
      if (direction === "next") search.findNext(needle, options)
      else search.findPrevious(needle, options)
    },
    [needle, findOptions],
  )

  // Re-running the search as the options change keeps the count honest; a
  // stale "12 matches" after switching on case sensitivity is worse than none.
  useEffect(() => {
    if (searching && needle) runSearch("next")
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [findOptions])

  const send = useCallback((data: string) => {
    const socket = socketRef.current
    if (socket?.readyState !== WebSocket.OPEN) {
      notify.error("Not connected")
      return
    }
    sendTerminalInput(socket, data)
    termRef.current?.focus()
  }, [])

  return (
    <Pane
      ref={frameRef}
      className={cn(
        "relative bg-surface-sunken",
        // In fullscreen the pane is the whole screen, so the rounded-sm corners
        // and border would draw a frame around nothing.
        fullscreen && "rounded-none border-0",
        copyMode && "terminal-tmux",
        className,
      )}
    >
      <div className="flex shrink-0 items-center gap-1 border-b border-hairline bg-surface-header px-2 py-1.5">
        {headerContent ? (
          <div className="flex min-w-0 flex-1 items-center gap-1">{headerContent}</div>
        ) : (
          <span className="min-w-0 flex-1 truncate font-mono text-xs text-muted-foreground">
            {subtitle ?? path}
            {shellTitle && (
              <span className="ml-2 rounded-sm bg-muted px-1 py-px text-micro text-foreground">
                {shellTitle}
              </span>
            )}
          </span>
        )}

        {searching ? (
          <div className="flex min-w-0 flex-wrap items-center gap-1">
            <Input
              autoFocus
              value={needle}
              onChange={(e) => setNeedle(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter") runSearch(e.shiftKey ? "previous" : "next")
                if (e.key === "Escape") {
                  setSearching(false)
                  searchRef.current?.clearDecorations()
                  termRef.current?.focus()
                }
              }}
              aria-label="Find in scrollback"
              placeholder="Find in scrollback"
              className="h-7 w-44 text-xs"
            />
            <span className="numeric w-14 shrink-0 text-center text-micro text-muted-foreground">
              {needle ? (matches.count ? `${matches.index + 1}/${matches.count}` : "none") : ""}
            </span>
            <FindToggle
              label="Match case"
              on={findOptions.caseSensitive}
              onClick={() => setFindOptions((o) => ({ ...o, caseSensitive: !o.caseSensitive }))}
            >
              <TextUppercase className="size-3.5" />
            </FindToggle>
            <FindToggle
              label="Whole word"
              on={findOptions.word}
              onClick={() => setFindOptions((o) => ({ ...o, word: !o.word }))}
            >
              <TextTitle className="size-3.5" />
            </FindToggle>
            <FindToggle
              label="Regular expression"
              on={findOptions.regex}
              onClick={() => setFindOptions((o) => ({ ...o, regex: !o.regex }))}
            >
              <SlashForward className="size-3.5" />
            </FindToggle>
            <PaneButton label="Previous match" onClick={() => runSearch("previous")}>
              <ArrowUp className="size-3.5" />
            </PaneButton>
            <PaneButton label="Next match" onClick={() => runSearch("next")}>
              <ArrowDown className="size-3.5" />
            </PaneButton>
            <PaneButton
              label="Close search"
              onClick={() => {
                setSearching(false)
                searchRef.current?.clearDecorations()
                termRef.current?.focus()
              }}
            >
              <Cross className="size-3.5" />
            </PaneButton>
          </div>
        ) : (
          <>
            <PaneButton
              label={`Search scrollback (${formatChord(map["terminal.search"])})`}
              onClick={() => setSearching(true)}
            >
              <MagnifyingGlass className="size-3.5" />
            </PaneButton>

            <SnippetMenu snippets={snippets} onSend={(command) => send(command + "\r")} />

            <SettingsMenu />

            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <Button variant="ghost" size="icon-sm" aria-label="Terminal actions">
                  <MoreHorizontal className="size-4" />
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end" className="w-60">
                <DropdownMenuLabel>Terminal actions</DropdownMenuLabel>
                <DropdownMenuItem
                  onSelect={() => termRef.current && copySelection(termRef.current)}
                >
                  <Copy className="size-4" /> Copy selection
                </DropdownMenuItem>
                <DropdownMenuItem
                  onSelect={() => termRef.current && downloadScrollback(termRef.current)}
                >
                  <Download className="size-4" /> Save scrollback
                </DropdownMenuItem>
                {cwd && onOpenFiles && (
                  <DropdownMenuItem onSelect={() => onOpenFiles(cwd)}>
                    <FolderOpen className="size-4" /> Open working folder
                  </DropdownMenuItem>
                )}
                <DropdownMenuSeparator />
                <DropdownMenuItem onSelect={() => setShortcuts(true)}>
                  <Command className="size-4" /> Keyboard shortcuts
                </DropdownMenuItem>
                <DropdownMenuItem
                  onSelect={() => {
                    termRef.current?.clear()
                    termRef.current?.focus()
                  }}
                >
                  <Trash className="size-4" /> Clear screen
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>

            <PaneButton
              label={
                (onToggleFullscreen ? fullscreenActive : fullscreen)
                  ? "Leave fullscreen (Esc)"
                  : "Fullscreen"
              }
              onClick={onToggleFullscreen ?? toggleFullscreen}
            >
              {(onToggleFullscreen ? fullscreenActive : fullscreen) ? (
                <FullscreenClose className="size-3.5" />
              ) : (
                <Fullscreen className="size-3.5" />
              )}
            </PaneButton>
          </>
        )}

        {/* No connection badge. A socket that is up is the unremarkable case
            and said nothing worth a pill in a row of controls; the one state
            worth knowing about announces itself — a dropped socket writes
            "— disconnected —" into the pane and the error banner takes over. */}
      </div>

      {/*
        A dropped socket is not retried on its own. Reconnecting re-issues the
        GET, and for a session the dashboard is holding that is harmless — but
        the same component drives the compose runner, where re-issuing the GET
        runs the command again. So the retry is a button, and it says what it
        will do.
      */}
      {state === "closed" && (
        <div className="flex items-center gap-2 border-b border-hairline bg-muted/40 px-3 py-1.5 text-xs">
          <span className="flex-1 text-muted-foreground">
            The connection to this session ended. It is still running on the server.
          </span>
          <Button size="xs" variant="outline" onClick={() => setGeneration((g) => g + 1)}>
            <RotateClockwise className="size-3" />
            Reconnect
          </Button>
        </div>
      )}

      {error && (
        <p className="border-b border-hairline bg-wash-danger px-3 py-1.5 text-xs text-destructive">
          {error}
        </p>
      )}

      <div className="relative isolate min-h-0 min-w-0 flex-1 overflow-hidden">
        <div
          ref={hostRef}
          className={cn(
            "absolute inset-3 z-0 overflow-hidden transition-colors duration-150 motion-reduce:transition-none",
            bell && "bg-mark",
          )}
          style={bell ? undefined : { backgroundColor: "var(--background)" }}
          // Click-to-focus-a-pane is a native capture listener installed with
          // the terminal, not a prop here: xterm stops the event before it
          // reaches React. See `onMouseDownCapture` in the connect effect.
          //
          // Middle-click paste is the X11 convention every Linux operator has
          // in their fingers, and the browser does not provide it for us.
          onAuxClick={(e) => {
            if (e.button === 1 && socketRef.current) {
              e.preventDefault()
              void requestPaste(socketRef.current, setPendingPaste)
            }
          }}
        />
        {copyMode && scroll.history > 0 && (
          <input
            type="range"
            aria-label="Terminal scrollback"
            aria-valuetext={
              scroll.offset ? `${scroll.offset} lines above live output` : "Live output"
            }
            min={0}
            max={scroll.history}
            step={1}
            value={scroll.history - Math.min(scroll.offset, scroll.history)}
            className="terminal-history absolute top-3 right-0.5 bottom-3 z-20 w-3 cursor-pointer"
            onPointerDown={() => {
              draggingScroll.current = true
            }}
            onChange={(event) => {
              const offset = scroll.history - Number(event.target.value)
              setScroll((value) => ({ ...value, offset }))
              if (!draggingScroll.current && socketRef.current?.readyState === WebSocket.OPEN)
                socketRef.current.send(JSON.stringify({ type: "scroll-to", offset }))
            }}
            onPointerUp={(event) => {
              draggingScroll.current = false
              if (socketRef.current?.readyState === WebSocket.OPEN)
                socketRef.current.send(
                  JSON.stringify({
                    type: "scroll-to",
                    offset: scroll.history - Number(event.currentTarget.value),
                  }),
                )
            }}
            onPointerCancel={() => {
              draggingScroll.current = false
            }}
          />
        )}
        {imageDrag && (
          <div className="pointer-events-none absolute inset-2 z-10 flex items-center justify-center rounded-md border border-dashed border-primary bg-background/90 text-xs font-medium text-primary shadow-sm">
            Drop image to upload and paste its path
          </div>
        )}
        {(!atBottom || scrolledBack) && (
          <Button
            size="xs"
            variant="secondary"
            className="pointer-events-auto absolute right-7 bottom-4 z-20 shadow-md"
            onClick={() => {
              // Two scrollbacks can be behind this: the emulator's, when
              // there is no tmux, and tmux's own. Ending both is what "the
              // end" means, and the second one is also what gives the pane
              // its keyboard back. `exit-copy` goes out unconditionally —
              // tmux can be in a mode the browser never recorded, and a cancel
              // sent to a pane that is not in one is a harmless no-op — and
              // the server's `copy-mode` reply is what actually settles the
              // flag.
              termRef.current?.scrollToBottom()
              wheeledRef.current = false
              scrolledBackRef.current = false
              setScrolledBack(false)
              const socket = socketRef.current
              if (copyMode && socket?.readyState === WebSocket.OPEN) {
                socket.send(JSON.stringify({ type: "exit-copy" }))
              }
              termRef.current?.focus()
            }}
          >
            <ChevronDoubleDown className="size-3" />
            Jump to the end
          </Button>
        )}
      </div>

      {/* The control keys, as buttons. Ctrl+C is unremarkable on a keyboard and
          impossible on a phone, and this panel is reached from a phone more
          often than its author would like. */}
      <div className="flex shrink-0 items-center gap-1 overflow-x-auto border-t border-hairline bg-surface-header px-3 py-1.5">
        <span className="mr-2 hidden text-hint text-muted-foreground sm:inline">Keys</span>
        {CONTROL_KEYS.map((key) => (
          <Tooltip key={key.label}>
            <TooltipTrigger asChild>
              <Button
                size="xs"
                variant="ghost"
                className="h-7 shrink-0 rounded-md border border-hairline px-2 font-mono text-hint text-muted-foreground hover:text-foreground"
                onClick={() => send(key.bytes)}
              >
                {key.label}
              </Button>
            </TooltipTrigger>
            <TooltipContent>{key.hint}</TooltipContent>
          </Tooltip>
        ))}
      </div>

      <PasteConfirmation
        paste={pendingPaste}
        onCancel={() => setPendingPaste(null)}
        onConfirm={() => {
          if (pendingPaste) send(pendingPaste.raw)
          setPendingPaste(null)
        }}
      />

      <ShortcutsDialog open={shortcuts} onOpenChange={setShortcuts} />
    </Pane>
  )
}

/** A square icon button sized for the pane's header strip. */
function PaneButton({
  label,
  onClick,
  children,
}: {
  label: string
  onClick: () => void
  children: React.ReactNode
}) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Button
          type="button"
          size="sm"
          variant="ghost"
          aria-label={label}
          className="size-7 shrink-0 p-0 text-muted-foreground hover:text-foreground"
          onClick={onClick}
        >
          {children}
        </Button>
      </TooltipTrigger>
      <TooltipContent>{label}</TooltipContent>
    </Tooltip>
  )
}

function FindToggle({
  label,
  on,
  onClick,
  children,
}: {
  label: string
  on: boolean
  onClick: () => void
  children: React.ReactNode
}) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Button
          type="button"
          size="sm"
          variant="ghost"
          aria-label={label}
          aria-pressed={on}
          className={cn(
            "size-7 shrink-0 p-0",
            on ? "bg-plot-primary text-primary" : "text-muted-foreground hover:text-foreground",
          )}
          onClick={onClick}
        >
          {children}
        </Button>
      </TooltipTrigger>
      <TooltipContent>{label}</TooltipContent>
    </Tooltip>
  )
}

function SnippetMenu({
  snippets,
  onSend,
}: {
  snippets: { id: string; label: string; command: string }[]
  onSend: (command: string) => void
}) {
  if (snippets.length === 0) return null
  return (
    <DropdownMenu>
      <Tooltip>
        <TooltipTrigger asChild>
          <DropdownMenuTrigger asChild>
            <Button
              type="button"
              size="sm"
              variant="ghost"
              aria-label="Send a saved command"
              className="size-7 shrink-0 p-0 text-muted-foreground hover:text-foreground"
            >
              <Lightning className="size-3.5" />
            </Button>
          </DropdownMenuTrigger>
        </TooltipTrigger>
        <TooltipContent>Send one of your saved commands to this shell</TooltipContent>
      </Tooltip>
      <DropdownMenuContent align="end" className="w-60">
        <DropdownMenuLabel className="text-xs">Send a command</DropdownMenuLabel>
        <DropdownMenuSeparator />
        {snippets.map((snippet) => (
          <DropdownMenuItem
            key={snippet.id}
            className="flex-col items-start gap-0 text-xs"
            onSelect={() => onSend(snippet.command)}
          >
            <span>{snippet.label}</span>
            <span className="font-mono text-micro text-muted-foreground">{snippet.command}</span>
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

function SettingsMenu() {
  const settings = useTerminalSettings()
  return (
    <Popover>
      <Tooltip>
        <TooltipTrigger asChild>
          <PopoverTrigger asChild>
            <Button
              type="button"
              size="sm"
              variant="ghost"
              aria-label="Terminal settings"
              className="size-7 shrink-0 p-0 text-muted-foreground hover:text-foreground"
            >
              <SettingsSliders className="size-3.5" />
            </Button>
          </PopoverTrigger>
        </TooltipTrigger>
        <TooltipContent>Terminal behaviour</TooltipContent>
      </Tooltip>
      <PopoverContent align="end" className="w-72 space-y-3 text-xs">
        <p className="eyebrow pt-1">Behaviour</p>
        <SettingSwitch
          label="Copy on select"
          hint="Selecting text copies it, as a native Linux terminal does."
          checked={settings.copyOnSelect}
          onChange={(copyOnSelect) => setTerminalSettings({ copyOnSelect })}
        />
        <SettingSwitch
          label="Confirm multi-line paste"
          hint="A pasted block runs every line but the last. This shows it first."
          checked={settings.confirmMultilinePaste}
          onChange={(confirmMultilinePaste) => setTerminalSettings({ confirmMultilinePaste })}
        />
        <SettingSwitch
          label="Flash on bell"
          checked={settings.visualBell}
          onChange={(visualBell) => setTerminalSettings({ visualBell })}
        />
        <SettingSwitch
          label="Notify on bell"
          hint="Needs the browser's permission, asked for once."
          checked={settings.notifyOnBell}
          onChange={(notifyOnBell) => {
            setTerminalSettings({ notifyOnBell })
            if (notifyOnBell && typeof Notification !== "undefined") {
              void Notification.requestPermission()
            }
          }}
        />
        <div className="space-y-1">
          <div className="flex items-center justify-between">
            <span>Scrollback</span>
            <span className="numeric text-muted-foreground">
              {settings.scrollback.toLocaleString()} lines
            </span>
          </div>
          <Slider
            aria-label="Terminal scrollback"
            min={1000}
            max={200000}
            step={1000}
            value={[settings.scrollback]}
            onValueChange={([v]) => setTerminalSettings({ scrollback: v })}
          />
        </div>
      </PopoverContent>
    </Popover>
  )
}

function SettingSwitch({
  label,
  hint,
  checked,
  onChange,
}: {
  label: string
  hint?: string
  checked: boolean
  onChange: (value: boolean) => void
}) {
  return (
    <div className="flex items-start justify-between gap-2">
      <span className="min-w-0">
        <span className="block">{label}</span>
        {hint && <span className="block text-micro text-muted-foreground">{hint}</span>}
      </span>
      <Switch
        aria-label={label}
        checked={checked}
        onCheckedChange={onChange}
        className="mt-0.5 shrink-0"
      />
    </div>
  )
}

/**
 * The multi-line paste confirmation.
 *
 * It shows the text rather than merely warning about it, because the warning
 * on its own does not help: the operator already believes they know what is on
 * the clipboard, and the whole failure mode is that they are wrong.
 */
function PasteConfirmation({
  paste,
  onConfirm,
  onCancel,
}: {
  paste: { raw: string; text: string } | null
  onConfirm: () => void
  onCancel: () => void
}) {
  const lines = paste ? paste.text.replace(/\n$/, "").split("\n") : []
  return (
    <Modal
      open={paste !== null}
      onOpenChange={(open) => !open && onCancel()}
      title={<>Paste {lines.length} lines?</>}
      description="This text contains line breaks that can execute commands as soon as they reach the
            terminal. Review every line before sending it."
      footer={
        <>
          <Button size="sm" variant="ghost" onClick={onCancel}>
            Cancel
          </Button>
          <Button size="sm" onClick={onConfirm}>
            Paste and run
          </Button>
        </>
      }
    >
      <pre className="max-h-56 overflow-auto rounded-md border bg-surface-sunken p-2 font-mono text-hint whitespace-pre-wrap">
        {paste?.text}
      </pre>
    </Modal>
  )
}

/**
 * Whether a chunk of input is a paste of more than one line.
 *
 * Length is what separates it from a keystroke: pressing Enter sends a single
 * carriage return, and a program that legitimately writes a line ending sends
 * it alongside something else. xterm normalises a pasted newline to \r, so
 * both endings count.
 */
/**
 * Whether a chunk from the emulator is the mouse reporting itself rather than
 * the operator typing.
 *
 * Two encodings, because tmux asks for the newer one and falls back: SGR
 * (`CSI < b ; x ; y M|m`, enabled by `?1006h`) and the original X10 form
 * (`CSI M` followed by three bytes, which may be anything at all — hence the
 * length check rather than a pattern).
 */
function isMouseReport(data: string): boolean {
  if (/^\x1b\[<\d+;\d+;\d+[Mm]$/.test(data)) return true
  return data.startsWith("\x1b[M") && data.length === 6
}

function isMultilinePaste(data: string): boolean {
  if (data.length < 2) return false
  return /[\r\n]/.test(data.replace(/[\r\n]+$/, "")) || /[\r\n]/.test(stripBracketedPaste(data))
}

/** The escape sequences a shell in bracketed-paste mode wraps a paste in. */
function stripBracketedPaste(data: string): string {
  return data.replace(/\u001b\[200~/g, "").replace(/\u001b\[201~/g, "")
}

/** What to show the reader: the text, without the wrapper and without \r. */
function readablePaste(data: string): string {
  return stripBracketedPaste(data).replace(/\r\n?/g, "\n")
}

async function copySelection(term: Terminal) {
  const selection = term.getSelection()
  if (!selection) {
    notify.info("Nothing is selected")
    return
  }
  await writeClipboard(selection)
}

async function writeClipboard(text: string) {
  await copyText(text, "Copied")
}

/**
 * Gives the pointer back to the page while the program in the pane is asking
 * for mouse reports.
 *
 * `shouldForceSelection` is xterm's own decision point for "this drag belongs
 * to the browser" — its selection service and its mouse-report forwarding both
 * ask it the same question, so answering once is what keeps the two from
 * disagreeing. Normally the answer is "only with Shift held"; here it is
 * inverted, because a plain drag selecting is the whole point and **Alt** is
 * left as the way through to the programs that want a mouse of their own
 * (vim, htop, less). The wheel is bound separately inside xterm and does not
 * consult this, so scrolling the history is untouched.
 *
 * It is reached through `_core` because xterm publishes no option for it. The
 * property names survive minification and have been there for years; if a
 * later xterm renames either, selection falls back to needing Shift rather
 * than breaking.
 */
function forcePointerToSelect(term: Terminal) {
  const selection = (
    term as unknown as {
      _core?: { _selectionService?: { shouldForceSelection?: (event: MouseEvent) => boolean } }
    }
  )._core?._selectionService
  if (!selection) return
  selection.shouldForceSelection = (event: MouseEvent) => !event.altKey
}

/**
 * Ctrl+C and Ctrl+V, as they behave everywhere else, without taking Ctrl+C
 * away from the shell.
 *
 * Ctrl+Shift+C and Ctrl+Shift+V are the terminal convention and stay bound,
 * but they are not what anybody's hands reach for, and a copy that has to be
 * found with the mouse is not copy. So Ctrl+C copies **only when something is
 * selected**, and clears the selection as it goes: the interrupt is never more
 * than one keypress away, and the next Ctrl+C is one.
 *
 * Ctrl+V returns false without calling `preventDefault`, which is the whole
 * trick — xterm then leaves the key alone instead of sending ^V, and the
 * browser's own paste runs, arriving through `onData` where the multi-line
 * confirmation still sees it. Reading the clipboard here instead would need a
 * permission Firefox does not grant at all.
 */
function clipboardKey(event: KeyboardEvent, term: Terminal): boolean {
  if (!(event.ctrlKey || event.metaKey) || event.shiftKey || event.altKey) return true
  if (event.code === "KeyC") {
    const selection = term.getSelection()
    if (!selection) return true
    event.preventDefault()
    term.clearSelection()
    void writeClipboard(selection)
    return false
  }
  if (event.code === "KeyV") return false
  return true
}

/**
 * Reads the clipboard and either sends it or hands it to the confirmation.
 *
 * Paste goes to the PTY as ordinary input rather than through xterm, so the
 * remote shell sees exactly the bytes it would from a keyboard.
 */
async function requestPaste(
  socket: WebSocket,
  ask: (paste: { raw: string; text: string }) => void,
) {
  if (socket.readyState !== WebSocket.OPEN) return
  try {
    const text = await navigator.clipboard.readText()
    if (!text) return
    if (terminalSettings().confirmMultilinePaste && isMultilinePaste(text)) {
      ask({ raw: text, text: readablePaste(text) })
      return
    }
    sendTerminalInput(socket, text)
  } catch {
    notify.error(
      "The browser refused clipboard access",
      "Ctrl+V pastes into the shell directly if the page has no permission.",
    )
  }
}

const terminalEncoder = new TextEncoder()

/** Keyboard and paste data use binary frames just like PTY output. */
function sendTerminalInput(socket: WebSocket, data: string) {
  socket.send(terminalEncoder.encode(data))
}

/**
 * Writes the whole scrollback out as a text file.
 *
 * Walked line by line off the buffer rather than through a serialiser addon:
 * what is wanted here is the *text* of a build that failed, to be attached to
 * an issue or grepped, and escape sequences in that are noise. Trailing blank
 * lines go, because a 50,000-line buffer is mostly empty.
 */
function downloadScrollback(term: Terminal) {
  const buffer = term.buffer.active
  const lines: string[] = []
  for (let i = 0; i < buffer.length; i++) {
    lines.push(buffer.getLine(i)?.translateToString(true) ?? "")
  }
  while (lines.length && lines[lines.length - 1].trim() === "") lines.pop()
  if (lines.length === 0) {
    notify.info("There is nothing in the scrollback yet")
    return
  }
  const blob = new Blob([lines.join("\n") + "\n"], { type: "text/plain;charset=utf-8" })
  const url = URL.createObjectURL(blob)
  const anchor = document.createElement("a")
  anchor.href = url
  anchor.download = `terminal-${new Date().toISOString().replace(/[:.]/g, "-")}.txt`
  anchor.click()
  URL.revokeObjectURL(url)
  notify.success(`Saved ${lines.length.toLocaleString()} lines`)
}
