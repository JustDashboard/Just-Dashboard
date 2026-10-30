// The request builder against a fetch that records what it was asked: what
// each kind of call puts on the wire (method, path with its parameters
// escaped, query, headers, body) and how an answer and a refusal come back.
// Written by hand, against the generated functions; regenerating sdk/ leaves
// this file alone. Run with: cd sdk/typescript && bun test
import { describe, expect, test } from "bun:test"

import {
  ApiError,
  Client,
  deployGet,
  deployRuns,
  deploymentRequestExport,
  deploymentRunCreate,
  deploymentRunStreamUrl,
  deploymentTriggerDelete,
  deploymentVariablePut,
} from "./client"
import { DeployOperation, RunState } from "./types"

interface Sent {
  url: string
  method: string
  headers: Record<string, string>
  body?: unknown
}

function recorder(status: number, answer: string, contentType = "application/json") {
  const sent: Sent[] = []
  const fetcher = (async (input: string | URL | Request, init?: RequestInit) => {
    sent.push({
      url: String(input),
      method: init?.method ?? "GET",
      headers: (init?.headers ?? {}) as Record<string, string>,
      body: typeof init?.body === "string" ? JSON.parse(init.body) : undefined,
    })
    return new Response(status === 204 ? null : answer, {
      status,
      headers: { "Content-Type": contentType },
    })
  }) as typeof fetch
  return { sent, fetcher }
}

describe("the request builder", () => {
  test("a write sends its path, body and key", async () => {
    const { sent, fetcher } = recorder(202, `{"id":91,"runNumber":4,"state":"queued"}`)
    const client = new Client({
      baseUrl: "https://dash.example.com",
      key: "vpsd_example",
      fetch: fetcher,
    })
    const run = await deploymentRunCreate(
      client,
      12,
      34,
      { operation: DeployOperation.Deploy, ref: "main" },
      { headers: { "Idempotency-Key": "ci-1" } },
    )
    expect(sent).toHaveLength(1)
    expect(sent[0]!.method).toBe("POST")
    expect(sent[0]!.url).toBe("https://dash.example.com/api/v1/deploy/12/environments/34/runs")
    expect(sent[0]!.headers.Authorization).toBe("Bearer vpsd_example")
    expect(sent[0]!.headers["Content-Type"]).toBe("application/json")
    expect(sent[0]!.headers.Accept).toBe("application/json")
    expect(sent[0]!.headers["Idempotency-Key"]).toBe("ci-1")
    expect(sent[0]!.body).toEqual({ operation: "deploy", ref: "main" })
    expect(run.state).toBe(RunState.Queued)
    expect(run.runNumber).toBe(4)
  })

  test("a path parameter is escaped, and the base is taken with or without /api/v1", async () => {
    for (const baseUrl of [
      "https://dash.example.com",
      "https://dash.example.com/",
      "https://dash.example.com/api/v1",
      "https://dash.example.com/api/v1/",
    ]) {
      const { sent, fetcher } = recorder(200, `{"variables":[]}`)
      await deploymentVariablePut(
        new Client({ baseUrl, key: "k", fetch: fetcher }),
        1,
        2,
        "A B/C",
        {
          value: "s3cret",
        },
      )
      expect(sent[0]!.method).toBe("PUT")
      expect(sent[0]!.url).toBe(
        "https://dash.example.com/api/v1/deploy/1/environments/2/variables/A%20B%2FC",
      )
      expect(sent[0]!.body).toEqual({ value: "s3cret" })
    }
  })

  test("a query leaves out what is not set", async () => {
    const { sent, fetcher } = recorder(200, `{"runs":[],"running":false}`)
    const client = new Client({ baseUrl: "http://127.0.0.1:8080", fetch: fetcher })
    await deployRuns(client, 7, { limit: 5, view: "engine", state: undefined, before: 0 })
    expect(sent[0]!.url).toBe(
      "http://127.0.0.1:8080/api/v1/deploy/7/runs?limit=5&view=engine&before=0",
    )
    expect(sent[0]!.headers["Content-Type"]).toBeUndefined()
    expect(sent[0]!.headers.Authorization).toBeUndefined()
    expect(sent[0]!.body).toBeUndefined()
    await deployRuns(client, 7)
    expect(sent[1]!.url).toBe("http://127.0.0.1:8080/api/v1/deploy/7/runs")
  })

  test("no content and a text answer", async () => {
    const empty = recorder(204, "")
    const none = await deploymentTriggerDelete(
      new Client({ baseUrl: "https://d", fetch: empty.fetcher }),
      3,
      4,
      5,
    )
    expect(none).toBeUndefined()
    expect(empty.sent[0]!.method).toBe("DELETE")
    expect(empty.sent[0]!.url).toBe("https://d/api/v1/deploy/3/environments/4/triggers/5")

    const csv = recorder(200, "time,status\n1,200\n", "text/csv")
    const text = await deploymentRequestExport(
      new Client({ baseUrl: "https://d", fetch: csv.fetcher }),
      3,
      { limit: 10 },
    )
    expect(text).toBe("time,status\n1,200\n")
    expect(csv.sent[0]!.headers.Accept).toBe("text/csv")
  })

  test("a refusal is an ApiError carrying the envelope", async () => {
    const { fetcher } = recorder(
      403,
      `{"error":{"code":"key_scope","message":"this deploy key was not given this deployment","field":"deployment"}}`,
    )
    const error = await deployGet(new Client({ baseUrl: "https://d", fetch: fetcher }), 9).catch(
      (e) => e,
    )
    expect(error).toBeInstanceOf(ApiError)
    expect(error.status).toBe(403)
    expect(error.code).toBe("key_scope")
    expect(error.field).toBe("deployment")
    expect(error.message).toBe("this deploy key was not given this deployment")

    const proxy = recorder(502, "<h1>Bad gateway</h1>", "text/html")
    const page = await deployGet(
      new Client({ baseUrl: "https://d", fetch: proxy.fetcher }),
      9,
    ).catch((e) => e)
    expect(page).toBeInstanceOf(ApiError)
    expect(page.status).toBe(502)
    expect(page.code).toBeUndefined()
    expect(page.message).toBe("<h1>Bad gateway</h1>")
  })

  test("a stream is given as its address", () => {
    expect(
      deploymentRunStreamUrl(new Client({ baseUrl: "https://dash.example.com/" }), 12, 99),
    ).toBe("wss://dash.example.com/api/v1/deploy/12/runs/99/stream")
    expect(deploymentRunStreamUrl(new Client({ baseUrl: "http://127.0.0.1:8080" }), 1, 2)).toBe(
      "ws://127.0.0.1:8080/api/v1/deploy/1/runs/2/stream",
    )
  })
})
