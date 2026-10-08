import { expect, test } from "bun:test"
import {
  captureDraftProblem,
  captureFinished,
  captureReading,
  captureRequest,
  newCaptureDraft,
} from "./network-captures"

test("capture drafts preserve literal family, bounded scope and an explicit incident reference", () => {
  const draft = {
    ...newCaptureDraft(),
    name: "Incident packet evidence",
    interface: "lo",
    family: "inet6",
    protocol: "udp",
    source: "fd42:5317::2",
    destination: "fd42:5317::3",
    port: "53117",
    incidentRunId: "a".repeat(32),
  }
  expect(captureRequest(draft)).toEqual({
    interface: "lo",
    family: "inet6",
    protocol: "udp",
    source: "fd42:5317::2",
    destination: "fd42:5317::3",
    port: 53117,
    packets: 1000,
    seconds: 30,
    maxBytes: 1048576,
    snapshotLength: 128,
    incidentRunId: "a".repeat(32),
  })
  for (const change of [
    { source: "localhost" },
    { source: "127.0.0.1" },
    { source: "::ffff:127.0.0.1" },
    { source: "fd42::2%lo" },
    { destination: "bad:address" },
    { protocol: "icmp" },
    { packets: "10001" },
    { packets: "1.5" },
    { seconds: "121" },
    { maxBytes: "2097153" },
    { snapshotLength: "65535" },
    { port: "0" },
    { incidentRunId: "../../private" },
    { name: "bad\nname" },
  ])
    expect(captureDraftProblem({ ...draft, ...change })).toBeDefined()
  expect(draft.source).toBe("fd42:5317::2")
})
test("capture completion and unknown drops are not inferred from provisional progress", () => {
  expect(captureFinished({ status: "cancelling" })).toBe(false)
  expect(captureFinished({ status: "interrupted" })).toBe(true)
  expect(captureReading({ status: "running" })).toEqual({ label: "Capturing", tone: "running" })
  expect(captureReading({ status: "interrupted" })).toEqual({
    label: "Interrupted",
    tone: "warning",
  })
})
