import type { MongoOperation } from "@/components/database/mongo/types"

/** One line for an operation that says what it is on: `find app.orders`, or its description. */
export function operationTitle(operation: MongoOperation): string {
  if (!operation.op || operation.op === "none") return operation.desc || "idle"
  return operation.ns ? `${operation.op} ${operation.ns}` : operation.op
}

/**
 * Whether an operation is a driver's heartbeat: the `hello` every connected
 * client keeps open against the server to hear of a change in its topology.
 * One is always "running" for each client, for up to ten seconds at a time,
 * and none of them is work anybody asked for — a list that led with a dozen
 * of them hid the one query that mattered.
 */
export function isHeartbeat(operation: MongoOperation): boolean {
  return (
    operation.op === "command" && /^\{\s*"(?:hello|isMaster|ismaster)"\s*:/.test(operation.command)
  )
}

/** The operations a reader came to see, and how many heartbeats were left out of them. */
export function withoutHeartbeats(operations: readonly MongoOperation[]) {
  const shown = operations.filter((operation) => !isHeartbeat(operation))
  return { shown, heartbeats: operations.length - shown.length }
}
