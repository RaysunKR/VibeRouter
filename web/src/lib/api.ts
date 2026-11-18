// ---- 类型定义 ----

export interface BalanceConf {
  type: string
  url?: string
}

export interface Provider {
  id: string
  name: string
  protocol: 'chat_completions' | 'responses' | 'anthropic'
  base_url: string
  api_key?: string
  preset_id?: string
  balance?: BalanceConf
}

export interface ProviderSyncResult {
  provider: Provider
  models_updated: number
  models_skipped?: string[]
}

export interface Model {
  name: string
  provider: string
  upstream_model: string
  api_key?: string
  base_url?: string
  protocol?: string
  multimodal: boolean
  long_context: boolean
  max_context_tokens: number
}

export interface GroupMember {
  model: string
  priority: number
}

export interface Group {
  name: string
  mode: 'balance' | 'smart'
  strategy?: string
  members?: GroupMember[]
  dispatcher_model?: string
  dispatcher_prompt?: string
  advanced?: GroupMember[]
  basic?: GroupMember[]
}

export interface ApiKey {
  id: string
  key: string
  name: string
  enabled: boolean
  created_at: string
}

export type WebToolProviderID = 'bocha' | 'kimi' | 'deepseek' | 'glm'

export interface WebToolProvider {
  id: WebToolProviderID
  enabled: boolean
  api_key?: string
  base_url: string
}

export interface WebTools {
  enabled: boolean
  mcp_enabled: boolean
  search_provider: WebToolProviderID
  fetch_provider: WebToolProviderID
  timeout_sec: number
  max_calls: number
  providers: WebToolProvider[]
}

export interface Config {
  server: { addr: string }
  routing: {
    long_context_threshold: number
    dispatcher_timeout_sec: number
    strategy: 'round_robin' | 'random'
  }
  providers: Provider[]
  models: Model[]
  groups: Group[]
  keys: ApiKey[]
  logs: { file: string; retention_days?: number }
  web_tools: WebTools
}

export interface Preset {
  id: string
  name: string
  protocol: string
  base_url: string
  balance?: BalanceConf
}

export interface BalanceResult {
  ok: boolean
  message?: string
  remaining: number
  unit: 'CNY' | 'USD' | 'percent'
  plan?: string
}

export interface LogEntry {
  ts: string
  key_name: string
  client_proto: string
  request_model: string
  mode: string
  group: string
  tier: string
  route_model: string
  upstream_model: string
  provider: string
  stream: boolean
  status: number
  latency_ms: number
  input_tokens: number
  output_tokens: number
  attempts: number
  error: string
  dispatcher_error: string
}

// ---- 请求封装 ----

import { dicts, getLang } from './i18n'

export class ApiError extends Error {
  status: number
  constructor(status: number, message: string) {
    super(message)
    this.status = status
  }
}

const TOKEN_KEY = 'viberouter_token'

export function getToken(): string | null {
  return localStorage.getItem(TOKEN_KEY)
}

export function setToken(token: string | null) {
  if (token) localStorage.setItem(TOKEN_KEY, token)
  else localStorage.removeItem(TOKEN_KEY)
}

async function request<T>(
  path: string,
  options: RequestInit = {},
): Promise<T> {
  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
    ...(options.headers as Record<string, string>),
  }
  const token = getToken()
  if (token) headers['Authorization'] = `Bearer ${token}`

  const res = await fetch(`/api${path}`, { ...options, headers })
  if (res.status === 401 && path !== '/login') {
    setToken(null)
    if (!location.hash.startsWith('#/login')) {
      location.hash = '#/login'
    }
    throw new ApiError(401, dicts[getLang()].api.sessionExpired)
  }
  if (!res.ok) {
    let message = dicts[getLang()].api.requestFailed(res.status)
    try {
      const body = await res.json()
      if (body?.error?.message) message = body.error.message
      else if (typeof body?.error === 'string') message = body.error
      else if (body?.message) message = body.message
    } catch {
      /* ignore */
    }
    throw new ApiError(res.status, message)
  }
  if (res.status === 204) return undefined as T
  return res.json()
}

// ---- API ----

export const api = {
  login: (username: string, password: string, captchaId: string, captchaCode: string) =>
    request<{ token: string }>('/login', {
      method: 'POST',
      body: JSON.stringify({
        username,
        password,
        captcha_id: captchaId,
        captcha_code: captchaCode,
      }),
    }),
  getCaptcha: () => request<{ captcha_id: string; image: string }>('/captcha'),
  getConfig: () => request<Config>('/config'),
  getPresets: () => request<Preset[]>('/presets'),
  refreshPresets: () => request<Preset[]>('/presets/refresh', { method: 'POST' }),

  createProvider: (p: Provider) =>
    request<Provider>('/providers', { method: 'POST', body: JSON.stringify(p) }),
  updateProvider: (id: string, p: Provider) =>
    request<Provider>(`/providers/${encodeURIComponent(id)}`, {
      method: 'PUT',
      body: JSON.stringify(p),
    }),
  syncProviderPreset: (id: string) =>
    request<ProviderSyncResult>(`/providers/${encodeURIComponent(id)}/sync-preset`, { method: 'POST' }),
  deleteProvider: (id: string) =>
    request<void>(`/providers/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  queryBalance: (id: string) =>
    request<BalanceResult>(`/providers/${encodeURIComponent(id)}/balance`, {
      method: 'POST',
    }),

  createModel: (m: Model) =>
    request<Model>('/models', { method: 'POST', body: JSON.stringify(m) }),
  updateModel: (name: string, m: Model) =>
    request<Model>(`/models/${encodeURIComponent(name)}`, {
      method: 'PUT',
      body: JSON.stringify(m),
    }),
  deleteModel: (name: string) =>
    request<void>(`/models/${encodeURIComponent(name)}`, { method: 'DELETE' }),

  createGroup: (g: Group) =>
    request<Group>('/groups', { method: 'POST', body: JSON.stringify(g) }),
  updateGroup: (name: string, g: Group) =>
    request<Group>(`/groups/${encodeURIComponent(name)}`, {
      method: 'PUT',
      body: JSON.stringify(g),
    }),
  deleteGroup: (name: string) =>
    request<void>(`/groups/${encodeURIComponent(name)}`, { method: 'DELETE' }),

  createKey: (name: string) =>
    request<ApiKey>('/keys', { method: 'POST', body: JSON.stringify({ name }) }),
  updateKey: (id: string, name: string, enabled: boolean) =>
    request<ApiKey>(`/keys/${encodeURIComponent(id)}`, {
      method: 'PUT',
      body: JSON.stringify({ name, enabled }),
    }),
  deleteKey: (id: string) =>
    request<void>(`/keys/${encodeURIComponent(id)}`, { method: 'DELETE' }),

  getLogs: (params: { limit?: number; status?: string; model?: string; key?: string; since?: string }) => {
    const q = new URLSearchParams()
    if (params.limit) q.set('limit', String(params.limit))
    if (params.status) q.set('status', params.status)
    if (params.model) q.set('model', params.model)
    if (params.key) q.set('key', params.key)
    if (params.since) q.set('since', params.since)
    return request<LogEntry[]>(`/logs?${q.toString()}`)
  },

  updateSettings: (body: {
    routing?: { long_context_threshold: number; dispatcher_timeout_sec: number; strategy: string }
    logs?: { file: string; retention_days: number }
    web_tools?: WebTools
  }) => request<{ ok: boolean }>('/settings', { method: 'PUT', body: JSON.stringify(body) }),

  getDefaultPrompt: () => request<{ prompt: string }>('/dispatcher/default-prompt'),
}
