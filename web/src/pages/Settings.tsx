import { useEffect, useState } from 'react'
import { toast } from 'sonner'
import { RefreshCw, Save } from 'lucide-react'
import { api, Config } from '@/lib/api'
import { useI18n } from '@/lib/i18n'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'

export default function SettingsPage() {
  const { d } = useI18n()
  const [config, setConfig] = useState<Config | null>(null)
  const [threshold, setThreshold] = useState('')
  const [timeoutSec, setTimeoutSec] = useState('')
  const [strategy, setStrategy] = useState('round_robin')
  const [retentionDays, setRetentionDays] = useState('')
  const [logFile, setLogFile] = useState('')
  const [saving, setSaving] = useState(false)

  const load = async () => {
    try {
      const c = await api.getConfig()
      setConfig(c)
      setThreshold(String(c.routing.long_context_threshold ?? 200000))
      setTimeoutSec(String(c.routing.dispatcher_timeout_sec ?? 5))
      setStrategy(c.routing.strategy || 'round_robin')
      setRetentionDays(String(c.logs.retention_days || 7))
      setLogFile(c.logs.file || 'logs/router.log')
    } catch (err) {
      toast.error(err instanceof Error ? err.message : d.common.loadFailed)
    }
  }

  useEffect(() => {
    load()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const save = async () => {
    const th = parseInt(threshold, 10)
    const to = parseInt(timeoutSec, 10)
    const rd = parseInt(retentionDays, 10)
    if (!th || th < 1 || !to || to < 1 || !rd || rd < 1) {
      toast.error(d.settings.positiveRequired)
      return
    }
    setSaving(true)
    try {
      await api.updateSettings({
        routing: {
          long_context_threshold: th,
          dispatcher_timeout_sec: to,
          strategy,
        },
        logs: { file: logFile, retention_days: rd },
      })
      toast.success(d.settings.saved)
      load()
    } catch (err) {
      toast.error(err instanceof Error ? err.message : d.common.saveFailed)
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="space-y-4 max-w-2xl">
      <div className="flex items-center justify-between">
        <h1 className="text-2xl font-bold">{d.settings.title}</h1>
        <div className="flex gap-2">
          <Button variant="outline" size="sm" onClick={load}>
            <RefreshCw className="h-4 w-4" /> {d.common.refresh}
          </Button>
          <Button size="sm" onClick={save} disabled={saving}>
            <Save className="h-4 w-4" /> {d.common.save}
          </Button>
        </div>
      </div>

      <Card>
        <CardHeader>
          <CardTitle>{d.settings.routing}</CardTitle>
          <CardDescription>{d.settings.routingDescription}</CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <div className="space-y-1.5">
            <Label>{d.settings.threshold}</Label>
            <Input
              type="number"
              value={threshold}
              onChange={(e) => setThreshold(e.target.value)}
            />
            <p className="text-xs text-muted-foreground">{d.settings.thresholdHint}</p>
          </div>
          <div className="space-y-1.5">
            <Label>{d.settings.dispatcherTimeout}</Label>
            <Input
              type="number"
              value={timeoutSec}
              onChange={(e) => setTimeoutSec(e.target.value)}
            />
          </div>
          <div className="space-y-1.5">
            <Label>{d.settings.strategy}</Label>
            <Select value={strategy} onValueChange={setStrategy}>
              <SelectTrigger>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="round_robin">{d.settings.roundRobin}</SelectItem>
                <SelectItem value="random">{d.settings.random}</SelectItem>
              </SelectContent>
            </Select>
          </div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>{d.settings.logsCard}</CardTitle>
          <CardDescription>{d.settings.logsDescription}</CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <div className="space-y-1.5">
            <Label>{d.settings.retentionDays}</Label>
            <Input
              type="number"
              min={1}
              value={retentionDays}
              onChange={(e) => setRetentionDays(e.target.value)}
            />
            <p className="text-xs text-muted-foreground">{d.settings.retentionDefault}</p>
          </div>
          <div className="space-y-1.5">
            <Label>{d.settings.logFile}</Label>
            <Input value={logFile} onChange={(e) => setLogFile(e.target.value)} />
            <p className="text-xs text-muted-foreground">{d.settings.logFileHint}</p>
          </div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>{d.settings.server}</CardTitle>
        </CardHeader>
        <CardContent className="space-y-3 text-sm">
          <div className="flex justify-between">
            <span className="text-muted-foreground">{d.settings.listenAddr}</span>
            <span className="font-mono">{config?.server.addr || '—'}</span>
          </div>
          <p className="text-xs text-muted-foreground">{d.settings.listenAddrHint}</p>
        </CardContent>
      </Card>
    </div>
  )
}
