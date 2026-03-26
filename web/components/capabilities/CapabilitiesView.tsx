'use client';

import { useCallback, useEffect, useState } from 'react';
import { Check, FolderOpen, Loader2, Plus, Puzzle, RefreshCw, Radar, X } from 'lucide-react';
import { useI18n } from '@/lib/i18n/I18nContext';
import { ErrorAlert, PageHeader, ToggleSwitch } from '@/components/ui';
import { apiGet, apiPost, type CapabilitiesState } from '@/lib/api';
import { SkillFilesModal } from './SkillFilesModal';
import { AddSkillModal } from './AddSkillModal';

export function CapabilitiesView() {
  const { t } = useI18n();
  const [state, setState] = useState<CapabilitiesState | null>(null);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [refreshing, setRefreshing] = useState(false);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [filesModal, setFilesModal] = useState<{ skillName: string; baseDir: string } | null>(null);
  const [addSkillOpen, setAddSkillOpen] = useState(false);

  const loadState = useCallback(async (options?: { silent?: boolean; poll?: boolean }) => {
    const silent = options?.silent ?? false;
    const poll = options?.poll ?? false;
    const currentVersion = state?.skills.version ?? 0;
    if (!silent) {
      setError('');
    }
    if (silent) {
      setRefreshing(true);
    }
    try {
      const next = await apiGet<CapabilitiesState>('/api/engine/capabilities');
      const nextVersion = next.skills.version ?? 0;
      if (poll && currentVersion > 0 && nextVersion > currentVersion) {
        setNotice(`Skills refreshed automatically to version ${nextVersion}.`);
      }
      setState(next);
    } catch (err) {
      if (!silent) {
        setError(err instanceof Error ? err.message : t('cap.loadError'));
      }
    } finally {
      setLoading(false);
      setRefreshing(false);
    }
  }, [state?.skills.version, t]);

  useEffect(() => {
    void loadState({ silent: false });
  }, [loadState]);

  useEffect(() => {
    const syncSkills = () => {
      if (document.hidden || saving) {
        return;
      }
      void loadState({ silent: true, poll: true });
    };

    const interval = window.setInterval(syncSkills, 5000);
    const handleVisibilityChange = () => {
      if (!document.hidden) {
        syncSkills();
      }
    };

    document.addEventListener('visibilitychange', handleVisibilityChange);
    return () => {
      window.clearInterval(interval);
      document.removeEventListener('visibilitychange', handleVisibilityChange);
    };
  }, [loadState, saving]);

  const toggleSkill = async (skillKey: string, enabled: boolean) => {
    if (!state) return;
    setSaving(true);
    setError('');
    setNotice('');
    try {
      const entries = { ...(state.skillsConfig.entries || {}) };
      entries[skillKey] = { ...(entries[skillKey] || {}), enabled };
      const next = await apiPost<CapabilitiesState>('/api/engine/capabilities/save', {
        skills: {
          ...state.skillsConfig,
          entries,
        },
      });
      setState(next);
    } catch (err) {
      setError(err instanceof Error ? err.message : t('cap.saveError'));
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="flex flex-col h-full bg-white dark:bg-zinc-950 text-zinc-900 dark:text-zinc-100 transition-colors">
      <PageHeader
        title={t('cap.title')}
        description={t('cap.desc')}
        meta={(
          <div className="mt-2 flex flex-wrap items-center gap-2 text-xs text-zinc-500 dark:text-zinc-400">
            <span className="inline-flex items-center gap-1 rounded-full bg-zinc-100 px-2.5 py-1 dark:bg-zinc-900">
              <Radar className={`h-3.5 w-3.5 ${refreshing ? 'animate-pulse' : ''}`} />
              Auto refresh on
            </span>
            <span className="rounded-full bg-zinc-100 px-2.5 py-1 dark:bg-zinc-900">
              Version {state?.skills.version ?? 0}
            </span>
            {state?.skills.workspaceDir ? (
              <span className="max-w-[20ch] truncate rounded-full bg-zinc-100 px-2.5 py-1 dark:bg-zinc-900" title={state.skills.workspaceDir}>
                {state.skills.workspaceDir}
              </span>
            ) : null}
          </div>
        )}
      >
        <button
          onClick={() => void loadState({ silent: false })}
          disabled={refreshing}
          className="rounded-lg border border-zinc-200 px-3 py-2 text-sm font-medium text-zinc-700 transition-colors hover:bg-zinc-100 dark:border-zinc-800 dark:text-zinc-200 dark:hover:bg-zinc-800"
        >
          <RefreshCw className={`h-4 w-4 ${refreshing ? 'animate-spin' : ''}`} />
        </button>
        <button
          onClick={() => setAddSkillOpen(true)}
          className="flex items-center gap-2 whitespace-nowrap rounded-lg bg-indigo-600 px-3 py-2 text-sm font-medium text-white transition-colors hover:bg-indigo-500"
        >
          <Plus className="h-4 w-4" />
          {t('cap.addSkill')}
        </button>
      </PageHeader>

      <div className="flex-1 overflow-y-auto p-6">
        <ErrorAlert message={error} className="mb-4" />
        {notice ? (
          <div className="mb-4 rounded-lg border border-emerald-200 bg-emerald-50 px-3 py-2 text-sm text-emerald-700 dark:border-emerald-900/40 dark:bg-emerald-950/30 dark:text-emerald-300">
            {notice}
          </div>
        ) : null}

        {loading ? (
          <div className="flex h-full items-center justify-center text-zinc-500 dark:text-zinc-400">
            <Loader2 className="mr-2 h-5 w-5 animate-spin" />
            {t('cap.loading')}
          </div>
        ) : (
          <div className="space-y-8">
            <section>
              <div className="mb-4 flex items-center gap-2">
                <Puzzle className="h-5 w-5 text-indigo-500" />
                <h3 className="text-lg font-medium text-zinc-900 dark:text-zinc-100">{t('cap.skillsSection')}</h3>
              </div>
              <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
                {(state?.skills.skills || []).map((skill) => {
                  const enabled = !skill.disabled;
                  const skillKey = skill.skillKey || skill.name;
                  return (
                    <div key={skillKey} className="bg-white dark:bg-zinc-900 border border-zinc-200 dark:border-zinc-800 rounded-xl p-4 flex flex-col justify-between h-full shadow-sm">
                      <div>
                        <div className="mb-3 flex items-center justify-between">
                          <div className="rounded-lg bg-indigo-50 p-1.5 dark:bg-indigo-500/10">
                            <Puzzle className="h-5 w-5 text-indigo-600 dark:text-indigo-400" />
                          </div>
                          <ToggleSwitch
                            checked={enabled}
                            onChange={(next) => void toggleSkill(skillKey, next)}
                            disabled={saving}
                          />
                        </div>
                        <h4 className="font-medium text-zinc-900 dark:text-zinc-100 mb-2">{skill.name}</h4>
                        <p className="text-sm text-zinc-600 dark:text-zinc-400 leading-relaxed line-clamp-3" title={skill.description || ''}>{skill.description || t('cap.noDescription')}</p>
                        {skill.missingEnv?.length ? (
                          <p className="mt-3 text-xs text-rose-600 dark:text-rose-300 truncate" title={`${t('cap.missingEnv')}: ${skill.missingEnv.join(', ')}`}>{t('cap.missingEnv')}: {skill.missingEnv.join(', ')}</p>
                        ) : null}
                        {skill.missingConfig?.length ? (
                          <p className="mt-2 text-xs text-amber-600 dark:text-amber-300 truncate" title={`${t('cap.missingConfig')}: ${skill.missingConfig.join(', ')}`}>{t('cap.missingConfig')}: {skill.missingConfig.join(', ')}</p>
                        ) : null}
                      </div>
                      <div className="mt-3 pt-3 border-t border-zinc-200 dark:border-zinc-800/50 flex items-center justify-between">
                        {enabled ? (
                          <span className="flex items-center gap-1 text-xs font-medium text-emerald-600 dark:text-emerald-400">
                            <Check className="h-3 w-3" /> {t('cap.active')}
                          </span>
                        ) : (
                          <span className="flex items-center gap-1 text-xs font-medium text-zinc-500">
                            <X className="h-3 w-3" /> {t('cap.disabled')}
                          </span>
                        )}
                        {skill.baseDir ? (
                          <button
                            onClick={() => setFilesModal({ skillName: skill.name, baseDir: skill.baseDir! })}
                            className="flex items-center gap-1 text-xs font-medium text-indigo-600 dark:text-indigo-400 hover:text-indigo-500 dark:hover:text-indigo-300 transition-colors"
                          >
                            <FolderOpen className="h-3 w-3" />
                            {t('cap.viewFiles')}
                          </button>
                        ) : null}
                      </div>
                    </div>
                  );
                })}
              </div>
            </section>
          </div>
        )}
      </div>

      <SkillFilesModal
        isOpen={filesModal !== null}
        onClose={() => setFilesModal(null)}
        skillName={filesModal?.skillName || ''}
        baseDir={filesModal?.baseDir || ''}
      />

      <AddSkillModal
        isOpen={addSkillOpen}
        onClose={() => setAddSkillOpen(false)}
        onImported={(next) => { setState(next); setAddSkillOpen(false); }}
      />
    </div>
  );
}
