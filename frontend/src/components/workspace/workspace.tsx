"use client"

import {
  createContext,
  useContext,
  useEffect,
  useEffectEvent,
  useRef,
  useState,
  useSyncExternalStore,
} from "react"
import { usePathname } from "next/navigation"
import { useMemoryState, useSessionState } from "@/lib/view-state"
import { Modal } from "@/components/modal"
import { Button } from "@/components/ui/button"
import { useWorkspaceCommands, type WorkspaceCommand } from "./commands"
import { nameIndex, nextIndex, overlayOpen, typing } from "./keys"

type Place = { item: string | null; field?: number; scroll: number[] }
const subscribe = () => () => {}
const client = () => true
const server = () => false
const visible = (node: HTMLElement) => node.getClientRects().length > 0
const itemSelector = "[data-workspace-item]"
const Help = createContext<{ name: string; show: () => void; restore: () => boolean } | null>(null)

export function useWorkspaceFocus() {
  return useContext(Help)?.restore
}

export function WorkspaceHelp({ compact = false }: { compact?: boolean }) {
  const help = useContext(Help)
  return help ? (
    <Button
      size="xs"
      variant="ghost"
      aria-label={`${help.name} shortcuts`}
      title={`${help.name} shortcuts`}
      onClick={help.show}
    >
      {compact ? <kbd>?</kbd> : `${help.name} shortcuts`}
    </Button>
  ) : null
}

