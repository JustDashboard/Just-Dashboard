/**
 * Typed client for the dashboard API.
 *
 * Two things every call here relies on:
 *  - `credentials: "include"` so the HttpOnly session cookie travels; the
 *    token is never readable from JS, which is the point of it.
 *  - `X-JD-CSRF` on every mutation, which makes a forged browser request need
 *    a CORS preflight the server will not authorise.
 *  - the `X-Confirm` header, which irreversible endpoints require. The server
 *    rejects the request without it, so a confirmation cannot be skipped by
 *    calling the API directly, and it sends back the exact phrase it wants in
 *    the error body rather than leaving the client to parse it out of prose.
 */

export const API_BASE = process.env.NEXT_PUBLIC_API_BASE ?? "/api/v1"

/** Headers required on browser mutations, including multipart uploads. */
export function mutationHeaders(): Record<string, string> {
  return { "X-JD-CSRF": "1" }
}

export type ApiErrorBody = {
  error: {
    code: string
    message: string
    phrase?: string
    /**
     * What was being attempted, to what, and why it did not work. All
     * optional: a route that says nothing beyond a message still works, and a
     * component that reads only `message` is unaffected.
     */
    resource?: string
    operation?: string
    reason?: string
    /** The subsystem's own error, for the expandable details. Never the headline. */
    raw?: string
    retryable?: boolean
    /**
     * The field a validation refusal is about, as a dotted path into the
     * request body (`runtime.internalPort`, `checks.2.config.path`), so a
     * form can attach the message to the control that caused it.
     */
    field?: string
  }
}

/**
 * The sentence to show a person when a request fails.
 *
 * `String(err)` on an Error yields "ApiError: path is outside the permitted
 * roots" — the class name is a detail of how this client is written, and it
 * appeared verbatim in toasts and inline error text. Anything that is not an
 * Error is stringified, because a rejected promise can carry anything.
 */
export function errorMessage(err: unknown): string {
  return err instanceof Error ? err.message : String(err)
}

export class ApiError extends Error {
  code: string
  status: number
  /**
   * The phrase the server wants echoed back in X-Confirm.
   *
   * It arrives as its own field. It used to be recovered by matching the first
   * quoted run in the human-readable message, which meant a phrase containing
   * a double quote — a file named `my"file`, say — was truncated to `my`: the
   * dialog asked for the wrong text and the server rejected every attempt, so
   * the file could not be deleted from the UI at all.
   */
  confirmPhrase?: string

  /**
   * The structured half of the error.
   *
   * "Something went wrong" is what a client shows when the server sent it a
   * sentence and nothing else. These are what let it show the sentence a
   * person would have written: which resource, which operation, the likely
   * reason, and — behind a disclosure rather than in the headline — exactly
   * what the subsystem underneath said.
   */
  resource?: string
  operation?: string
  reason?: string
  raw?: string
  retryable?: boolean
  field?: string

  constructor(
    status: number,
    code: string,
    message: string,
    phrase?: string,
    detail?: Partial<
      Pick<ApiError, "resource" | "operation" | "reason" | "raw" | "retryable" | "field">
    >,
  ) {
    super(message)
    this.name = "ApiError"
    this.status = status
    this.code = code
    this.confirmPhrase = phrase
    Object.assign(this, detail)
  }

  get needsConfirmation() {
    return this.code === "confirmation_required" || this.code === "confirmation_mismatch"
  }

  get isAuthProblem() {
    return (
      this.status === 401 ||
      this.code === "account_disabled" ||
      this.code === "password_change_required"
    )
  }

  get needsTotp() {
    return this.code === "totp_required" || this.code === "totp_enrollment_required"
  }
}

/**
 * The array index a validation refusal names, when `ApiError.field` points
 * into `arrayName` — `checks[2]` or `checks.2`, with or without a deeper
 * suffix — so a settings form can mark the refused row rather than toast.
 */
export function refusedIndex(field: string | undefined, arrayName: string) {
  if (!field) return undefined
  const match = new RegExp(`^${arrayName}(?:\\[(\\d+)\\]|\\.(\\d+))`).exec(field)
  if (!match) return undefined
  return Number(match[1] ?? match[2])
}

type RequestOptions = {
  method?: string
  body?: unknown
  /** Echoed in X-Confirm to satisfy a typed-confirmation endpoint. */
  confirm?: string
  signal?: AbortSignal
  query?: Record<string, string | number | boolean | undefined | null>
}

