import { useEffect, useState } from 'react'
import { toast } from 'sonner'
import { RefreshCw, Save } from 'lucide-react'
import { api, WebToolProvider, WebToolProviderID, WebTools } from '@/lib/api'
import { useI18n } from '@/lib/i18n'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'

const providerIDs: WebToolProviderID[] = ['bocha', 'kimi', 'deepseek', 'glm']

const providerNames: Record<WebToolProviderID, string> = {
  bocha: 'Bocha',
  kimi: 'Kimi',
  deepseek: 'DeepSeek',
  glm: 'GLM',
}

const providerBaseURLs: Record<WebToolProviderID, string> = {
  bocha: 'https://api.bocha.cn',
  kimi: 'https://api.moonshot.cn',
  deepseek: 'https://api.deepseek.com',
  glm: 'https://open.bigmodel.cn',
}

function normalizeWebTools(value?: WebTools): WebTools {
  return {
    enabled: value?.enabled ?? false,
    mcp_enabled: value?.mcp_enabled ?? false,
    search_provider: value?.search_provider || 'deepseek',
    fetch_provider: value?.fetch_provider || 'deepseek',
    timeout_sec: value?.timeout_sec ?? 30,
    max_calls: value?.max_calls ?? 8,
    providers: providerIDs.map((id) => {
      const existing = value?.providers?.find((provider) => provider.id === id)
      return {
        id,
        enabled: existing?.enabled ?? false,
        api_key: existing?.api_key ?? '',
        base_url: existing?.base_url ?? providerBaseURLs[id],
      }
    }),
  }
}

