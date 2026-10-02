import { plural } from "@/lib/format"
import type { ConfirmRequest } from "@/components/confirm-dialog"
import { FormFact } from "@/components/form"
import type { Engine } from "@/components/database/engine"
import { EngineMark } from "@/components/database/kit"
import { POWER } from "@/components/database/home/power"
import type { DbConnectionSummary } from "@/components/database/shell/types"

/**
 * What a stop or a restart is confirmed with: the server drawn as itself —
 * its engine, its name, the container or the unit the action lands on — and
 * a sentence saying what happens to what is connected to it (§7). The route
 * takes no typed phrase; this is the guard against stopping the wrong one.
 */
export function powerConfirmation(
  action: "stop" | "restart",
  engine: Engine,
  summary: DbConnectionSummary,
): Omit<ConfirmRequest, "action"> {
  const container = summary.container
  const unit = summary.unit
  const target = container?.name ?? unit?.name
  const verb = POWER[action].label
  return {
    title: `${verb} ${summary.name}`,
    confirmLabel: verb,
    subject: {
      mark: <EngineMark engine={engine} size="sm" />,
      name: summary.name,
      facts: (
        <>
          <FormFact label="Engine">
            {summary.versionNumber ? `${engine.label} ${summary.versionNumber}` : engine.label}
          </FormFact>
          {container && (
            <FormFact label="Container" mono>
              {container.name}
            </FormFact>
          )}
          {!container && unit && (
            <FormFact label="Unit" mono>
              {unit.name}
            </FormFact>
          )}
        </>
      ),
    },
    description: (
      <>
        <p>
          {action === "stop"
            ? `Every session on it is disconnected and nothing can reach it until it is started again.`
            : `Every session on it is disconnected while it goes down and comes back.`}{" "}
          {container
            ? `The container${target ? ` ${target}` : ""} is given 90 seconds to shut down cleanly before Docker ends it.`
            : `systemd stops the unit${target ? ` ${target}` : ""} and waits for it.`}
        </p>
        {summary.consumers > 0 && (
          <p>
            {plural(summary.consumers, "deployment environment")}{" "}
            {summary.consumers === 1 ? "is" : "are"} bound to this database and will lose it
            {action === "stop" ? "." : " for as long as that takes."}
          </p>
        )}
      </>
    ),
  }
}
