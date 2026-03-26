'use client';

import { useEffect, useMemo, useState } from 'react';
import { Folder, Plus, RefreshCw, Save, Trash2 } from 'lucide-react';
import { useI18n } from '@/lib/i18n/I18nContext';
import { DirectoryPickerField, ErrorAlert, PageHeader } from '@/components/ui';
import { apiGet, apiPost, type SandboxState } from '@/lib/api';

type AgentDraft = {
  sandboxEnabled: boolean;
  sandboxDirs: string[];
};

export function SandboxView() {
  const { t } = useI18n();
  const [state, setState] = useState<SandboxState | null>(null);
  const [drafts, setDrafts] = useState<Record<string, AgentDraft>>({});
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');

  const buildDrafts = (data: SandboxState): Record<string, AgentDraft> =>
    Object.fromEntries(
      data.agents.map((agent) => [
        agent.agentId,
        {
          sandboxEnabled: agent.sandboxEnabled ?? false,
          sandboxDirs: agent.sandboxDirs?.length ? [...agent.sandboxDirs] : [''],
        },
      ])
    );

  const loadState = async () => {
    setError('');
    try {
      const next = await apiGet<SandboxState>('/api/engine/sandbox');
      setState(next);
      setDrafts(buildDrafts(next));
    } catch (err) {
      setError(err instanceof Error ? err.message : t('sandbox.loadError'));
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    void loadState();
  }, []);

  const hasChanges = useMemo(() => {
    if (!state) return false;
    return state.agents.some((agent) => {
      const draft = drafts[agent.agentId];
      if (!draft) return false;
      const origEnabled = agent.sandboxEnabled ?? false;
      const origDirs = agent.sandboxDirs ?? [];
      if (draft.sandboxEnabled !== origEnabled) return true;
      const draftDirs = draft.sandboxDirs.filter((d) => d.trim() !== '');
      if (draftDirs.length !== origDirs.length) return true;
      return draftDirs.some((d, i) => d !== origDirs[i]);
    });
  }, [drafts, state]);

  const persist = async () => {
    if (!state) return;
    setSaving(true);
    setError('');
    try {
      const saved = await apiPost<SandboxState>('/api/engine/sandbox/save', {
        agents: state.agents.map((agent) => {
          const draft = drafts[agent.agentId];
          return {
            agentId: agent.agentId,
            sandboxEnabled: draft?.sandboxEnabled ?? false,
            sandboxDirs: (draft?.sandboxDirs ?? []).filter((d) => d.trim() !== ''),
          };
        }),
      });
      setState(saved);
      setDrafts(buildDrafts(saved));
    } catch (err) {
      setError(err instanceof Error ? err.message : t('sandbox.saveError'));
    } finally {
      setSaving(false);
    }
  };

  const updateDraft = (agentId: string, patch: Partial<AgentDraft>) => {
    setDrafts((prev) => ({
      ...prev,
      [agentId]: { ...prev[agentId], ...patch },
    }));
  };

  const updateDir = (agentId: string, index: number, value: string) => {
    setDrafts((prev) => {
      const dirs = [...(prev[agentId]?.sandboxDirs ?? [''])];
      dirs[index] = value;
      return { ...prev, [agentId]: { ...prev[agentId], sandboxDirs: dirs } };
    });
  };

  const addDir = (agentId: string) => {
    setDrafts((prev) => {
      const dirs = [...(prev[agentId]?.sandboxDirs ?? ['']), ''];
      return { ...prev, [agentId]: { ...prev[agentId], sandboxDirs: dirs } };
    });
  };

  const removeDir = (agentId: string, index: number) => {
    setDrafts((prev) => {
      const dirs = [...(prev[agentId]?.sandboxDirs ?? [''])];
      dirs.splice(index, 1);
      if (dirs.length === 0) dirs.push('');
      return { ...prev, [agentId]: { ...prev[agentId], sandboxDirs: dirs } };
    });
  };

  return (
    <div className="flex flex-col h-full bg-white dark:bg-zinc-950 text-zinc-900 dark:text-zinc-100 transition-colors">
      <PageHeader title={t('sandbox.title')} description={t('sandbox.desc')}>
        <button
          onClick={() => void loadState()}
          className="rounded-lg border border-zinc-200 px-3 py-2 text-sm font-medium text-zinc-700 transition-colors hover:bg-zinc-100 dark:border-zinc-800 dark:text-zinc-200 dark:hover:bg-zinc-800"
        >
          <RefreshCw className="h-4 w-4" />
        </button>
        <button
          onClick={() => void persist()}
          disabled={saving || !hasChanges}
          className="inline-flex items-center gap-2 rounded-lg bg-indigo-600 px-3 py-2 text-sm font-medium text-white transition-colors hover:bg-indigo-500 disabled:cursor-not-allowed disabled:opacity-50"
        >
          <Save className="h-4 w-4" />
          {saving ? t('common.saving') : t('common.save')}
        </button>
      </PageHeader>

      <div className="flex-1 overflow-y-auto p-6">
        <div className="space-y-6">
          {error ? (
            <ErrorAlert message={error} />
          ) : null}
          <div>
            <h3 className="text-lg font-medium text-zinc-900 dark:text-zinc-100 mb-6">{t('sandbox.visual')}</h3>
          </div>

          {loading || !state ? (
            <div className="rounded-xl border border-zinc-200 bg-white p-6 text-sm text-zinc-500 shadow-sm dark:border-zinc-800 dark:bg-zinc-900 dark:text-zinc-400">
              {t('sandbox.loading')}
            </div>
          ) : (
            <div className="space-y-4">
              {state.agents.map((agent) => {
                const draft = drafts[agent.agentId];
                if (!draft) return null;
                return (
                  <div key={agent.agentId} className="rounded-xl border border-zinc-200 bg-white p-5 shadow-sm dark:border-zinc-800 dark:bg-zinc-900">
                    <div className="mb-3 flex items-center gap-3">
                      <Folder className="w-5 h-5 text-indigo-500 dark:text-indigo-400" />
                      <div className="flex-1">
                        <p className="font-medium text-zinc-900 dark:text-zinc-200">{agent.agentId}</p>
                        {agent.agentDir ? (
                          <p className="text-xs text-zinc-500 font-mono mt-0.5">{t('sandbox.agentDir', { value: agent.agentDir })}</p>
                        ) : null}
                      </div>
                      <label className="relative inline-flex cursor-pointer items-center gap-2">
                        <input
                          type="checkbox"
                          checked={draft.sandboxEnabled}
                          onChange={(e) => updateDraft(agent.agentId, { sandboxEnabled: e.target.checked })}
                          disabled={saving}
                          className="sr-only peer"
                        />
                        <div className="h-5 w-9 rounded-full bg-zinc-200 after:absolute after:left-[2px] after:top-[2px] after:h-4 after:w-4 after:rounded-full after:border after:border-zinc-300 after:bg-white after:transition-all after:content-[''] peer-checked:bg-indigo-600 peer-checked:after:translate-x-full peer-checked:after:border-white dark:border-zinc-600 dark:bg-zinc-700" />
                        <span className="text-sm font-medium text-zinc-700 dark:text-zinc-300">{t('sandbox.enableToggle')}</span>
                      </label>
                    </div>

                    <div className={`transition-opacity ${draft.sandboxEnabled ? 'opacity-100' : 'opacity-40 pointer-events-none'}`}>
                      <div className="mb-4">
                        <label className="mb-2 block text-sm font-medium text-zinc-700 dark:text-zinc-300">{t('sandbox.workspaceDir')}</label>
                        <input
                          type="text"
                          value={agent.workspaceDir || ''}
                          readOnly
                          className="w-full rounded-lg border border-zinc-200 bg-zinc-100 px-3 py-2 text-sm text-zinc-500 outline-none dark:border-zinc-800 dark:bg-zinc-950 dark:text-zinc-400"
                        />
                        <p className="mt-1 text-xs text-zinc-500 dark:text-zinc-400">{t('sandbox.workspaceDirHint')}</p>
                      </div>
                      <label className="mb-2 block text-sm font-medium text-zinc-700 dark:text-zinc-300">{t('sandbox.sandboxDirs')}</label>
                      <div className="space-y-2">
                        {draft.sandboxDirs.map((dir, index) => (
                          <div key={index} className="flex items-center gap-2">
                            <DirectoryPickerField
                              value={dir}
                              onChange={(value) => updateDir(agent.agentId, index, value)}
                              placeholder={t('sandbox.sandboxDirPlaceholder')}
                              browseLabel={t('sandbox.selectDir')}
                              browsePrompt={t('sandbox.selectDirPrompt')}
                              disabled={saving || !draft.sandboxEnabled}
                              variant="inline"
                              className="flex-1"
                              onBrowseError={(message) => setError(message || t('sandbox.browseDirError'))}
                            />
                            {draft.sandboxDirs.length > 1 ? (
                              <button
                                onClick={() => removeDir(agent.agentId, index)}
                                disabled={saving}
                                className="rounded-lg border border-zinc-200 p-2 text-zinc-400 hover:text-red-500 dark:border-zinc-800 dark:hover:text-red-400"
                              >
                                <Trash2 className="h-4 w-4" />
                              </button>
                            ) : null}
                          </div>
                        ))}
                        <button
                          onClick={() => addDir(agent.agentId)}
                          disabled={saving}
                          className="inline-flex items-center gap-1.5 rounded-lg border border-dashed border-zinc-300 px-3 py-1.5 text-xs font-medium text-zinc-500 transition-colors hover:border-indigo-400 hover:text-indigo-600 dark:border-zinc-700 dark:text-zinc-400 dark:hover:border-indigo-500 dark:hover:text-indigo-400"
                        >
                          <Plus className="h-3.5 w-3.5" />
                          {t('sandbox.addDir')}
                        </button>
                      </div>
                    </div>
                  </div>
                );
              })}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
