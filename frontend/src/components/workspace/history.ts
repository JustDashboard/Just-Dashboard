"use client"

import {
  useCallback,
  useEffect,
  useEffectEvent,
  useRef,
  useMemo,
  useSyncExternalStore,
} from "react"
import { useSearchParams } from "next/navigation"
import { useSessionState } from "@/lib/view-state"

const popListeners = new Set<() => void>()
let visit = 0
const pop = () => {
  visit++
  for (const notify of popListeners) notify()
}
const subscribe = (listener: () => void) => {
  if (!popListeners.size) window.addEventListener("popstate", pop)
  popListeners.add(listener)
  return () => {
    popListeners.delete(listener)
    // Each subscription has its own cleanup; the last observer owns the native listener.
    if (!popListeners.size) window.removeEventListener("popstate", pop)
  }
}
const snapshot = () => visit
const server = () => 0
const hydrated = () => true
const notHydrated = () => false
const noop = () => () => {}
export function useHistoryVisit() {
  return useSyncExternalStore(subscribe, snapshot, server)
}

/** A submitted question is a history entry; typing settles into one question. */
export function useFilterHistory<T extends Record<string, string>>(key: string, defaults: T) {
  const ready = useSyncExternalStore(noop, hydrated, notHydrated)
  const params = useSearchParams()
  const defaultsKey = JSON.stringify(defaults)
  const schema = useMemo(() => JSON.parse(defaultsKey) as T, [defaultsKey])
  const names = useMemo(() => Object.keys(schema), [schema])
  const read = (search: URLSearchParams) =>
    Object.fromEntries(names.map((name) => [name, search.get(name) ?? schema[name]])) as T
  const initial = names.some((name) => params.has(name))
    ? read(new URLSearchParams(params.toString()))
    : undefined
  const [value, setValue] = useSessionState<T>(key, defaults, initial)
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null)
  const changing = useRef(false)
  const arrived = useRef(false)
  const write = useCallback(
    (next: T, push: boolean) => {
      const url = new URL(window.location.href)
      for (const name of names) {
        if (next[name] === schema[name]) url.searchParams.delete(name)
        else url.searchParams.set(name, next[name])
      }
      if (url.href !== window.location.href)
        window.history[push ? "pushState" : "replaceState"]({ jdWorkspace: key }, "", url)
    },
    [key, names, schema],
  )
  const arrive = useEffectEvent(() => {
    if (timer.current) clearTimeout(timer.current)
    changing.current = false
    arrived.current = true
    setValue(read(new URLSearchParams(window.location.search)))
  })
  useEffect(() => {
    window.addEventListener("popstate", arrive)
    return () => {
      window.removeEventListener("popstate", arrive)
      if (timer.current) clearTimeout(timer.current)
    }
  }, [])
  // Restore a bare rail arrival without creating an extra Back step.
  useEffect(() => {
    if (!ready) return
    if (arrived.current) {
      arrived.current = false
      return
    }
    if (!changing.current) write(value, false)
  }, [write, value, ready])
  const change = (next: T | ((previous: T) => T), immediate = false) => {
    changing.current = true
    setValue((previous) => {
      const resolved = typeof next === "function" ? next(previous) : next
      if (timer.current) clearTimeout(timer.current)
      if (immediate) write(resolved, true)
      else timer.current = setTimeout(() => write(resolved, true), 400)
      return resolved
    })
  }
  return [value, change] as const
}

export function pushQuestion() {
  window.history.pushState({ jdWorkspace: "question" }, "", window.location.href)
}

export function useQuestionHistory(onCommit: () => void) {
  const editing = useRef(false)
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null)
  const commit = () => {
    if (timer.current) clearTimeout(timer.current)
    pushQuestion()
    editing.current = false
    onCommit()
  }
  const begin = () => {
    editing.current = true
    if (timer.current) clearTimeout(timer.current)
    timer.current = setTimeout(commit, 400)
  }
  useEffect(
    () => () => {
      if (timer.current) clearTimeout(timer.current)
    },
    [],
  )
  return { editing, commit, begin }
}
