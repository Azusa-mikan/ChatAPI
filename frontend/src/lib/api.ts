type RequestError = Error & {
  responseBody?: unknown
}

function resolveRequestUrl(url: string): string {
  if (/^(?:[a-z]+:)?\/\//i.test(url)) {
    return url
  }
  const baseUrl = import.meta.env.BASE_URL || '/'
  const normalizedBase = baseUrl.endsWith('/') ? baseUrl : `${baseUrl}/`
  const normalizedPath = url.replace(/^\/+/, '')
  return `${normalizedBase}${normalizedPath}`
}

// extractErrorMessage mirrors the backend's error envelopes while staying
// tolerant of the plain-text bodies produced by http.Error. It must never
// surface an empty message, so it falls back to the raw body text and finally
// to a generic message.
function extractErrorMessage(body: unknown, rawText: string): string {
  if (body && typeof body === 'object') {
    const record = body as Record<string, unknown>
    const nested = record.error
    if (nested && typeof nested === 'object') {
      const message = (nested as Record<string, unknown>).message
      if (typeof message === 'string' && message) return message
    } else if (typeof nested === 'string' && nested) {
      return nested
    }
    if (typeof record.message === 'string' && record.message) return record.message
  }
  const text = rawText.trim()
  return text || '请求失败'
}

export async function requestJson<T>(url: string, init?: RequestInit): Promise<T> {
  let response: Response
  try {
    response = await fetch(resolveRequestUrl(url), {
      credentials: 'include',
      headers: {
        'Content-Type': 'application/json',
        ...(init?.headers ?? {}),
      },
      ...init,
    })
  } catch {
    throw new Error('无法连接到后端')
  }

  // Read the raw body once. Error responses may be JSON envelopes or plain
  // text (the HTTP layer uses http.Error), and response.json() consumes the
  // stream, so we parse it ourselves to keep the text fallback available.
  const raw = await response.text().catch(() => '')
  let body: unknown = null
  if (raw) {
    try {
      body = JSON.parse(raw)
    } catch {
      body = null
    }
  }

  if (!response.ok) {
    const error: RequestError = new Error(extractErrorMessage(body, raw))
    if (body && typeof body === 'object') {
      error.responseBody = body
    }
    throw error
  }
  return body as T
}

export async function requestFormJson<T>(url: string, form: FormData): Promise<T> {
	let response: Response
	try {
		response = await fetch(resolveRequestUrl(url), {
			method: 'POST',
			credentials: 'include',
			body: form,
		})
	} catch {
		throw new Error('无法连接到后端')
	}
	const raw = await response.text().catch(() => '')
	let body: unknown = null
	if (raw) {
		try {
			body = JSON.parse(raw)
		} catch {
			body = null
		}
	}
	if (!response.ok) {
		throw new Error(extractErrorMessage(body, raw))
	}
	return body as T
}

export function resolveWebSocketUrl(url: string): string {
  const resolved = resolveRequestUrl(url)
  const target = new URL(resolved, window.location.origin)
  target.protocol = target.protocol === 'https:' ? 'wss:' : 'ws:'
  return target.toString()
}

export function resolveEventSourceUrl(url: string): string {
  return new URL(resolveRequestUrl(url), window.location.origin).toString()
}
