'use client';

import { useCallback, useEffect, useMemo, useState } from 'react';
import {
    Activity,
    AlertCircle,
    Loader2,
    Pause,
    Play,
    RefreshCw,
    RotateCcw,
} from 'lucide-react';
import { ErrorAlert, PageHeader } from '@/components/ui';
import {
    apiGet,
    apiPost,
    type ACPControlResult,
    type ACPResumeResult,
    type ACPResumeResults,
    type ACPSessionObservability,
    type ACPSessionsState,
    type ACPSessionStatus,
} from '@/lib/api';
import { useI18n } from '@/lib/i18n/I18nContext';

function formatTimestamp(
    value: number | undefined,
    formatter: Intl.DateTimeFormat,
    fallbackNever: string,
    fallbackUnknown: string,
): string {
    if (!value) {
        return fallbackNever;
    }
    const normalized = value > 1_000_000_000_000 ? value : value * 1000;
    const date = new Date(normalized);
    if (Number.isNaN(date.getTime())) {
        return fallbackUnknown;
    }
    return formatter.format(date);
}

function statusTone(state: string): string {
    const normalized = state.trim().toLowerCase();
    if (normalized === 'active' || normalized === 'running') {
        return 'bg-emerald-500/10 text-emerald-600 dark:text-emerald-400';
    }
    if (normalized === 'paused') {
        return 'bg-amber-500/10 text-amber-600 dark:text-amber-400';
    }
    if (normalized === 'error' || normalized === 'failed') {
        return 'bg-rose-500/10 text-rose-600 dark:text-rose-400';
    }
    return 'bg-zinc-500/10 text-zinc-600 dark:text-zinc-300';
}

