'use client';

import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
  CheckCircle2,
  ChevronRight,
  ChevronLeft,
  Cloud,
  ExternalLink,
  Loader2,
  LogIn,
  MessageSquare,
  ShieldAlert,
  Sparkles,
  SkipForward,
} from 'lucide-react';
import { motion, AnimatePresence } from 'motion/react';
import { useI18n } from '@/lib/i18n/I18nContext';
import { Select } from '@/components/ui';
import { ToggleSwitch } from '@/components/ui';
import {
  apiGet,
  apiPost,
  type BrainModelPreset,
  type BrainState,
  type CerebellumState,
  type ChannelsState,
  type LocalizedText,
  type OAuthDeviceCodeStartResponse,
  type OAuthDeviceCodePollResponse,
  type OAuthStatusResponse,
} from '@/lib/api';
import { WeixinQRLogin } from '@/components/channels/WeixinQRLogin';

function formatBytes(bytes: number): string {
  if (bytes <= 0) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  let value = bytes;
  let unitIndex = 0;
  while (value >= 1024 && unitIndex < units.length - 1) {
    value /= 1024;
    unitIndex += 1;
  }
  return `${value >= 10 || unitIndex === 0 ? value.toFixed(0) : value.toFixed(1)} ${units[unitIndex]}`;
}

interface OnboardingViewProps {
  onComplete: () => void;
}

