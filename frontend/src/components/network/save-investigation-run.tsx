"use client"

import { SaveRunSnapshot } from "./tools/save-run"
import type { PathRequest } from "@/lib/network-investigator-types"

export function SaveInvestigationRun({
  request,
  disabled,
}: {
  request: PathRequest
  disabled: boolean
}) {
  return (
    <SaveRunSnapshot
      snapshot={{ kind: "investigation", request }}
      label="Connection path"
      disabled={disabled}
    />
  )
}
