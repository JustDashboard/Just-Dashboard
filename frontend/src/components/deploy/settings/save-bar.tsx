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
import {
  appliesLine,
  pendingLine,
  type SettingApplies,
} from "@/components/deploy/settings/save-bar-words"

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
 * rather than the window, so it centres on the column it saves. A column that
 * shares the area with something beside it (a rail of siblings) asks for the
 * bar `inline`, and it holds to the foot of that column instead.
 *
 * The forms still save one at a time and each still answers for itself: a
 * refused field says so under the field and its form stays dirty, and the bar
 * stays with what is left. When everything went through it says *Saved* and
 * when it applies, then leaves. ⌘S (Ctrl+S) is Save while the bar is up.
 *
 * What the bar says is one rule, in `save-bar-words.ts`. Its second line is
 * the parts and when their saves take effect, from each form's `applies`
 * (`SettingApplies`: at once, when nginx next reloads, when the server
 * restarts, on the next deployment; a page holding several is told which is
 * soonest and what the rest wait for). A form whose save does something no
 * kind says gives the line in its own words (`note`: "Restarts the
 * dashboard") and may name the command (`verb`: "Apply and restart"); those
 * stand for the whole bar, so a page has one such form, and the note is
 * where that form says why it cannot be saved (the password form's "The new
 * passwords do not match yet"). A form without one gets the bar's own words
 * for a value that cannot be saved.
 *
 * A proxy site's form keeps a bar of its own (`proxy/site-settings.tsx`): it
 * is one draft with two saves whose difference matters (reload or not), where
 * this bar is many forms and one command. The two share where they float and
 * the shortcut's keycap (`useSaveBarHost`, `saveShortcut`).
 */

export type { SettingApplies } from "@/components/deploy/settings/save-bar-words"

type EntryState = {
  name: string
  dirty: boolean
  changes: number
  saving: boolean
  invalid: boolean
  applies: SettingApplies
  /**
   * The bar's command where pressing it does more than save: "Apply and
   * restart", "Change password". Left out, it is Save.
   */
  verb?: string
  /**
   * When and how the form applies, in the form's own words, where neither
   * kind of `applies` says it: "Restarts the dashboard".
   */
  note?: string
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
    a.applies === b.applies &&
    a.verb === b.verb &&
    a.note === b.note
  )
}

// The content area beside the rail, which the bar centres on. It is in the
// document for the whole life of the shell, so it never needs re-reading.
const subscribe = () => () => {}
const contentArea = () => document.querySelector<HTMLElement>('[data-slot="sidebar-inset"]')
const noContentArea = () => null

/** Where a floating save bar is drawn: the content area, once in the browser. */
export function useSaveBarHost() {
  return useSyncExternalStore(subscribe, contentArea, noContentArea)
}

/** The Save shortcut's keycap. Read once a bar is on screen, which is only ever in the browser. */
export const saveShortcut = () => (/Mac|iPhone|iPad/.test(navigator.userAgent) ? "⌘S" : "Ctrl S")

export function SaveBarProvider({
  children,
  inline,
}: {
  children: React.ReactNode
  /**
   * The bar holds to the foot of the column it is in rather than floating
   * over the content area: for a settings column that shares the area with
   * something beside it (a rail of siblings), where the area's centre is not
   * the column's.
   */
  inline?: boolean
}) {
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
        inline={inline}
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
  const { name, dirty, changes, saving, invalid, applies, verb, note } = state
  useEffect(() => {
    if (!bar || !enabled) return
    bar.register(id, { name, dirty, changes, saving, invalid, applies, verb, note }, ref)
  }, [bar, enabled, id, name, dirty, changes, saving, invalid, applies, verb, note])
  useEffect(() => {
    if (!bar || !enabled) return
    return () => bar.unregister(id)
  }, [bar, enabled, id])
  return bar?.saveAll
}

function SaveBar({
  entries,
  phase,
  saved,
  inline,
  onSave,
  onDiscard,
}: {
  entries: Record<string, EntryState>
  phase: Phase
  saved: EntryState[]
  inline?: boolean
  onSave: () => void
  onDiscard: () => void
}) {
  const host = useSaveBarHost()
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

  if ((!host && !inline) || !visible) return null

  // Every write went through. A form can look dirty for a moment longer —
  // until its saved value is read back — and that is not an edit left over.
  const done = phase === "saved"
  const shown = done || dirty.length === 0 ? saved : dirty
  // A form that says these itself is the only one on its page that does.
  const verb = shown.find((entry) => entry.verb)?.verb ?? "Save"
  const note = shown.find((entry) => entry.note)?.note

  const bar = (
    <div
      className={cn(
        "pointer-events-none z-40 flex justify-center",
        inline ? "sticky bottom-4" : "absolute inset-x-0 bottom-4 px-3 md:bottom-6",
      )}
    >
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
              ? (note ?? appliesLine(shown.map((entry) => entry.applies)))
              : pendingLine(shown, invalid)}
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
              {verb}
              <kbd
                aria-hidden
                className={cn(
                  "ml-0.5 font-sans text-hint font-normal opacity-60 max-sm:hidden",
                  saving && "hidden",
                )}
              >
                {saveShortcut()}
              </kbd>
            </Button>
          </div>
        )}
      </div>
    </div>
  )
  return inline || !host ? bar : createPortal(bar, host)
}
