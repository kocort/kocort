'use client';

import { useCallback, useEffect, useState } from 'react';
import { Globe, Loader2, Save } from 'lucide-react';
import { useI18n } from '@/lib/i18n/I18nContext';
import { ErrorAlert, PageHeader, Select, ToggleSwitch } from '@/components/ui';
import { apiGet, apiPost, type NetworkState } from '@/lib/api';

export function NetworkSettingsView() {
    const { t, language, setLanguage } = useI18n();
    const [useSystemProxy, setUseSystemProxy] = useState(true);
    const [proxyUrl, setProxyUrl] = useState('');
    const [languagePreference, setLanguagePreference] = useState<NetworkState['language']>('system');
    const [loading, setLoading] = useState(true);
    const [saving, setSaving] = useState(false);
    const [error, setError] = useState('');
    const [saved, setSaved] = useState(false);

    const loadState = useCallback(async () => {
        setError('');
        try {
            const state = await apiGet<NetworkState>('/api/system/network');
            setUseSystemProxy(state.useSystemProxy ?? true);
            setProxyUrl(state.proxyUrl || '');
            setLanguagePreference(state.language || 'system');
        } catch (err) {
            setError(err instanceof Error ? err.message : t('network.loadError'));
        } finally {
            setLoading(false);
        }
    }, [t]);

    useEffect(() => {
        void loadState();
    }, [loadState]);

    const handleSave = async () => {
        setSaving(true);
        setError('');
        setSaved(false);
        try {
            const next = await apiPost<NetworkState>('/api/system/network/save', {
                useSystemProxy,
                proxyUrl,
                language: languagePreference,
            });
            setUseSystemProxy(next.useSystemProxy ?? true);
            setProxyUrl(next.proxyUrl || '');
            setLanguagePreference(next.language || 'system');
            setLanguage(next.language || 'system');
            setSaved(true);
            setTimeout(() => setSaved(false), 2000);
        } catch (err) {
            setError(err instanceof Error ? err.message : t('network.saveError'));
        } finally {
            setSaving(false);
        }
    };

    if (loading) {
        return (
            <div className="flex h-full items-center justify-center bg-white text-zinc-900 transition-colors dark:bg-zinc-950 dark:text-zinc-100">
                <Loader2 className="h-6 w-6 animate-spin text-zinc-400" />
            </div>
        );
    }

    return (
        <div className="flex h-full flex-col bg-white text-zinc-900 transition-colors dark:bg-zinc-950 dark:text-zinc-100">
            <PageHeader
                title={t('network.title')}
                description={t('network.desc')}
            />

            <div className="flex-1 overflow-y-auto p-6">
                <div className="space-y-6">
                    {error && <ErrorAlert message={error} />}

                    <div className="rounded-xl border border-zinc-200 bg-white p-6 dark:border-zinc-800 dark:bg-zinc-900/50">
                        <div className="mb-4 flex items-center gap-2">
                            <Globe className="h-5 w-5 text-zinc-500" />
                            <h3 className="text-sm font-semibold text-zinc-900 dark:text-zinc-100">{t('network.sectionTitle')}</h3>
                        </div>

                        <p className="mb-4 text-xs text-zinc-500 dark:text-zinc-400">
                            {t('network.sectionDesc')}
                        </p>

                        <div className="space-y-4">
                            <div className="flex items-start justify-between gap-4 rounded-lg border border-zinc-200 bg-zinc-50 px-4 py-3 dark:border-zinc-800 dark:bg-zinc-950/60">
                                <div className="space-y-1">
                                    <div className="text-sm font-medium text-zinc-900 dark:text-zinc-100">
                                        {t('network.useSystemProxy')}
                                    </div>
                                    <p className="text-xs text-zinc-500 dark:text-zinc-400">
                                        {t('network.useSystemProxyDesc')}
                                    </p>
                                </div>
                                <ToggleSwitch checked={useSystemProxy} onChange={setUseSystemProxy} />
                            </div>

                            <div>
                                <label className="mb-1.5 block text-xs font-medium text-zinc-700 dark:text-zinc-300">
                                    {t('network.proxyUrl')}
                                </label>
                                <input
                                    type="text"
                                    value={proxyUrl}
                                    onChange={(e) => setProxyUrl(e.target.value)}
                                    placeholder={t('network.proxyPlaceholder')}
                                    className="w-full rounded-lg border border-zinc-300 bg-white px-3 py-2 text-sm text-zinc-900 placeholder:text-zinc-400 focus:border-indigo-500 focus:outline-none focus:ring-1 focus:ring-indigo-500 dark:border-zinc-700 dark:bg-zinc-800 dark:text-zinc-100 dark:placeholder:text-zinc-500"
                                />
                                <p className="mt-1.5 text-xs text-zinc-500 dark:text-zinc-400">
                                    {t('network.proxyHelp')}
                                </p>
                            </div>

                            <div>
                                <label className="mb-1.5 block text-xs font-medium text-zinc-700 dark:text-zinc-300">
                                    {t('network.language')}
                                </label>
                                <Select
                                    value={languagePreference}
                                    onChange={(value) => setLanguagePreference(value as NetworkState['language'])}
                                    options={[
                                        {
                                            value: 'system',
                                            label: `${t('network.languageSystem')} (${language === 'zh' ? t('network.languageChinese') : t('network.languageEnglish')})`,
                                        },
                                        { value: 'en', label: t('network.languageEnglish') },
                                        { value: 'zh', label: t('network.languageChinese') },
                                    ]}
                                />
                                <p className="mt-1.5 text-xs text-zinc-500 dark:text-zinc-400">
                                    {t('network.languageHelp')}
                                </p>
                            </div>

                            <div className="flex items-center gap-3">
                                <button
                                    type="button"
                                    onClick={() => void handleSave()}
                                    disabled={saving}
                                    className="inline-flex items-center gap-2 rounded-lg bg-indigo-600 px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-indigo-500 disabled:opacity-50"
                                >
                                    {saving ? (
                                        <Loader2 className="h-4 w-4 animate-spin" />
                                    ) : (
                                        <Save className="h-4 w-4" />
                                    )}
                                    {t('network.save')}
                                </button>
                                {saved && (
                                    <span className="text-xs font-medium text-emerald-600 dark:text-emerald-400">
                                        {t('network.saved')}
                                    </span>
                                )}
                            </div>
                        </div>
                    </div>
                </div>
            </div>
        </div>
    );
}
