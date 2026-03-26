'use client';

import { useEffect, useState } from 'react';
import { Check, Loader2, RefreshCw, Wrench, X } from 'lucide-react';
import { useI18n } from '@/lib/i18n/I18nContext';
import { ErrorAlert, PageHeader, ToggleSwitch } from '@/components/ui';
import { apiGet, apiPost, type CapabilitiesState } from '@/lib/api';

export function ToolsView() {
  const { t } = useI18n();
  const [state, setState] = useState<CapabilitiesState | null>(null);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');

  const loadState = async () => {
    setError('');
    try {
      const next = await apiGet<CapabilitiesState>('/api/engine/capabilities');
      setState(next);
    } catch (err) {
      setError(err instanceof Error ? err.message : t('tools.loadError'));
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    void loadState();
  }, []);

  const toggleTool = async (toolName: string, enabled: boolean) => {
    setSaving(true);
    setError('');
    try {
      const next = await apiPost<CapabilitiesState>('/api/engine/capabilities/save', {
        toolToggles: {
          [toolName]: enabled,
        },
      });
      setState(next);
    } catch (err) {
      setError(err instanceof Error ? err.message : t('tools.saveError'));
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="flex h-full flex-col bg-white text-zinc-900 transition-colors dark:bg-zinc-950 dark:text-zinc-100">
      <PageHeader title={t('tools.title')} description={t('tools.desc')}>
        <button
          onClick={() => void loadState()}
          className="rounded-lg border border-zinc-200 px-3 py-2 text-sm font-medium text-zinc-700 transition-colors hover:bg-zinc-100 dark:border-zinc-800 dark:text-zinc-200 dark:hover:bg-zinc-800"
        >
          <RefreshCw className="h-4 w-4" />
        </button>
      </PageHeader>

      <div className="flex-1 overflow-y-auto p-6">
        <ErrorAlert message={error} className="mb-4" />

        {loading ? (
          <div className="flex h-full items-center justify-center text-zinc-500 dark:text-zinc-400">
            <Loader2 className="mr-2 h-5 w-5 animate-spin" />
            {t('tools.loading')}
          </div>
        ) : (
          <div className="space-y-4">
            <div className="mb-4 flex items-center gap-2">
              <Wrench className="h-5 w-5 text-indigo-500" />
              <h3 className="text-lg font-medium text-zinc-900 dark:text-zinc-100">{t('tools.sectionTitle')}</h3>
            </div>
            <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
              {(state?.tools || []).map((tool) => (
                <div key={tool.name} className="flex h-56 flex-col overflow-hidden rounded-xl border border-zinc-200 bg-white p-4 shadow-sm dark:border-zinc-800 dark:bg-zinc-900">
                  <div className="mb-2 flex items-center justify-between gap-3">
                    <h4 className="truncate font-medium text-zinc-900 dark:text-zinc-100" title={tool.name}>{tool.name}</h4>
                    <ToggleSwitch
                      checked={tool.allowed}
                      onChange={(next) => void toggleTool(tool.name, next)}
                      disabled={saving}
                    />
                  </div>
                  <p
                    className="line-clamp-3 min-h-[3.75rem] text-sm text-zinc-600 dark:text-zinc-400"
                    title={tool.description || t('tools.noDescription')}
                  >
                    {tool.description || t('tools.noDescription')}
                  </p>
                  <div className="mt-3 flex min-h-0 flex-wrap content-start gap-2 overflow-hidden">
                    {tool.elevated ? <span className="rounded-full bg-amber-500/10 px-2 py-1 text-xs text-amber-600 dark:text-amber-300">{t('tools.elevated')}</span> : null}
                    {tool.ownerOnly ? <span className="rounded-full bg-zinc-100 px-2 py-1 text-xs text-zinc-600 dark:bg-zinc-800 dark:text-zinc-300">{t('tools.ownerOnly')}</span> : null}
                    {tool.pluginId ? (
                      <span
                        className="max-w-full truncate rounded-full bg-indigo-500/10 px-2 py-1 text-xs text-indigo-600 dark:text-indigo-300"
                        title={`${t('tools.plugin')}: ${tool.pluginId}`}
                      >
                        {t('tools.plugin')}: {tool.pluginId}
                      </span>
                    ) : null}
                  </div>
                  <div className="mt-auto border-t border-zinc-200 pt-4 dark:border-zinc-800/50">
                    <span className={`flex items-center gap-1 text-xs font-medium ${tool.allowed ? 'text-emerald-600 dark:text-emerald-400' : 'text-zinc-500'}`}>
                      {tool.allowed ? <Check className="h-3 w-3" /> : <X className="h-3 w-3" />}
                      {tool.allowed ? t('tools.allowed') : t('tools.blocked')}
                    </span>
                  </div>
                </div>
              ))}
            </div>
          </div>
        )}
      </div>
    </div>
  );
}
