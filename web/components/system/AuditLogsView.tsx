'use client';

import { useEffect, useMemo, useState } from 'react';
import { RefreshCw } from 'lucide-react';
import { useI18n } from '@/lib/i18n/I18nContext';
import { ErrorAlert, PageHeader, Select } from '@/components/ui';
import { apiPost, type AuditEvent, type AuditListRequest } from '@/lib/api';

type FilterState = {
  category: string;
  type: string;
  level: string;
  text: string;
};

const DEFAULT_FILTERS: FilterState = {
  category: '',
  type: '',
  level: '',
  text: '',
};

const CATEGORY_OPTIONS = ['', 'runtime', 'model', 'tool', 'channel', 'delivery', 'task', 'sandbox', 'environment', 'config', 'cerebellum'];
const LEVEL_OPTIONS = ['', 'debug', 'info', 'warn', 'error'];

function normalizeAuditEvent(event: AuditEvent | Record<string, unknown>): AuditEvent {
  const record = event as Record<string, unknown>;
  return {
    occurredAt:
      typeof record.occurredAt === 'string'
        ? record.occurredAt
        : typeof record.OccurredAt === 'string'
          ? record.OccurredAt
          : undefined,
    category:
      typeof record.category === 'string'
        ? record.category
        : typeof record.Category === 'string'
          ? record.Category
          : '',
    type: typeof record.type === 'string' ? record.type : typeof record.Type === 'string' ? record.Type : '',
    level:
      typeof record.level === 'string'
        ? record.level
        : typeof record.Level === 'string'
          ? record.Level
          : undefined,
    agentId:
      typeof record.agentId === 'string'
        ? record.agentId
        : typeof record.AgentID === 'string'
          ? record.AgentID
          : undefined,
    message:
      typeof record.message === 'string'
        ? record.message
        : typeof record.Message === 'string'
          ? record.Message
          : undefined,
    sessionKey:
      typeof record.sessionKey === 'string'
        ? record.sessionKey
        : typeof record.SessionKey === 'string'
          ? record.SessionKey
          : undefined,
    runId:
      typeof record.runId === 'string'
        ? record.runId
        : typeof record.RunID === 'string'
          ? record.RunID
          : undefined,
    taskId:
      typeof record.taskId === 'string'
        ? record.taskId
        : typeof record.TaskID === 'string'
          ? record.TaskID
          : undefined,
    toolName:
      typeof record.toolName === 'string'
        ? record.toolName
        : typeof record.ToolName === 'string'
          ? record.ToolName
          : undefined,
    channel:
      typeof record.channel === 'string'
        ? record.channel
        : typeof record.Channel === 'string'
          ? record.Channel
          : undefined,
    data:
      record.data && typeof record.data === 'object'
        ? (record.data as Record<string, unknown>)
        : record.Data && typeof record.Data === 'object'
          ? (record.Data as Record<string, unknown>)
          : undefined,
  };
}

function eventBadgeClass(category: string): string {
  switch (category) {
    case 'model':
      return 'bg-emerald-50 dark:bg-emerald-500/10 text-emerald-700 dark:text-emerald-300';
    case 'runtime':
      return 'bg-sky-50 dark:bg-sky-500/10 text-sky-700 dark:text-sky-300';
    case 'tool':
      return 'bg-purple-50 dark:bg-purple-500/10 text-purple-700 dark:text-purple-300';
    case 'channel':
      return 'bg-cyan-50 dark:bg-cyan-500/10 text-cyan-700 dark:text-cyan-300';
    case 'delivery':
      return 'bg-blue-50 dark:bg-blue-500/10 text-blue-700 dark:text-blue-300';
    case 'task':
      return 'bg-amber-50 dark:bg-amber-500/10 text-amber-700 dark:text-amber-300';
    case 'sandbox':
      return 'bg-rose-50 dark:bg-rose-500/10 text-rose-700 dark:text-rose-300';
    case 'environment':
      return 'bg-lime-50 dark:bg-lime-500/10 text-lime-700 dark:text-lime-300';
    case 'config':
      return 'bg-zinc-100 dark:bg-zinc-700 text-zinc-700 dark:text-zinc-200';
    case 'cerebellum':
      return 'bg-indigo-50 dark:bg-indigo-500/10 text-indigo-700 dark:text-indigo-300';
    default:
      return 'bg-zinc-100 dark:bg-zinc-700 text-zinc-700 dark:text-zinc-200';
  }
}

