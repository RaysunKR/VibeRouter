import { useEffect, useState } from 'react'
import { toast } from 'sonner'
import { Pencil, Plus, RefreshCw, Trash2, Wallet } from 'lucide-react'
import { api, Config, Preset, Provider, BalanceResult } from '@/lib/api'
import { useI18n } from '@/lib/i18n'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Badge } from '@/components/ui/badge'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'

const emptyProvider: Provider = {
  id: '',
  name: '',
  protocol: 'chat_completions',
  base_url: '',
  api_key: '',
}

function nextProviderID(presetID: string, providers: Provider[]): string {
  const used = new Set(providers.map((p) => p.id))
  if (!used.has(presetID)) return presetID
  for (let i = 2; ; i++) {
    const id = `${presetID}-${i}`
    if (!used.has(id)) return id
  }
}

export default function ProvidersPage() {
  const { d } = useI18n()
  const [config, setConfig] = useState<Config | null>(null)
  const [presets, setPresets] = useState<Preset[]>([])
  const [dialogOpen, setDialogOpen] = useState(false)
  const [editing, setEditing] = useState<Provider | null>(null)
  const [form, setForm] = useState<Provider>(emptyProvider)
  const [presetId, setPresetId] = useState('')
  const [saving, setSaving] = useState(false)
  const [syncingPresets, setSyncingPresets] = useState(false)
  const [syncingProviderID, setSyncingProviderID] = useState('')
  const [balances, setBalances] = useState<Record<string, BalanceResult | { error: string } | 'loading'>>({})

  const load = async () => {
    try {
      const [cfg, ps] = await Promise.all([api.getConfig(), api.getPresets()])
      setConfig(cfg)
      setPresets(ps || [])
    } catch (err) {
      toast.error(err instanceof Error ? err.message : d.common.loadFailed)
    }
  }

  const syncPresets = async () => {
    setSyncingPresets(true)
    try {
      const ps = await api.refreshPresets()
      setPresets(ps || [])
      toast.success(d.providers.presetsSynced(ps.length))
    } catch (err) {
      toast.error(err instanceof Error ? err.message : d.providers.presetsSyncFailed)
    } finally {
      setSyncingPresets(false)
    }
  }

  const syncProvider = async (p: Provider) => {
    if (!confirm(d.providers.syncPresetConfirm(p.name))) return
    setSyncingProviderID(p.id)
    try {
      const result = await api.syncProviderPreset(p.id)
      toast.success(d.providers.providerPresetSynced(result.models_updated, (result.models_skipped || []).length))
      await load()
    } catch (err) {
      toast.error(err instanceof Error ? err.message : d.providers.providerSyncFailed)
    } finally {
      setSyncingProviderID('')
    }
  }

  useEffect(() => {
    load()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const openCreate = () => {
    setEditing(null)
    setForm(emptyProvider)
    setPresetId('')
    setDialogOpen(true)
  }

  const openEdit = (p: Provider) => {
    setEditing(p)
    setForm({ ...p, api_key: p.api_key || '' })
    setDialogOpen(true)
  }

  const applyPreset = (id: string) => {
    setPresetId(id)
    if (id === 'custom') {
      setForm(emptyProvider)
      return
    }
    const p = presets.find((x) => x.id === id)
    if (!p) return
    setForm((f) => ({
      ...f,
      id: nextProviderID(p.id, config?.providers || []),
      name: p.name,
      protocol: (p.protocol as Provider['protocol']) || 'chat_completions',
      base_url: p.base_url,
      preset_id: p.id,
      balance: p.balance,
    }))
  }

  const save = async () => {
    if (!form.name || !form.base_url) {
      toast.error(d.providers.required)
      return
    }
    setSaving(true)
    try {
      const body: Provider = { ...form }
      if (!body.api_key) delete body.api_key
      if (!body.id) body.id = body.name
      if (editing) {
        await api.updateProvider(editing.id, body)
        toast.success(d.providers.updated)
      } else {
        await api.createProvider(body)
        toast.success(d.providers.created)
      }
      setDialogOpen(false)
      await load()
    } catch (err) {
      toast.error(err instanceof Error ? err.message : d.common.saveFailed)
    } finally {
      setSaving(false)
    }
  }

  const remove = async (p: Provider) => {
    if (!confirm(d.providers.deleteConfirm(p.name))) return
    try {
      await api.deleteProvider(p.id)
      toast.success(d.common.deleted)
      await load()
    } catch (err) {
      toast.error(err instanceof Error ? err.message : d.common.deleteFailed)
    }
  }

  const queryBalance = async (p: Provider) => {
    setBalances((b) => ({ ...b, [p.id]: 'loading' }))
    try {
      const res = await api.queryBalance(p.id)
      setBalances((b) => ({ ...b, [p.id]: res }))
    } catch (err) {
      setBalances((b) => ({
        ...b,
        [p.id]: { error: err instanceof Error ? err.message : d.providers.queryFailed },
      }))
    }
  }

  const renderBalance = (p: Provider) => {
    const b = balances[p.id]
    if (!b) return null
    if (b === 'loading') return <Badge variant="secondary">{d.providers.querying}</Badge>
    if ('error' in b) return <span className="text-xs text-destructive">{b.error}</span>
    if (!b.ok) return <span className="text-xs text-destructive">{b.message || d.providers.queryFailed}</span>
    if (b.unit === 'percent') {
      return <Badge variant="secondary">{d.providers.quotaRemaining(b.remaining)}</Badge>
    }
    return (
      <Badge variant="secondary">{d.providers.remaining(b.remaining, b.unit, b.plan)}</Badge>
    )
  }

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h1 className="text-2xl font-bold">{d.providers.title}</h1>
        <div className="flex gap-2">
          <Button variant="outline" size="sm" data-testid="provider-sync-presets" onClick={syncPresets} disabled={syncingPresets}>
            <RefreshCw className={`h-4 w-4 ${syncingPresets ? 'animate-spin' : ''}`} />
            {syncingPresets ? d.providers.syncingPresets : d.providers.syncPresets}
          </Button>
          <Button variant="outline" size="sm" onClick={load}>
            <RefreshCw className="h-4 w-4" /> {d.common.refresh}
          </Button>
          <Button size="sm" data-testid="provider-add" onClick={openCreate}>
            <Plus className="h-4 w-4" /> {d.providers.add}
          </Button>
        </div>
      </div>

      <div className="rounded-lg border bg-card">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{d.common.name}</TableHead>
              <TableHead>{d.providers.protocol}</TableHead>
              <TableHead>{d.providers.baseUrl}</TableHead>
              <TableHead>{d.providers.apiKey}</TableHead>
              <TableHead>{d.providers.balance}</TableHead>
              <TableHead className="text-right">{d.common.actions}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {(config?.providers || []).map((p) => (
              <TableRow key={p.id} data-testid={`provider-row-${p.id}`} data-provider-id={p.id}>
                <TableCell>
                  <div className="font-medium">{p.name}</div>
                  <div className="text-xs text-muted-foreground">{p.id}</div>
                </TableCell>
                <TableCell>
                  <Badge variant="outline">{d.protocols[p.protocol] || p.protocol}</Badge>
                </TableCell>
                <TableCell className="max-w-[280px] truncate text-muted-foreground">
                  {p.base_url}
                </TableCell>
                <TableCell className="text-muted-foreground">
                  {p.api_key ? '••••••' : '—'}
                </TableCell>
                <TableCell>
                  <div className="flex items-center gap-2" data-testid={`provider-balance-${p.id}`}>
                    {p.balance && (
                      <Button
                        variant="ghost"
                        size="sm"
                        data-testid={`provider-check-balance-${p.id}`}
                        aria-label={`${d.providers.checkBalance}: ${p.name}`}
                        onClick={() => queryBalance(p)}
                        disabled={balances[p.id] === 'loading'}
                      >
                        <Wallet className="h-4 w-4" /> {d.providers.checkBalance}
                      </Button>
                    )}
                    {renderBalance(p)}
                  </div>
                </TableCell>
                <TableCell className="text-right">
                  {p.preset_id && (
                    <Button
                      variant="ghost"
                      size="icon"
                      title={d.providers.syncPreset}
                      aria-label={`${d.providers.syncPreset}: ${p.name}`}
                      data-testid={`provider-sync-${p.id}`}
                      disabled={syncingProviderID === p.id}
                      onClick={() => syncProvider(p)}
                    >
                      <RefreshCw className={`h-4 w-4 ${syncingProviderID === p.id ? 'animate-spin' : ''}`} />
                    </Button>
                  )}
                  <Button
                    variant="ghost"
                    size="icon"
                    aria-label={`${d.providers.edit}: ${p.name}`}
                    data-testid={`provider-edit-${p.id}`}
                    onClick={() => openEdit(p)}
                  >
                    <Pencil className="h-4 w-4" />
                  </Button>
                  <Button
                    variant="ghost"
                    size="icon"
                    aria-label={`${d.common.actions}: ${p.name}`}
                    data-testid={`provider-delete-${p.id}`}
                    onClick={() => remove(p)}
                  >
                    <Trash2 className="h-4 w-4 text-destructive" />
                  </Button>
                </TableCell>
              </TableRow>
            ))}
            {config && config.providers.length === 0 && (
              <TableRow>
                <TableCell colSpan={6} className="text-center text-muted-foreground py-8">
                  {d.providers.empty}
                </TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>
      </div>

      <Dialog open={dialogOpen} onOpenChange={setDialogOpen}>
        <DialogContent data-testid="provider-dialog">
          <DialogHeader>
            <DialogTitle>{editing ? d.providers.edit : d.providers.add}</DialogTitle>
            <DialogDescription>
              {editing ? d.providers.editDescription : d.providers.createDescription}
            </DialogDescription>
          </DialogHeader>

          {!editing && (
            <div className="space-y-2">
              <Label htmlFor="provider-preset">{d.providers.fromPreset}</Label>
              <Select value={presetId} onValueChange={applyPreset}>
                <SelectTrigger id="provider-preset" data-testid="provider-preset">
                  <SelectValue placeholder={d.providers.presetPlaceholder} />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="custom">{d.providers.custom}</SelectItem>
                  {presets.map((p) => (
                    <SelectItem key={p.id} value={p.id}>
                      {p.name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
          )}

          {editing && (
            <div className="space-y-2">
              <Label htmlFor="provider-preset-source">{d.providers.presetSource}</Label>
              <Select
                value={form.preset_id || 'none'}
                onValueChange={(v) => setForm({ ...form, preset_id: v === 'none' ? '' : v })}
              >
                <SelectTrigger id="provider-preset-source" data-testid="provider-preset-source">
                  <SelectValue placeholder={d.providers.presetSource} />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="none">{d.providers.noPreset}</SelectItem>
                  {form.preset_id && !presets.some((p) => p.id === form.preset_id) && (
                    <SelectItem value={form.preset_id}>{form.preset_id}</SelectItem>
                  )}
                  {presets.map((p) => (
                    <SelectItem key={p.id} value={p.id}>
                      {p.name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
              <p className="text-xs text-muted-foreground">{d.providers.presetSourceHint}</p>
            </div>
          )}

          <div className="grid grid-cols-2 gap-4">
            <div className="space-y-2">
              <Label htmlFor="provider-id">ID</Label>
              <Input
                id="provider-id"
                data-testid="provider-id"
                value={form.id}
                onChange={(e) => setForm({ ...form, id: e.target.value })}
                placeholder={d.providers.idPlaceholder}
                disabled={!!editing}
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="provider-name">{d.common.name}</Label>
              <Input
                id="provider-name"
                data-testid="provider-name"
                value={form.name}
                onChange={(e) => setForm({ ...form, name: e.target.value })}
                placeholder={d.providers.namePlaceholder}
              />
            </div>
          </div>

          <div className="space-y-2">
            <Label htmlFor="provider-protocol">{d.providers.protocol}</Label>
            <Select
              value={form.protocol}
              onValueChange={(v) => setForm({ ...form, protocol: v as Provider['protocol'] })}
            >
              <SelectTrigger id="provider-protocol" data-testid="provider-protocol">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="chat_completions">{d.protocols.chat_completions}</SelectItem>
                <SelectItem value="responses">{d.protocols.responses}</SelectItem>
                <SelectItem value="anthropic">{d.protocols.anthropic}</SelectItem>
              </SelectContent>
            </Select>
            <p className="text-xs text-muted-foreground">{d.protocols.protocolHint}</p>
          </div>

          <div className="space-y-2">
            <Label htmlFor="provider-base-url">{d.providers.baseUrl}</Label>
            <Input
              id="provider-base-url"
              data-testid="provider-base-url"
              value={form.base_url}
              onChange={(e) => setForm({ ...form, base_url: e.target.value })}
              placeholder="https://api.example.com"
            />
          </div>

          <div className="space-y-2">
            <Label htmlFor="provider-api-key">{d.providers.apiKey}</Label>
            <Input
              id="provider-api-key"
              data-testid="provider-api-key"
              value={form.api_key || ''}
              onChange={(e) => setForm({ ...form, api_key: e.target.value })}
              placeholder={editing?.api_key ? d.providers.keepKey : 'sk-...'}
              type="password"
              autoComplete="new-password"
            />
          </div>

          <DialogFooter>
            <Button variant="outline" onClick={() => setDialogOpen(false)}>
              {d.common.cancel}
            </Button>
            <Button data-testid="provider-save" onClick={save} disabled={saving}>
              {saving ? d.common.saving : d.common.save}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}
