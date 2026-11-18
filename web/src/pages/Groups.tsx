import { useEffect, useState } from 'react'
import { toast } from 'sonner'
import { Pencil, Plus, RefreshCw, Trash2, X } from 'lucide-react'
import { api, Config, Group, GroupMember, Model, Provider } from '@/lib/api'
import { useI18n } from '@/lib/i18n'
import { modelProviderLabel } from '@/lib/labels'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Badge } from '@/components/ui/badge'
import { Textarea } from '@/components/ui/textarea'
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

const emptyGroup: Group = {
  name: '',
  mode: 'balance',
  strategy: 'round_robin',
  members: [],
}

function MemberEditor({
  title,
  testId,
  members,
  models,
  providers,
  onChange,
}: {
  title: string
  testId: string
  members: GroupMember[]
  models: Model[]
  providers: Provider[]
  onChange: (members: GroupMember[]) => void
}) {
  const { d } = useI18n()

  const update = (i: number, patch: Partial<GroupMember>) => {
    const next = members.map((m, idx) => (idx === i ? { ...m, ...patch } : m))
    onChange(next)
  }

  return (
    <div className="space-y-2" data-testid={testId}>
      <div className="flex items-center justify-between">
        <Label>{title}</Label>
        <Button
          type="button"
          variant="outline"
          size="sm"
          data-testid={`${testId}-add`}
          onClick={() => onChange([...members, { model: models[0]?.name || '', priority: 1 }])}
        >
          <Plus className="h-3 w-3" /> {d.groups.addMember}
        </Button>
      </div>
      <div className="space-y-2">
        {members.map((m, i) => (
          <div key={i} className="flex items-center gap-2" data-testid={`${testId}-member-${i}`}>
            <div className="flex-1">
              <Select value={m.model} onValueChange={(v) => update(i, { model: v })}>
                <SelectTrigger aria-label={`${title} ${d.groups.selectModel} ${i + 1}`} data-testid={`${testId}-member-${i}-model`}>
                  <SelectValue placeholder={d.groups.selectModel} />
                </SelectTrigger>
                <SelectContent>
                  {models.map((md) => (
                    <SelectItem key={md.name} value={md.name}>
                      {modelProviderLabel(md, providers)}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <Input
              type="number"
              min={0}
              className="w-24"
              aria-label={`${title} ${d.groups.priority} ${i + 1}`}
              data-testid={`${testId}-member-${i}-priority`}
              value={m.priority}
              onChange={(e) => update(i, { priority: parseInt(e.target.value) || 0 })}
              placeholder={d.groups.priority}
            />
            <Button
              type="button"
              variant="ghost"
              size="icon"
              aria-label={`${title} ${i + 1}`}
              data-testid={`${testId}-member-${i}-remove`}
              onClick={() => onChange(members.filter((_, idx) => idx !== i))}
            >
              <X className="h-4 w-4" />
            </Button>
          </div>
        ))}
        {members.length === 0 && (
          <div className="text-sm text-muted-foreground py-1">{d.groups.noMembers}</div>
        )}
      </div>
    </div>
  )
}

export default function GroupsPage() {
  const { d } = useI18n()
  const [config, setConfig] = useState<Config | null>(null)
  const [dialogOpen, setDialogOpen] = useState(false)
  const [editing, setEditing] = useState<Group | null>(null)
  const [form, setForm] = useState<Group>(emptyGroup)
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
    setForm({ ...emptyGroup, members: [] })
    setDialogOpen(true)
  }

  const openEdit = (g: Group) => {
    setEditing(g)
    setForm({
      ...g,
      strategy: g.strategy || 'round_robin',
      members: g.members ? [...g.members] : [],
      advanced: g.advanced ? [...g.advanced] : [],
      basic: g.basic ? [...g.basic] : [],
      dispatcher_model: g.dispatcher_model || '',
      dispatcher_prompt: g.dispatcher_prompt || '',
    })
    setDialogOpen(true)
  }

  const fillDefaultPrompt = async () => {
    try {
      const res = await api.getDefaultPrompt()
      setForm((f) => ({ ...f, dispatcher_prompt: res.prompt }))
      toast.success(d.groups.promptFilled)
    } catch (err) {
      toast.error(err instanceof Error ? err.message : d.groups.promptFetchFailed)
    }
  }

  const save = async () => {
    if (!form.name) {
      toast.error(d.groups.required)
      return
    }
    setSaving(true)
    try {
      const body: Group = { name: form.name, mode: form.mode }
      if (form.mode === 'balance') {
        body.strategy = form.strategy || 'round_robin'
        body.members = form.members || []
      } else {
        body.dispatcher_model = form.dispatcher_model
        body.dispatcher_prompt = form.dispatcher_prompt
        body.advanced = form.advanced || []
        body.basic = form.basic || []
      }
      if (editing) {
        await api.updateGroup(editing.name, body)
        toast.success(d.groups.updated)
      } else {
        await api.createGroup(body)
        toast.success(d.groups.created)
      }
      setDialogOpen(false)
      await load()
    } catch (err) {
      toast.error(err instanceof Error ? err.message : d.common.saveFailed)
    } finally {
      setSaving(false)
    }
  }

  const remove = async (g: Group) => {
    if (!confirm(d.groups.deleteConfirm(g.name))) return
    try {
      await api.deleteGroup(g.name)
      toast.success(d.common.deleted)
      await load()
    } catch (err) {
      toast.error(err instanceof Error ? err.message : d.common.deleteFailed)
    }
  }

  const models = config?.models || []
  const providers = config?.providers || []

  const memberSummary = (list?: GroupMember[]) =>
    (list || [])
      .slice()
      .sort((a, b) => a.priority - b.priority)
      .map((m) => {
        const model = models.find((md) => md.name === m.model)
        const label = model ? modelProviderLabel(model, providers) : m.model
        return `${label} (${m.priority})`
      })
      .join('、')

  const dispatcherLabel = (name?: string) => {
    const model = models.find((m) => m.name === name)
    return model ? modelProviderLabel(model, providers) : name || '—'
  }

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h1 className="text-2xl font-bold">{d.groups.title}</h1>
        <div className="flex gap-2">
          <Button variant="outline" size="sm" onClick={load}>
            <RefreshCw className="h-4 w-4" /> {d.common.refresh}
          </Button>
          <Button size="sm" data-testid="group-add" onClick={openCreate}>
            <Plus className="h-4 w-4" /> {d.groups.add}
          </Button>
        </div>
      </div>

      <div className="rounded-lg border bg-card">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{d.groups.name}</TableHead>
              <TableHead>{d.groups.mode}</TableHead>
              <TableHead>{d.groups.strategyDispatcher}</TableHead>
              <TableHead>{d.groups.members}</TableHead>
              <TableHead className="text-right">{d.common.actions}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {(config?.groups || []).map((g) => (
              <TableRow key={g.name} data-testid={`group-row-${g.name}`} data-group-name={g.name}>
                <TableCell className="font-medium">{g.name}</TableCell>
                <TableCell>
                  <Badge variant={g.mode === 'smart' ? 'default' : 'secondary'}>
                    {g.mode === 'smart' ? d.groups.modeSmart : d.groups.modeBalance}
                  </Badge>
                </TableCell>
                <TableCell className="text-muted-foreground">
                  {g.mode === 'smart' ? dispatcherLabel(g.dispatcher_model) : g.strategy || '—'}
                </TableCell>
                <TableCell className="max-w-[360px]">
                  {g.mode === 'smart' ? (
                    <div className="space-y-1 text-xs">
                      <div>
                        <span className="text-muted-foreground">{d.groups.advancedTier}：</span>
                        {memberSummary(g.advanced) || '—'}
                      </div>
                      <div>
                        <span className="text-muted-foreground">{d.groups.basicTier}：</span>
                        {memberSummary(g.basic) || '—'}
                      </div>
                    </div>
                  ) : (
                    <span className="text-xs">{memberSummary(g.members) || '—'}</span>
                  )}
                </TableCell>
                <TableCell className="text-right">
                  <Button
                    variant="ghost"
                    size="icon"
                    aria-label={`${d.groups.edit}: ${g.name}`}
                    data-testid={`group-edit-${g.name}`}
                    onClick={() => openEdit(g)}
                  >
                    <Pencil className="h-4 w-4" />
                  </Button>
                  <Button
                    variant="ghost"
                    size="icon"
                    aria-label={`${d.common.actions}: ${g.name}`}
                    data-testid={`group-delete-${g.name}`}
                    onClick={() => remove(g)}
                  >
                    <Trash2 className="h-4 w-4 text-destructive" />
                  </Button>
                </TableCell>
              </TableRow>
            ))}
            {config && config.groups.length === 0 && (
              <TableRow>
                <TableCell colSpan={5} className="text-center text-muted-foreground py-8">
                  {d.groups.empty}
                </TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>
      </div>

      <Dialog open={dialogOpen} onOpenChange={setDialogOpen}>
        <DialogContent className="max-w-2xl" data-testid="group-dialog">
          <DialogHeader>
            <DialogTitle>{editing ? d.groups.edit : d.groups.add}</DialogTitle>
            <DialogDescription>{d.groups.dialogDescription}</DialogDescription>
          </DialogHeader>

          <div className="grid grid-cols-2 gap-4">
            <div className="space-y-2">
              <Label htmlFor="group-name">{d.groups.name}</Label>
              <Input
                id="group-name"
                data-testid="group-name"
                value={form.name}
                onChange={(e) => setForm({ ...form, name: e.target.value })}
                placeholder={d.groups.namePlaceholder}
                disabled={!!editing}
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="group-mode">{d.groups.mode}</Label>
              <Select
                value={form.mode}
                onValueChange={(v) => setForm({ ...form, mode: v as Group['mode'] })}
              >
                <SelectTrigger id="group-mode" data-testid="group-mode">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="balance">{d.groups.modeBalance}</SelectItem>
                  <SelectItem value="smart">{d.groups.modeSmart}</SelectItem>
                </SelectContent>
              </Select>
            </div>
          </div>

          {form.mode === 'balance' ? (
            <>
              <div className="space-y-2">
                <Label htmlFor="group-strategy">{d.groups.strategy}</Label>
                <Select
                  value={form.strategy || 'round_robin'}
                  onValueChange={(v) => setForm({ ...form, strategy: v })}
                >
                  <SelectTrigger id="group-strategy" data-testid="group-strategy">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="round_robin">{d.groups.roundRobin}</SelectItem>
                    <SelectItem value="random">{d.groups.random}</SelectItem>
                  </SelectContent>
                </Select>
              </div>
              <MemberEditor
                title={d.groups.membersTitle}
                testId="group-members"
                members={form.members || []}
                models={models}
                providers={providers}
                onChange={(members) => setForm({ ...form, members })}
              />
            </>
          ) : (
            <>
              <div className="space-y-2">
                <Label htmlFor="group-dispatcher-model">{d.groups.dispatcherModel}</Label>
                <Select
                  value={form.dispatcher_model || ''}
                  onValueChange={(v) => setForm({ ...form, dispatcher_model: v })}
                >
                  <SelectTrigger id="group-dispatcher-model" data-testid="group-dispatcher-model">
                    <SelectValue placeholder={d.groups.dispatcherPlaceholder} />
                  </SelectTrigger>
                  <SelectContent>
                    {models.map((m) => (
                      <SelectItem key={m.name} value={m.name}>
                        {modelProviderLabel(m, providers)}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
              <div className="space-y-2">
                <div className="flex items-center justify-between">
                  <Label htmlFor="group-dispatcher-prompt">{d.groups.dispatcherPrompt}</Label>
                  <Button type="button" variant="outline" size="sm" data-testid="group-fill-default-prompt" onClick={fillDefaultPrompt}>
                    {d.groups.fillDefaultPrompt}
                  </Button>
                </div>
                <Textarea
                  id="group-dispatcher-prompt"
                  data-testid="group-dispatcher-prompt"
                  rows={5}
                  value={form.dispatcher_prompt || ''}
                  onChange={(e) => setForm({ ...form, dispatcher_prompt: e.target.value })}
                  placeholder={d.groups.promptPlaceholder}
                />
              </div>
              <div className="grid grid-cols-2 gap-4">
                <MemberEditor
                  title={d.groups.advancedTier}
                  testId="group-advanced"
                  members={form.advanced || []}
                  models={models}
                  providers={providers}
                  onChange={(advanced) => setForm({ ...form, advanced })}
                />
                <MemberEditor
                  title={d.groups.basicTier}
                  testId="group-basic"
                  members={form.basic || []}
                  models={models}
                  providers={providers}
                  onChange={(basic) => setForm({ ...form, basic })}
                />
              </div>
            </>
          )}

          <DialogFooter>
            <Button variant="outline" onClick={() => setDialogOpen(false)}>
              {d.common.cancel}
            </Button>
            <Button data-testid="group-save" onClick={save} disabled={saving}>
              {saving ? d.common.saving : d.common.save}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}
