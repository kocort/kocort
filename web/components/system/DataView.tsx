'use client';

import { useEffect, useMemo, useState } from 'react';
import { FileText, Loader2, RefreshCw, Save } from 'lucide-react';
import { useI18n } from '@/lib/i18n/I18nContext';
import { ErrorAlert, PageHeader } from '@/components/ui';
import { apiGet, apiPost, type DataState } from '@/lib/api';

export function DataView() {
  const { t } = useI18n();
  const [state, setState] = useState<DataState | null>(null);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const [systemPrompt, setSystemPrompt] = useState('');
  const [files, setFiles] = useState<Record<string, string>>({});

  // 只显示已存在的文件（被智能体注入的文件）
  const orderedFiles = useMemo(() => (state?.files || []).filter(file => file.exists), [state]);

  const loadState = async () => {
    setError('');
    try {
      const next = await apiGet<DataState>('/api/engine/data');
      setState(next);
      setSystemPrompt(next.systemPrompt || '');
      setFiles(
        Object.fromEntries((next.files || []).map((file) => [file.name, file.content || ''])),
      );
    } catch (err) {
      setError(err instanceof Error ? err.message : t('data.loadError'));
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    void loadState();
  }, []);

  const saveAll = async () => {
    setSaving(true);
    setError('');
    try {
      const next = await apiPost<DataState>('/api/engine/data/save', {
        systemPrompt,
        files: orderedFiles.map((file) => ({
          name: file.name,
          content: files[file.name] ?? '',
        })),
      });
      setState(next);
      setSystemPrompt(next.systemPrompt || '');
      setFiles(
        Object.fromEntries((next.files || []).map((file) => [file.name, file.content || ''])),
      );
    } catch (err) {
      setError(err instanceof Error ? err.message : t('data.saveError'));
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="flex h-full flex-col bg-white text-zinc-900 transition-colors dark:bg-zinc-950 dark:text-zinc-100">
      <PageHeader
        title={t('data.title')}
        description={t('data.desc')}
        meta={
          <p className="mt-1 text-xs text-zinc-400">
            {t('data.agentMeta', { agent: state?.defaultAgent || 'main' })} · {t('data.workspaceMeta', { workspace: state?.workspace || '-' })}
          </p>
        }
      >
        <button
          onClick={() => void loadState()}
          className="rounded-lg border border-zinc-200 px-3 py-2 text-sm font-medium text-zinc-700 transition-colors hover:bg-zinc-100 dark:border-zinc-800 dark:text-zinc-200 dark:hover:bg-zinc-800"
        >
          <RefreshCw className="h-4 w-4" />
        </button>
        <button
          onClick={() => void saveAll()}
          disabled={saving}
          className="flex items-center gap-2 rounded-lg bg-indigo-600 px-3 py-2 text-sm font-medium text-white transition-colors hover:bg-indigo-500 disabled:cursor-not-allowed disabled:opacity-50"
        >
          {saving ? <Loader2 className="h-4 w-4 animate-spin" /> : <Save className="h-4 w-4" />}
          {t('common.save')}
        </button>
      </PageHeader>

      <div className="flex-1 space-y-8 overflow-y-auto p-6">
        <ErrorAlert message={error} />

        {loading ? (
          <div className="flex items-center justify-center rounded-xl border border-zinc-200 bg-white p-8 text-zinc-500 dark:border-zinc-800 dark:bg-zinc-900 dark:text-zinc-400">
            <Loader2 className="mr-2 h-5 w-5 animate-spin" />
            {t('data.loading')}
          </div>
        ) : (
          <>
            <section className="rounded-xl border border-zinc-200 bg-white p-5 shadow-sm dark:border-zinc-800 dark:bg-zinc-900">
              <div className="mb-4">
                <h3 className="text-lg font-medium text-zinc-900 dark:text-zinc-100">{t('data.systemPrompt')}</h3>
                <p className="text-sm text-zinc-500 dark:text-zinc-400">{t('data.systemPromptDesc')}</p>
              </div>
              <textarea
                value={systemPrompt}
                onChange={(e) => setSystemPrompt(e.target.value)}
                className="min-h-[180px] w-full rounded-xl border border-zinc-200 bg-zinc-50 px-4 py-3 text-sm outline-none transition-all focus:border-indigo-500 focus:ring-1 focus:ring-indigo-500 dark:border-zinc-800 dark:bg-zinc-950 dark:text-zinc-200"
              />
            </section>

            <section className="space-y-4">
              <div>
                <h3 className="text-lg font-medium text-zinc-900 dark:text-zinc-100">{t('data.contextFiles')}</h3>
                <p className="text-sm text-zinc-500 dark:text-zinc-400">{t('data.contextFilesDesc')}</p>
              </div>
              {orderedFiles.map((file) => (
                <div key={file.name} className="rounded-xl border border-zinc-200 bg-white p-5 shadow-sm dark:border-zinc-800 dark:bg-zinc-900">
                  <div className="mb-3 flex items-center gap-3">
                    <div className="rounded-lg bg-zinc-100 p-2 dark:bg-zinc-800">
                      <FileText className="h-5 w-5 text-indigo-600 dark:text-indigo-400" />
                    </div>
                    <div>
                      <h4 className="font-medium text-zinc-900 dark:text-zinc-100">{file.name}</h4>
                      <p className="text-xs text-zinc-500">{file.exists ? t('data.existingFile') : t('data.newFile')}</p>
                    </div>
                  </div>
                  <textarea
                    value={files[file.name] ?? ''}
                    onChange={(e) => setFiles((current) => ({ ...current, [file.name]: e.target.value }))}
                    className="min-h-[180px] w-full rounded-xl border border-zinc-200 bg-zinc-50 px-4 py-3 font-mono text-sm outline-none transition-all focus:border-indigo-500 focus:ring-1 focus:ring-indigo-500 dark:border-zinc-800 dark:bg-zinc-950 dark:text-zinc-200"
                    placeholder={t('data.filePlaceholder', { file: file.name })}
                  />
                </div>
              ))}
            </section>
          </>
        )}
      </div>
    </div>
  );
}
