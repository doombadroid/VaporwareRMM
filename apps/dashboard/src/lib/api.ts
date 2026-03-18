import { API_BASE_URL } from '@/lib/config'

const fetcher = async (url: string, options?: RequestInit) => {
  const response = await fetch(`${API_BASE_URL}${url}`, {
    ...options,
    headers: {
      'Content-Type': 'application/json',
      ...(options?.headers || {}),
    },
  })

  if (!response.ok) {
    throw new Error(`API error: ${response.statusText}`)
  }

  return response.json()
}

export const api = {
  get: (url: string, options?: RequestInit) => fetcher(url, { method: 'GET', ...options }),
  post: (url: string, body?: any, options?: RequestInit) =>
    fetcher(url, { method: 'POST', body: JSON.stringify(body), ...options }),
  put: (url: string, body?: any, options?: RequestInit) =>
    fetcher(url, { method: 'PUT', body: JSON.stringify(body), ...options }),
  del: (url: string, options?: RequestInit) => fetcher(url, { method: 'DELETE', ...options }),
}