/** Page-owned keys and place memory; editors and portalled controls own their own keys. */
export function Workspace({
  name,
  stateKey,
  refresh,
  escape,
  commands = [],
  children,
  rows = true,
  search = true,
  openItems = true,
  memory = false,
}: {
  name: string
  stateKey?: string
  refresh?: () => void
  escape?: () => boolean
  commands?: WorkspaceCommand[]
  children: React.ReactNode
  rows?: boolean
  search?: boolean
  openItems?: boolean
  memory?: boolean
}) {
  const pathname = usePathname()
  const [arrivalPath] = useState(pathname)
  const key = stateKey ?? arrivalPath
  const root = useRef<HTMLDivElement>(null)
  const [help, setHelp] = useState(false)
  const hydrated = useSyncExternalStore(subscribe, client, server)
  const sessionPlace = useSessionState<Place | null>(`workspace.${key}.place`, null)
  const memoryPlace = useMemoryState<Place | null>(`workspace.${key}.place`, null)
  const [place, remember] = memory ? memoryPlace : sessionPlace
  const { register } = useWorkspaceCommands()
  const prefix = useRef({ text: "", time: 0 })
  const items = () =>
    Array.from(root.current?.querySelectorAll<HTMLElement>(itemSelector) ?? []).filter(visible)
  const focus = (item: HTMLElement) => {
    const primary = item.querySelector<HTMLElement>("[data-workspace-primary]") ?? item
    if (!primary.matches("a, button, input, [tabindex]")) primary.tabIndex = -1
    primary.focus({ preventScroll: true })
    primary.scrollIntoView({ block: "nearest", inline: "nearest" })
  }
  const find = () => {
    const input =
      root.current?.querySelector<HTMLInputElement>("[data-workspace-search]") ??
      root.current?.querySelector<HTMLInputElement>("[data-page-search]")
    input?.focus()
    input?.select()
  }
  const restoreFocus = () => {
    const item = items().find((item) => item.dataset.workspaceItem === place?.item)
    if (!item) return false
    focus(item)
    return true
  }
  const onKey = useEffectEvent((event: KeyboardEvent) => {
    if (event.defaultPrevented || event.isComposing || overlayOpen()) return
    const target = event.target instanceof HTMLElement ? event.target : null
    if (target && target !== document.body && !root.current?.contains(target)) return
    const mod = event.ctrlKey || event.metaKey
    const inEditor = !!target?.closest(".monaco-editor, .xterm, [contenteditable='true']")
    if (mod && event.key.toLowerCase() === "f" && !inEditor && !event.altKey && !event.shiftKey) {
      if (!root.current?.querySelector("[data-page-search], [data-workspace-search]")) return
      event.preventDefault()
      find()
      return
    }
    if (event.key === "Escape" && target?.matches("[data-page-search], [data-workspace-search]")) {
      if (escape?.()) event.preventDefault()
      const first = items()[0]
      if (first) focus(first)
      else target.blur()
      return
    }
    if (typing(target) && !target?.matches("input[type='checkbox'][data-workspace-item]")) return
    const chord = `${event.ctrlKey ? "Ctrl+" : ""}${event.metaKey ? "Meta+" : ""}${event.altKey ? "Alt+" : ""}${event.shiftKey ? "Shift+" : ""}${event.key}`
    const command = commands.find((command) => command.chord === chord && !command.disabled)
    if (command) {
      event.preventDefault()
      command.run()
      return
    }
    if (
      refresh &&
      (event.key === "F5" || (mod && event.key.toLowerCase() === "r")) &&
      !event.shiftKey &&
      !event.altKey
    ) {
      event.preventDefault()
      refresh()
    } else if (!mod && !event.altKey && event.key === "?") {
      event.preventDefault()
      setHelp(true)
    } else if (!mod && !event.altKey && event.key === "Escape" && escape?.()) {
      event.preventDefault()
    } else if (!mod && !event.altKey && rows) {
      const list = items()
      const current = target?.closest<HTMLElement>(itemSelector)
      const at = list.findIndex((item) => item === current)
      let next = nextIndex(list.length, at, event.key)
      if (next < 0 && event.key.length === 1 && /[\p{L}\p{N}]/u.test(event.key)) {
        const now = Date.now()
        const before = now - prefix.current.time < 700 ? prefix.current.text : ""
        const text = before === event.key ? event.key : before + event.key
        prefix.current = { text, time: now }
        next = nameIndex(
          list.map((item) => item.dataset.workspaceName ?? item.textContent?.trim() ?? ""),
          text,
          before.length > 0 && text !== event.key ? at - 1 : at,
        )
      }
      if (next >= 0) {
        event.preventDefault()
        focus(list[next])
      }
    }
  })
  useEffect(() => {
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
  }, [])

  const runCommand = useEffectEvent((id: string) => {
    if (id === "find") find()
    else if (id === "refresh") refresh?.()
    else if (id === "help") setHelp(true)
    else commands.find((command) => command.id === id && !command.disabled)?.run()
  })
  const signature = JSON.stringify(
    commands.map(({ id, label, keys, chord, disabled }) => ({ id, label, keys, chord, disabled })),
  )
  const hasRefresh = !!refresh
  useEffect(() => {
    const choices: WorkspaceCommand[] = [
      ...(search ? [{ id: "find", label: `Find in ${name}`, keys: "Ctrl/⌘+F" }] : []),
      ...(hasRefresh ? [{ id: "refresh", label: `Refresh ${name}`, keys: "F5 · Ctrl/⌘+R" }] : []),
      ...JSON.parse(signature),
      { id: "help", label: `${name} shortcuts`, keys: "?" },
    ].map((command) => ({ ...command, run: () => runCommand(command.id) }))
    return register({ name, commands: choices })
  }, [name, signature, hasRefresh, search, register])

  const readPlace = useEffectEvent(() => place)
  useEffect(() => {
    const node = root.current
    if (!node || !hydrated) return
    const shell = node.closest<HTMLElement>("[data-workspace-shell-scroll]")
    const scrollers = () =>
      [
        shell,
        ...node.querySelectorAll<HTMLElement>(
          "[data-workspace-scroll], [data-slot='table-container']",
        ),
      ].filter((el): el is HTMLElement => !!el)
    const fields = () =>
      Array.from(
        node.querySelectorAll<HTMLElement>("input:not([type='hidden']), textarea, select"),
      ).filter(visible)
    let visitPlace: Place | null = null
    let restoring = true
    let applyingFocus = false
    let frame = 0
    let focusFrame = 0
    let restoreFrame = 0
    let lastItem = place?.item ?? null
    let lastField = place?.field
    let lastScroll = place?.scroll ?? scrollers().map((el) => el.scrollTop)
    let focusedTarget: HTMLElement | undefined
    const finish = () => {
      restoring = false
      observer.disconnect()
      cancelAnimationFrame(focusFrame)
      cancelAnimationFrame(restoreFrame)
    }
    const restore = () => {
      if (!visitPlace) return
      const list = scrollers()
      if (list.length < visitPlace.scroll.length) return
      list.forEach((el, index) => {
        el.scrollTop = visitPlace?.scroll[index] ?? 0
      })
      if (list.some((el, index) => el.scrollTop < (visitPlace?.scroll[index] ?? 0))) return
      if (focusedTarget?.isConnected && document.activeElement === focusedTarget) return
      const rememberedField = lastField === undefined ? undefined : fields()[lastField]
      if (lastField !== undefined && !rememberedField) return
      let primary = rememberedField
      if (visitPlace.item) {
        const item = items().find((el) => el.dataset.workspaceItem === visitPlace?.item)
        if (!item) return
        primary = item.querySelector<HTMLElement>("[data-workspace-primary]") ?? item
      }
      if (primary) {
        if (focusFrame) return
        // Next restores route focus after the rows mount; resolve the row again after that.
        // Responsive layouts may replace the first hydrated row before the frame is painted.
        focusFrame = requestAnimationFrame(() => {
          focusFrame = requestAnimationFrame(() => {
            focusFrame = 0
            if (!restoring) return
            const item = visitPlace?.item
              ? items().find((el) => el.dataset.workspaceItem === visitPlace?.item)
              : undefined
            const target = item
              ? (item.querySelector<HTMLElement>("[data-workspace-primary]") ?? item)
              : lastField === undefined
                ? undefined
                : fields()[lastField]
            if (!target?.isConnected || overlayOpen()) return
            if (!target.matches("a, button, input, textarea, select, [tabindex]"))
              target.tabIndex = -1
            applyingFocus = true
            target.focus({ preventScroll: true })
            applyingFocus = false
            if (document.activeElement === target) focusedTarget = target
          })
        })
      }
    }
    const observer = new MutationObserver(() => {
      if (restoring) restore()
    })
    // Read after the stored hook switches keys/hydrates, once per visit rather than on writes.
    restoreFrame = requestAnimationFrame(() => {
      if (!restoring) return
      visitPlace = readPlace()
      if (!visitPlace) {
        finish()
        return
      }
      lastItem = visitPlace.item
      lastField = visitPlace.field
      lastScroll = visitPlace.scroll
      observer.observe(node, { childList: true, subtree: true })
      // Cached routes can reveal an ancestor without mutating the rows beneath this node.
      const retry = () => {
        if (!restoring) return
        restore()
        restoreFrame = requestAnimationFrame(retry)
      }
      retry()
    })
    // A deleted row must not keep fighting the reader or prevent saving the new place.
    const timeout = setTimeout(finish, 2000)
    const interrupted = () => {
      finish()
      cancelAnimationFrame(focusFrame)
    }
    // Hydration can replace a focused row after its first paint. Watch until the deadline,
    // but any reader interaction ends restoration before its own focus/scroll is saved.
    window.addEventListener("pointerdown", interrupted, { once: true, capture: true })
    window.addEventListener("keydown", interrupted, { once: true, capture: true })
    window.addEventListener("wheel", interrupted, { once: true, passive: true })
    const save = () => {
      if (restoring) return
      const next = {
        item: lastItem,
        field: lastField,
        scroll: lastScroll,
      }
      remember((prev) => (JSON.stringify(prev) === JSON.stringify(next) ? prev : next))
    }
    const onScroll = () => {
      if (restoring) return
      lastScroll = scrollers().map((el) => el.scrollTop)
      if (frame) return
      frame = requestAnimationFrame(() => {
        frame = 0
        save()
      })
    }
    const onFocus = (event: FocusEvent) => {
      const target = event.target as HTMLElement
      const item = target.closest<HTMLElement>(itemSelector)
      const field = fields().indexOf(target)
      if (applyingFocus || (!item && field < 0)) return
      finish()
      lastScroll = scrollers().map((el) => el.scrollTop)
      if (item) {
        lastItem = item.dataset.workspaceItem ?? null
        lastField = undefined
        save()
      } else {
        lastItem = null
        lastField = field
        save()
      }
    }
    shell?.addEventListener("scroll", onScroll, true)
    node.addEventListener("focusin", onFocus)
    return () => {
      if (frame) cancelAnimationFrame(frame)
      if (focusFrame) cancelAnimationFrame(focusFrame)
      if (restoreFrame) cancelAnimationFrame(restoreFrame)
      // Route/step commits can replace the DOM before cleanup; retain the last visible offsets.
      save()
      clearTimeout(timeout)
      observer.disconnect()
      window.removeEventListener("pointerdown", interrupted, true)
      window.removeEventListener("keydown", interrupted, true)
      window.removeEventListener("wheel", interrupted)
      shell?.removeEventListener("scroll", onScroll, true)
      node.removeEventListener("focusin", onFocus)
    }
    // The visit is read once. Writes retain its focus and scroll without replaying restoration.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key, hydrated, memory])

  return (
    <Help.Provider value={{ name, show: () => setHelp(true), restore: restoreFocus }}>
      <div ref={root} data-native-workspace={name} className="contents">
        {children}
        <Modal
          open={help}
          onOpenChange={setHelp}
          title={`${name} shortcuts`}
          description={`Keyboard commands for ${name}.`}
        >
          <dl className="divide-y divide-hairline text-body">
            {[
              ...(search ? [["Find", "Ctrl/⌘+F"]] : []),
              ...(refresh ? [["Refresh data", "F5 · Ctrl/⌘+R"]] : []),
              ...(rows
                ? [
                    ["Move between items", "↑ / ↓ · Home / End"],
                    ["Jump to a name", "Type its first letters"],
                    ...(openItems ? [["Open item", "Enter"]] : []),
                  ]
                : []),
              ...commands
                .filter((command) => command.keys)
                .map((command) => [command.label, command.keys]),
              ["Show shortcuts", "?"],
            ].map(([label, keys]) => (
              <div key={label} className="flex flex-wrap justify-between gap-2 py-2">
                <dt>{label}</dt>
                <dd className="text-muted-foreground">
                  <kbd>{keys}</kbd>
                </dd>
              </div>
            ))}
          </dl>
        </Modal>
      </div>
    </Help.Provider>
  )
}
