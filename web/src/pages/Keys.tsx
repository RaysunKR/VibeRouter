import { useEffect, useState } from 'react'
import { toast } from 'sonner'
import { Check, Copy, Pencil, Plus, RefreshCw, Trash2 } from 'lucide-react'
import { api, ApiKey, Config } from '@/lib/api'
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
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'

function maskKey(key: string) {
  if (key.length <= 8) return key
  return `${key.slice(0, 6)}…${key.slice(-4)}`
}

export default function KeysPage() {
  const { d, lang } = useI18n()
  const [config, setConfig] = useState<Config | null>(null)
  const [createOpen, setCreateOpen] = useState(false)
  const [newName, setNewName] = useState('')
  const [renameTarget, setRenameTarget] = useState<ApiKey | null>(null)
  const [renameValue, setRenameValue] = useState('')
  const [copied, setCopied] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)

  const dateLocale = lang === 'zh' ? 'zh-CN' : 'en-US'

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

  const copy = async (k: ApiKey) => {
    try {
      await navigator.clipboard.writeText(k.key)
      setCopied(k.id)
      setTimeout(() => setCopied(null), 1500)
    } catch {
      toast.error(d.keys.copyFailed)
    }
  }

  const create = async () => {
    if (!newName.trim()) {
      toast.error(d.keys.nameRequired)
      return
    }
    setSaving(true)
    try {
      await api.createKey(newName.trim())
      toast.success(d.keys.keyCreated)
      setCreateOpen(false)
      setNewName('')
      await load()
    } catch (err) {
      toast.error(err instanceof Error ? err.message : d.keys.createFailed)
    } finally {
      setSaving(false)
    }
  }

  const toggle = async (k: ApiKey, enabled: boolean) => {
    try {
      await api.updateKey(k.id, k.name, enabled)
      toast.success(d.keys.toggled(enabled))
      await load()
    } catch (err) {
      toast.error(err instanceof Error ? err.message : d.keys.toggleFailed)
    }
  }

  const rename = async () => {
    if (!renameTarget) return
    if (!renameValue.trim()) {
      toast.error(d.keys.nameRequired)
      return
    }
    setSaving(true)
    try {
      await api.updateKey(renameTarget.id, renameValue.trim(), renameTarget.enabled)
      toast.success(d.keys.renamed)
      setRenameTarget(null)
      await load()
    } catch (err) {
      toast.error(err instanceof Error ? err.message : d.keys.renameFailed)
    } finally {
      setSaving(false)
    }
  }

  const remove = async (k: ApiKey) => {
    if (!confirm(d.keys.deleteConfirm(k.name))) return
    try {
      await api.deleteKey(k.id)
      toast.success(d.common.deleted)
      await load()
    } catch (err) {
      toast.error(err instanceof Error ? err.message : d.common.deleteFailed)
    }
  }

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h1 className="text-2xl font-bold">{d.keys.title}</h1>
        <div className="flex gap-2">
          <Button variant="outline" size="sm" onClick={load}>
            <RefreshCw className="h-4 w-4" /> {d.common.refresh}
          </Button>
          <Button size="sm" onClick={() => setCreateOpen(true)}>
            <Plus className="h-4 w-4" /> {d.keys.create}
          </Button>
        </div>
      </div>

      <div className="rounded-lg border bg-card">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{d.common.name}</TableHead>
              <TableHead>{d.keys.key}</TableHead>
              <TableHead>{d.keys.status}</TableHead>
              <TableHead>{d.keys.createdAt}</TableHead>
              <TableHead className="text-right">{d.common.actions}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {(config?.keys || []).map((k) => (
              <TableRow key={k.id}>
                <TableCell className="font-medium">{k.name}</TableCell>
                <TableCell>
                  <div className="flex items-center gap-2">
                    <code className="text-xs text-muted-foreground">{maskKey(k.key)}</code>
                    <Button variant="ghost" size="icon" className="h-7 w-7" onClick={() => copy(k)}>
                      {copied === k.id ? (
                        <Check className="h-3.5 w-3.5 text-green-500" />
                      ) : (
                        <Copy className="h-3.5 w-3.5" />
                      )}
                    </Button>
                  </div>
                </TableCell>
                <TableCell>
                  <div className="flex items-center gap-2">
                    <Switch checked={k.enabled} onCheckedChange={(v) => toggle(k, v)} />
                    <Badge variant={k.enabled ? 'success' : 'secondary'}>
                      {k.enabled ? d.keys.enabled : d.keys.disabled}
                    </Badge>
                  </div>
                </TableCell>
                <TableCell className="text-muted-foreground text-xs">
                  {k.created_at ? new Date(k.created_at).toLocaleString(dateLocale) : '—'}
                </TableCell>
                <TableCell className="text-right">
                  <Button
                    variant="ghost"
                    size="icon"
                    onClick={() => {
                      setRenameTarget(k)
                      setRenameValue(k.name)
                    }}
                  >
                    <Pencil className="h-4 w-4" />
                  </Button>
                  <Button variant="ghost" size="icon" onClick={() => remove(k)}>
                    <Trash2 className="h-4 w-4 text-destructive" />
                  </Button>
                </TableCell>
              </TableRow>
            ))}
            {config && config.keys.length === 0 && (
              <TableRow>
                <TableCell colSpan={5} className="text-center text-muted-foreground py-8">
                  {d.keys.empty}
                </TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>
      </div>

      <Dialog open={createOpen} onOpenChange={setCreateOpen}>
        <DialogContent className="max-w-sm">
          <DialogHeader>
            <DialogTitle>{d.keys.create}</DialogTitle>
          </DialogHeader>
          <div className="space-y-2">
            <Label>{d.common.name}</Label>
            <Input
              value={newName}
              onChange={(e) => setNewName(e.target.value)}
              placeholder={d.keys.namePlaceholder}
              onKeyDown={(e) => e.key === 'Enter' && create()}
            />
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setCreateOpen(false)}>
              {d.common.cancel}
            </Button>
            <Button onClick={create} disabled={saving}>
              {d.keys.createBtn}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={!!renameTarget} onOpenChange={(v) => !v && setRenameTarget(null)}>
        <DialogContent className="max-w-sm">
          <DialogHeader>
            <DialogTitle>{d.keys.rename}</DialogTitle>
          </DialogHeader>
          <div className="space-y-2">
            <Label>{d.common.name}</Label>
            <Input
              value={renameValue}
              onChange={(e) => setRenameValue(e.target.value)}
              onKeyDown={(e) => e.key === 'Enter' && rename()}
            />
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setRenameTarget(null)}>
              {d.common.cancel}
            </Button>
            <Button onClick={rename} disabled={saving}>
              {d.common.save}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}
