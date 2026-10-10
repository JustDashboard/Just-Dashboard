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

/** Whether an operation is the server's own background work: nobody's connection asked for it. */
export function isInternal(operation: MongoOperation): boolean {
  return !operation.client
}

/**
 * Whether an operation is one a reader may stop from here: a client's own
 * work, in progress. A heartbeat is neither work nor worth stopping — the
 * driver it belongs to drops its connection and opens another — and the
 * server's own tasks are not a client's to interrupt.
 */
export function stoppable(operation: MongoOperation): boolean {
  return (
    Boolean(operation.opId) && operation.active && !isInternal(operation) && !isHeartbeat(operation)
  )
}

/**
 * The operations a reader came to see — the work clients asked for — with
 * how many heartbeats and how many of the server's own tasks were left out.
 */
export function clientWork(operations: readonly MongoOperation[]) {
  const heartbeats = operations.filter(isHeartbeat).length
  const internal = operations.filter(isInternal).length
  return {
    shown: operations.filter((operation) => !isHeartbeat(operation) && !isInternal(operation)),
    heartbeats,
    internal,
  }
}

/** "the heartbeats of 2 connected clients and 3 of the server's own tasks": what a list left out, or `""`. */
export function leftOutWords(heartbeats: number, internal: number): string {
  return [
    heartbeats === 1
      ? "the heartbeat of one connected client"
      : heartbeats > 1
        ? `the heartbeats of ${heartbeats} connected clients`
        : "",
    internal === 1
      ? "one task of the server's own"
      : internal > 1
        ? `${internal} tasks of the server's own`
        : "",
  ]
    .filter(Boolean)
    .join(" and ")
}
