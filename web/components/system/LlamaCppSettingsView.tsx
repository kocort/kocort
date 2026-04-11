'use client';

import { useCallback, useEffect, useRef, useState } from 'react';
import { Cpu, Download, Loader2, Save, Check, X } from 'lucide-react';
import { useI18n } from '@/lib/i18n/I18nContext';
import { ErrorAlert, PageHeader, Select } from '@/components/ui';
import { apiGet, apiPost, type LlamaCppState } from '@/lib/api';

export function LlamaCppSettingsView() {
    const { t } = useI18n();
    const [state, setState] = useState<LlamaCppState | null>(null);
    const [version, setVersion] = useState('');
    const [gpuType, setGpuType] = useState('');
    const [loading, setLoading] = useState(true);
    const [saving, setSaving] = useState(false);
    const [error, setError] = useState('');
    const [saved, setSaved] = useState(false);
    const pollRef = useRef<ReturnType<typeof setInterval> | null>(null);

    const dlProgress = state?.downloadProgress;

    const loadState = useCallback(async () => {
        setError('');
        try {
            const s = await apiGet<LlamaCppState>('/api/system/llamacpp');
            setState(s);
            setVersion(s.version || s.defaultVersion);
            setGpuType(s.gpuType || 'auto');
        } catch (err) {
            setError(err instanceof Error ? err.message : t('llamacpp.loadError'));
        } finally {
            setLoading(false);
        }
    }, [t]);

    useEffect(() => {
        void loadState();
    }, [loadState]);

    // ── Progress polling ─────────────────────────────────────────────
    const startProgressPoll = useCallback(() => {
        if (pollRef.current) return;
        pollRef.current = setInterval(async () => {
            try {
                const next = await apiGet<LlamaCppState>('/api/system/llamacpp');
                setState(next);
                if (!next.downloadProgress?.active) {
                    if (pollRef.current) {
                        clearInterval(pollRef.current);
                        pollRef.current = null;
                    }
                }
            } catch {
                // ignore polling errors
            }
        }, 1000);
    }, []);

    const stopProgressPoll = useCallback(() => {
        if (pollRef.current) {
            clearInterval(pollRef.current);
            pollRef.current = null;
        }
    }, []);

    useEffect(() => {
        if (dlProgress?.active) {
            startProgressPoll();
        } else {
            stopProgressPoll();
        }
    }, [dlProgress?.active, startProgressPoll, stopProgressPoll]);

    useEffect(() => stopProgressPoll, [stopProgressPoll]);

    // ── Actions ──────────────────────────────────────────────────────
    const handleSave = async () => {
        setSaving(true);
        setError('');
        setSaved(false);
        try {
            const next = await apiPost<LlamaCppState>('/api/system/llamacpp/save', {
                version,
                gpuType,
            });
            setState(next);
            setVersion(next.version || next.defaultVersion);
            setGpuType(next.gpuType || 'auto');
            setSaved(true);
            setTimeout(() => setSaved(false), 2000);
        } catch (err) {
            setError(err instanceof Error ? err.message : t('llamacpp.saveError'));
        } finally {
            setSaving(false);
        }
    };

    const handleDownload = async () => {
        setError('');
        try {
            const next = await apiPost<LlamaCppState>('/api/system/llamacpp/download', {
                version,
                gpuType,
            });
            setState(next);
        } catch (err) {
            setError(err instanceof Error ? err.message : t('llamacpp.downloadStartError'));
        }
    };

    const handleCancelDownload = async () => {
        setError('');
        try {
            const next = await apiPost<LlamaCppState>('/api/system/llamacpp/download/cancel', {});
            setState(next);
        } catch (err) {
            setError(err instanceof Error ? err.message : t('llamacpp.downloadCancelError'));
        }
    };

    const effectiveGpu =
        gpuType === 'auto' || gpuType === '' ? state?.detectedGpuType || 'cpu' : gpuType;
    const isVariantDownloaded = (state?.downloadedVariants ?? []).some(
        (v) => v.version === version && v.gpuType === effectiveGpu,
    );

    if (loading) {
        return (
            <div className="flex h-full items-center justify-center bg-white text-zinc-900 transition-colors dark:bg-zinc-950 dark:text-zinc-100">
                <Loader2 className="h-6 w-6 animate-spin text-zinc-400" />
            </div>
        );
    }

    const gpuOptions = (state?.availableGpuTypes || ['cpu']).map((g) => ({
        value: g,
        label:
            g === 'auto'
                ? `${t('llamacpp.gpuAuto')} (${state?.detectedGpuType?.toUpperCase() || 'CPU'})`
                : g === 'cpu'
                    ? 'CPU'
                    : g.toUpperCase(),
    }));

    const uniqueVersions = [...new Set((state?.downloadedVariants ?? []).map((v) => v.version))];
    const versionOptions = uniqueVersions.map((v) => ({
        value: v,
        label: v === state?.defaultVersion ? `${v} (${t('llamacpp.default')})` : v,
    }));
    // Ensure current version is in the option list even if not downloaded
    if (version && !versionOptions.some((o) => o.value === version)) {
        versionOptions.push({ value: version, label: version });
    }
    // Ensure default version is in the list
    if (state?.defaultVersion && !versionOptions.some((o) => o.value === state.defaultVersion)) {
        versionOptions.push({
            value: state.defaultVersion,
            label: `${state.defaultVersion} (${t('llamacpp.default')})`,
        });
    }

    return (
        <div className="flex h-full flex-col bg-white text-zinc-900 transition-colors dark:bg-zinc-950 dark:text-zinc-100">
            <PageHeader
                title={t('llamacpp.title')}
                description={t('llamacpp.desc')}
            />

            <div className="flex-1 overflow-y-auto p-6">
                <div className="space-y-6">
                    {error && <ErrorAlert message={error} />}

                    {/* Status card */}
                    <div className="rounded-xl border border-zinc-200 bg-white p-6 dark:border-zinc-800 dark:bg-zinc-900/50">
                        <div className="mb-4 flex items-center gap-2">
                            <Cpu className="h-5 w-5 text-zinc-500" />
                            <h3 className="text-sm font-semibold text-zinc-900 dark:text-zinc-100">
                                {t('llamacpp.statusTitle')}
                            </h3>
                        </div>

                        <div className="mb-4 grid grid-cols-2 gap-4 text-sm">
                            <div>
                                <span className="text-zinc-500 dark:text-zinc-400">{t('llamacpp.currentVersion')}</span>
                                <p className="font-medium text-zinc-900 dark:text-zinc-100">
                                    {state?.version || state?.defaultVersion || '-'}
                                </p>
                            </div>
                            <div>
                                <span className="text-zinc-500 dark:text-zinc-400">{t('llamacpp.loadStatus')}</span>
                                <p className="font-medium">
                                    {state?.loaded ? (
                                        <span className="text-emerald-600 dark:text-emerald-400">{t('llamacpp.loaded')}</span>
                                    ) : (
                                        <span className="text-zinc-500 dark:text-zinc-400">{t('llamacpp.notLoaded')}</span>
                                    )}
                                </p>
                            </div>
                            {state?.libDir && (
                                <div className="col-span-2">
                                    <span className="text-zinc-500 dark:text-zinc-400">{t('llamacpp.libDir')}</span>
                                    <p className="break-all font-mono text-xs text-zinc-700 dark:text-zinc-300">
                                        {state.libDir}
                                    </p>
                                </div>
                            )}
                        </div>
                    </div>

                    {/* Configuration card */}
                    <div className="rounded-xl border border-zinc-200 bg-white p-6 dark:border-zinc-800 dark:bg-zinc-900/50">
                        <div className="mb-4 flex items-center gap-2">
                            <Cpu className="h-5 w-5 text-zinc-500" />
                            <h3 className="text-sm font-semibold text-zinc-900 dark:text-zinc-100">
                                {t('llamacpp.configTitle')}
                            </h3>
                        </div>

                        <p className="mb-4 text-xs text-zinc-500 dark:text-zinc-400">
                            {t('llamacpp.configDesc')}
                        </p>

                        <div className="space-y-4">
                            <div>
                                <label className="mb-1.5 block text-xs font-medium text-zinc-700 dark:text-zinc-300">
                                    {t('llamacpp.version')}
                                </label>
                                <Select
                                    value={version}
                                    onChange={setVersion}
                                    options={versionOptions}
                                    allowCustomValue
                                    placeholder={state?.defaultVersion || 'b8720'}
                                />
                                <p className="mt-1.5 text-xs text-zinc-500 dark:text-zinc-400">
                                    {t('llamacpp.versionHelp')}
                                </p>
                            </div>

                            <div>
                                <label className="mb-1.5 block text-xs font-medium text-zinc-700 dark:text-zinc-300">
                                    {t('llamacpp.gpuType')}
                                </label>
                                <Select
                                    value={gpuType}
                                    onChange={setGpuType}
                                    options={gpuOptions}
                                />
                                <p className="mt-1.5 text-xs text-zinc-500 dark:text-zinc-400">
                                    {t('llamacpp.gpuTypeHelp')}
                                </p>
                            </div>

                            {/* Downloaded variants list */}
                            {(state?.downloadedVariants?.length ?? 0) > 0 && (
                                <div>
                                    <label className="mb-1.5 block text-xs font-medium text-zinc-700 dark:text-zinc-300">
                                        {t('llamacpp.downloadedVersions')}
                                    </label>
                                    <div className="flex flex-wrap gap-2">
                                        {state?.downloadedVariants?.map((v) => {
                                            const key = `${v.version}-${v.gpuType}`;
                                            const isActive = v.version === state.version && v.gpuType === (state.gpuType || 'cpu');
                                            return (
                                                <span
                                                    key={key}
                                                    className="inline-flex items-center gap-1 rounded-md bg-zinc-100 px-2 py-1 text-xs font-medium text-zinc-700 dark:bg-zinc-800 dark:text-zinc-300"
                                                >
                                                    {isActive && <Check className="h-3 w-3 text-emerald-500" />}
                                                    {v.version} ({v.gpuType})
                                                </span>
                                            );
                                        })}
                                    </div>
                                </div>
                            )}

                            <div className="flex items-center gap-3">
                                <button
                                    type="button"
                                    onClick={() => void handleSave()}
                                    disabled={saving || !!dlProgress?.active}
                                    className="inline-flex items-center gap-2 rounded-lg bg-indigo-600 px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-indigo-500 disabled:opacity-50"
                                >
                                    {saving ? (
                                        <Loader2 className="h-4 w-4 animate-spin" />
                                    ) : (
                                        <Save className="h-4 w-4" />
                                    )}
                                    {t('llamacpp.save')}
                                </button>

                                {!dlProgress?.active && (
                                    <button
                                        type="button"
                                        onClick={() => void handleDownload()}
                                        disabled={isVariantDownloaded}
                                        className="inline-flex items-center gap-2 rounded-lg border border-zinc-300 bg-white px-4 py-2 text-sm font-medium text-zinc-700 transition-colors hover:bg-zinc-50 disabled:opacity-50 dark:border-zinc-700 dark:bg-zinc-800 dark:text-zinc-300 dark:hover:bg-zinc-700"
                                    >
                                        <Download className="h-4 w-4" />
                                        {isVariantDownloaded ? t('llamacpp.alreadyDownloaded') : t('llamacpp.download')}
                                    </button>
                                )}

                                {saved && (
                                    <span className="text-xs font-medium text-emerald-600 dark:text-emerald-400">
                                        {t('llamacpp.saved')}
                                    </span>
                                )}
                            </div>

                            {/* Download progress */}
                            {dlProgress?.active && (
                                <div className="space-y-2 rounded-lg border border-indigo-200 bg-indigo-50/50 p-4 dark:border-indigo-800/50 dark:bg-indigo-950/30">
                                    <div className="flex items-center justify-between text-xs">
                                        <span className="font-medium text-zinc-700 dark:text-zinc-300">
                                            {t('llamacpp.downloading')} {dlProgress.version} ({dlProgress.gpuType})
                                        </span>
                                        <div className="flex items-center gap-2">
                                            {dlProgress.totalBytes > 0 && (
                                                <span className="text-zinc-500 dark:text-zinc-400">
                                                    {formatBytes(dlProgress.downloadedBytes)} / {formatBytes(dlProgress.totalBytes)}
                                                </span>
                                            )}
                                            <button
                                                type="button"
                                                onClick={() => void handleCancelDownload()}
                                                className="inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-xs text-red-600 transition-colors hover:bg-red-100 dark:text-red-400 dark:hover:bg-red-900/30"
                                                title={t('common.cancel')}
                                            >
                                                <X className="h-3.5 w-3.5" />
                                            </button>
                                        </div>
                                    </div>
                                    <div className="h-2 overflow-hidden rounded-full bg-zinc-200 dark:bg-zinc-700">
                                        <div
                                            className="h-full rounded-full bg-indigo-500 transition-all duration-300"
                                            style={{
                                                width: `${dlProgress.totalBytes > 0 ? Math.min(100, Math.round((dlProgress.downloadedBytes / dlProgress.totalBytes) * 100)) : 0}%`,
                                            }}
                                        />
                                    </div>
                                </div>
                            )}

                            {/* Download error or canceled */}
                            {!dlProgress?.active && dlProgress?.error && (
                                <ErrorAlert message={dlProgress.error} />
                            )}
                        </div>
                    </div>
                </div>
            </div>
        </div>
    );
}

function formatBytes(bytes: number): string {
    if (bytes < 1024) return `${bytes} B`;
    if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
    if (bytes < 1024 * 1024 * 1024) return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
    return `${(bytes / (1024 * 1024 * 1024)).toFixed(2)} GB`;
}
