import { useEffect, useState } from 'react'
import { toast } from 'sonner'
import { ChevronDown, ChevronRight, RefreshCw, Search } from 'lucide-react'
import { api, LogEntry } from '@/lib/api'
import { useI18n } from '@/lib/i18n'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Badge } from '@/components/ui/badge'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { Fragment } from 'react'

const protoLabel: Record<string, string> = {
  chat_completions: 'chat',
  responses: 'responses',
  anthropic: 'anthropic',
}

function statusBadge(status: number) {
  if (status >= 200 && status < 300) {
    return <Badge variant="success">{status}</Badge>
  }
  return <Badge variant="destructive">{status || '—'}</Badge>
}

export default function LogsPage() {
  const { d, lang } = useI18n()
  const [logs, setLogs] = useState<LogEntry[]>([])
  const [status, setStatus] = useState('')
  const [model, setModel] = useState('')
  const [keyName, setKeyName] = useState('')
  const [limit, setLimit] = useState('200')
  const [loading, setLoading] = useState(false)
  const [expanded, setExpanded] = useState<Set<number>>(new Set())

  const dateLocale = lang === 'zh' ? 'zh-CN' : 'en-US'

  const load = async () => {
    setLoading(true)
    try {
      const res = await api.getLogs({
        limit: parseInt(limit) || 200,
        status: status.trim(),
        model: model.trim(),
        key: keyName.trim(),
      })
      setLogs(res || [])
      setExpanded(new Set())
    } catch (err) {
      toast.error(err instanceof Error ? err.message : d.logs.loadFailed)
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    load()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const toggleExpand = (i: number) => {
    setExpanded((s) => {
      const next = new Set(s)
      if (next.has(i)) next.delete(i)
      else next.add(i)
      return next
    })
  }

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h1 className="text-2xl font-bold">{d.logs.title}</h1>
      </div>

      <div className="flex flex-wrap items-center gap-2">
        <Input
          className="w-36"
          aria-label={d.logs.statusPlaceholder}
          data-testid="logs-filter-status"
          placeholder={d.logs.statusPlaceholder}
          value={status}
          onChange={(e) => setStatus(e.target.value)}
          onKeyDown={(e) => e.key === 'Enter' && load()}
        />
        <Input
          className="w-48"
          aria-label={d.logs.modelPlaceholder}
          data-testid="logs-filter-model"
          placeholder={d.logs.modelPlaceholder}
          value={model}
          onChange={(e) => setModel(e.target.value)}
          onKeyDown={(e) => e.key === 'Enter' && load()}
        />
        <Input
          className="w-36"
          aria-label={d.logs.keyPlaceholder}
          data-testid="logs-filter-key"
          placeholder={d.logs.keyPlaceholder}
          value={keyName}
          onChange={(e) => setKeyName(e.target.value)}
          onKeyDown={(e) => e.key === 'Enter' && load()}
        />
        <Input
          className="w-28"
          type="number"
          min={1}
          aria-label={d.logs.limitPlaceholder}
          data-testid="logs-filter-limit"
          placeholder={d.logs.limitPlaceholder}
          value={limit}
          onChange={(e) => setLimit(e.target.value)}
          onKeyDown={(e) => e.key === 'Enter' && load()}
        />
        <Button variant="outline" size="sm" data-testid="logs-search" onClick={load} disabled={loading}>
          <Search className="h-4 w-4" /> {d.logs.search}
        </Button>
        <Button variant="outline" size="sm" data-testid="logs-refresh" onClick={load} disabled={loading}>
          <RefreshCw className={loading ? 'h-4 w-4 animate-spin' : 'h-4 w-4'} /> {d.common.refresh}
        </Button>
      </div>

      <div className="rounded-lg border bg-card">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className="w-8"></TableHead>
              <TableHead>{d.logs.time}</TableHead>
              <TableHead>{d.keys.key}</TableHead>
              <TableHead>{d.logs.protocol}</TableHead>
              <TableHead>{d.logs.modelRouting}</TableHead>
              <TableHead>{d.logs.groupTier}</TableHead>
              <TableHead>{d.logs.status}</TableHead>
              <TableHead>{d.logs.latency}</TableHead>
              <TableHead>{d.logs.tokens}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {logs.map((l, i) => {
              const hasError = !!(l.error || l.dispatcher_error)
              const isOpen = expanded.has(i)
              return (
                <Fragment key={i}>
                  <TableRow
                    className={hasError ? 'cursor-pointer' : ''}
                    data-testid="log-row"
                    data-log-index={i}
                    data-log-status={l.status}
                    data-log-model={l.request_model}
                    data-log-key={l.key_name}
                    data-log-group={l.group}
                    aria-expanded={hasError ? isOpen : undefined}
                    onClick={() => hasError && toggleExpand(i)}
                  >
                    <TableCell className="pr-0">
                      {hasError &&
                        (isOpen ? (
                          <ChevronDown className="h-4 w-4 text-muted-foreground" />
                        ) : (
                          <ChevronRight className="h-4 w-4 text-muted-foreground" />
                        ))}
                    </TableCell>
                    <TableCell className="whitespace-nowrap text-xs text-muted-foreground">
                      {l.ts ? new Date(l.ts).toLocaleString(dateLocale) : '—'}
                    </TableCell>
                    <TableCell className="text-xs">{l.key_name || '—'}</TableCell>
                    <TableCell>
                      <Badge variant="outline">{protoLabel[l.client_proto] || l.client_proto}</Badge>
                    </TableCell>
                    <TableCell className="text-xs">
                      <span>{l.request_model}</span>
                      {l.route_model && l.route_model !== l.request_model && (
                        <>
                          <span className="text-muted-foreground"> → </span>
                          <span className="text-foreground">{l.route_model}</span>
                        </>
                      )}
                      {l.upstream_model && l.upstream_model !== (l.route_model || l.request_model) && (
                        <div className="text-muted-foreground">
                          {d.logs.upstream}: {l.upstream_model}
                        </div>
                      )}
                    </TableCell>
                    <TableCell>
                      <div className="flex gap-1 flex-wrap">
                        {l.group && <Badge variant="secondary">{l.group}</Badge>}
                        {l.tier && <Badge variant="secondary">{l.tier}</Badge>}
                        {l.stream && <Badge variant="outline">{d.logs.stream}</Badge>}
                      </div>
                    </TableCell>
                    <TableCell>{statusBadge(l.status)}</TableCell>
                    <TableCell className="text-xs text-muted-foreground">
                      {l.latency_ms ? `${(l.latency_ms / 1000).toFixed(2)}s` : '—'}
                    </TableCell>
                    <TableCell className="text-xs text-muted-foreground whitespace-nowrap">
                      {l.input_tokens || 0} / {l.output_tokens || 0}
                    </TableCell>
                  </TableRow>
                  {isOpen && hasError && (
                    <TableRow data-testid={`log-error-detail-${i}`}>
                      <TableCell colSpan={9} className="bg-muted/30">
                        <div className="space-y-1 text-xs" data-testid="log-error-details">
                          {l.error && (
                            <div>
                              <span className="text-destructive font-medium">{d.logs.error}：</span>
                              <span className="text-destructive/90">{l.error}</span>
                            </div>
                          )}
                          {l.dispatcher_error && (
                            <div>
                              <span className="text-destructive font-medium">
                                {d.logs.dispatcherError}：
                              </span>
                              <span className="text-destructive/90">{l.dispatcher_error}</span>
                            </div>
                          )}
                          <div className="text-muted-foreground">
                            {d.logs.detail(l.provider || '—', l.mode || '—', l.attempts || 1)}
                          </div>
                        </div>
                      </TableCell>
                    </TableRow>
                  )}
                </Fragment>
              )
            })}
            {logs.length === 0 && !loading && (
              <TableRow>
                <TableCell colSpan={9} className="text-center text-muted-foreground py-8">
                  {d.logs.empty}
                </TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>
      </div>
    </div>
  )
}
