"use client"

import { useState } from "react"
import { plural, truncateMiddle } from "@/lib/format"
import { Button } from "@/components/ui/button"
import { Modal } from "@/components/modal"

export type ConflictPolicy = "replace" | "keep" | "skip"

type Request = {
  names: string[]
  dir: string
  /** What is about to happen to them: "uploaded", "moved", "copied". */
  verb: string
  resolve: (policy: ConflictPolicy | null) => void
}

/**
 * The question every file manager asks before it clobbers something.
 *
 * An upload, a move and a paste used to replace whatever held the name
 * already, silently — the server's rename and the browser's upload both
 * defaulted to it. The server now refuses an occupied name unless told to
 * replace it, and this is where it gets told: once per operation, with the
 * three answers a desktop offers, and never per file, because forty prompts
 * for forty photographs is how people learn to click Replace without reading.
 */
export function useConflicts() {
  const [request, setRequest] = useState<Request | null>(null)

  const ask = (names: string[], dir: string, verb: string) =>
    new Promise<ConflictPolicy | null>((resolve) => setRequest({ names, dir, verb, resolve }))

  const answer = (policy: ConflictPolicy | null) => {
    request?.resolve(policy)
    setRequest(null)
  }

  const dialog = request ? (
    <Modal
      open
      onOpenChange={(open) => !open && answer(null)}
      size="sm"
      title={`${plural(request.names.length, "item")} already ${
        request.names.length === 1 ? "exists" : "exist"
      } here`}
      description={`Choose what happens to the ${request.verb} items that share a name with something in ${request.dir}.`}
      footer={
        <>
          <Button variant="ghost" onClick={() => answer(null)}>
            Cancel
          </Button>
          <Button variant="outline" onClick={() => answer("skip")}>
            Skip {request.names.length === 1 ? "it" : "them"}
          </Button>
          <Button variant="outline" onClick={() => answer("keep")}>
            Keep both
          </Button>
          <Button variant="destructive" onClick={() => answer("replace")}>
            Replace
          </Button>
        </>
      }
    >
      <div className="space-y-3 text-body">
        <ul className="max-h-40 space-y-0.5 overflow-auto font-mono text-xs">
          {request.names.slice(0, 12).map((name) => (
            <li key={name} className="truncate" title={name}>
              {truncateMiddle(name, 60)}
            </li>
          ))}
          {request.names.length > 12 && (
            <li className="text-muted-foreground">and {request.names.length - 12} more</li>
          )}
        </ul>
        <p className="text-xs leading-relaxed text-muted-foreground">
          <b className="font-medium text-foreground">Replace</b> overwrites what is there.{" "}
          <b className="font-medium text-foreground">Keep both</b> gives the new one a numbered
          name. <b className="font-medium text-foreground">Skip</b> leaves those out and{" "}
          {request.verb === "uploaded" ? "uploads" : "handles"} the rest.
        </p>
      </div>
    </Modal>
  ) : null

  return { ask, dialog }
}