function buildUrl(path: string, query?: RequestOptions["query"]) {
  const url = `${API_BASE}${path}`
  if (!query) return url
  const params = new URLSearchParams()
  // undefined and null mean "no opinion" and are dropped; "" is a value the
  // caller chose and is sent. Dropping it too meant a filter that means
  // "empty" could never be expressed, and the omission looked identical to
  // never having set the parameter.
  for (const [key, value] of Object.entries(query)) {
    if (value !== undefined && value !== null) {
      params.set(key, String(value))
    }
  }
  const qs = params.toString()
  return qs ? `${url}?${qs}` : url
}

export async function api<T>(path: string, options: RequestOptions = {}): Promise<T> {
  const headers: Record<string, string> = {}
  const method = options.method ?? "GET"
  if (!["GET", "HEAD", "OPTIONS", "TRACE"].includes(method.toUpperCase())) {
    Object.assign(headers, mutationHeaders())
  }
  if (options.body !== undefined) headers["Content-Type"] = "application/json"
  if (options.confirm !== undefined) {
    headers["X-Confirm"] = encodeURIComponent(options.confirm)
    headers["X-Confirm-Encoding"] = "uri"
  }

  const res = await fetch(buildUrl(path, options.query), {
    method,
    headers,
    credentials: "include",
    signal: options.signal,
    body: options.body === undefined ? undefined : JSON.stringify(options.body),
  })

  return readResponse<T>(res)
}

async function readResponse<T>(res: Response): Promise<T> {
  if (res.status === 204) return undefined as T

  const text = await res.text()
  let parsed: unknown = undefined
  if (text) {
    try {
      parsed = JSON.parse(text)
    } catch {
      if (!res.ok) throw new ApiError(res.status, "unknown", text.slice(0, 300))
    }
  }

  if (!res.ok) {
    const body = parsed as ApiErrorBody | undefined
    if (body?.error?.code === "password_change_required" && typeof window !== "undefined") {
      window.dispatchEvent(new Event("jd:password-change-required"))
    }
    throw new ApiError(
      res.status,
      body?.error?.code ?? "unknown",
      body?.error?.message ?? res.statusText,
      body?.error?.phrase,
      {
        resource: body?.error?.resource,
        operation: body?.error?.operation,
        reason: body?.error?.reason,
        raw: body?.error?.raw,
        retryable: body?.error?.retryable,
        field: body?.error?.field,
      },
    )
  }
  return parsed as T
}

/** Multipart POST through the same authenticated fetch/error path as JSON. */
export async function postForm<T>(
  path: string,
  body: FormData,
  opts: Pick<RequestOptions, "query" | "signal"> = {},
): Promise<T> {
  const res = await fetch(buildUrl(path, opts.query), {
    method: "POST",
    headers: mutationHeaders(),
    credentials: "include",
    signal: opts.signal,
    body,
  })
  return readResponse<T>(res)
}

export const get = <T>(path: string, query?: RequestOptions["query"], signal?: AbortSignal) =>
  api<T>(path, { query, signal })

export const post = <T>(
  path: string,
  body?: unknown,
  opts: Omit<RequestOptions, "method" | "body"> = {},
) => api<T>(path, { ...opts, method: "POST", body })

export const put = <T>(
  path: string,
  body?: unknown,
  opts: Omit<RequestOptions, "method" | "body"> = {},
) => api<T>(path, { ...opts, method: "PUT", body })

export const patch = <T>(
  path: string,
  body?: unknown,
  opts: Omit<RequestOptions, "method" | "body"> = {},
) => api<T>(path, { ...opts, method: "PATCH", body })

export const del = <T>(path: string, opts: Omit<RequestOptions, "method"> = {}) =>
  api<T>(path, { ...opts, method: "DELETE" })

/** Absolute URL for a download or archive link the browser follows directly. */
export function downloadUrl(path: string, query?: RequestOptions["query"]) {
  return buildUrl(path, query)
}

/**
 * WebSocket URL for a streaming endpoint.
 *
 * Next's rewrites proxy HTTP but not upgrade requests, so in development the
 * socket has to address the Go backend directly via NEXT_PUBLIC_WS_BASE.
 * Cookies ignore port when deciding same-site, so the session still travels.
 * In production both sit behind one reverse proxy and this is same-origin.
 */
export function wsUrl(path: string, query?: RequestOptions["query"]) {
  const explicit = process.env.NEXT_PUBLIC_WS_BASE
  const suffix = buildUrl(path, query)
  if (explicit) return `${explicit.replace(/^http/, "ws").replace(/\/$/, "")}${suffix}`
  const proto = window.location.protocol === "https:" ? "wss:" : "ws:"
  return `${proto}//${window.location.host}${suffix}`
}
