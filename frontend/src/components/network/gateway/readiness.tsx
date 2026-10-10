"use client"

import Link from "next/link"
import { useState } from "react"
import { post, put } from "@/lib/api"
import { relativeTime } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { ForwardCheck, GatewayForward } from "@/lib/types"
import { Warning } from "@/components/icons"
import { Detail, DetailList } from "@/components/page"
import { Notice } from "@/components/state"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import {
  checkTone,
  checkWord,
  externalWord,
  forwardRequest,
  forwardTitle,
  readinessWord,
} from "@/components/network/gateway/reading"
import { useAuth } from "@/hooks/use-auth"

const ADMISSION_WORD: Record<string, string> = {
  present: "owned rules present",
  absent: "an owned rule is missing",
  unreadable: "could not be read",
  unsupported: "no admission chain here",
  not_required: "not needed",
}

/**
 * An existing forward's evidence, from what is installed to what was measured.
 *
 * Installed is the rules, the family's forwarding switch and the owned
 * admission rules; none of it says a visitor gets through. Two measurements
 * can: a check of the target from this server (something answers there), and
 * an external source's connection to the public port after the forward's last
 * change, which is the only thing that moves reachability off "unmeasured".
 * A drifted auto source translation — the host's networks changed after it
 * was decided — is said here with the save that re-decides it.
 */
export function ForwardReadiness({
  forward,
  writable,
  onChanged,
}: {
  forward: GatewayForward
  writable: boolean
  onChanged: () => void
}) {
  const { can } = useAuth()
  const [check, setCheck] = useState<ForwardCheck | undefined>(forward.check)
  const [checking, setChecking] = useState(false)
  const [deciding, setDeciding] = useState(false)
  const r = forward.readiness
  const word = readinessWord(r)
  const runCheck = async () => {
    setChecking(true)
    try {
      setCheck(await post<ForwardCheck>("/network/gateway/verify", { forwardId: forward.id }))
    } catch (err) {
      notify.error("The target was not checked", err)
    } finally {
      setChecking(false)
    }
  }
  const redecide = async () => {
    setDeciding(true)
    try {
      await put(`/network/gateway/forwards/${forward.id}`, forwardRequest(forward))
      notify.success(`${forwardTitle(forward)} re-decided`)
      onChanged()
    } catch (err) {
      notify.error("The decision was not saved", err)
    } finally {
      setDeciding(false)
    }
  }
  return (
    <section aria-label="Readiness and evidence" className="min-w-0 space-y-3">
      <p className="flex items-center gap-2 text-body font-medium">
        In force
        <Tag tone={word.tone}>{word.label}</Tag>
      </p>
      {r && (
        <DetailList>
          <Detail label="Rules">
            {r.policy === "disabled"
              ? "none while switched off"
              : `${r.rules} of ${r.expected} in the loaded table`}
          </Detail>
          <Detail label="Forwarding">{r.forwarding ? "on for its family" : "off"}</Detail>
          <Detail label="Admission">{ADMISSION_WORD[r.admission] ?? r.admission}</Detail>
          <Detail label="Reached">
            {r.reachability === "verified"
              ? "from an external source, after the last change"
              : r.reachability === "failed"
                ? "refused from an external source"
                : "not measured since the last change"}
          </Detail>
        </DetailList>
      )}
      {r?.reason && <p className="text-hint text-muted-foreground">{r.reason}</p>}

      {forward.decision?.drift && (
        <Notice
          tone="warning"
          icon={Warning}
          title="The host's networks changed since auto decided"
        >
          <p>
            {forward.decision.reason} The saved choice{" "}
            {forward.decision.stored ? "masquerades" : "keeps the visitor's address"}; saving the
            forward decides again.
          </p>
          <Button
            type="button"
            size="sm"
            variant="outline"
            className="mt-2"
            pending={deciding}
            disabled={!writable || !can("system.admin") || deciding}
            onClick={() => void redecide()}
          >
            Decide again
          </Button>
        </Notice>
      )}

      <div className="space-y-1.5">
        <div className="flex flex-wrap items-center gap-2">
          <Button
            type="button"
            size="sm"
            variant="outline"
            pending={checking}
            disabled={!can("system.admin") || checking || !forward.enabled}
            onClick={() => void runCheck()}
          >
            Check the target
          </Button>
          {check && (
            <span className="text-hint text-muted-foreground">
              <Tag tone={checkTone(check)}>{check.status.replace("_", " ")}</Tag> {checkWord(check)}{" "}
              · {relativeTime(check.checkedAt)}
              {!check.current && " · before the last change"}
            </span>
          )}
        </div>
        {check && <p className="text-hint text-muted-foreground">{check.basis}</p>}
      </div>

      <div className="space-y-1 text-hint text-muted-foreground">
        {forward.external ? (
          <>
            <p>
              <Tag tone={forward.external.status === "connected" ? "success" : "warning"}>
                {forward.external.status}
              </Tag>{" "}
              {externalWord(forward.external)} · {relativeTime(forward.external.checkedAt)}
              {forward.external.location ? ` · ${forward.external.location}` : ""}
              {!forward.external.current && " · before the last change, so it does not count"}
            </p>
            <p>{forward.external.basis}</p>
          </>
        ) : (
          forward.protocol !== "udp" && (
            <p>
              No enrolled external source has measured port {forward.ports} since the last change.{" "}
              <Link
                href="/network/external"
                className="rounded-sm underline underline-offset-2 focus-ring hover:text-foreground"
              >
                Run one from External checks
              </Link>
              .
            </p>
          )
        )}
      </div>
    </section>
  )
}
