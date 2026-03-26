'use client';

import { useEffect, useState } from 'react';
import { Activity, Server } from 'lucide-react';
import { useI18n } from '@/lib/i18n/I18nContext';
import { ErrorAlert, PageHeader } from '@/components/ui';
import { apiGet, type DashboardSnapshot } from '@/lib/api';

export function DashboardView() {
  const { t } = useI18n();
  const [snapshot, setSnapshot] = useState<DashboardSnapshot | null>(null);
  const [error, setError] = useState('');

  useEffect(() => {
    let cancelled = false;
    const load = async () => {
      try {
        const next = await apiGet<DashboardSnapshot>('/api/system/dashboard');
        if (!cancelled) {
          setSnapshot(next);
          setError('');
        }
      } catch (err) {
        if (!cancelled) {
          setError(err instanceof Error ? err.message : t('dash.loadError'));
        }
      }
    };
    void load();
    const timer = window.setInterval(() => void load(), 5000);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, []);

  return (
    <div className="flex flex-col h-full bg-white dark:bg-zinc-950 text-zinc-900 dark:text-zinc-100 transition-colors">
      <PageHeader title={t('dash.title')} description={t('dash.desc')} />

      <div className="flex-1 overflow-y-auto p-6">
        <div className="space-y-6">
          <ErrorAlert message={error} />
          <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
            <div className="bg-white dark:bg-zinc-900 border border-zinc-200 dark:border-zinc-800 rounded-xl p-5 shadow-sm">
              <div className="flex items-center justify-between mb-4">
                <h3 className="text-sm font-medium text-zinc-500 dark:text-zinc-400">{t('dash.gateway')}</h3>
                <div className={`w-2 h-2 rounded-full ${snapshot?.runtime.healthy ? 'bg-emerald-500 shadow-[0_0_8px_rgba(16,185,129,0.5)] animate-pulse' : 'bg-rose-500'}`} />
              </div>
              <p className="text-2xl font-semibold text-zinc-900 dark:text-zinc-100">{snapshot?.runtime.healthy ? t('dash.online') : t('dash.offline')}</p>
              <p className="text-xs text-zinc-500 mt-1 font-mono">agent={snapshot?.runtime.configuredAgent || 'main'}</p>
            </div>

            <div className="bg-white dark:bg-zinc-900 border border-zinc-200 dark:border-zinc-800 rounded-xl p-5 shadow-sm">
              <div className="flex items-center justify-between mb-4">
                <h3 className="text-sm font-medium text-zinc-500 dark:text-zinc-400">{t('dash.local')}</h3>
                <Server className="w-4 h-4 text-indigo-500 dark:text-indigo-400" />
              </div>
              <p className="text-2xl font-semibold text-zinc-900 dark:text-zinc-100">{snapshot?.activeRuns.total || 0}</p>
              <p className="text-xs text-zinc-500 mt-1 font-mono">{t('dash.activeRuns')}</p>
            </div>

            <div className="bg-white dark:bg-zinc-900 border border-zinc-200 dark:border-zinc-800 rounded-xl p-5 shadow-sm">
              <div className="flex items-center justify-between mb-4">
                <h3 className="text-sm font-medium text-zinc-500 dark:text-zinc-400">{t('dash.cloud')}</h3>
                <Activity className="w-4 h-4 text-emerald-500 dark:text-emerald-400" />
              </div>
              <p className="text-2xl font-semibold text-zinc-900 dark:text-zinc-100">{snapshot?.providers?.length || 0}</p>
              <p className="text-xs text-zinc-500 mt-1 font-mono">{t('dash.providers')}</p>
            </div>
          </div>

          <div className="bg-white dark:bg-zinc-900 border border-zinc-200 dark:border-zinc-800 rounded-xl p-6 shadow-sm">
            <h3 className="text-base font-semibold text-zinc-900 dark:text-zinc-100 mb-6 flex items-center gap-2">
              <Activity className="w-5 h-5 text-indigo-600 dark:text-indigo-400" />
              {t('dash.runtimeSummary')}
            </h3>
            <div className="grid grid-cols-1 gap-6 md:grid-cols-2">
              {/* Tasks Section */}
              <div className="rounded-xl border border-zinc-200 bg-gradient-to-br from-white to-zinc-50 p-5 dark:border-zinc-800 dark:from-zinc-900 dark:to-zinc-900/50">
                <div className="flex items-center justify-between mb-4">
                  <p className="text-xs font-semibold uppercase tracking-wider text-zinc-500 dark:text-zinc-400">{t('dash.tasks')}</p>
                  <div className="rounded-full bg-indigo-500/10 dark:bg-indigo-400/10 px-2 py-1">
                    <span className="text-xs font-bold text-indigo-600 dark:text-indigo-400">
                      {(snapshot?.tasks.running || 0) + (snapshot?.tasks.queued || 0) + (snapshot?.tasks.scheduled || 0) + (snapshot?.tasks.failed || 0)}
                    </span>
                  </div>
                </div>
                <div className="space-y-3">
                  <div className="flex items-center justify-between py-2 border-b border-zinc-100 dark:border-zinc-800/50">
                    <span className="text-sm text-zinc-700 dark:text-zinc-300 flex items-center gap-2">
                      <div className="w-2 h-2 rounded-full bg-emerald-500 animate-pulse" />
                      {t('dash.running')}
                    </span>
                    <span className="text-lg font-bold text-emerald-600 dark:text-emerald-400 tabular-nums">
                      {snapshot?.tasks.running || 0}
                    </span>
                  </div>
                  <div className="flex items-center justify-between py-2 border-b border-zinc-100 dark:border-zinc-800/50">
                    <span className="text-sm text-zinc-700 dark:text-zinc-300 flex items-center gap-2">
                      <div className="w-2 h-2 rounded-full bg-amber-500" />
                      {t('dash.queued')}
                    </span>
                    <span className="text-lg font-bold text-amber-600 dark:text-amber-400 tabular-nums">
                      {snapshot?.tasks.queued || 0}
                    </span>
                  </div>
                  <div className="flex items-center justify-between py-2 border-b border-zinc-100 dark:border-zinc-800/50">
                    <span className="text-sm text-zinc-700 dark:text-zinc-300 flex items-center gap-2">
                      <div className="w-2 h-2 rounded-full bg-blue-500" />
                      {t('dash.scheduled')}
                    </span>
                    <span className="text-lg font-bold text-blue-600 dark:text-blue-400 tabular-nums">
                      {snapshot?.tasks.scheduled || 0}
                    </span>
                  </div>
                  <div className="flex items-center justify-between py-2">
                    <span className="text-sm text-zinc-700 dark:text-zinc-300 flex items-center gap-2">
                      <div className="w-2 h-2 rounded-full bg-rose-500" />
                      {t('dash.failed')}
                    </span>
                    <span className="text-lg font-bold text-rose-600 dark:text-rose-400 tabular-nums">
                      {snapshot?.tasks.failed || 0}
                    </span>
                  </div>
                </div>
              </div>

              {/* Delivery Queue Section */}
              <div className="rounded-xl border border-zinc-200 bg-gradient-to-br from-white to-zinc-50 p-5 dark:border-zinc-800 dark:from-zinc-900 dark:to-zinc-900/50">
                <div className="flex items-center justify-between mb-4">
                  <p className="text-xs font-semibold uppercase tracking-wider text-zinc-500 dark:text-zinc-400">{t('dash.deliveryQueue')}</p>
                  <div className="rounded-full bg-purple-500/10 dark:bg-purple-400/10 px-2 py-1">
                    <span className="text-xs font-bold text-purple-600 dark:text-purple-400">
                      {(snapshot?.deliveryQueue.pending || 0) + (snapshot?.deliveryQueue.failed || 0)}
                    </span>
                  </div>
                </div>
                <div className="space-y-3">
                  <div className="flex items-center justify-between py-2 border-b border-zinc-100 dark:border-zinc-800/50">
                    <span className="text-sm text-zinc-700 dark:text-zinc-300 flex items-center gap-2">
                      <div className="w-2 h-2 rounded-full bg-amber-500" />
                      {t('dash.pending')}
                    </span>
                    <span className="text-lg font-bold text-amber-600 dark:text-amber-400 tabular-nums">
                      {snapshot?.deliveryQueue.pending || 0}
                    </span>
                  </div>
                  <div className="flex items-center justify-between py-2 border-b border-zinc-100 dark:border-zinc-800/50">
                    <span className="text-sm text-zinc-700 dark:text-zinc-300 flex items-center gap-2">
                      <div className="w-2 h-2 rounded-full bg-rose-500" />
                      {t('dash.failed')}
                    </span>
                    <span className="text-lg font-bold text-rose-600 dark:text-rose-400 tabular-nums">
                      {snapshot?.deliveryQueue.failed || 0}
                    </span>
                  </div>
                  <div className="flex items-center justify-between py-2">
                    <span className="text-sm text-zinc-700 dark:text-zinc-300 flex items-center gap-2">
                      <div className="w-2 h-2 rounded-full bg-indigo-500" />
                      {t('dash.subagents')}
                    </span>
                    <span className="text-lg font-bold text-indigo-600 dark:text-indigo-400 tabular-nums">
                      {snapshot?.runtime.activeSubagents || 0}
                    </span>
                  </div>
                </div>
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}
