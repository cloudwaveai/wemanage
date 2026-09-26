// Same-origin API wrapper with CSRF handling.
//
// The CSRF token comes from GET /api/me (or the dev-login response) and must be
// sent as the X-CSRF-Token header on every mutating request. The session cookie
// travels automatically because every request uses credentials: "same-origin".

let csrfToken = "";

export function setCsrfToken(token: string | undefined | null) {
  if (token) csrfToken = token;
}

export function getCsrfToken() {
  return csrfToken;
}

export class ApiError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.status = status;
    this.name = "ApiError";
  }
}

async function request<T>(
  method: string,
  path: string,
  body?: unknown
): Promise<T> {
  const headers: Record<string, string> = {};
  const mutating = method !== "GET" && method !== "HEAD";
  if (mutating) {
    headers["Content-Type"] = "application/json";
    if (csrfToken) headers["X-CSRF-Token"] = csrfToken;
  }

  const res = await fetch(path, {
    method,
    credentials: "same-origin",
    headers,
    body: body === undefined ? undefined : JSON.stringify(body),
  });

  // 204 / empty body
  const text = await res.text();
  let data: any = undefined;
  if (text) {
    try {
      data = JSON.parse(text);
    } catch {
      data = text;
    }
  }

  if (!res.ok) {
    const msg =
      (data && typeof data === "object" && (data.error as string)) ||
      (typeof data === "string" && data) ||
      `请求失败 (${res.status})`;
    throw new ApiError(res.status, msg);
  }
  return data as T;
}

export const api = {
  get: <T>(path: string) => request<T>("GET", path),
  post: <T>(path: string, body?: unknown) => request<T>("POST", path, body),
  put: <T>(path: string, body?: unknown) => request<T>("PUT", path, body),
  patch: <T>(path: string, body?: unknown) => request<T>("PATCH", path, body),
  del: <T>(path: string, body?: unknown) => request<T>("DELETE", path, body),
};
