import { STORAGE_LIMIT } from "@/components/database/ops/performance-api"

/**
 * What a list of Performance and the page of one of its rows hold in common:
 * where the list's filter, order and fold are kept, and the name of the one
 * request both make.
 *
 * A statement, a table and a session each open as a page with their list
 * beside them as a rail (`performance-detail`). The rail is the list "as it
 * was left" only if both read the same memory, so the keys are here, in a
 * module that is neither's page: a route's bundle is everything its page
 * reaches (`page-modules.test.js`), and the list importing a key from the
 * page would ship the page with it.
 *
 * A request's name is its `usePoll` key: the list and the page are then one
 * request and one answer, so a row opens on figures already in hand.
 */

export const statementsFilterKey = (id: number) => `databases.${id}.performance.statements.filter`

export const tablesFilterKey = (id: number) => `databases.${id}.performance.tables.filter`
export const tablesOrderKey = (id: number) => `databases.${id}.performance.tables.order`
export const tableStatsKey = (id: number) => `/databases/${id}/tablestats?limit=${STORAGE_LIMIT}`

export const sessionsFilterKey = (id: number) => `databases.${id}.performance.sessions.filter`
export const sessionsIdleKey = (id: number) => `databases.${id}.performance.idle`
export const activityKey = (id: number) => `/databases/${id}/activity`

/** What a table's bytes are, in the colours of the strip that draws them. */
export const TABLE_PARTS = [
  { key: "tableBytes", label: "Rows", color: "var(--chart-1)" },
  { key: "indexBytes", label: "Indexes", color: "var(--chart-2)" },
  { key: "toastBytes", label: "Large values", color: "var(--chart-4)" },
] as const