export default function WebToolsPage() {
  const { d } = useI18n()
  const [form, setForm] = useState<WebTools>(() => normalizeWebTools())
  const [saving, setSaving] = useState(false)

  const load = async () => {
    try {
      const config = await api.getConfig()
      setForm(normalizeWebTools(config.web_tools))
    } catch (err) {
      toast.error(err instanceof Error ? err.message : d.common.loadFailed)
    }
  }

  useEffect(() => {
    load()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const updateProvider = (
    id: WebToolProviderID,
    patch: Partial<Pick<WebToolProvider, 'enabled' | 'api_key' | 'base_url'>>,
  ) => {
    setForm((current) => ({
      ...current,
      providers: current.providers.map((provider) =>
        provider.id === id ? { ...provider, ...patch } : provider,
      ),
    }))
  }

  const save = async () => {
    if (form.fetch_provider === 'bocha') {
      toast.error(d.webTools.invalidFetchProvider)
      return
    }
    if (
      !Number.isInteger(form.timeout_sec) ||
      form.timeout_sec < 1 ||
      !Number.isInteger(form.max_calls) ||
      form.max_calls < 1
    ) {
      toast.error(d.webTools.positiveRequired)
      return
    }
    setSaving(true)
    try {
      await api.updateSettings({ web_tools: form })
      toast.success(d.webTools.saved)
      await load()
    } catch (err) {
      toast.error(err instanceof Error ? err.message : d.common.saveFailed)
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="max-w-4xl space-y-4">
      <div className="flex items-center justify-between">
        <h1 className="text-2xl font-bold">{d.webTools.title}</h1>
        <div className="flex gap-2">
          <Button variant="outline" size="sm" onClick={load}>
            <RefreshCw className="h-4 w-4" /> {d.common.refresh}
          </Button>
          <Button size="sm" data-testid="web-tools-save" onClick={save} disabled={saving}>
            <Save className="h-4 w-4" /> {saving ? d.common.saving : d.common.save}
          </Button>
        </div>
      </div>

      <Card>
        <CardHeader>
          <CardTitle>{d.webTools.access}</CardTitle>
          <CardDescription>{d.webTools.accessDescription}</CardDescription>
        </CardHeader>
        <CardContent className="space-y-6">
          <div className="grid gap-4 md:grid-cols-2">
            <div className="flex items-start justify-between gap-4 rounded-lg border p-4">
              <div className="space-y-1">
                <Label htmlFor="web-tools-native">{d.webTools.nativeBridge}</Label>
                <p className="text-xs text-muted-foreground">{d.webTools.nativeBridgeHint}</p>
              </div>
              <Switch
                id="web-tools-native"
                data-testid="web-tools-native"
                checked={form.enabled}
                onCheckedChange={(enabled) => setForm({ ...form, enabled })}
              />
            </div>
            <div className="flex items-start justify-between gap-4 rounded-lg border p-4">
              <div className="space-y-1">
                <Label htmlFor="web-tools-mcp">{d.webTools.mcp}</Label>
                <p className="text-xs text-muted-foreground">{d.webTools.mcpHint}</p>
              </div>
              <Switch
                id="web-tools-mcp"
                data-testid="web-tools-mcp"
                checked={form.mcp_enabled}
                onCheckedChange={(mcp_enabled) => setForm({ ...form, mcp_enabled })}
              />
            </div>
          </div>
          <div className="space-y-1.5">
            <Label>{d.webTools.mcpEndpoint}</Label>
            <code className="block overflow-x-auto rounded-md border bg-muted/50 px-3 py-2 text-sm">
              {window.location.origin}/mcp
            </code>
            <p className="text-xs text-muted-foreground">{d.webTools.mcpAuthHint}</p>
          </div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>{d.webTools.routing}</CardTitle>
          <CardDescription>{d.webTools.routingDescription}</CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <div className="grid gap-4 md:grid-cols-2">
            <div className="space-y-1.5">
              <Label htmlFor="web-tools-search-provider">{d.webTools.searchProvider}</Label>
              <Select
                value={form.search_provider}
                onValueChange={(value) =>
                  setForm({ ...form, search_provider: value as WebToolProviderID })
                }
              >
                <SelectTrigger id="web-tools-search-provider" data-testid="web-tools-search-provider">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {providerIDs.map((id) => (
                    <SelectItem key={id} value={id}>
                      {providerNames[id]}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="web-tools-fetch-provider">{d.webTools.fetchProvider}</Label>
              <Select
                value={form.fetch_provider}
                onValueChange={(value) =>
                  setForm({ ...form, fetch_provider: value as WebToolProviderID })
                }
              >
                <SelectTrigger id="web-tools-fetch-provider" data-testid="web-tools-fetch-provider">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {providerIDs
                    .filter((id) => id !== 'bocha')
                    .map((id) => (
                      <SelectItem key={id} value={id}>
                        {providerNames[id]}
                      </SelectItem>
                    ))}
                </SelectContent>
              </Select>
              <p className="text-xs text-muted-foreground">{d.webTools.bochaSearchOnly}</p>
            </div>
          </div>
          <div className="grid gap-4 md:grid-cols-2">
            <div className="space-y-1.5">
              <Label htmlFor="web-tools-timeout">{d.webTools.timeout}</Label>
              <Input
                id="web-tools-timeout"
                data-testid="web-tools-timeout"
                type="number"
                min={1}
                step={1}
                value={form.timeout_sec || ''}
                onChange={(e) =>
                  setForm({ ...form, timeout_sec: parseInt(e.target.value, 10) || 0 })
                }
              />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="web-tools-max-calls">{d.webTools.maxCalls}</Label>
              <Input
                id="web-tools-max-calls"
                data-testid="web-tools-max-calls"
                type="number"
                min={1}
                step={1}
                value={form.max_calls || ''}
                onChange={(e) =>
                  setForm({ ...form, max_calls: parseInt(e.target.value, 10) || 0 })
                }
              />
              <p className="text-xs text-muted-foreground">{d.webTools.maxCallsHint}</p>
            </div>
          </div>
        </CardContent>
      </Card>

      <div>
        <h2 className="text-lg font-semibold">{d.webTools.providers}</h2>
        <p className="mt-1 text-sm text-muted-foreground">{d.webTools.providersDescription}</p>
      </div>
      <div className="grid gap-4 md:grid-cols-2">
        {form.providers.map((provider) => (
          <Card key={provider.id} data-testid={`web-tool-provider-${provider.id}`}>
            <CardHeader className="flex-row items-start justify-between space-y-0">
              <div className="space-y-1.5">
                <CardTitle>{providerNames[provider.id]}</CardTitle>
                <CardDescription>{d.webTools.capabilities[provider.id]}</CardDescription>
              </div>
              <Switch
                aria-label={`${providerNames[provider.id]} ${d.webTools.enabled}`}
                data-testid={`web-tool-${provider.id}-enabled`}
                checked={provider.enabled}
                onCheckedChange={(enabled) => updateProvider(provider.id, { enabled })}
              />
            </CardHeader>
            <CardContent className="space-y-4">
              <div className="space-y-1.5">
                <Label htmlFor={`web-tool-${provider.id}-api-key`}>{d.webTools.apiKey}</Label>
                <Input
                  id={`web-tool-${provider.id}-api-key`}
                  data-testid={`web-tool-${provider.id}-api-key`}
                  type="password"
                  autoComplete="new-password"
                  value={provider.api_key || ''}
                  onChange={(e) => updateProvider(provider.id, { api_key: e.target.value })}
                />
              </div>
              <div className="space-y-1.5">
                <Label htmlFor={`web-tool-${provider.id}-base-url`}>{d.webTools.baseUrl}</Label>
                <Input
                  id={`web-tool-${provider.id}-base-url`}
                  data-testid={`web-tool-${provider.id}-base-url`}
                  value={provider.base_url}
                  onChange={(e) => updateProvider(provider.id, { base_url: e.target.value })}
                />
              </div>
            </CardContent>
          </Card>
        ))}
      </div>
    </div>
  )
}
