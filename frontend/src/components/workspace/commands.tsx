"use client"

import { createContext, useContext, useState, useCallback } from "react"

export type WorkspaceCommand = {
  id: string
  label: string
  keys?: string
  chord?: string
  run: () => void
  disabled?: boolean
}
type Registration = { name: string; commands: WorkspaceCommand[] }
const Context = createContext<{
  current: Registration | null
  register: (value: Registration) => () => void
}>({ current: null, register: () => () => {} })

export function WorkspaceCommandsProvider({ children }: { children: React.ReactNode }) {
  const [current, setCurrent] = useState<Registration | null>(null)
  const register = useCallback((value: Registration) => {
    setCurrent(value)
    return () => setCurrent((current) => (current === value ? null : current))
  }, [])
  return <Context.Provider value={{ current, register }}>{children}</Context.Provider>
}

export const useWorkspaceCommands = () => useContext(Context)