function formatEventBody(log: AuditEvent): string {
  if (log.message && log.message.trim() !== '') {
    return log.message;
  }
  if (log.category === 'model' && log.type === 'text_delta') {
    const text = log.data?.text;
    if (typeof text === 'string' && text.trim() !== '') {
      return text;
    }
  }
  if (log.category === 'model' && log.type === 'reasoning_delta') {
    const text = log.data?.text;
    if (typeof text === 'string' && text.trim() !== '') {
      return text;
    }
  }
  if (log.category === 'model' && log.type === 'request_started' && log.data) {
    return JSON.stringify(log.data, null, 2);
  }
  if (log.data && Object.keys(log.data).length > 0) {
    return JSON.stringify(log.data, null, 2);
  }
  return `${log.category}.${log.type}`;
}

function formatMetaLine(log: AuditEvent): string[] {
  return [
    log.agentId ? `agent=${log.agentId}` : '',
    log.sessionKey ? `session=${log.sessionKey}` : '',
    log.runId ? `run=${log.runId}` : '',
    log.taskId ? `task=${log.taskId}` : '',
    log.toolName ? `tool=${log.toolName}` : '',
    log.channel ? `channel=${log.channel}` : '',
    log.level ? `level=${log.level}` : '',
  ].filter(Boolean);
}