export function ACPView() {
    const { t, language } = useI18n();
    const [sessions, setSessions] = useState<ACPSessionStatus[]>([]);
    const [selectedSessionKey, setSelectedSessionKey] = useState('');
    const [observability, setObservability] = useState<Record<string, ACPSessionObservability>>({});
    const [loading, setLoading] = useState(true);
    const [refreshing, setRefreshing] = useState(false);
    const [actionKey, setActionKey] = useState('');
    const [error, setError] = useState('');
    const [notice, setNotice] = useState('');

    const dateFormatter = useMemo(
        () => new Intl.DateTimeFormat(language === 'zh' ? 'zh-CN' : 'en-US', {
            dateStyle: 'medium',
            timeStyle: 'short',
        }),
        [language],
    );

    const formatSessionTimestamp = useCallback(
        (value?: number) => formatTimestamp(value, dateFormatter, t('acp.never'), t('acp.unknown')),
        [dateFormatter, t],
    );

    const actionLabel = useCallback((action: string) => {
        switch (action) {
            case 'pause':
                return t('acp.pause');
            case 'resume-runtime':
                return t('acp.resumeRuntime');
            default:
                return action;
        }
    }, [t]);

    const activeSessionsCount = useMemo(
        () => sessions.filter((session) => session.state === 'active').length,
        [sessions],
    );

    const loadSessions = useCallback(async (initial = false) => {
        if (initial) {
            setLoading(true);
        } else {
            setRefreshing(true);
        }
        setError('');
        try {
            const next = await apiGet<ACPSessionsState>('/api/engine/acp/sessions');
            setSessions(next.sessions || []);
            setSelectedSessionKey((current) => {
                if (current && next.sessions.some((session) => session.sessionKey === current)) {
                    return current;
                }
                return next.sessions[0]?.sessionKey || '';
            });
        } catch (err) {
            setError(err instanceof Error ? err.message : t('acp.loadError'));
        } finally {
            setLoading(false);
            setRefreshing(false);
        }
    }, [t]);

    const loadObservability = useCallback(async (sessionKey: string) => {
        if (!sessionKey) {
            return;
        }
        setActionKey(`inspect:${sessionKey}`);
        setError('');
        try {
            const next = await apiGet<ACPSessionObservability>(`/api/engine/acp/session/observability?sessionKey=${encodeURIComponent(sessionKey)}`);
            setObservability((current) => ({ ...current, [sessionKey]: next }));
            setSelectedSessionKey(sessionKey);
        } catch (err) {
            setError(err instanceof Error ? err.message : t('acp.inspectError'));
        } finally {
            setActionKey('');
        }
    }, [t]);

    useEffect(() => {
        void loadSessions(true);
    }, [loadSessions]);

    useEffect(() => {
        if (!selectedSessionKey || observability[selectedSessionKey]) {
            return;
        }
        void loadObservability(selectedSessionKey);
    }, [loadObservability, observability, selectedSessionKey]);

    const selectedSession = useMemo(
        () => sessions.find((session) => session.sessionKey === selectedSessionKey) || null,
        [selectedSessionKey, sessions],
    );

    const selectedObservability = selectedSessionKey ? observability[selectedSessionKey] : undefined;

    const runControl = async (session: ACPSessionStatus, action: string) => {
        const pendingKey = `${action}:${session.sessionKey}`;
        setActionKey(pendingKey);
        setError('');
        setNotice('');
        try {
            const result = await apiPost<ACPControlResult>('/api/engine/acp/session/control', {
                sessionKey: session.sessionKey,
                backendID: session.backend,
                action,
            });
            setNotice(
                result.success
                    ? t('acp.sessionsApplied', { sessionKey: session.sessionKey, action: actionLabel(action) })
                    : (result.error || t('acp.failedAction', { action: actionLabel(action) })),
            );
            await loadSessions(false);
            await loadObservability(session.sessionKey);
        } catch (err) {
            setError(err instanceof Error ? err.message : t('acp.failedAction', { action: actionLabel(action) }));
        } finally {
            setActionKey('');
        }
    };

    const runResume = async (session: ACPSessionStatus) => {
        const pendingKey = `resume:${session.sessionKey}`;
        setActionKey(pendingKey);
        setError('');
        setNotice('');
        try {
            const result = await apiPost<ACPResumeResult>('/api/engine/acp/session/resume', {
                sessionKey: session.sessionKey,
                backendID: session.backend,
                agent: session.agent,
                mode: session.mode,
            });
            setNotice(
                result.resumed
                    ? t('acp.sessionResumed', { sessionKey: session.sessionKey })
                    : t('acp.sessionResumeSkipped', { sessionKey: session.sessionKey }),
            );
            await loadSessions(false);
            await loadObservability(session.sessionKey);
        } catch (err) {
            setError(err instanceof Error ? err.message : t('acp.resumeActionError'));
        } finally {
            setActionKey('');
        }
    };

    const resumePersistent = async () => {
        setActionKey('resume-persistent');
        setError('');
        setNotice('');
        try {
            const result = await apiPost<ACPResumeResults>('/api/engine/acp/sessions/resume-persistent', {});
            const resumedCount = (result.results || []).filter((entry) => entry.resumed).length;
            setNotice(
                resumedCount > 0
                    ? t('acp.resumePersistentSuccess', { count: String(resumedCount) })
                    : t('acp.noPersistentSessions'),
            );
            await loadSessions(false);
        } catch (err) {
            setError(err instanceof Error ? err.message : t('acp.resumePersistentError'));
        } finally {
            setActionKey('');
        }
    };

    return (
        <div className="flex h-full flex-col bg-white text-zinc-900 transition-colors dark:bg-zinc-950 dark:text-zinc-100">
            <PageHeader
                title={t('acp.title')}
                description={t('acp.description')}
                meta={(
                    <div className="mt-2 flex items-center gap-3 text-xs text-zinc-500 dark:text-zinc-400">
                        <span>{t('acp.total', { count: String(sessions.length) })}</span>
                        <span>{`${activeSessionsCount} ${t('acp.active')}`}</span>
                    </div>
                )}
            >
                <button
                    onClick={() => void loadSessions(false)}
                    disabled={refreshing}
                    className="rounded-lg border border-zinc-200 px-3 py-2 text-sm font-medium text-zinc-700 transition-colors hover:bg-zinc-100 disabled:cursor-not-allowed disabled:opacity-60 dark:border-zinc-800 dark:text-zinc-200 dark:hover:bg-zinc-800"
                >
                    <RefreshCw className={`h-4 w-4 ${refreshing ? 'animate-spin' : ''}`} />
                </button>
                <button
                    onClick={() => void resumePersistent()}
                    disabled={actionKey === 'resume-persistent'}
                    className="inline-flex items-center gap-2 rounded-lg bg-indigo-600 px-3 py-2 text-sm font-medium text-white transition-colors hover:bg-indigo-500 disabled:cursor-not-allowed disabled:opacity-60"
                >
                    {actionKey === 'resume-persistent' ? <Loader2 className="h-4 w-4 animate-spin" /> : <RotateCcw className="h-4 w-4" />}
                    {t('acp.resumePersistent')}
                </button>
            </PageHeader>

            <div className="flex-1 overflow-y-auto p-6">
                <ErrorAlert message={error} className="mb-4" />

                {notice ? (
                    <div className="mb-4 rounded-xl border border-emerald-200 bg-emerald-50 px-4 py-3 text-sm text-emerald-700 dark:border-emerald-900/50 dark:bg-emerald-950/30 dark:text-emerald-300">
                        {notice}
                    </div>
                ) : null}

                {loading ? (
                    <div className="flex h-full items-center justify-center text-zinc-500 dark:text-zinc-400">
                        <Loader2 className="mr-2 h-5 w-5 animate-spin" />
                        {t('acp.loading')}
                    </div>
                ) : sessions.length === 0 ? (
                    <div className="rounded-2xl border border-dashed border-zinc-300 bg-zinc-50 px-6 py-10 text-center dark:border-zinc-800 dark:bg-zinc-900/40">
                        <Activity className="mx-auto mb-3 h-8 w-8 text-zinc-400" />
                        <h3 className="text-base font-medium text-zinc-900 dark:text-zinc-100">{t('acp.empty')}</h3>
                        <p className="mt-2 text-sm text-zinc-500 dark:text-zinc-400">
                            {t('acp.emptyDesc')}
                        </p>
                    </div>
                ) : (
                    <div className="grid gap-6 xl:grid-cols-[minmax(0,1.25fr)_minmax(320px,0.9fr)]">
                        <section className="space-y-4">
                            {sessions.map((session) => {
                                const isSelected = session.sessionKey === selectedSessionKey;
                                return (
                                    <article
                                        key={session.sessionKey}
                                        className={`rounded-2xl border p-4 shadow-sm transition-colors ${isSelected
                                                ? 'border-indigo-300 bg-indigo-50/60 dark:border-indigo-800 dark:bg-indigo-950/20'
                                                : 'border-zinc-200 bg-white dark:border-zinc-800 dark:bg-zinc-900'
                                            }`}
                                    >
                                        <div className="flex flex-col gap-4 lg:flex-row lg:items-start lg:justify-between">
                                            <div className="min-w-0">
                                                <div className="flex flex-wrap items-center gap-2">
                                                    <button
                                                        onClick={() => void loadObservability(session.sessionKey)}
                                                        className="text-left text-sm font-semibold text-zinc-900 hover:text-indigo-600 dark:text-zinc-100 dark:hover:text-indigo-400"
                                                    >
                                                        {session.sessionKey}
                                                    </button>
                                                    <span className={`inline-flex rounded-full px-2 py-1 text-xs font-medium ${statusTone(session.state)}`}>
                                                        {session.state || t('acp.unknownState')}
                                                    </span>
                                                </div>
                                                <div className="mt-3 grid gap-2 text-sm text-zinc-600 dark:text-zinc-400 md:grid-cols-2">
                                                    <div>{t('acp.backend')}: <span className="font-medium text-zinc-800 dark:text-zinc-200">{session.backend || t('acp.na')}</span></div>
                                                    <div>{t('acp.agent')}: <span className="font-medium text-zinc-800 dark:text-zinc-200">{session.agent || t('acp.na')}</span></div>
                                                    <div>{t('acp.mode')}: <span className="font-medium text-zinc-800 dark:text-zinc-200">{session.mode || t('acp.na')}</span></div>
                                                    <div>{t('acp.lastActivity')}: <span className="font-medium text-zinc-800 dark:text-zinc-200">{formatSessionTimestamp(session.lastActivityAt)}</span></div>
                                                </div>
                                                {session.lastError ? (
                                                    <div className="mt-3 inline-flex items-start gap-2 rounded-lg bg-rose-500/10 px-3 py-2 text-xs text-rose-700 dark:text-rose-300">
                                                        <AlertCircle className="mt-0.5 h-3.5 w-3.5 shrink-0" />
                                                        <span>{session.lastError}</span>
                                                    </div>
                                                ) : null}
                                            </div>

                                            <div className="flex flex-wrap items-center gap-2">
                                                <button
                                                    onClick={() => void loadObservability(session.sessionKey)}
                                                    disabled={actionKey === `inspect:${session.sessionKey}`}
                                                    className="rounded-lg border border-zinc-200 px-3 py-2 text-xs font-medium text-zinc-700 transition-colors hover:bg-zinc-100 disabled:cursor-not-allowed disabled:opacity-60 dark:border-zinc-700 dark:text-zinc-200 dark:hover:bg-zinc-800"
                                                >
                                                    {t('acp.inspect')}
                                                </button>
                                                <button
                                                    onClick={() => void runControl(session, 'pause')}
                                                    disabled={actionKey === `pause:${session.sessionKey}`}
                                                    className="inline-flex items-center gap-1 rounded-lg border border-zinc-200 px-3 py-2 text-xs font-medium text-zinc-700 transition-colors hover:bg-zinc-100 disabled:cursor-not-allowed disabled:opacity-60 dark:border-zinc-700 dark:text-zinc-200 dark:hover:bg-zinc-800"
                                                >
                                                    {actionKey === `pause:${session.sessionKey}` ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Pause className="h-3.5 w-3.5" />}
                                                    {t('acp.pause')}
                                                </button>
                                                <button
                                                    onClick={() => void runControl(session, 'resume-runtime')}
                                                    disabled={actionKey === `resume-runtime:${session.sessionKey}`}
                                                    className="inline-flex items-center gap-1 rounded-lg border border-zinc-200 px-3 py-2 text-xs font-medium text-zinc-700 transition-colors hover:bg-zinc-100 disabled:cursor-not-allowed disabled:opacity-60 dark:border-zinc-700 dark:text-zinc-200 dark:hover:bg-zinc-800"
                                                >
                                                    {actionKey === `resume-runtime:${session.sessionKey}` ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Play className="h-3.5 w-3.5" />}
                                                    {t('acp.resumeRuntime')}
                                                </button>
                                                <button
                                                    onClick={() => void runResume(session)}
                                                    disabled={actionKey === `resume:${session.sessionKey}`}
                                                    className="inline-flex items-center gap-1 rounded-lg bg-indigo-600 px-3 py-2 text-xs font-medium text-white transition-colors hover:bg-indigo-500 disabled:cursor-not-allowed disabled:opacity-60"
                                                >
                                                    {actionKey === `resume:${session.sessionKey}` ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <RotateCcw className="h-3.5 w-3.5" />}
                                                    {t('acp.resume')}
                                                </button>
                                            </div>
                                        </div>
                                    </article>
                                );
                            })}
                        </section>

                        <aside className="rounded-2xl border border-zinc-200 bg-zinc-50/70 p-4 shadow-sm dark:border-zinc-800 dark:bg-zinc-900/70">
                            <div className="mb-4">
                                <h3 className="text-sm font-semibold text-zinc-900 dark:text-zinc-100">{t('acp.details')}</h3>
                                <p className="mt-1 text-xs text-zinc-500 dark:text-zinc-400">
                                    {t('acp.inspectHint')}
                                </p>
                            </div>

                            {!selectedSession ? (
                                <p className="text-sm text-zinc-500 dark:text-zinc-400">{t('acp.selectHint')}</p>
                            ) : !selectedObservability ? (
                                <div className="flex items-center text-sm text-zinc-500 dark:text-zinc-400">
                                    <Loader2 className="mr-2 h-4 w-4 animate-spin" />
                                    {t('acp.loadingObservability')}
                                </div>
                            ) : (
                                <div className="space-y-4 text-sm">
                                    <div className="grid gap-3 sm:grid-cols-2">
                                        <div className="rounded-xl border border-zinc-200 bg-white p-3 dark:border-zinc-800 dark:bg-zinc-950">
                                            <div className="text-xs uppercase tracking-wide text-zinc-400">{t('acp.runtime')}</div>
                                            <div className="mt-2 text-zinc-700 dark:text-zinc-200">{selectedObservability.runtimeSession || t('acp.na')}</div>
                                        </div>
                                        <div className="rounded-xl border border-zinc-200 bg-white p-3 dark:border-zinc-800 dark:bg-zinc-950">
                                            <div className="text-xs uppercase tracking-wide text-zinc-400">{t('acp.observed')}</div>
                                            <div className="mt-2 text-zinc-700 dark:text-zinc-200">{selectedObservability.observedAt || t('acp.na')}</div>
                                        </div>
                                        <div className="rounded-xl border border-zinc-200 bg-white p-3 dark:border-zinc-800 dark:bg-zinc-950">
                                            <div className="text-xs uppercase tracking-wide text-zinc-400">{t('acp.backendSession')}</div>
                                            <div className="mt-2 break-all text-zinc-700 dark:text-zinc-200">{selectedObservability.backendSessionId || t('acp.na')}</div>
                                        </div>
                                        <div className="rounded-xl border border-zinc-200 bg-white p-3 dark:border-zinc-800 dark:bg-zinc-950">
                                            <div className="text-xs uppercase tracking-wide text-zinc-400">{t('acp.agentSession')}</div>
                                            <div className="mt-2 break-all text-zinc-700 dark:text-zinc-200">{selectedObservability.agentSessionId || t('acp.na')}</div>
                                        </div>
                                    </div>

                                    <div className="rounded-xl border border-zinc-200 bg-white p-3 dark:border-zinc-800 dark:bg-zinc-950">
                                        <div className="text-xs uppercase tracking-wide text-zinc-400">{t('acp.workingDirectory')}</div>
                                        <div className="mt-2 break-all text-zinc-700 dark:text-zinc-200">{selectedObservability.cwd || t('acp.na')}</div>
                                    </div>

                                    <div className="rounded-xl border border-zinc-200 bg-white p-3 dark:border-zinc-800 dark:bg-zinc-950">
                                        <div className="mb-2 flex items-center justify-between">
                                            <span className="text-xs uppercase tracking-wide text-zinc-400">{t('acp.runtimeStatus')}</span>
                                            <span className="text-xs text-zinc-500 dark:text-zinc-400">
                                                {t('acp.activeTurn')}: {selectedObservability.hasActiveTurn ? t('acp.hasActiveTurnYes') : t('acp.hasActiveTurnNo')}
                                            </span>
                                        </div>
                                        <pre className="overflow-x-auto rounded-lg bg-zinc-950/95 p-3 text-xs text-zinc-100">
                                            {JSON.stringify(selectedObservability.runtimeStatus || {}, null, 2)}
                                        </pre>
                                    </div>

                                    <div className="rounded-xl border border-zinc-200 bg-white p-3 dark:border-zinc-800 dark:bg-zinc-950">
                                        <div className="mb-2 text-xs uppercase tracking-wide text-zinc-400">{t('acp.capabilities')}</div>
                                        <pre className="overflow-x-auto rounded-lg bg-zinc-950/95 p-3 text-xs text-zinc-100">
                                            {JSON.stringify(selectedObservability.capabilities || {}, null, 2)}
                                        </pre>
                                    </div>
                                </div>
                            )}
                        </aside>
                    </div>
                )}
            </div>
        </div>
    );
}
