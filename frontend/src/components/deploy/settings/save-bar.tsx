"use client"

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useId,
  useMemo,
  useRef,
  useState,
  useSyncExternalStore,
} from "react"
import { createPortal } from "react-dom"
import { CheckCircle } from "@/components/icons"
import { cn } from "@/lib/utils"
import { StatusDot } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { TextShimmer } from "@/components/ui/text-shimmer"

/**
 * One Save for a whole settings page, floating at the bottom of the window
 * while anything on the page holds an edit.
 *
 * Every form used to end on its own Save at the fields' right edge, so a page
 * of three forms carried three buttons in three places, and none of them was
 * where the reader's eye was once they had changed something two screens up.
 * Now a form says only whether it is dirty and how to save itself; the bar
 * appears when the first edit lands, counts the edits across the page, names
 * the parts they are in, and Save writes each dirty form in page order. It
 * floats — the one thing on a settings page that does — so it takes a
 * popover's surface and shadow (§2), and it is drawn over the content area
 * rather than the window, so it centres on the column it saves.
 *
 * The forms still save one at a time and each still answers for itself: a
 * refused field says so under the field and its form stays dirty, and the bar
 * stays with what is left. When everything went through it says *Saved* and
 * when it applies, then leaves. ⌘S (Ctrl+S) is Save while the bar is up.
 */

export type SettingApplies = "next-deployment" | "immediately"

type EntryState = {
  name: string
  dirty: boolean
  changes: number
  saving: boolean
  invalid: boolean
  applies: SettingApplies
}

type EntryActions = {
  /** Resolves true when the form's save went through. */
  save: () => Promise<boolean>
  discard?: () => void
}

type SaveBarContextValue = {
  register: (id: string, state: EntryState, actions: { current: EntryActions }) => void
  unregister: (id: string) => void
  saveAll: () => Promise<void>
}

const SaveBarContext = createContext<SaveBarContextValue | null>(null)

type Phase = "idle" | "saving" | "saved"

const SAVED_FOR = 1600

function sameEntry(a: EntryState | undefined, b: EntryState) {
  return (
    a !== undefined &&
    a.name === b.name &&
    a.dirty === b.dirty &&
    a.changes === b.changes &&
    a.saving === b.saving &&
    a.invalid === b.invalid &&
    a.applies === b.applies
  )
}

// The content area beside the rail, which the bar centres on. It is in the
// document for the whole life of the shell, so it never needs re-reading.
const subscribe = () => () => {}
const contentArea = () => document.querySelector<HTMLElement>('[data-slot="sidebar-inset"]')
const noContentArea = () => null

export function SaveBarProvider({ children }: { children: React.ReactNode }) {
  const [entries, setEntries] = useState<Record<string, EntryState>>({})
  const actions = useRef(new Map<string, { current: EntryActions }>())
  const latest = useRef(entries)
  const [phase, setPhase] = useState<Phase>("idle")
  const phaseRef = useRef(phase)
  // What the last Save was for, so *Saved* can still say when it applies
  // once the forms it saved have gone clean.
  const [saved, setSaved] = useState<EntryState[]>([])

  useEffect(() => {
    latest.current = entries
    phaseRef.current = phase
  })

  const register = useCallback((id: string, state: EntryState, ref: { current: EntryActions }) => {
    actions.current.set(id, ref)
    const before = latest.current[id]
    // A new edit while the bar still says *Saved* is news the bar has to
    // carry at once, rather than after the confirmation has had its time.
    const edited = before && (state.changes > before.changes || (state.dirty && !before.dirty))
    if (phaseRef.current === "saved" && edited) {
      phaseRef.current = "idle"
      setPhase("idle")
    }
    setEntries((previous) =>
      sameEntry(previous[id], state) ? previous : { ...previous, [id]: state },
    )
  }, [])
  const unregister = useCallback((id: string) => {
    actions.current.delete(id)
    setEntries((previous) => {
      if (!(id in previous)) return previous
      const next = { ...previous }
      delete next[id]
      return next
    })
  }, [])

  const saveAll = useCallback(async () => {
    if (phaseRef.current === "saving") return
    const dirty = Object.entries(latest.current).filter(([, entry]) => entry.dirty)
    if (dirty.length === 0 || dirty.some(([, entry]) => entry.invalid)) return
    phaseRef.current = "saving"
    setPhase("saving")
    setSaved(dirty.map(([, entry]) => entry))
    let everyone = true
    // One after another, in page order: two forms on one page write the same
    // configuration, and the second has to be sent after the first's
    // revision came back.
    for (const [id] of dirty) {
      const ok = await actions.current.get(id)?.current.save()
      if (!ok) everyone = false
    }
    setPhase(everyone ? "saved" : "idle")
  }, [])

  useEffect(() => {
    if (phase !== "saved") return
    const timer = window.setTimeout(() => setPhase("idle"), SAVED_FOR)
    return () => window.clearTimeout(timer)
  }, [phase])

  const value = useMemo(() => ({ register, unregister, saveAll }), [register, unregister, saveAll])

  return (
    <SaveBarContext.Provider value={value}>
      {children}
      <SaveBar
        entries={entries}
        phase={phase}
        saved={saved}
        onSave={() => void saveAll()}
        onDiscard={() => {
          for (const [id, entry] of Object.entries(entries))
            if (entry.dirty) actions.current.get(id)?.current.discard?.()
        }}
      />
    </SaveBarContext.Provider>
  )
}

