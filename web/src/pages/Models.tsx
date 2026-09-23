import { useEffect, useState } from 'react'
import { toast } from 'sonner'
import { Pencil, Plus, RefreshCw, Trash2 } from 'lucide-react'
import { api, Config, Model } from '@/lib/api'
import { useI18n } from '@/lib/i18n'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Badge } from '@/components/ui/badge'
import { Switch } from '@/components/ui/switch'
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

const emptyModel: Model = {
  name: '',
  provider: '',
  upstream_model: '',
  api_key: '',
  base_url: '',
  protocol: '',
  multimodal: false,
  long_context: false,
  max_context_tokens: 0,
}

export default function ModelsPage() {
  const { d } = useI18n()
  const [config, setConfig] = useState<Config | null>(null)
  const [dialogOpen, setDialogOpen] = useState(false)
  const [editing, setEditing] = useState<Model | null>(null)
  const [form, setForm] = useState<Model>(emptyModel)
  const [saving, setSaving] = useState(false)

  const load = async () => {
    try {
      setConfig(await api.getConfig())
    } catch (err) {
      toast.error(err instanceof Error ? err.message : d.common.loadFailed)
    }
  }

  useEffect(() => {
    load()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const openCreate = () => {
    setEditing(null)
    setForm({ ...emptyModel, provider: config?.providers[0]?.id || '' })
    setDialogOpen(true)
  }

  const openEdit = (m: Model) => {
    setEditing(m)
    setForm({ ...m, api_key: m.api_key || '', base_url: m.base_url || '', protocol: m.protocol || '' })
    setDialogOpen(true)
  }

  const save = async () => {
    if (!form.name || !form.provider || !form.upstream_model) {
      toast.error(d.models.required)
      return
    }
    setSaving(true)
    try {
      const body: Model = { ...form }
      if (!body.api_key) delete body.api_key
      if (!body.base_url) delete body.base_url
      if (!body.protocol) delete body.protocol
      if (editing) {
        await api.updateModel(editing.name, body)
        toast.success(d.models.updated)
      } else {
        await api.createModel(body)
        toast.success(d.models.created)
      }
      setDialogOpen(false)
      await load()
    } catch (err) {
      toast.error(err instanceof Error ? err.message : d.common.saveFailed)
    } finally {
      setSaving(false)
    }
  }

  const remove = async (m: Model) => {
    if (!confirm(d.models.deleteConfirm(m.name))) return
    try {
      await api.deleteModel(m.name)
      toast.success(d.common.deleted)
      await load()
    } catch (err) {
      toast.error(err instanceof Error ? err.message : d.common.deleteFailed)
    }
  }

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h1 className="text-2xl font-bold">{d.models.title}</h1>
        <div className="flex gap-2">
          <Button variant="outline" size="sm" onClick={load}>
            <RefreshCw className="h-4 w-4" /> {d.common.refresh}
          </Button>
          <Button size="sm" onClick={openCreate}>
            <Plus className="h-4 w-4" /> {d.models.add}
          </Button>
        </div>
      </div>

      <div className="rounded-lg border bg-card">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{d.models.routeName}</TableHead>
              <TableHead>{d.models.provider}</TableHead>
              <TableHead>{d.models.upstreamModel}</TableHead>
              <TableHead>{d.models.capabilities}</TableHead>
              <TableHead>{d.models.maxContext}</TableHead>
              <TableHead className="text-right">{d.common.actions}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {(config?.models || []).map((m) => (
              <TableRow key={m.name}>
                <TableCell className="font-medium">{m.name}</TableCell>
                <TableCell>
                  <Badge variant="outline">
                    {config?.providers.find((p) => p.id === m.provider)?.name || m.provider}
                  </Badge>
                </TableCell>
                <TableCell className="text-muted-foreground">{m.upstream_model}</TableCell>
                <TableCell>
                  <div className="flex gap-1.5">
                    {m.multimodal && <Badge variant="secondary">{d.models.multimodal}</Badge>}
                    {m.long_context && <Badge variant="secondary">{d.models.longContext}</Badge>}
                    {!m.multimodal && !m.long_context && (
                      <span className="text-muted-foreground">—</span>
                    )}
                  </div>
                </TableCell>
                <TableCell className="text-muted-foreground">
                  {m.max_context_tokens ? m.max_context_tokens.toLocaleString() : '—'}
                </TableCell>
                <TableCell className="text-right">
                  <Button variant="ghost" size="icon" onClick={() => openEdit(m)}>
                    <Pencil className="h-4 w-4" />
                  </Button>
                  <Button variant="ghost" size="icon" onClick={() => remove(m)}>
                    <Trash2 className="h-4 w-4 text-destructive" />
                  </Button>
                </TableCell>
              </TableRow>
            ))}
            {config && config.models.length === 0 && (
              <TableRow>
                <TableCell colSpan={6} className="text-center text-muted-foreground py-8">
                  {d.models.empty}
                </TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>
      </div>

      <Dialog open={dialogOpen} onOpenChange={setDialogOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{editing ? d.models.edit : d.models.add}</DialogTitle>
            <DialogDescription>{d.models.dialogDescription}</DialogDescription>
          </DialogHeader>

          <div className="grid grid-cols-2 gap-4">
            <div className="space-y-2">
              <Label>{d.models.routeName}</Label>
              <Input
                value={form.name}
                onChange={(e) => setForm({ ...form, name: e.target.value })}
                placeholder={d.models.routeNamePlaceholder}
                disabled={!!editing}
              />
            </div>
            <div className="space-y-2">
              <Label>{d.models.provider}</Label>
              {(config?.providers.length ?? 0) === 0 ? (
                <div className="rounded-md border border-dashed p-3 text-sm text-muted-foreground">
                  {d.models.noProviders}
                </div>
              ) : (
                <Select
                  value={form.provider}
                  onValueChange={(v) => setForm({ ...form, provider: v })}
                >
                  <SelectTrigger>
                    <SelectValue placeholder={d.models.selectProvider} />
                  </SelectTrigger>
                  <SelectContent>
                    {(config?.providers || []).map((p) => (
                      <SelectItem key={p.id} value={p.id}>
                        {p.name}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              )}
            </div>
          </div>

          <div className="space-y-2">
            <Label>{d.models.upstreamModel}</Label>
            <Input
              value={form.upstream_model}
              onChange={(e) => setForm({ ...form, upstream_model: e.target.value })}
              placeholder={d.models.upstreamPlaceholder}
            />
          </div>

          <div className="grid grid-cols-2 gap-4">
            <div className="space-y-2">
              <Label>{d.models.apiKeyOverride}</Label>
              <Input
                value={form.api_key || ''}
                onChange={(e) => setForm({ ...form, api_key: e.target.value })}
                placeholder={d.models.apiKeyPlaceholder}
                type="password"
                autoComplete="new-password"
              />
            </div>
            <div className="space-y-2">
              <Label>{d.models.baseUrlOverride}</Label>
              <Input
                value={form.base_url || ''}
                onChange={(e) => setForm({ ...form, base_url: e.target.value })}
                placeholder={d.models.baseUrlPlaceholder}
              />
            </div>
          </div>

          <div className="grid grid-cols-2 gap-4">
            <div className="space-y-2">
              <Label>{d.models.protocolOverride}</Label>
              <Select
                value={form.protocol || 'default'}
                onValueChange={(v) => setForm({ ...form, protocol: v === 'default' ? '' : v })}
              >
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="default">{d.protocols.default}</SelectItem>
                  <SelectItem value="chat_completions">{d.protocols.chat_completions}</SelectItem>
                  <SelectItem value="responses">{d.protocols.responses}</SelectItem>
                  <SelectItem value="anthropic">{d.protocols.anthropic}</SelectItem>
                </SelectContent>
              </Select>
            </div>
            <div className="space-y-2">
              <Label>{d.models.maxTokens}</Label>
              <Input
                type="number"
                min={0}
                value={form.max_context_tokens || ''}
                onChange={(e) =>
                  setForm({ ...form, max_context_tokens: parseInt(e.target.value) || 0 })
                }
                placeholder={d.models.maxTokensPlaceholder}
              />
            </div>
          </div>

          <div className="flex gap-8">
            <div className="flex items-center gap-2">
              <Switch
                checked={form.multimodal}
                onCheckedChange={(v) => setForm({ ...form, multimodal: v })}
              />
              <Label>{d.models.multimodal}</Label>
            </div>
            <div className="flex items-center gap-2">
              <Switch
                checked={form.long_context}
                onCheckedChange={(v) => setForm({ ...form, long_context: v })}
              />
              <Label>{d.models.longContext}</Label>
            </div>
          </div>

          <DialogFooter>
            <Button variant="outline" onClick={() => setDialogOpen(false)}>
              {d.common.cancel}
            </Button>
            <Button onClick={save} disabled={saving || (config?.providers.length ?? 0) === 0}>
              {saving ? d.common.saving : d.common.save}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}
