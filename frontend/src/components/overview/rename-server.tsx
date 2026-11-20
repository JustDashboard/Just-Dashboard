"use client"

import { useCallback } from "react"
import { get, put } from "@/lib/api"
import { setHost } from "@/lib/metrics/store"
import { serverName, serverNameProblem } from "@/lib/server-name"
import { notify } from "@/lib/toast"
import type { HostInfo, ServerName, ServerRename } from "@/lib/types"
import { type PromptOptions, usePrompt } from "@/components/prompt-dialog"

/**
 * Renames the server from its own line: one field, opened on the name it
 * has, and the machine's line follows the moment the host has taken the new
 * one.
 *
 * The form opens at once and learns the tailnet name a moment later, since
 * that is asked of Tailscale on the host. A server on a tailnet keeps its name
 * there, because this dashboard's address is spelt with it; the form says so
 * under the field rather than leaving the operator to wonder whether the
 * address they are on is about to stop answering.
 */
export function useRenameServer(host: HostInfo | undefined) {
  const { prompt, dialog } = usePrompt()
  const current = host?.hostname ?? ""

  const open = useCallback(() => {
    const options = (tailnet?: string): PromptOptions => ({
      title: "Rename this server",
      label: "Name",
      initial: current,
      verb: "Rename",
      mono: true,
      refusal: "The server was not renamed",
      validate: serverNameProblem,
      hint: tailnet ? (
        <>
          On your tailnet it stays <span className="font-mono text-foreground">{tailnet}</span>, so
          this dashboard&apos;s address keeps working.
        </>
      ) : (
        "Letters, digits and hyphens."
      ),
      onSubmit: async (value) => {
        const name = serverName(value)
        if (name === current) return
        const result = await put<ServerRename>("/system/hostname/", { hostname: name })
        // The line reads the shared host record, which the socket sends once
        // per connection; asking again is what moves every page's title. A
        // read that fails here is not a rename that failed.
        await get<HostInfo>("/system/host")
          .then(setHost)
          .catch(() => {})
        if (result.warnings?.length)
          notify.warning(`Renamed to ${result.hostname}`, {
            description: result.warnings.join(" "),
            duration: Infinity,
          })
        else notify.success(`Renamed to ${result.hostname}`)
      },
    })
    prompt(options())
    get<ServerName>("/system/hostname/")
      .then((state) => {
        // Only a form still open is told; `prompt` would open a closed one.
        if (state.tailscaleName) prompt((open) => (open ? options(state.tailscaleName) : open))
      })
      .catch(() => {
        // The form works without it: the rename holds the tailnet name itself.
      })
  }, [current, prompt])

  return { open, dialog }
}