/**
 * Puts a form on the page's bar. Everything but `save` and `discard` is read
 * as it changes; those two are read at the moment they are pressed, so the
 * bar always calls the form's current ones.
 */
export function useSaveBarEntry(
  enabled: boolean,
  state: EntryState,
  entryActions: EntryActions,
): (() => Promise<void>) | undefined {
  const bar = useContext(SaveBarContext)
  const id = useId()
  const ref = useRef(entryActions)
  useEffect(() => {
    ref.current = entryActions
  })
  const { name, dirty, changes, saving, invalid, applies } = state
  useEffect(() => {
    if (!bar || !enabled) return
    bar.register(id, { name, dirty, changes, saving, invalid, applies }, ref)
  }, [bar, enabled, id, name, dirty, changes, saving, invalid, applies])
  useEffect(() => {
    if (!bar || !enabled) return
    return () => bar.unregister(id)
  }, [bar, enabled, id])
  return bar?.saveAll
}

function appliesLine(dirty: EntryState[]) {
  const kinds = new Set(dirty.map((entry) => entry.applies))
  if (kinds.size > 1) return "Some apply now, the rest on your next deployment"
  return kinds.has("immediately") ? "Applies immediately" : "Applies on your next deployment"
}

function names(dirty: EntryState[]) {
  const list = [...new Set(dirty.map((entry) => entry.name))]
  if (list.length <= 1) return list[0] ?? ""
  return `${list.slice(0, -1).join(", ")} and ${list.at(-1)}`
}

// Read once the bar is on screen, which is only ever in the browser.
const shortcut = () => (/Mac|iPhone|iPad/.test(navigator.userAgent) ? "⌘S" : "Ctrl S")

function SaveBar({
  entries,
  phase,
  saved,
  onSave,
  onDiscard,
}: {
  entries: Record<string, EntryState>
  phase: Phase
  saved: EntryState[]
  onSave: () => void
  onDiscard: () => void
}) {
  const host = useSyncExternalStore(subscribe, contentArea, noContentArea)
  const all = Object.values(entries)
  const dirty = all.filter((entry) => entry.dirty)
  const changes = dirty.reduce((sum, entry) => sum + Math.max(entry.changes, 1), 0)
  const invalid = dirty.some((entry) => entry.invalid)
  const saving = phase === "saving" || all.some((entry) => entry.saving)
  const visible = dirty.length > 0 || phase !== "idle"

  useEffect(() => {
    if (dirty.length === 0) return
    const onKey = (event: KeyboardEvent) => {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "s") {
        event.preventDefault()
        onSave()
      }
    }
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
  }, [dirty.length, onSave])

  if (!host || !visible) return null

  // Every write went through. A form can look dirty for a moment longer —
  // until its saved value is read back — and that is not an edit left over.
  const done = phase === "saved"
  const shown = done || dirty.length === 0 ? saved : dirty

  return createPortal(
    <div className="pointer-events-none absolute inset-x-0 bottom-4 z-40 flex justify-center px-3 md:bottom-6">
      <div
        role="region"
        aria-label="Save changes"
        aria-live="polite"
        className="pointer-events-auto flex w-full max-w-xl animate-rise items-center gap-3 rounded-xl border border-border-strong bg-popover py-2.5 pr-2.5 pl-4 text-popover-foreground shadow-lg"
      >
        {/* Keyed on what it is saying, so each change of state arrives
            (§11) rather than being repainted under the reader's eye. */}
        <div
          key={done ? "done" : saving ? "saving" : "dirty"}
          className="min-w-0 flex-1 animate-rise"
        >
          <p className="flex min-w-0 items-center gap-2 text-body leading-snug font-medium">
            {done ? (
              <CheckCircle aria-hidden className="size-4 shrink-0 text-success" />
            ) : (
              <StatusDot tone="warning" />
            )}
            {done ? (
              <span>Saved</span>
            ) : saving ? (
              <TextShimmer>
                {changes === 1 ? "Saving 1 change…" : `Saving ${changes} changes…`}
              </TextShimmer>
            ) : (
              <span className="numeric">
                {changes === 1 ? "1 unsaved change" : `${changes} unsaved changes`}
              </span>
            )}
          </p>
          <p className="mt-0.5 pl-4 text-xs text-muted-foreground max-sm:line-clamp-2 sm:truncate">
            {done
              ? appliesLine(shown)
              : invalid
                ? "A value is out of range — fix it to save"
                : `${names(shown)} · ${appliesLine(shown)}`}
          </p>
        </div>
        {!done && (
          <div className="flex shrink-0 items-center gap-1.5">
            <Button
              type="button"
              variant="ghost"
              size="sm"
              disabled={saving}
              onClick={onDiscard}
              className="max-sm:h-10"
            >
              Discard
            </Button>
            <Button
              type="button"
              size="sm"
              pending={saving}
              disabled={invalid}
              onClick={onSave}
              className="max-sm:h-10"
            >
              Save
              <kbd
                aria-hidden
                className={cn(
                  "ml-0.5 font-sans text-hint font-normal opacity-60 max-sm:hidden",
                  saving && "hidden",
                )}
              >
                {shortcut()}
              </kbd>
            </Button>
          </div>
        )}
      </div>
    </div>,
    host,
  )
}
