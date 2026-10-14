"use client"

import { useCallback, useRef, useState } from "react"
import type { OperationReading } from "@/components/operation-progress"

export type Operation = OperationReading & { id: number; startedAt: number }

/** Keep simultaneous file operations visible until each request settles. */
export function useOperations() {
  const [operations, setOperations] = useState<Operation[]>([])
  const next = useRef(0)
  const run = useCallback(
    async <T>(
      label: string,
      task: (report: (reading: Partial<OperationReading>) => void) => Promise<T>,
    ): Promise<T> => {
      const id = ++next.current
      setOperations((held) => [...held, { id, label, startedAt: Date.now() }])
      const report = (reading: Partial<OperationReading>) =>
        setOperations((held) =>
          held.map((operation) => (operation.id === id ? { ...operation, ...reading } : operation)),
        )
      try {
        return await task(report)
      } finally {
        setOperations((held) => held.filter((operation) => operation.id !== id))
      }
    },
    [],
  )
  return { operations, run }
}
