import { expect, test } from '@playwright/test'
import type { APIRequestContext, Locator, Page, Response } from '@playwright/test'

interface ApiKey {
  id: string
  key: string
  name: string
  enabled: boolean
}

interface Config {
  providers: Array<{
    id: string
    name: string
    protocol: string
    base_url: string
    api_key?: string
    preset_id?: string
  }>
  models: Array<{
    name: string
    provider: string
    upstream_model: string
    api_key?: string
    base_url?: string
    protocol?: string
    multimodal: boolean
    long_context: boolean
    max_context_tokens: number
  }>
  groups: Array<{
    name: string
    mode: string
    strategy?: string
    members?: Array<{ model: string; priority: number }>
    dispatcher_model?: string
    dispatcher_prompt?: string
    advanced?: Array<{ model: string; priority: number }>
    basic?: Array<{ model: string; priority: number }>
  }>
  keys: ApiKey[]
  routing: {
    long_context_threshold: number
    dispatcher_timeout_sec: number
    strategy: string
  }
  logs: { file: string; retention_days?: number }
  web_tools: {
    enabled: boolean
    mcp_enabled: boolean
    search_provider: string
    fetch_provider: string
    timeout_sec: number
    max_calls: number
    providers: Array<{ id: string; enabled: boolean; api_key?: string; base_url: string }>
  }
}

interface LogEntry {
  key_name: string
  request_model: string
  route_model: string
  group: string
  tier: string
  status: number
  attempts: number
}

const baseURL = (process.env.VR_E2E_BASE_URL || 'http://127.0.0.1:18181').replace(/\/+$/, '')
const mockURL = (process.env.VR_E2E_MOCK_URL || '').replace(/\/+$/, '')
if (!mockURL) throw new Error('VR_E2E_MOCK_URL is required')

const initialKey = 'vr-e2ekey'
const providerA = 'deepseek'
const providerB = 'deepseek-2'
const providerAName = 'E2E DeepSeek Primary'
const providerBName = 'E2E DeepSeek Secondary'
const modelMain = 'e2e-browser-main'
const modelSecond = 'e2e-browser-second'
const modelBroken = 'e2e-browser-broken'
const balanceGroup = 'e2e-browser-balance'
const smartGroup = 'e2e-browser-smart'
const keyName = 'e2e-browser-key'
const renamedKeyName = 'e2e-browser-key-renamed'

const browserErrors = new WeakMap<Page, string[]>()

test.beforeEach(({ page }) => {
  const errors: string[] = []
  browserErrors.set(page, errors)
  page.on('pageerror', (error) => errors.push(`pageerror: ${error.message}`))
  page.on('console', (message) => {
    if (message.type() === 'error') errors.push(`console: ${message.text()}`)
  })
})

test.afterEach(({ page }) => {
  const errors = (browserErrors.get(page) || []).filter(
    (message) => !/Failed to load resource: .*status of (401|409)/.test(message),
  )
  expect(errors, 'unexpected browser console/page errors').toEqual([])
})

function isApi(response: Response, method: string, path: string) {
  return response.request().method() === method && new URL(response.url()).pathname === path
}

async function waitForMutation(
  page: Page,
  testId: string,
  method: string,
  path: string,
  expectedStatus = 200,
) {
  const responsePromise = page.waitForResponse((response) => isApi(response, method, path))
  await page.getByTestId(testId).click()
  const response = await responsePromise
  expect(response.status()).toBe(expectedStatus)
  return response
}