export function AuditLogsView() {
  const { t } = useI18n();
  const [logs, setLogs] = useState<AuditEvent[]>([]);
  const [filters, setFilters] = useState<FilterState>(DEFAULT_FILTERS);
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(false);

  const requestBody = useMemo<AuditListRequest>(
    () => ({
      category: filters.category || undefined,
      type: filters.type || undefined,
      level: filters.level || undefined,
      text: filters.text || undefined,
      limit: 200,
    }),
    [filters],
  );

  const loadLogs = async (body: AuditListRequest) => {
    setLoading(true);
    setError('');
    try {
      const response = await apiPost<{ events: AuditEvent[] }>('/api/system/audit/list', body);
      setLogs((response.events || []).map((event) => normalizeAuditEvent(event)).reverse());
    } catch (err) {
      setError(err instanceof Error ? err.message : t('audit.loadError'));
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    let cancelled = false;
    const load = async () => {
      try {
        const response = await apiPost<{ events: AuditEvent[] }>('/api/system/audit/list', requestBody);
        if (!cancelled) {
          setLogs((response.events || []).map((event) => normalizeAuditEvent(event)).reverse());
          setError('');
        }
      } catch (err) {
        if (!cancelled) {
          setError(err instanceof Error ? err.message : t('audit.loadError'));
        }
      } finally {
        if (!cancelled) {
          setLoading(false);
        }
      }
    };
    setLoading(true);
    void load();
    return () => {
      cancelled = true;
    };
  }, [requestBody]);

  return (
    <div className="flex flex-col h-full bg-white dark:bg-zinc-950 text-zinc-900 dark:text-zinc-100 transition-colors">
      <PageHeader title={t('audit.title')} description={t('audit.desc')} />

      <div className="flex-1 overflow-y-auto p-6">
        <div className="space-y-4">
          <ErrorAlert message={error} />

          <div className="rounded-2xl border border-zinc-200 bg-white p-4 shadow-sm dark:border-zinc-800 dark:bg-zinc-900">
            <div className="flex items-center justify-between gap-4 mb-4">
              <h3 className="text-lg font-medium text-zinc-900 dark:text-zinc-100">{t('audit.trail')}</h3>
              <div className="flex items-center gap-2">
                <button
                  onClick={() => void loadLogs(requestBody)}
                  className="p-2 text-zinc-400 hover:text-zinc-600 dark:hover:text-zinc-200 transition-colors rounded-lg hover:bg-zinc-100 dark:hover:bg-zinc-800"
                >
                  <RefreshCw className={`w-4 h-4 ${loading ? 'animate-spin' : ''}`} />
                </button>
              </div>
            </div>

            <div className="grid gap-3 md:grid-cols-4">
              <Select
                value={filters.category}
                onChange={(val) => setFilters((prev) => ({ ...prev, category: val }))}
                placeholder={t('audit.allCategories')}
                options={CATEGORY_OPTIONS.filter(Boolean).map((option) => ({
                  value: option,
                  label: option,
                }))}
                className="rounded-xl bg-white dark:bg-zinc-950"
              />

              <input
                value={filters.type}
                onChange={(event) => setFilters((prev) => ({ ...prev, type: event.target.value }))}
                placeholder={t('audit.filterType')}
                className="rounded-xl border border-zinc-200 bg-white px-3 py-2 text-sm text-zinc-700 placeholder:text-zinc-400 dark:border-zinc-700 dark:bg-zinc-950 dark:text-zinc-200"
              />

              <Select
                value={filters.level}
                onChange={(val) => setFilters((prev) => ({ ...prev, level: val }))}
                placeholder={t('audit.allLevels')}
                options={LEVEL_OPTIONS.filter(Boolean).map((option) => ({
                  value: option,
                  label: option,
                }))}
                className="rounded-xl bg-white dark:bg-zinc-950"
              />

              <input
                value={filters.text}
                onChange={(event) => setFilters((prev) => ({ ...prev, text: event.target.value }))}
                placeholder={t('audit.searchPlaceholder')}
                className="rounded-xl border border-zinc-200 bg-white px-3 py-2 text-sm text-zinc-700 placeholder:text-zinc-400 dark:border-zinc-700 dark:bg-zinc-950 dark:text-zinc-200"
              />
            </div>
          </div>

          <div className="space-y-3">
            {logs.map((log, index) => {
              const body = formatEventBody(log);
              const meta = formatMetaLine(log);
              return (
                <div key={`${log.occurredAt || 'event'}-${index}`} className="bg-white dark:bg-zinc-900 border border-zinc-200 dark:border-zinc-800 rounded-xl p-4 flex items-start gap-4 shadow-sm">
                  <div className="mt-1 text-zinc-500 font-mono text-xs w-28 shrink-0">
                    {log.occurredAt ? new Date(log.occurredAt).toLocaleTimeString() : '--'}
                  </div>
                  <div className="flex-1 min-w-0">
                    <div className="flex flex-wrap items-center gap-2 mb-2">
                      <span className={`text-xs font-medium px-2 py-0.5 rounded-full ${eventBadgeClass(log.category)}`}>
                        {log.category}.{log.type}
                      </span>
                      {log.level ? (
                        <span className="text-[11px] uppercase tracking-wide text-zinc-500 dark:text-zinc-400">{log.level}</span>
                      ) : null}
                    </div>
                    {meta.length > 0 ? (
                      <p className="mb-2 font-mono text-[11px] text-zinc-500 dark:text-zinc-400 break-all">{meta.join('  ')}</p>
                    ) : null}
                    <pre className="whitespace-pre-wrap break-words text-sm text-zinc-700 dark:text-zinc-300 font-mono">{body}</pre>
                  </div>
                </div>
              );
            })}
            {!loading && logs.length === 0 ? (
              <div className="rounded-xl border border-dashed border-zinc-300 px-4 py-8 text-center text-sm text-zinc-500 dark:border-zinc-700 dark:text-zinc-400">
                {t('audit.noEvents')}
              </div>
            ) : null}
          </div>
        </div>
      </div>
    </div>
  );
}