export function OnboardingView({ onComplete }: OnboardingViewProps) {
  const { t, language } = useI18n();
  const [step, setStep] = useState(1);
  const totalSteps = 3;

  // ─── Step 2: Channel Binding State ───
  const [channelBound, setChannelBound] = useState(false);
  const [channelToken, setChannelToken] = useState('');
  const [channelBaseUrl, setChannelBaseUrl] = useState('');

  // ─── Step 1: Cloud Model State ───
  const [presets, setPresets] = useState<BrainModelPreset[]>([]);
  const [selectedProviderId, setSelectedProviderId] = useState('');
  const [selectedModelId, setSelectedModelId] = useState('');
  const [apiKey, setApiKey] = useState('');
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');

  // OAuth
  const [oauthSession, setOAuthSession] = useState<OAuthDeviceCodeStartResponse | null>(null);
  const [oauthPolling, setOAuthPolling] = useState(false);
  const [oauthAuthed, setOAuthAuthed] = useState<Record<string, boolean>>({});
  const [oauthError, setOAuthError] = useState('');
  const oauthPollRef = useRef<ReturnType<typeof setInterval> | null>(null);

  // ─── Step 2: Cerebellum State ───
  const [enableCerebellum, setEnableCerebellum] = useState(false);
  const [cerebellumCatalog, setCerebellumCatalog] = useState<Array<{
    id: string; name: string; description?: LocalizedText; size?: string; filename?: string;
  }>>([]);
  const [selectedCerebellumModel, setSelectedCerebellumModel] = useState('');
  const [cerebellumState, setCerebellumState] = useState<CerebellumState | null>(null);
  const [pendingCerebellumSetup, setPendingCerebellumSetup] = useState(false);
  const brainPollRef = useRef<ReturnType<typeof setInterval> | null>(null);

  const refreshBrainState = useCallback(async () => {
    const state = await apiGet<BrainState>('/api/engine/brain');
    setPresets(state.modelPresets || []);
    setCerebellumState(state.cerebellum || null);
    if (state.cerebellum?.catalog) {
      setCerebellumCatalog(state.cerebellum.catalog);
      setSelectedCerebellumModel((current) => {
        if (current) return current;
        return state.cerebellum?.catalog?.[0]?.id || '';
      });
    }
    return state;
  }, []);

  // Load presets and OAuth status on mount
  useEffect(() => {
    refreshBrainState().catch(() => { });

    apiGet<OAuthStatusResponse>('/api/engine/brain/oauth/status')
      .then((resp) => setOAuthAuthed(resp.authenticated || {}))
      .catch(() => { });
  }, [refreshBrainState]);

  // Auto-select the free qwen-portal preset by default
  useEffect(() => {
    if (presets.length > 0 && !selectedProviderId) {
      const freePreset = presets.find(p => p.free);
      if (freePreset) {
        setSelectedProviderId(freePreset.id);
        if (freePreset.models?.length) {
          setSelectedModelId(freePreset.models[0].id);
        }
      } else {
        setSelectedProviderId(presets[0].id);
        if (presets[0].models?.length) {
          setSelectedModelId(presets[0].models[0].id);
        }
      }
    }
  }, [presets, selectedProviderId]);

  const selectedProvider = useMemo(
    () => presets.find(p => p.id === selectedProviderId),
    [presets, selectedProviderId]
  );
  const isOAuthPreset = selectedProvider?.authKind === 'oauth-device-code';
  const isOAuthAuthed = isOAuthPreset && oauthAuthed[selectedProviderId];

  const providerOptions = useMemo(() => presets.map(p => ({
    value: p.id,
    label: (language === 'zh' && p.labelZh) ? `${p.labelZh} (${p.label})` : p.label,
    labelNode: (
      <div className="flex items-center gap-2">
        <span>{(language === 'zh' && p.labelZh) ? `${p.labelZh} (${p.label})` : p.label}</span>
        {p.free && (
          <span className="px-1.5 py-0.5 text-[10px] font-medium bg-emerald-100 dark:bg-emerald-900/40 text-emerald-700 dark:text-emerald-300 rounded-full">
            {t('onboarding.step1.free')}
          </span>
        )}
      </div>
    ),
  })), [presets, language, t]);

  const modelOptions = useMemo(() => {
    if (!selectedProvider) return [];
    return selectedProvider.models.map(m => ({
      value: m.id,
      label: m.name || m.id,
    }));
  }, [selectedProvider]);

  // ─── OAuth Flow ───
  const startOAuth = useCallback(async (presetId: string) => {
    setOAuthError('');
    setOAuthPolling(true);
    try {
      const resp = await apiPost<OAuthDeviceCodeStartResponse>('/api/engine/brain/oauth/start', { presetId });
      setOAuthSession(resp);
      window.open(resp.verificationUrl, '_blank');
      const pollInterval = (resp.interval || 5) * 1000;
      oauthPollRef.current = setInterval(async () => {
        try {
          const poll = await apiPost<OAuthDeviceCodePollResponse>('/api/engine/brain/oauth/poll', { sessionId: resp.sessionId });
          if (poll.status === 'success') {
            setOAuthAuthed((prev) => ({ ...prev, [presetId]: true }));
            setOAuthPolling(false);
            setOAuthSession(null);
            if (oauthPollRef.current) { clearInterval(oauthPollRef.current); oauthPollRef.current = null; }
          } else if (poll.status === 'error' || poll.status === 'expired') {
            setOAuthError(poll.error || t('brain.oauthExpired'));
            setOAuthPolling(false);
            setOAuthSession(null);
            if (oauthPollRef.current) { clearInterval(oauthPollRef.current); oauthPollRef.current = null; }
          }
        } catch { /* ignore poll errors */ }
      }, pollInterval);
    } catch (err) {
      setOAuthError(err instanceof Error ? err.message : t('brain.oauthStartError'));
      setOAuthPolling(false);
    }
  }, [t]);

  useEffect(() => {
    return () => {
      if (oauthPollRef.current) { clearInterval(oauthPollRef.current); oauthPollRef.current = null; }
      if (brainPollRef.current) { clearInterval(brainPollRef.current); brainPollRef.current = null; }
    };
  }, []);

  useEffect(() => {
    if (cerebellumState?.downloadProgress?.active) {
      if (brainPollRef.current) return;
      brainPollRef.current = setInterval(() => {
        void refreshBrainState().catch(() => { });
      }, 1000);
      return;
    }
    if (brainPollRef.current) {
      clearInterval(brainPollRef.current);
      brainPollRef.current = null;
    }
  }, [cerebellumState?.downloadProgress?.active, refreshBrainState]);

  const completeSetup = useCallback(async () => {
    setSaving(true);
    try {
      await apiPost('/api/setup/complete', { completed: true });
      onComplete();
    } catch (err) {
      setError(err instanceof Error ? err.message : t('onboarding.saveError'));
    } finally {
      setSaving(false);
      setPendingCerebellumSetup(false);
    }
  }, [onComplete, t]);

  const ensureCerebellumDefaultModel = useCallback(async () => {
    if (!enableCerebellum || !selectedCerebellumModel) return;
    const selectedCatalogEntry = cerebellumCatalog.find((entry) => entry.id === selectedCerebellumModel);
    const targetModelId = selectedCatalogEntry?.filename?.replace(/\.gguf$/i, '') || selectedCerebellumModel;
    if (!targetModelId || cerebellumState?.modelId === targetModelId) return;
    const next = await apiPost<BrainState>('/api/engine/brain/cerebellum/model', { modelId: targetModelId });
    setCerebellumState(next.cerebellum || null);
  }, [cerebellumCatalog, cerebellumState?.modelId, enableCerebellum, selectedCerebellumModel]);

  // ─── Save & Complete ───
  const handleSaveModelAndNext = useCallback(async () => {
    if (!selectedProvider || !selectedModelId) return;
    setSaving(true);
    setError('');
    try {
      await apiPost('/api/engine/brain/models/upsert', {
        presetId: selectedProviderId,
        providerId: selectedProviderId,
        modelId: selectedModelId,
        displayName: selectedProvider.models.find(m => m.id === selectedModelId)?.name || selectedModelId,
        baseUrl: selectedProvider.baseUrl,
        api: selectedProvider.api,
        apiKey: apiKey || undefined,
        reasoning: selectedProvider.models.find(m => m.id === selectedModelId)?.reasoning || false,
        contextWindow: selectedProvider.models.find(m => m.id === selectedModelId)?.contextWindow || 0,
        maxTokens: selectedProvider.models.find(m => m.id === selectedModelId)?.maxTokens || 0,
      });
      // Set as default
      await apiPost('/api/engine/brain/models/default', {
        providerId: selectedProviderId,
        modelId: selectedModelId,
      });
      setStep(2); // Go to channel binding step
    } catch (err) {
      setError(err instanceof Error ? err.message : t('onboarding.saveError'));
    } finally {
      setSaving(false);
    }
  }, [selectedProvider, selectedProviderId, selectedModelId, apiKey, t]);

  // ─── Step 2: Save channel and proceed ───
  const handleSaveChannelAndNext = useCallback(async () => {
    if (!channelToken) {
      setStep(3);
      return;
    }
    setSaving(true);
    setError('');
    try {
      // Load current channels state
      const state = await apiGet<ChannelsState>('/api/integrations/channels');
      const entries = { ...(state.config.entries || {}) } as Record<string, Record<string, unknown>>;
      const rand = String(Math.floor(Math.random() * 100)).padStart(2, '0');
      const channelId = `weixin${rand}`;
      entries[channelId] = {
        enabled: true,
        agent: 'main',
        config: {
          driver: 'weixin',
          ...(channelBaseUrl ? { baseUrl: channelBaseUrl } : {}),
        },
        defaultAccount: 'main',
        accounts: {
          main: { token: channelToken },
        },
      };
      await apiPost('/api/integrations/channels/save', {
        channels: { ...state.config, entries },
      });
      setStep(3);
    } catch (err) {
      setError(err instanceof Error ? err.message : t('onboarding.saveError'));
    } finally {
      setSaving(false);
    }
  }, [channelToken, channelBaseUrl, t]);

  const handleFinish = useCallback(async () => {
    setError('');

    const selectedCatalogEntry = cerebellumCatalog.find((entry) => entry.id === selectedCerebellumModel);
    const selectedModelFileId = selectedCatalogEntry?.filename?.replace(/\.gguf$/i, '') || selectedCerebellumModel;
    const alreadyDownloaded = (cerebellumState?.models || []).some((model) => model.id === selectedModelFileId);
    const activeDownload = cerebellumState?.downloadProgress;

    if (!enableCerebellum || !selectedCerebellumModel || alreadyDownloaded) {
      if (enableCerebellum && selectedCerebellumModel && alreadyDownloaded) {
        await ensureCerebellumDefaultModel();
      }
      await completeSetup();
      return;
    }

    if (activeDownload?.active && activeDownload.presetId === selectedCerebellumModel) {
      setPendingCerebellumSetup(true);
      return;
    }

    setSaving(true);
    try {
      const next = await apiPost<BrainState>('/api/engine/brain/cerebellum/download', { presetId: selectedCerebellumModel });
      setCerebellumState(next.cerebellum || null);
      setPendingCerebellumSetup(true);
    } catch (err) {
      setError(err instanceof Error ? err.message : t('onboarding.saveError'));
    } finally {
      setSaving(false);
    }
  }, [cerebellumCatalog, cerebellumState?.downloadProgress, cerebellumState?.models, completeSetup, enableCerebellum, ensureCerebellumDefaultModel, selectedCerebellumModel, t]);

  const handleCancelCerebellumDownload = useCallback(async () => {
    setError('');
    try {
      const next = await apiPost<BrainState>('/api/engine/brain/cerebellum/download/cancel', {});
      setCerebellumState(next.cerebellum || null);
      setPendingCerebellumSetup(false);
    } catch (err) {
      setError(err instanceof Error ? err.message : t('brain.downloadCancelError'));
    }
  }, [t]);

  const handleSkipStep1 = useCallback(async () => {
    setStep(2);
  }, []);

  const handleSkipStep2 = useCallback(async () => {
    setStep(3);
  }, []);

  const handleSkipStep3 = useCallback(async () => {
    setSaving(true);
    try {
      await apiPost('/api/setup/complete', { completed: true });
      onComplete();
    } catch {
      onComplete();
    }
  }, [onComplete]);

  const canProceedStep1 = isOAuthPreset ? isOAuthAuthed : (apiKey.trim().length > 0 && selectedModelId.length > 0);

  const selectedCerebellumEntry = cerebellumCatalog.find(c => c.id === selectedCerebellumModel);
  const cerebellumDownloadProgress = cerebellumState?.downloadProgress;
  const selectedCerebellumModelId = selectedCerebellumEntry?.filename?.replace(/\.gguf$/i, '') || selectedCerebellumModel;
  const selectedCerebellumDownloaded = (cerebellumState?.models || []).some((model) => model.id === selectedCerebellumModelId);
  const isSelectedCerebellumDownloading = cerebellumDownloadProgress?.active && cerebellumDownloadProgress.presetId === selectedCerebellumModel;
  const cerebellumDownloadPct =
    isSelectedCerebellumDownloading && (cerebellumDownloadProgress?.totalBytes || 0) > 0
      ? Math.min(100, Math.round(((cerebellumDownloadProgress?.downloadedBytes || 0) / (cerebellumDownloadProgress?.totalBytes || 1)) * 100))
      : 0;

  useEffect(() => {
    if (!pendingCerebellumSetup) return;
    if (cerebellumDownloadProgress?.active) return;
    if (cerebellumDownloadProgress?.canceled) {
      setPendingCerebellumSetup(false);
      return;
    }
    if (cerebellumDownloadProgress?.error) {
      setPendingCerebellumSetup(false);
      setError(cerebellumDownloadProgress.error);
      return;
    }
    if (selectedCerebellumDownloaded) {
      void (async () => {
        try {
          await ensureCerebellumDefaultModel();
          await completeSetup();
        } catch (err) {
          setPendingCerebellumSetup(false);
          setError(err instanceof Error ? err.message : t('onboarding.saveError'));
        }
      })();
    }
  }, [completeSetup, cerebellumDownloadProgress?.active, cerebellumDownloadProgress?.canceled, cerebellumDownloadProgress?.error, ensureCerebellumDefaultModel, pendingCerebellumSetup, selectedCerebellumDownloaded, t]);

  return (
    <div className="flex items-center justify-center min-h-screen bg-gradient-to-br from-zinc-50 via-white to-indigo-50/30 dark:from-zinc-950 dark:via-zinc-950 dark:to-indigo-950/10 p-4">
      <motion.div
        initial={{ opacity: 0, y: 30 }}
        animate={{ opacity: 1, y: 0 }}
        transition={{ duration: 0.5, ease: 'easeOut' }}
        className="w-full max-w-lg"
      >
        {/* Header */}
        <div className="text-center mb-8">
          <motion.div
            initial={{ scale: 0.8, opacity: 0 }}
            animate={{ scale: 1, opacity: 1 }}
            transition={{ delay: 0.1, duration: 0.4 }}
            className="inline-flex items-center justify-center w-16 h-16 rounded-2xl bg-indigo-100 dark:bg-indigo-900/30 mb-4"
          >
            <Sparkles className="w-8 h-8 text-indigo-600 dark:text-indigo-400" />
          </motion.div>
          <h1 className="text-2xl font-bold text-zinc-900 dark:text-zinc-100">{t('onboarding.title')}</h1>
          <p className="mt-2 text-sm text-zinc-500 dark:text-zinc-400">{t('onboarding.subtitle')}</p>
        </div>

        {/* Progress indicator */}
        <div className="flex items-center justify-center gap-3 mb-6">
          {[1, 2, 3].map(s => (
            <div key={s} className="flex items-center gap-2">
              <div className={`w-8 h-8 rounded-full flex items-center justify-center text-sm font-semibold transition-colors ${s < step ? 'bg-indigo-600 text-white' :
                s === step ? 'bg-indigo-600 text-white' :
                  'bg-zinc-200 dark:bg-zinc-700 text-zinc-500 dark:text-zinc-400'
                }`}>
                {s < step ? <CheckCircle2 className="w-4 h-4" /> : s}
              </div>
              {s < totalSteps && (
                <div className={`w-12 h-0.5 rounded-full transition-colors ${s < step ? 'bg-indigo-600' : 'bg-zinc-200 dark:bg-zinc-700'
                  }`} />
              )}
            </div>
          ))}
        </div>
        <p className="text-center text-xs text-zinc-400 dark:text-zinc-500 mb-6">
          {t('onboarding.step', { current: String(step), total: String(totalSteps) })}
        </p>

        {/* Card */}
        <div className="bg-white dark:bg-zinc-900 border border-zinc-200 dark:border-zinc-800 rounded-2xl shadow-lg overflow-hidden">
          <AnimatePresence mode="wait">
            {step === 1 && (
              <motion.div
                key="step1"
                initial={{ opacity: 0, x: 30 }}
                animate={{ opacity: 1, x: 0 }}
                exit={{ opacity: 0, x: -30 }}
                transition={{ duration: 0.25 }}
                className="p-6"
              >
                <div className="flex items-center gap-3 mb-4">
                  <div className="p-2 rounded-lg bg-blue-100 dark:bg-blue-900/30">
                    <Cloud className="w-5 h-5 text-blue-600 dark:text-blue-400" />
                  </div>
                  <div>
                    <h2 className="text-lg font-semibold text-zinc-900 dark:text-zinc-100">{t('onboarding.step1.title')}</h2>
                  </div>
                </div>
                <p className="text-sm text-zinc-500 dark:text-zinc-400 mb-5 leading-relaxed">{t('onboarding.step1.desc')}</p>

                {/* Provider Select */}
                <div className="space-y-4">
                  <div>
                    <label className="block text-sm font-medium text-zinc-700 dark:text-zinc-300 mb-1.5">
                      {t('onboarding.step1.providerLabel')}
                    </label>
                    <Select
                      value={selectedProviderId}
                      onChange={(v) => {
                        setSelectedProviderId(v);
                        setApiKey('');
                        setOAuthSession(null);
                        setOAuthPolling(false);
                        setOAuthError('');
                        if (oauthPollRef.current) { clearInterval(oauthPollRef.current); oauthPollRef.current = null; }
                        const provider = presets.find(p => p.id === v);
                        if (provider?.models?.length) {
                          setSelectedModelId(provider.models[0].id);
                        } else {
                          setSelectedModelId('');
                        }
                      }}
                      options={providerOptions}
                      searchable
                    />
                  </div>

                  {/* Model Select */}
                  {selectedProvider && (
                    <div>
                      <label className="block text-sm font-medium text-zinc-700 dark:text-zinc-300 mb-1.5">
                        {t('onboarding.step1.modelLabel')}
                      </label>
                      <Select
                        value={selectedModelId}
                        onChange={setSelectedModelId}
                        options={modelOptions}
                      />
                    </div>
                  )}

                  {/* OAuth flow */}
                  {isOAuthPreset && (
                    <div className="mt-2">
                      {isOAuthAuthed ? (
                        <div className="flex items-center gap-2 p-3 rounded-xl bg-emerald-50 dark:bg-emerald-900/20 border border-emerald-200 dark:border-emerald-800">
                          <CheckCircle2 className="w-5 h-5 text-emerald-600 dark:text-emerald-400" />
                          <span className="text-sm font-medium text-emerald-700 dark:text-emerald-300">{t('onboarding.step1.oauthAuthorized')}</span>
                        </div>
                      ) : oauthPolling ? (
                        <div className="space-y-3">
                          <div className="flex items-center gap-2 p-3 rounded-xl bg-amber-50 dark:bg-amber-900/20 border border-amber-200 dark:border-amber-800">
                            <Loader2 className="w-4 h-4 text-amber-600 dark:text-amber-400 animate-spin" />
                            <span className="text-sm text-amber-700 dark:text-amber-300">{t('onboarding.step1.oauthPolling')}</span>
                          </div>
                          {oauthSession?.userCode && (
                            <div className="flex items-center justify-between p-3 rounded-xl bg-zinc-50 dark:bg-zinc-800/50 border border-zinc-200 dark:border-zinc-700">
                              <span className="text-xs text-zinc-500 dark:text-zinc-400">{t('onboarding.step1.oauthUserCode')}</span>
                              <code className="text-lg font-mono font-bold tracking-widest text-indigo-600 dark:text-indigo-400">{oauthSession.userCode}</code>
                            </div>
                          )}
                        </div>
                      ) : (
                        <div className="space-y-3">
                          <p className="text-xs text-zinc-400 dark:text-zinc-500">{t('onboarding.step1.oauthHint')}</p>
                          <button
                            onClick={() => startOAuth(selectedProviderId)}
                            className="flex items-center gap-2 px-4 py-2.5 rounded-xl bg-indigo-600 hover:bg-indigo-700 text-white text-sm font-medium transition-colors shadow-sm"
                          >
                            <LogIn className="w-4 h-4" />
                            {t('onboarding.step1.oauthAuthorize')}
                            <ExternalLink className="w-3 h-3 opacity-50" />
                          </button>
                        </div>
                      )}
                      {oauthError && (
                        <p className="mt-2 text-xs text-red-500">{oauthError}</p>
                      )}
                    </div>
                  )}

                  {/* API Key for non-OAuth providers */}
                  {selectedProvider && !isOAuthPreset && (
                    <div>
                      <label className="block text-sm font-medium text-zinc-700 dark:text-zinc-300 mb-1.5">
                        {t('onboarding.step1.apiKeyLabel')}
                      </label>
                      <input
                        type="password"
                        value={apiKey}
                        onChange={e => setApiKey(e.target.value)}
                        placeholder={t('onboarding.step1.apiKeyPlaceholder')}
                        className="w-full px-3 py-2 rounded-xl border border-zinc-200 dark:border-zinc-700 bg-zinc-50 dark:bg-zinc-800 text-sm text-zinc-900 dark:text-zinc-100 placeholder-zinc-400 dark:placeholder-zinc-500 outline-none focus:border-indigo-400 dark:focus:border-indigo-500 focus:ring-1 focus:ring-indigo-400/30 transition-colors"
                      />
                    </div>
                  )}
                </div>

                {error && (
                  <p className="mt-3 text-xs text-red-500">{error}</p>
                )}

                {/* Actions */}
                <div className="flex items-center justify-between mt-6 pt-4 border-t border-zinc-100 dark:border-zinc-800">
                  <button
                    onClick={handleSkipStep1}
                    className="flex items-center gap-1.5 px-3 py-2 text-sm text-zinc-400 hover:text-zinc-600 dark:text-zinc-500 dark:hover:text-zinc-300 transition-colors"
                  >
                    <SkipForward className="w-3.5 h-3.5" />
                    {t('onboarding.skip')}
                  </button>
                  <button
                    onClick={handleSaveModelAndNext}
                    disabled={!canProceedStep1 || saving}
                    className="flex items-center gap-2 px-5 py-2.5 rounded-xl bg-indigo-600 hover:bg-indigo-700 disabled:bg-indigo-400 disabled:cursor-not-allowed text-white text-sm font-medium transition-colors shadow-sm"
                  >
                    {saving ? (
                      <>
                        <Loader2 className="w-4 h-4 animate-spin" />
                        {t('onboarding.savingModel')}
                      </>
                    ) : (
                      <>
                        {t('onboarding.next')}
                        <ChevronRight className="w-4 h-4" />
                      </>
                    )}
                  </button>
                </div>
              </motion.div>
            )}

            {step === 2 && (
              <motion.div
                key="step2"
                initial={{ opacity: 0, x: 30 }}
                animate={{ opacity: 1, x: 0 }}
                exit={{ opacity: 0, x: -30 }}
                transition={{ duration: 0.25 }}
                className="p-6"
              >
                <div className="flex items-center gap-3 mb-4">
                  <div className="p-2 rounded-lg bg-green-100 dark:bg-green-900/30">
                    <MessageSquare className="w-5 h-5 text-green-600 dark:text-green-400" />
                  </div>
                  <div>
                    <h2 className="text-lg font-semibold text-zinc-900 dark:text-zinc-100">{t('onboarding.step2.title' as any)}</h2>
                  </div>
                </div>
                <p className="text-sm text-zinc-500 dark:text-zinc-400 mb-5 leading-relaxed">{t('onboarding.step2.desc' as any)}</p>

                <WeixinQRLogin
                  autoStart
                  onLoginSuccess={(token, baseUrl) => {
                    setChannelToken(token);
                    if (baseUrl) setChannelBaseUrl(baseUrl);
                    setChannelBound(true);
                  }}
                />

                {error && (
                  <p className="mt-3 text-xs text-red-500">{error}</p>
                )}

                {/* Actions */}
                <div className="flex items-center justify-between mt-6 pt-4 border-t border-zinc-100 dark:border-zinc-800">
                  <div className="flex items-center gap-2">
                    <button
                      onClick={() => setStep(1)}
                      className="flex items-center gap-1.5 px-3 py-2 text-sm text-zinc-500 hover:text-zinc-700 dark:text-zinc-400 dark:hover:text-zinc-200 transition-colors"
                    >
                      <ChevronLeft className="w-3.5 h-3.5" />
                      {t('onboarding.previous')}
                    </button>
                  </div>
                  <div className="flex items-center gap-2">
                    <button
                      onClick={handleSkipStep2}
                      className="flex items-center gap-1.5 px-3 py-2 text-sm text-zinc-400 hover:text-zinc-600 dark:text-zinc-500 dark:hover:text-zinc-300 transition-colors"
                    >
                      <SkipForward className="w-3.5 h-3.5" />
                      {t('onboarding.skip')}
                    </button>
                    <button
                      onClick={handleSaveChannelAndNext}
                      disabled={!channelBound || saving}
                      className="flex items-center gap-2 px-5 py-2.5 rounded-xl bg-indigo-600 hover:bg-indigo-700 disabled:bg-indigo-400 disabled:cursor-not-allowed text-white text-sm font-medium transition-colors shadow-sm"
                    >
                      {saving ? (
                        <Loader2 className="w-4 h-4 animate-spin" />
                      ) : (
                        <>
                          {t('onboarding.next')}
                          <ChevronRight className="w-4 h-4" />
                        </>
                      )}
                    </button>
                  </div>
                </div>
              </motion.div>
            )}

            {step === 3 && (
              <motion.div
                key="step3"
                initial={{ opacity: 0, x: 30 }}
                animate={{ opacity: 1, x: 0 }}
                exit={{ opacity: 0, x: -30 }}
                transition={{ duration: 0.25 }}
                className="p-6"
              >
                <div className="flex items-center gap-3 mb-4">
                  <div className="p-2 rounded-lg bg-amber-100 dark:bg-amber-900/30">
                    <ShieldAlert className="w-5 h-5 text-amber-600 dark:text-amber-400" />
                  </div>
                  <div>
                    <h2 className="text-lg font-semibold text-zinc-900 dark:text-zinc-100">{t('onboarding.step3.title' as any)}</h2>
                  </div>
                </div>
                <p className="text-sm text-zinc-500 dark:text-zinc-400 mb-5 leading-relaxed">{t('onboarding.step3.desc' as any)}</p>

                <div className="space-y-4">
                  {/* Enable toggle */}
                  <div className="flex items-center justify-between p-4 rounded-xl bg-zinc-50 dark:bg-zinc-800/50 border border-zinc-200 dark:border-zinc-700">
                    <span className="text-sm font-medium text-zinc-700 dark:text-zinc-300">
                      {t('onboarding.step3.enableLabel' as any)}
                    </span>
                    <ToggleSwitch checked={enableCerebellum} onChange={setEnableCerebellum} />
                  </div>

                  {enableCerebellum && (
                    <motion.div
                      initial={{ opacity: 0, height: 0 }}
                      animate={{ opacity: 1, height: 'auto' }}
                      exit={{ opacity: 0, height: 0 }}
                      className="space-y-4"
                    >
                      {/* Model selection */}
                      <div>
                        <label className="block text-sm font-medium text-zinc-700 dark:text-zinc-300 mb-1.5">
                          {t('onboarding.step3.modelLabel' as any)}
                        </label>
                        <Select
                          value={selectedCerebellumModel}
                          onChange={setSelectedCerebellumModel}
                          options={cerebellumCatalog.map(c => ({
                            value: c.id,
                            label: c.name,
                            labelNode: (
                              <div className="flex items-center justify-between w-full">
                                <span>{c.name}</span>
                                {c.size && (
                                  <span className="ml-2 text-xs text-zinc-400 dark:text-zinc-500">{c.size}</span>
                                )}
                              </div>
                            ),
                          }))}
                        />
                      </div>

                      {/* Download hint */}
                      {selectedCerebellumEntry?.size && (
                        <div className="flex items-start gap-2 p-3 rounded-xl bg-amber-50 dark:bg-amber-900/20 border border-amber-200 dark:border-amber-800">
                          <ShieldAlert className="w-4 h-4 text-amber-600 dark:text-amber-400 mt-0.5 shrink-0" />
                          <p className="text-xs text-amber-700 dark:text-amber-300 leading-relaxed">
                            {t('onboarding.step3.downloadHint' as any, { size: selectedCerebellumEntry.size })}
                          </p>
                        </div>
                      )}

                      {isSelectedCerebellumDownloading && cerebellumDownloadProgress ? (
                        <div className="space-y-3 rounded-xl border border-zinc-200 bg-zinc-50 p-4 dark:border-zinc-700 dark:bg-zinc-800/50">
                          <div className="flex items-center justify-between text-sm text-zinc-600 dark:text-zinc-300">
                            <span className="flex items-center gap-2 font-medium">
                              <Loader2 className="h-4 w-4 animate-spin text-indigo-500" />
                              {t('brain.catalogDownloading')}
                            </span>
                            <span className="text-xs text-zinc-500 dark:text-zinc-400">
                              {cerebellumDownloadProgress.totalBytes > 0
                                ? `${formatBytes(cerebellumDownloadProgress.downloadedBytes)} / ${formatBytes(cerebellumDownloadProgress.totalBytes)} (${cerebellumDownloadPct}%)`
                                : formatBytes(cerebellumDownloadProgress.downloadedBytes)}
                            </span>
                          </div>
                          <div className="h-2 w-full overflow-hidden rounded-full bg-zinc-200 dark:bg-zinc-700">
                            <div
                              className="h-full rounded-full bg-indigo-500 transition-all duration-300"
                              style={{ width: cerebellumDownloadProgress.totalBytes > 0 ? `${cerebellumDownloadPct}%` : '100%' }}
                            />
                          </div>
                          <div className="flex justify-end">
                            <button
                              onClick={handleCancelCerebellumDownload}
                              className="px-3 py-1.5 rounded-lg border border-zinc-300 dark:border-zinc-600 text-sm text-zinc-700 dark:text-zinc-300 hover:bg-white dark:hover:bg-zinc-700 transition-colors"
                            >
                              {t('common.cancel')}
                            </button>
                          </div>
                        </div>
                      ) : null}

                      {!isSelectedCerebellumDownloading && cerebellumDownloadProgress?.error && !cerebellumDownloadProgress.canceled && cerebellumDownloadProgress.presetId === selectedCerebellumModel ? (
                        <div className="rounded-xl border border-rose-200 bg-rose-50 p-3 text-xs text-rose-700 dark:border-rose-900/50 dark:bg-rose-950/30 dark:text-rose-300">
                          {t('brain.downloadFailed')}: {cerebellumDownloadProgress.error}
                        </div>
                      ) : null}

                      {!isSelectedCerebellumDownloading && selectedCerebellumDownloaded ? (
                        <div className="rounded-xl border border-emerald-200 bg-emerald-50 p-3 text-xs text-emerald-700 dark:border-emerald-900/50 dark:bg-emerald-950/30 dark:text-emerald-300">
                          {t('brain.catalogDownloaded')}
                        </div>
                      ) : null}
                    </motion.div>
                  )}
                </div>

                {error && (
                  <p className="mt-3 text-xs text-red-500">{error}</p>
                )}

                {/* Actions */}
                <div className="flex items-center justify-between mt-6 pt-4 border-t border-zinc-100 dark:border-zinc-800">
                  <div className="flex items-center gap-2">
                    <button
                      onClick={() => setStep(2)}
                      className="flex items-center gap-1.5 px-3 py-2 text-sm text-zinc-500 hover:text-zinc-700 dark:text-zinc-400 dark:hover:text-zinc-200 transition-colors"
                    >
                      <ChevronLeft className="w-3.5 h-3.5" />
                      {t('onboarding.previous')}
                    </button>
                  </div>
                  <div className="flex items-center gap-2">
                    <button
                      onClick={handleSkipStep3}
                      className="flex items-center gap-1.5 px-3 py-2 text-sm text-zinc-400 hover:text-zinc-600 dark:text-zinc-500 dark:hover:text-zinc-300 transition-colors"
                    >
                      <SkipForward className="w-3.5 h-3.5" />
                      {t('onboarding.skip')}
                    </button>
                    <button
                      onClick={handleFinish}
                      disabled={saving || pendingCerebellumSetup || Boolean(cerebellumDownloadProgress?.active)}
                      className="flex items-center gap-2 px-5 py-2.5 rounded-xl bg-indigo-600 hover:bg-indigo-700 disabled:bg-indigo-400 disabled:cursor-not-allowed text-white text-sm font-medium transition-colors shadow-sm"
                    >
                      {saving ? (
                        <Loader2 className="w-4 h-4 animate-spin" />
                      ) : pendingCerebellumSetup ? (
                        <Loader2 className="w-4 h-4 animate-spin" />
                      ) : (
                        <Sparkles className="w-4 h-4" />
                      )}
                      {pendingCerebellumSetup ? t('brain.catalogDownloading') : t('onboarding.finish')}
                    </button>
                  </div>
                </div>
              </motion.div>
            )}
          </AnimatePresence>
        </div>
      </motion.div>
    </div>
  );
}