async function signIn(page: Page) {
  await expect(page.getByLabel('Username')).toBeVisible()
  await page.getByLabel('Username').fill('admin')
  await page.getByLabel('Password', { exact: true }).fill('admin')
  await page.getByLabel('Captcha', { exact: true }).fill('0000')
  await expect(page.getByRole('button', { name: 'Sign in', exact: true })).toBeEnabled()
  const loginPromise = page.waitForResponse((response) => isApi(response, 'POST', '/api/login'))
  const configPromise = page.waitForResponse((response) => isApi(response, 'GET', '/api/config'))
  const presetsPromise = page.waitForResponse((response) => isApi(response, 'GET', '/api/presets'))
  await page.getByRole('button', { name: 'Sign in', exact: true }).click()
  const loginResponse = await loginPromise
  expect(loginResponse.status()).toBe(200)
  const body = (await loginResponse.json()) as { token: string }
  expect(body.token).toBeTruthy()
  await expect(page).toHaveURL(/#\/providers$/)
  expect((await configPromise).status()).toBe(200)
  expect((await presetsPromise).status()).toBe(200)
  return body.token
}

async function login(page: Page) {
  await page.goto('/')
  await expect(page).toHaveURL(/#\/login$/)
  return signIn(page)
}

async function openConfigPage(page: Page, navId: string, route: string) {
  const configPromise = page.waitForResponse((response) => isApi(response, 'GET', '/api/config'))
  await page.getByTestId(navId).click()
  await expect(page).toHaveURL(new RegExp(`#/${route}$`))
  expect((await configPromise).status()).toBe(200)
}

async function openProviders(page: Page) {
  const configPromise = page.waitForResponse((response) => isApi(response, 'GET', '/api/config'))
  const presetsPromise = page.waitForResponse((response) => isApi(response, 'GET', '/api/presets'))
  await page.getByTestId('nav-providers').click()
  await expect(page).toHaveURL(/#\/providers$/)
  expect((await configPromise).status()).toBe(200)
  expect((await presetsPromise).status()).toBe(200)
}

async function openLogs(page: Page) {
  const logsPromise = page.waitForResponse((response) => isApi(response, 'GET', '/api/logs'))
  await page.getByTestId('nav-logs').click()
  await expect(page).toHaveURL(/#\/logs$/)
  expect((await logsPromise).status()).toBe(200)
}

async function selectOption(page: Page, testId: string, optionName: string) {
  const trigger = page.getByTestId(testId)
  await expect(trigger).toBeVisible()
  await trigger.click()
  const option = page.getByRole('option', { name: optionName, exact: true })
  await expect(option).toBeVisible()
  await option.click()
  await expect(trigger).toContainText(optionName)
}

async function setSwitch(locator: Locator, enabled: boolean) {
  await expect(locator).toBeVisible()
  const state = enabled ? 'checked' : 'unchecked'
  if ((await locator.getAttribute('data-state')) !== state) await locator.click()
  await expect(locator).toHaveAttribute('data-state', state)
}

function acceptDialog(page: Page) {
  page.once('dialog', (dialog) => {
    void dialog.accept()
  })
}

async function getConfig(request: APIRequestContext, token: string) {
  const response = await request.get('/api/config', {
    headers: { Authorization: `Bearer ${token}` },
  })
  expect(response.status()).toBe(200)
  return (await response.json()) as Config
}

async function getLogs(request: APIRequestContext, token: string, query = 'limit=200') {
  const response = await request.get(`/api/logs?${query}`, {
    headers: { Authorization: `Bearer ${token}` },
  })
  expect(response.status()).toBe(200)
  return (await response.json()) as LogEntry[]
}

async function chat(request: APIRequestContext, key: string, model: string, text: string) {
  return request.post(`${baseURL}/v1/chat/completions`, {
    headers: { Authorization: `Bearer ${key}` },
    data: { model, messages: [{ role: 'user', content: text }] },
  })
}

function providerOption(name: string, id: string) {
  return `${name} (${id})`
}

function modelOption(model: string, providerName: string, providerID: string) {
  return `${model} · ${providerOption(providerName, providerID)}`
}

async function createProvider(page: Page, id: string, name: string, apiKey: string) {
  await page.getByTestId('provider-add').click()
  await expect(page.getByTestId('provider-dialog')).toBeVisible()
  await selectOption(page, 'provider-preset', 'DeepSeek')
  await expect(page.getByTestId('provider-id')).toHaveValue(id)
  await page.getByTestId('provider-name').fill(name)
  await page.getByTestId('provider-base-url').fill(mockURL)
  await page.getByTestId('provider-api-key').fill(apiKey)
  await waitForMutation(page, 'provider-save', 'POST', '/api/providers')
  await expect(page.getByTestId('provider-dialog')).toHaveCount(0)
  await expect(page.getByTestId(`provider-row-${id}`)).toContainText(name)
}

interface ModelInput {
  name: string
  providerID: string
  providerName: string
  apiKey?: string
  baseURLOverride?: string
  protocol?: string
  multimodal?: boolean
  longContext?: boolean
  maxContext: number
}

async function createModel(page: Page, input: ModelInput) {
  await page.getByTestId('model-add').click()
  await expect(page.getByTestId('model-dialog')).toBeVisible()
  await page.getByTestId('model-name').fill(input.name)
  await selectOption(
    page,
    'model-provider',
    providerOption(input.providerName, input.providerID),
  )
  await page.getByTestId('model-upstream').fill('real-chat')
  if (input.apiKey) await page.getByTestId('model-api-key').fill(input.apiKey)
  if (input.baseURLOverride) {
    await page.getByTestId('model-base-url').fill(input.baseURLOverride)
  }
  if (input.protocol) await selectOption(page, 'model-protocol', input.protocol)
  await page.getByTestId('model-max-context').fill(String(input.maxContext))
  await setSwitch(page.getByTestId('model-multimodal'), input.multimodal ?? false)
  await setSwitch(page.getByTestId('model-long-context'), input.longContext ?? false)
  await waitForMutation(page, 'model-save', 'POST', '/api/models')
  await expect(page.getByTestId('model-dialog')).toHaveCount(0)
  await expect(page.getByTestId(`model-row-${input.name}`)).toBeVisible()
}

async function searchLogs(page: Page) {
  const responsePromise = page.waitForResponse((response) => isApi(response, 'GET', '/api/logs'))
  await page.getByTestId('logs-search').click()
  const response = await responsePromise
  expect(response.status()).toBe(200)
  return {
    response,
    entries: (await response.json()) as LogEntry[],
  }
}

test.describe.serial('production browser workflow', () => {
  test('01 authentication, persistence, localization, navigation, logout and 401', async ({ page }) => {
    await page.goto('/')
    await expect(page).toHaveURL(/#\/login$/)
    expect(await page.evaluate(() => localStorage.getItem('viberouter_token'))).toBeNull()

    await page.getByLabel('Username').fill('admin')
    await page.getByLabel('Password', { exact: true }).fill('wrong-password')
    await page.getByLabel('Captcha', { exact: true }).fill('0000')
    await expect(page.getByRole('button', { name: 'Sign in', exact: true })).toBeEnabled()
    const failedLogin = page.waitForResponse((response) => isApi(response, 'POST', '/api/login'))
    await page.getByRole('button', { name: 'Sign in', exact: true }).click()
    expect((await failedLogin).status()).toBe(401)
    await expect(page.getByTestId('login-error')).toContainText('Invalid username or password')
    await expect(page).toHaveURL(/#\/login$/)

    await signIn(page)
    const token = await page.evaluate(() => localStorage.getItem('viberouter_token'))
    expect(token).toBeTruthy()
    await page.reload()
    await expect(page).toHaveURL(/#\/providers$/)
    await expect(page.getByRole('heading', { name: 'Providers', exact: true })).toBeVisible()
    expect(await page.evaluate(() => localStorage.getItem('viberouter_token'))).toBe(token)

    await page.getByTestId('language-switch').click()
    await expect(page.locator('html')).toHaveAttribute('lang', 'zh-CN')
    await expect(page.getByTestId('nav-providers')).toContainText('供应商')
    await page.reload()
    await expect(page.locator('html')).toHaveAttribute('lang', 'zh-CN')
    await expect(page.getByRole('heading', { name: '供应商', exact: true })).toBeVisible()
    await page.getByTestId('language-switch').click()
    await expect(page.locator('html')).toHaveAttribute('lang', 'en')

    const pages = [
      ['nav-models', /#\/models$/, 'Models'],
      ['nav-groups', /#\/groups$/, 'Group Routing'],
      ['nav-keys', /#\/keys$/, 'Keys'],
      ['nav-web-tools', /#\/web-tools$/, 'Web Tools'],
      ['nav-logs', /#\/logs$/, 'Logs'],
      ['nav-settings', /#\/settings$/, 'Settings'],
      ['nav-providers', /#\/providers$/, 'Providers'],
    ] as const
    for (const [navId, url, heading] of pages) {
      await page.getByTestId(navId).click()
      await expect(page).toHaveURL(url)
      await expect(page.getByRole('heading', { name: heading, exact: true })).toBeVisible()
    }

    await page.getByTestId('nav-logout').click()
    await expect(page).toHaveURL(/#\/login$/)
    expect(await page.evaluate(() => localStorage.getItem('viberouter_token'))).toBeNull()

    await signIn(page)
    await page.evaluate(() => localStorage.setItem('viberouter_token', 'invalid-e2e-token'))
    await page.reload()
    await expect(page).toHaveURL(/#\/login$/)
    expect(await page.evaluate(() => localStorage.getItem('viberouter_token'))).toBeNull()
  })

  test('02 create two DeepSeek providers, edit binding and query balance', async ({ page, request }) => {
    const token = await login(page)
    const initial = await getConfig(request, token)
    expect(initial.providers || []).toEqual([])
    expect(initial.models || []).toEqual([])
    expect(initial.groups || []).toEqual([])
    expect((initial.keys || []).some((key) => key.key === initialKey)).toBe(true)

    await waitForMutation(page, 'provider-sync-presets', 'POST', '/api/presets/refresh')
    await createProvider(page, providerA, 'E2E DeepSeek One', 'upkey')
    await createProvider(page, providerB, providerBName, 'web-key')

    await page.getByTestId(`provider-edit-${providerA}`).click()
    await expect(page.getByTestId('provider-dialog')).toBeVisible()
    await page.getByTestId('provider-name').fill(providerAName)
    await selectOption(page, 'provider-preset-source', 'No linked preset')
    await waitForMutation(page, 'provider-save', 'PUT', `/api/providers/${providerA}`)
    await expect(page.getByTestId(`provider-row-${providerA}`)).toContainText(providerAName)
    let config = await getConfig(request, token)
    expect(config.providers.find((provider) => provider.id === providerA)?.preset_id).toBeFalsy()

    await page.getByTestId(`provider-edit-${providerA}`).click()
    await selectOption(page, 'provider-preset-source', 'DeepSeek')
    await waitForMutation(page, 'provider-save', 'PUT', `/api/providers/${providerA}`)
    await expect(page.getByTestId('provider-dialog')).toHaveCount(0)

    await waitForMutation(
      page,
      `provider-check-balance-${providerA}`,
      'POST',
      `/api/providers/${providerA}/balance`,
    )
    await expect(page.getByTestId(`provider-balance-${providerA}`)).toContainText('88.5 CNY')

    config = await getConfig(request, token)
    const first = config.providers.find((provider) => provider.id === providerA)
    const second = config.providers.find((provider) => provider.id === providerB)
    expect(first).toMatchObject({
      name: providerAName,
      preset_id: 'deepseek',
      base_url: mockURL,
      api_key: 'upkey',
    })
    expect(second).toMatchObject({
      name: providerBName,
      preset_id: 'deepseek',
      base_url: mockURL,
      api_key: 'web-key',
    })
  })

  test('03 create and edit models, then update capabilities from the preset', async ({ page, request }) => {
    const token = await login(page)
    await openConfigPage(page, 'nav-models', 'models')
    await createModel(page, {
      name: modelMain,
      providerID: providerA,
      providerName: providerAName,
      baseURLOverride: mockURL,
      protocol: 'OpenAI-compatible (Chat Completions)',
      multimodal: true,
      longContext: true,
      maxContext: 128000,
    })

    await page.getByTestId(`model-edit-${modelMain}`).click()
    await expect(page.getByTestId('model-dialog')).toBeVisible()
    await expect(page.getByTestId('model-name')).toBeDisabled()
    await setSwitch(page.getByTestId('model-multimodal'), false)
    await setSwitch(page.getByTestId('model-long-context'), false)
    await page.getByTestId('model-max-context').fill('64000')
    await page.getByTestId('model-base-url').fill(mockURL)
    await waitForMutation(page, 'model-save', 'PUT', `/api/models/${modelMain}`)
    await expect(page.getByTestId('model-dialog')).toHaveCount(0)

    await createModel(page, {
      name: modelSecond,
      providerID: providerB,
      providerName: providerBName,
      maxContext: 128000,
    })
    await createModel(page, {
      name: modelBroken,
      providerID: providerB,
      providerName: providerBName,
      apiKey: 'badkey',
      baseURLOverride: mockURL,
      protocol: 'OpenAI-compatible (Chat Completions)',
      maxContext: 32000,
    })

    let config = await getConfig(request, token)
    let edited = config.models.find((model) => model.name === modelMain)
    expect(edited).toMatchObject({
      provider: providerA,
      upstream_model: 'real-chat',
      base_url: mockURL,
      protocol: 'chat_completions',
      multimodal: false,
      long_context: false,
      max_context_tokens: 64000,
    })
    expect(config.models.find((model) => model.name === modelBroken)?.api_key).toBe('badkey')

    await openProviders(page)
    acceptDialog(page)
    await waitForMutation(
      page,
      `provider-sync-${providerA}`,
      'POST',
      `/api/providers/${providerA}/sync-preset`,
    )
    await expect(page.getByTestId(`provider-row-${providerA}`)).toContainText(mockURL)

    config = await getConfig(request, token)
    expect(config.providers.find((provider) => provider.id === providerA)?.base_url).toBe(mockURL)
    edited = config.models.find((model) => model.name === modelMain)
    expect(edited).toMatchObject({
      upstream_model: 'real-chat',
      multimodal: true,
      long_context: true,
      max_context_tokens: 200001,
      base_url: mockURL,
      protocol: 'chat_completions',
    })
    expect(config.models.find((model) => model.name === modelBroken)?.api_key).toBe('badkey')

    await openConfigPage(page, 'nav-models', 'models')
    const row = page.getByTestId(`model-row-${modelMain}`)
    await expect(row).toContainText('Multimodal')
    await expect(row).toContainText('Long context')
    await expect(row).toContainText('200,001')
  })

  test('04 create balance and smart groups and verify real routing', async ({ page, request }) => {
    const token = await login(page)
    await openConfigPage(page, 'nav-groups', 'groups')

    await page.getByTestId('group-add').click()
    await expect(page.getByTestId('group-dialog')).toBeVisible()
    await page.getByTestId('group-name').fill(balanceGroup)
    await selectOption(page, 'group-strategy', 'Round robin (round_robin)')
    await page.getByTestId('group-members-add').click()
    await selectOption(
      page,
      'group-members-member-0-model',
      modelOption(modelBroken, providerBName, providerB),
    )
    await page.getByTestId('group-members-member-0-priority').fill('1')
    await page.getByTestId('group-members-add').click()
    await selectOption(
      page,
      'group-members-member-1-model',
      modelOption(modelMain, providerAName, providerA),
    )
    await page.getByTestId('group-members-member-1-priority').fill('2')
    await page.getByTestId('group-members-add').click()
    await selectOption(
      page,
      'group-members-member-2-model',
      modelOption(modelSecond, providerBName, providerB),
    )
    await page.getByTestId('group-members-member-2-priority').fill('2')
    await waitForMutation(page, 'group-save', 'POST', '/api/groups')
    await expect(page.getByTestId(`group-row-${balanceGroup}`)).toBeVisible()

    await page.getByTestId('group-add').click()
    await page.getByTestId('group-name').fill(smartGroup)
    await selectOption(page, 'group-mode', 'Smart balancing')
    await selectOption(
      page,
      'group-dispatcher-model',
      modelOption(modelMain, providerAName, providerA),
    )
    await waitForMutation(
      page,
      'group-fill-default-prompt',
      'GET',
      '/api/dispatcher/default-prompt',
    )
    await expect(page.getByTestId('group-dispatcher-prompt')).toHaveValue(/请求复杂度分类器/)
    await page.getByTestId('group-advanced-add').click()
    await selectOption(
      page,
      'group-advanced-member-0-model',
      modelOption(modelSecond, providerBName, providerB),
    )
    await page.getByTestId('group-basic-add').click()
    await selectOption(
      page,
      'group-basic-member-0-model',
      modelOption(modelMain, providerAName, providerA),
    )
    await waitForMutation(page, 'group-save', 'POST', '/api/groups')
    await expect(page.getByTestId(`group-row-${smartGroup}`)).toBeVisible()

    const config = await getConfig(request, token)
    expect(config.groups.find((group) => group.name === balanceGroup)).toMatchObject({
      mode: 'balance',
      strategy: 'round_robin',
      members: [
        { model: modelBroken, priority: 1 },
        { model: modelMain, priority: 2 },
        { model: modelSecond, priority: 2 },
      ],
    })
    const smart = config.groups.find((group) => group.name === smartGroup)
    expect(smart).toMatchObject({
      mode: 'smart',
      dispatcher_model: modelMain,
      advanced: [{ model: modelSecond, priority: 1 }],
      basic: [{ model: modelMain, priority: 1 }],
    })
    expect(smart?.dispatcher_prompt).toContain('请求复杂度分类器')

    for (const text of ['round robin one', 'round robin two']) {
      const response = await chat(request, initialKey, balanceGroup, text)
      expect(response.status()).toBe(200)
      expect(JSON.stringify(await response.json())).toContain('MOCK[chat]')
    }
    await expect.poll(async () => {
      const logs = await getLogs(request, token, `limit=20&model=${balanceGroup}`)
      const relevant = logs.filter((entry) => entry.request_model === balanceGroup)
      const routes = new Set(relevant.map((entry) => entry.route_model))
      return (
        relevant.length === 2 &&
        relevant.every((entry) => entry.status === 200 && entry.attempts === 2) &&
        routes.has(modelMain) &&
        routes.has(modelSecond)
      )
    }).toBe(true)

    const basicResponse = await chat(request, initialKey, smartGroup, 'basic question')
    expect(basicResponse.status()).toBe(200)
    await expect.poll(async () => {
      const logs = await getLogs(request, token, `limit=20&model=${smartGroup}`)
      return logs.some(
        (entry) => entry.request_model === smartGroup && entry.tier === 'basic' && entry.route_model === modelMain,
      )
    }).toBe(true)

    const advancedResponse = await chat(request, initialKey, smartGroup, 'advanced multi-step reasoning')
    expect(advancedResponse.status()).toBe(200)
    await expect.poll(async () => {
      const logs = await getLogs(request, token, `limit=20&model=${smartGroup}`)
      return logs.some(
        (entry) => entry.request_model === smartGroup && entry.tier === 'advanced' && entry.route_model === modelSecond,
      )
    }).toBe(true)
  })

  test('05 create, rename, disable, enable and delete a key with real auth checks', async ({ page, request }) => {
    const token = await login(page)
    await openConfigPage(page, 'nav-keys', 'keys')
    await page.getByTestId('key-create').click()
    await page.getByTestId('key-create-name').fill(keyName)
    const createResponse = await waitForMutation(page, 'key-create-submit', 'POST', '/api/keys')
    const created = (await createResponse.json()) as ApiKey
    expect(created.key).toMatch(/^vr-/)
    await expect(page.getByTestId(`key-row-${created.id}`)).toContainText(keyName)

    await page.getByTestId(`key-rename-${created.id}`).click()
    await page.getByTestId('key-rename-name').fill(renamedKeyName)
    await waitForMutation(page, 'key-rename-save', 'PUT', `/api/keys/${created.id}`)
    await expect(page.getByTestId(`key-row-${created.id}`)).toContainText(renamedKeyName)

    const enabledResponse = await chat(request, created.key, modelMain, 'new key enabled')
    expect(enabledResponse.status()).toBe(200)

    await waitForMutation(page, `key-enabled-${created.id}`, 'PUT', `/api/keys/${created.id}`)
    await expect(page.getByTestId(`key-enabled-${created.id}`)).toHaveAttribute('data-state', 'unchecked')
    const disabledResponse = await chat(request, created.key, modelMain, 'disabled key')
    expect(disabledResponse.status()).toBe(401)

    await waitForMutation(page, `key-enabled-${created.id}`, 'PUT', `/api/keys/${created.id}`)
    await expect(page.getByTestId(`key-enabled-${created.id}`)).toHaveAttribute('data-state', 'checked')
    const reenabledResponse = await chat(request, created.key, modelMain, 'new key reenabled')
    expect(reenabledResponse.status()).toBe(200)

    const failedResponse = await chat(request, created.key, modelBroken, 'show upstream 402')
    expect(failedResponse.status()).toBe(502)
    expect(await failedResponse.text()).toContain('402')

    acceptDialog(page)
    await waitForMutation(page, `key-delete-${created.id}`, 'DELETE', `/api/keys/${created.id}`)
    await expect(page.getByTestId(`key-row-${created.id}`)).toHaveCount(0)
    const config = await getConfig(request, token)
    expect(config.keys.some((key) => key.id === created.id)).toBe(false)
    expect(config.keys.some((key) => key.key === initialKey && key.enabled)).toBe(true)
  })

  test('06 configure WebTools and exercise Responses, Anthropic and MCP', async ({ page, request }) => {
    const token = await login(page)
    await openConfigPage(page, 'nav-web-tools', 'web-tools')
    const before = await getConfig(request, token)
    await expect(page.getByTestId('web-tools-timeout')).toHaveValue(String(before.web_tools.timeout_sec || 30))

    await setSwitch(page.getByTestId('web-tools-native'), true)
    await setSwitch(page.getByTestId('web-tools-mcp'), true)
    await selectOption(page, 'web-tools-search-provider', 'DeepSeek')
    await selectOption(page, 'web-tools-fetch-provider', 'DeepSeek')
    await setSwitch(page.getByTestId('web-tool-deepseek-enabled'), true)
    await page.getByTestId('web-tool-deepseek-api-key').fill('web-key')
    await page.getByTestId('web-tool-deepseek-base-url').fill(mockURL)
    await page.getByTestId('web-tools-timeout').fill('7')
    await page.getByTestId('web-tools-max-calls').fill('4')
    await waitForMutation(page, 'web-tools-save', 'PUT', '/api/settings')
    await expect(page.getByTestId('web-tool-deepseek-base-url')).toHaveValue(mockURL)

    const config = await getConfig(request, token)
    expect(config.web_tools).toMatchObject({
      enabled: true,
      mcp_enabled: true,
      search_provider: 'deepseek',
      fetch_provider: 'deepseek',
      timeout_sec: 7,
      max_calls: 4,
    })
    expect(config.web_tools.providers.find((provider) => provider.id === 'deepseek')).toMatchObject({
      enabled: true,
      api_key: 'web-key',
      base_url: mockURL,
    })

    const responsesResult = await request.post(`${baseURL}/v1/responses`, {
      headers: { Authorization: `Bearer ${initialKey}` },
      data: { model: modelMain, input: 'hosted search', tools: [{ type: 'web_search' }] },
    })
    expect(responsesResult.status()).toBe(200)
    const responsesBody = await responsesResult.json()
    expect(responsesBody.output.some((item: { type: string }) => item.type === 'web_search_call')).toBe(true)
    expect(JSON.stringify(responsesBody)).toContain('MOCK[chat]')

    const anthropicSearch = await request.post(`${baseURL}/v1/messages`, {
      headers: { Authorization: `Bearer ${initialKey}` },
      data: {
        model: modelMain,
        max_tokens: 500,
        messages: [{ role: 'user', content: 'hosted search' }],
        tools: [{ type: 'web_search_20250305', name: 'web_search' }],
      },
    })
    expect(anthropicSearch.status()).toBe(200)
    const anthropicSearchBody = await anthropicSearch.json()
    expect(
      anthropicSearchBody.content.some((block: { type: string }) => block.type === 'web_search_tool_result'),
    ).toBe(true)

    const anthropicFetch = await request.post(`${baseURL}/v1/messages`, {
      headers: { Authorization: `Bearer ${initialKey}` },
      data: {
        model: modelMain,
        max_tokens: 500,
        messages: [{ role: 'user', content: 'hosted fetch' }],
        tools: [{ type: 'web_fetch_20250910', name: 'web_fetch' }],
      },
    })
    expect(anthropicFetch.status()).toBe(200)
    const anthropicFetchBody = await anthropicFetch.json()
    expect(
      anthropicFetchBody.content.some((block: { type: string }) => block.type === 'web_fetch_tool_result'),
    ).toBe(true)

    const mcpHeaders = {
      Authorization: `Bearer ${initialKey}`,
      Accept: 'application/json, text/event-stream',
      'MCP-Protocol-Version': '2025-11-25',
    }
    const initialize = await request.post(`${baseURL}/mcp`, {
      headers: mcpHeaders,
      data: {
        jsonrpc: '2.0',
        id: 1,
        method: 'initialize',
        params: { protocolVersion: '2025-11-25', capabilities: {}, clientInfo: { name: 'browser-e2e', version: '1' } },
      },
    })
    expect(initialize.status()).toBe(200)
    expect((await initialize.json()).result.serverInfo.name).toBe('viberouter')

    const mcpSearch = await request.post(`${baseURL}/mcp`, {
      headers: mcpHeaders,
      data: {
        jsonrpc: '2.0',
        id: 2,
        method: 'tools/call',
        params: { name: 'web_search', arguments: { query: 'latest news', count: 1 } },
      },
    })
    expect(mcpSearch.status()).toBe(200)
    const mcpSearchBody = await mcpSearch.json()
    expect(mcpSearchBody.result.isError).toBeFalsy()
    expect(mcpSearchBody.result.structuredContent.provider).toBe('deepseek')
    expect(mcpSearchBody.result.structuredContent.results[0].url).toBe('https://example.com/search')

    const mcpFetch = await request.post(`${baseURL}/mcp`, {
      headers: mcpHeaders,
      data: {
        jsonrpc: '2.0',
        id: 3,
        method: 'tools/call',
        params: { name: 'web_fetch', arguments: { url: 'https://example.com/article' } },
      },
    })
    expect(mcpFetch.status()).toBe(200)
    const mcpFetchBody = await mcpFetch.json()
    expect(mcpFetchBody.result.isError).toBeFalsy()
    expect(mcpFetchBody.result.structuredContent.provider).toBe('deepseek')
    expect(mcpFetchBody.result.structuredContent.content).toContain('fetched content')

    await setSwitch(page.getByTestId('web-tools-mcp'), false)
    await waitForMutation(page, 'web-tools-save', 'PUT', '/api/settings')
    await expect.poll(async () => (await getConfig(request, token)).web_tools.mcp_enabled).toBe(false)
    const disabledMCP = await request.post(`${baseURL}/mcp`, {
      headers: mcpHeaders,
      data: { jsonrpc: '2.0', id: 4, method: 'tools/list', params: {} },
    })
    expect(disabledMCP.status()).toBe(404)
  })

  test('07 save and reload routing and log settings', async ({ page, request }) => {
    const token = await login(page)
    await openConfigPage(page, 'nav-settings', 'settings')
    const before = await getConfig(request, token)
    await expect(page.getByTestId('settings-threshold')).toHaveValue(String(before.routing.long_context_threshold))
    await page.getByTestId('settings-threshold').fill('150000')
    await page.getByTestId('settings-dispatcher-timeout').fill('9')
    await selectOption(page, 'settings-strategy', 'Random (random)')
    await page.getByTestId('settings-retention-days').fill('3')
    await expect(page.getByTestId('settings-log-file')).toHaveValue(before.logs.file)
    await waitForMutation(page, 'settings-save', 'PUT', '/api/settings')

    const reloadConfig = page.waitForResponse((response) => isApi(response, 'GET', '/api/config'))
    await page.reload()
    expect((await reloadConfig).status()).toBe(200)
    await expect(page.getByTestId('settings-threshold')).toHaveValue('150000')
    await expect(page.getByTestId('settings-dispatcher-timeout')).toHaveValue('9')
    await expect(page.getByTestId('settings-strategy')).toContainText('Random (random)')
    await expect(page.getByTestId('settings-retention-days')).toHaveValue('3')
    await expect(page.getByTestId('settings-log-file')).toHaveValue(before.logs.file)

    const config = await getConfig(request, token)
    expect(config.routing).toEqual({
      long_context_threshold: 150000,
      dispatcher_timeout_sec: 9,
      strategy: 'random',
    })
    expect(config.logs.retention_days).toBe(3)
    expect(config.logs.file).toBe(before.logs.file)
  })

  test('08 filter logs by key, status, model and limit and expand an error', async ({ page }) => {
    await login(page)
    await openLogs(page)

    await page.getByTestId('logs-filter-key').fill(renamedKeyName)
    const keyFilter = await searchLogs(page)
    expect(new URL(keyFilter.response.url()).searchParams.get('key')).toBe(renamedKeyName)
    expect(keyFilter.entries.length).toBeGreaterThan(0)
    expect(keyFilter.entries.every((entry) => entry.key_name === renamedKeyName)).toBe(true)
    await expect(page.getByTestId('log-row')).toHaveCount(keyFilter.entries.length)
    await expect(page.getByTestId('log-row').first()).toHaveAttribute('data-log-key', renamedKeyName)

    await page.getByTestId('logs-filter-key').fill('')
    await page.getByTestId('logs-filter-status').fill('200')
    await page.getByTestId('logs-filter-model').fill(balanceGroup)
    const groupFilter = await searchLogs(page)
    expect(groupFilter.entries.length).toBeGreaterThan(0)
    expect(
      groupFilter.entries.every(
        (entry) => entry.status === 200 && entry.request_model === balanceGroup && entry.group === balanceGroup,
      ),
    ).toBe(true)
    await expect(page.getByTestId('log-row')).toHaveCount(groupFilter.entries.length)
    await expect(page.getByTestId('log-row').first()).toContainText(balanceGroup)

    await page.getByTestId('logs-filter-limit').fill('1')
    const limited = await searchLogs(page)
    expect(limited.entries).toHaveLength(1)
    await expect(page.getByTestId('log-row')).toHaveCount(1)

    await page.getByTestId('logs-filter-limit').fill('20')
    await page.getByTestId('logs-filter-status').fill('502')
    await page.getByTestId('logs-filter-model').fill(modelBroken)
    await page.getByTestId('logs-filter-key').fill(renamedKeyName)
    const failures = await searchLogs(page)
    expect(failures.entries.length).toBeGreaterThan(0)
    expect(
      failures.entries.every(
        (entry) => entry.status === 502 && entry.request_model === modelBroken && entry.key_name === renamedKeyName,
      ),
    ).toBe(true)
    const failureRow = page.getByTestId('log-row').first()
    await expect(failureRow).toHaveAttribute('data-log-status', '502')
    await failureRow.click()
    await expect(failureRow).toHaveAttribute('aria-expanded', 'true')
    await expect(page.getByTestId('log-error-details')).toContainText('402')
  })

  test('09 verify deletion protection and clean up created resources', async ({ page, request }) => {
    const token = await login(page)
    await openConfigPage(page, 'nav-models', 'models')
    acceptDialog(page)
    await waitForMutation(page, `model-delete-${modelMain}`, 'DELETE', `/api/models/${modelMain}`, 409)
    await expect(page.getByText(/model still referenced by group/).last()).toBeVisible()
    await expect(page.getByTestId(`model-row-${modelMain}`)).toBeVisible()

    await openConfigPage(page, 'nav-groups', 'groups')
    for (const group of [balanceGroup, smartGroup]) {
      acceptDialog(page)
      await waitForMutation(page, `group-delete-${group}`, 'DELETE', `/api/groups/${group}`)
      await expect(page.getByTestId(`group-row-${group}`)).toHaveCount(0)
    }

    await openConfigPage(page, 'nav-models', 'models')
    for (const model of [modelMain, modelSecond, modelBroken]) {
      acceptDialog(page)
      await waitForMutation(page, `model-delete-${model}`, 'DELETE', `/api/models/${model}`)
      await expect(page.getByTestId(`model-row-${model}`)).toHaveCount(0)
    }

    await openProviders(page)
    for (const provider of [providerA, providerB]) {
      acceptDialog(page)
      await waitForMutation(page, `provider-delete-${provider}`, 'DELETE', `/api/providers/${provider}`)
      await expect(page.getByTestId(`provider-row-${provider}`)).toHaveCount(0)
    }

    const config = await getConfig(request, token)
    expect(config.providers || []).toEqual([])
    expect(config.models || []).toEqual([])
    expect(config.groups || []).toEqual([])
    expect(config.keys.some((key) => key.key === initialKey)).toBe(true)
    expect(config.keys.some((key) => key.name === keyName || key.name === renamedKeyName)).toBe(false)
  })
})
