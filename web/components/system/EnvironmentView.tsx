'use client';

import { useEffect, useState } from 'react';
import { Eye, EyeOff, Plus, Trash2 } from 'lucide-react';
import { useI18n } from '@/lib/i18n/I18nContext';
import { ErrorAlert, Modal, PageHeader, ToggleSwitch, ConfirmDialog } from '@/components/ui';
import { apiGet, apiPost, type EnvironmentState } from '@/lib/api';

export function EnvironmentView() {
  const { t } = useI18n();
  const [state, setState] = useState<EnvironmentState | null>(null);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const [visibleValues, setVisibleValues] = useState<Record<string, boolean>>({});
  const [isModalOpen, setIsModalOpen] = useState(false);
  const [newEnvKey, setNewEnvKey] = useState('');
  const [newEnvValue, setNewEnvValue] = useState('');
  const [deleteTarget, setDeleteTarget] = useState<{ id: string; key: string } | null>(null);

  const loadState = async () => {
    setError('');
    try {
      const next = await apiGet<EnvironmentState>('/api/system/environment');
      setState(next);
    } catch (err) {
      setError(err instanceof Error ? err.message : t('env.loadError'));
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    void loadState();
  }, []);

  const toggleVisibility = (id: string) => {
    setVisibleValues((prev) => ({ ...prev, [id]: !prev[id] }));
  };

  const persist = async (entries: Record<string, unknown>, strict = state?.environment.strict) => {
    if (!state) return;
    setSaving(true);
    setError('');
    try {
      const next = await apiPost<EnvironmentState>('/api/system/environment/save', {
        environment: {
          ...state.environment,
          strict,
          entries,
        },
      });
      setState(next);
    } catch (err) {
      setError(err instanceof Error ? err.message : t('env.saveError'));
    } finally {
      setSaving(false);
    }
  };

  const handleAddEnv = async () => {
    if (!newEnvKey || !newEnvValue) return;
    const entries = { ...(state?.environment.entries || {}) };
    entries[newEnvKey] = { value: newEnvValue, masked: true };
    await persist(entries);
    setIsModalOpen(false);
    setNewEnvKey('');
    setNewEnvValue('');
  };

  const deleteEnv = async () => {
    if (!deleteTarget) return;
    const entries = { ...(state?.environment.entries || {}) };
    delete entries[deleteTarget.id];
    setDeleteTarget(null);
    await persist(entries);
  };

  const envs = Object.entries(state?.environment.entries || {}).map(([key, entry]) => ({
    id: key,
    key,
    value: visibleValues[key] ? (state?.resolved?.[key] || entry.value || entry.fromEnv || '') : (state?.masked?.[key] || '••••••••••••••••'),
  }));

  return (
    <div className="flex flex-col h-full bg-white dark:bg-zinc-950 text-zinc-900 dark:text-zinc-100 transition-colors">
      <PageHeader title={t('env.title')} description={t('env.desc')} />

      <div className="flex-1 overflow-y-auto p-6">
        <div className="space-y-6">
          <ErrorAlert message={error} />
          <div className="flex justify-between items-center">
            <div>
              <h3 className="text-lg font-medium text-zinc-900 dark:text-zinc-100 mb-1">{t('env.vars')}</h3>
              <p className="text-sm text-zinc-500 dark:text-zinc-400">{t('env.varsDesc')}</p>
            </div>
            <button
              onClick={() => setIsModalOpen(true)}
              className="flex items-center gap-2 bg-indigo-600 hover:bg-indigo-500 text-white px-3 py-1.5 rounded-lg text-sm font-medium transition-colors"
            >
              <Plus className="w-4 h-4" />
              {t('env.addVar')}
            </button>
          </div>

          <div className="bg-white dark:bg-zinc-900 border border-zinc-200 dark:border-zinc-800 rounded-xl overflow-hidden shadow-sm">
            {loading ? (
              <div className="p-6 text-sm text-zinc-500 dark:text-zinc-400">{t('env.loading')}</div>
            ) : (
              <table className="w-full text-left text-sm text-zinc-600 dark:text-zinc-400">
                <thead className="bg-zinc-50 dark:bg-zinc-950/50 text-xs uppercase text-zinc-500 border-b border-zinc-200 dark:border-zinc-800">
                  <tr>
                    <th className="px-6 py-3 font-medium">{t('env.key')}</th>
                    <th className="px-6 py-3 font-medium">{t('env.value')}</th>
                    <th className="px-6 py-3 font-medium text-right">{t('env.actions')}</th>
                  </tr>
                </thead>
                <tbody>
                  {envs.map((env, index) => (
                    <tr key={env.id} className={`hover:bg-zinc-50 dark:hover:bg-zinc-800/30 transition-colors ${index !== envs.length - 1 ? 'border-b border-zinc-200 dark:border-zinc-800/50' : ''}`}>
                      <td className="px-6 py-4 font-mono text-zinc-900 dark:text-zinc-300">{env.key}</td>
                      <td className="px-6 py-4 font-mono text-zinc-500">
                        <div className="flex items-center gap-2">
                          <span>{env.value}</span>
                          <button
                            onClick={() => toggleVisibility(env.id)}
                            className="text-zinc-400 hover:text-zinc-600 dark:text-zinc-500 dark:hover:text-zinc-300 transition-colors"
                          >
                            {visibleValues[env.id] ? <EyeOff className="w-4 h-4" /> : <Eye className="w-4 h-4" />}
                          </button>
                        </div>
                      </td>
                      <td className="px-6 py-4 text-right">
                        <button
                          onClick={() => setDeleteTarget({ id: env.id, key: env.key })}
                          className="text-rose-500 hover:text-rose-600 dark:text-rose-400 dark:hover:text-rose-300 transition-colors p-1 rounded hover:bg-rose-50 dark:hover:bg-rose-950/50"
                        >
                          <Trash2 className="w-4 h-4" />
                        </button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </div>

          <div className="space-y-6 pt-6 border-t border-zinc-200 dark:border-zinc-800">
            <div>
              <h3 className="text-lg font-medium text-zinc-900 dark:text-zinc-100 mb-2">{t('env.security')}</h3>
              <p className="text-sm text-zinc-500 dark:text-zinc-400 mb-6">{t('env.securityDesc')}</p>
            </div>

            <div className="bg-white dark:bg-zinc-900 border border-zinc-200 dark:border-zinc-800 rounded-xl p-5 flex items-center justify-between shadow-sm">
              <div>
                <h4 className="font-medium text-zinc-900 dark:text-zinc-200">{t('env.strictMode')}</h4>
                <p className="text-sm text-zinc-500 mt-1">{t('env.strictModeDesc')}</p>
              </div>
              <ToggleSwitch
                checked={state?.environment.strict || false}
                onChange={(next) => void persist({ ...(state?.environment.entries || {}) }, next)}
              />
            </div>

            <div className="flex justify-end">
              <button
                onClick={() =>
                  void apiPost<EnvironmentState>('/api/system/environment/reload', {})
                    .then(setState)
                    .catch((err) => setError(err instanceof Error ? err.message : t('env.reloadError')))
                }
                className="rounded-lg border border-zinc-200 px-4 py-2 text-sm font-medium text-zinc-700 transition-colors hover:bg-zinc-100 dark:border-zinc-800 dark:text-zinc-300 dark:hover:bg-zinc-800"
              >
                {t('env.reload')}
              </button>
            </div>
          </div>
        </div>
      </div>

      <Modal isOpen={isModalOpen} onClose={() => setIsModalOpen(false)} title={t('env.addVar')}>
        <div className="space-y-4">
          <div>
            <label className="block text-sm font-medium text-zinc-700 dark:text-zinc-300 mb-1">{t('env.key')}</label>
            <input
              type="text"
              value={newEnvKey}
              onChange={(e) => setNewEnvKey(e.target.value)}
              placeholder={t('env.keyPlaceholder')}
              className="w-full bg-zinc-50 dark:bg-zinc-950 border border-zinc-200 dark:border-zinc-800 rounded-lg p-2.5 text-sm text-zinc-900 dark:text-zinc-200 focus:border-indigo-500 focus:ring-1 focus:ring-indigo-500 transition-all outline-none font-mono"
            />
          </div>
          <div>
            <label className="block text-sm font-medium text-zinc-700 dark:text-zinc-300 mb-1">{t('env.value')}</label>
            <input
              type="password"
              value={newEnvValue}
              onChange={(e) => setNewEnvValue(e.target.value)}
              placeholder={t('env.valuePlaceholder')}
              className="w-full bg-zinc-50 dark:bg-zinc-950 border border-zinc-200 dark:border-zinc-800 rounded-lg p-2.5 text-sm text-zinc-900 dark:text-zinc-200 focus:border-indigo-500 focus:ring-1 focus:ring-indigo-500 transition-all outline-none font-mono"
            />
          </div>
          <div className="pt-4 flex justify-end gap-2">
            <button
              onClick={() => setIsModalOpen(false)}
              className="px-4 py-2 text-sm font-medium text-zinc-700 dark:text-zinc-300 hover:bg-zinc-100 dark:hover:bg-zinc-800 rounded-lg transition-colors"
            >
              {t('common.cancel')}
            </button>
            <button
              onClick={() => void handleAddEnv()}
              disabled={!newEnvKey || !newEnvValue || saving}
              className="px-4 py-2 text-sm font-medium text-white bg-indigo-600 hover:bg-indigo-500 rounded-lg transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
            >
              {saving ? t('common.saving') : t('env.addVariable')}
            </button>
          </div>
        </div>
      </Modal>

      <ConfirmDialog
        isOpen={deleteTarget !== null}
        onClose={() => setDeleteTarget(null)}
        onConfirm={() => void deleteEnv()}
        title={t('common.deleteConfirmTitle')}
        message={t('common.deleteConfirmMessage')}
        confirmText={t('common.delete')}
        loading={saving}
      />
    </div>
  );
}
