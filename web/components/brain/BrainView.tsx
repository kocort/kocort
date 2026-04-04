'use client';

import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
  AlertCircle,
  CheckCircle2,
  Cloud,
  Cpu,
  Database,
  Download,
  ExternalLink,
  Globe,
  HardDrive,
  Loader2,
  LogIn,
  LogOut,
  Pencil,
  Play,
  Plus,
  RefreshCw,
  RotateCcw,
  Settings2,
  ShieldAlert,
  Square,
  Star,
  Trash2,
} from 'lucide-react';
import { useI18n } from '@/lib/i18n/I18nContext';
import { resolveLocalizedText } from '@/lib/i18n/localizedText';
import { ErrorAlert, Modal, PageHeader, Select, ConfirmDialog } from '@/components/ui';
import { apiGet, apiPost, type BrainModelPreset, type BrainModelRecord, type BrainState, type CerebellumState, type LocalModelState, type NetworkState, type SamplingParams, type OAuthDeviceCodeStartResponse, type OAuthDeviceCodePollResponse, type OAuthStatusResponse } from '@/lib/api';

type ModelForm = {
  mode: 'preset' | 'custom';
  // Preset mode
  presetProviderId: string;
  presetModelInput: string;
  // Edit tracking
  existingProviderId: string;
  existingModelId: string;
  // Custom mode fields
  modelId: string;
  baseUrl: string;
  api: string;
  apiKey: string;
  reasoning: boolean;
  contextWindow: string;
  maxTokens: string;
};

function defaultModelForm(): ModelForm {
  return {
    mode: 'preset',
    presetProviderId: '',
    presetModelInput: '',
    existingProviderId: '',
    existingModelId: '',
    modelId: '',
    baseUrl: '',
    api: 'openai-completions',
    apiKey: '',
    reasoning: true,
    contextWindow: '',
    maxTokens: '',
  };
}

/** Derive a provider ID from a base URL or model ID. */
function deriveProviderId(baseUrl: string, modelId: string): string {
  const url = baseUrl.trim();
  const mid = modelId.trim();
  if (url) {
    try {
      const host = new URL(url).hostname.toLowerCase();
      const parts = host.split('.');
      const skip = new Set(['api', 'www', 'integrate', 'com', 'net', 'org', 'io', 'ai', 'cn', 'co']);
      const meaningful = parts.filter((p) => !skip.has(p));
      const candidate = (meaningful.length > 0 ? meaningful[0] : parts[parts.length - 2] ?? '').replace(/[^a-z0-9-]/g, '');
      if (candidate) return candidate;
    } catch {
      /* ignore invalid URL */
    }
  }
  if (mid.includes('/')) {
    return mid.split('/')[0].toLowerCase().replace(/[^a-z0-9-]/g, '');
  }
  return '';
}

/** Derive a human-readable display name from a model ID. */
function deriveDisplayName(modelId: string): string {
  const id = modelId.trim();
  if (!id) return '';
  // Use the part after last '/' if there is one (strip provider prefix)
  const base = id.includes('/') ? id.split('/').slice(1).join('/') : id;
  return base
    .replace(/[-_]/g, ' ')
    .replace(/\b\w/g, (c) => c.toUpperCase())
    .trim();
}

function recordLabel(record: BrainModelRecord): string {
  return record.displayName?.trim() || record.modelId;
}

function maskKey(value?: string): string {
  const key = String(value || '').trim();
  if (!key) return '';
  if (key.length <= 10) return `${key.slice(0, 3)}...`;
  return `${key.slice(0, 6)}...${key.slice(-4)}`;
}

/** Format bytes to human-readable size. */
function formatBytes(bytes: number): string {
  if (bytes <= 0) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB'];
  const i = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1);
  const value = bytes / Math.pow(1024, i);
  return `${value.toFixed(i > 1 ? 1 : 0)} ${units[i]}`;
}

const API_KIND_OPTIONS = [
  { value: 'openai-completions', label: 'OpenAI Compatible' },
  { value: 'anthropic-messages', label: 'Anthropic Messages' },
];

type BrainMode = 'cloud' | 'local';
type LocalDeleteTarget = {
  scope: 'brainLocal' | 'cerebellum';
  modelId: string;
  modelName: string;
};

export function BrainView() {
  const { t, language } = useI18n();
  const [state, setState] = useState<BrainState | null>(null);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const [modelModal, setModelModal] = useState<'create' | 'edit' | null>(null);
  const [modelForm, setModelForm] = useState<ModelForm>(defaultModelForm());
  const [cerebellumOperating, setCerebellumOperating] = useState(false);
  const [cerebellumPendingAction, setCerebellumPendingAction] = useState<'starting' | 'stopping' | 'restarting' | null>(null);
  const [brainLocalOperating, setBrainLocalOperating] = useState(false);
  const [brainLocalPendingAction, setBrainLocalPendingAction] = useState<'starting' | 'stopping' | 'restarting' | null>(null);
  const [modeSwitching, setModeSwitching] = useState(false);
  const [deleteTarget, setDeleteTarget] = useState<BrainModelRecord | null>(null);
  const [localDeleteTarget, setLocalDeleteTarget] = useState<LocalDeleteTarget | null>(null);

  // Proxy popup state (shown when download fails)
  const [proxyModalOpen, setProxyModalOpen] = useState(false);
  const [proxyModalUrl, setProxyModalUrl] = useState('');
  const [proxyModalSaving, setProxyModalSaving] = useState(false);
  const [proxyModalError, setProxyModalError] = useState('');
  const [proxyRetryPresetId, setProxyRetryPresetId] = useState('');

  // OAuth device-code flow state
  const [oauthSession, setOAuthSession] = useState<OAuthDeviceCodeStartResponse | null>(null);
  const [oauthPolling, setOAuthPolling] = useState(false);
  const [oauthAuthed, setOAuthAuthed] = useState<Record<string, boolean>>({});
  const [oauthError, setOAuthError] = useState('');
  const oauthPollRef = useRef<ReturnType<typeof setInterval> | null>(null);

  // Params modal state (shared for brain local and cerebellum)
  const [paramsModalTarget, setParamsModalTarget] = useState<'brainLocal' | 'cerebellum' | null>(null);
  const [paramsForm, setParamsForm] = useState({
    temp: 0.1, topP: 0.9, topK: 40, minP: 0, typicalP: 0,
    repeatLastN: 64, penaltyRepeat: 1.1, penaltyFreq: 0, penaltyPresent: 0,
    threads: 4, contextSize: 4096, gpuLayers: 0,
    enableThinking: true,
  });
  const [paramsSaving, setParamsSaving] = useState(false);
  const [paramsError, setParamsError] = useState('');

  // Download progress polling
  const pollRef = useRef<ReturnType<typeof setInterval> | null>(null);

  const presets = state?.modelPresets || [];
  const records = useMemo(
    () =>
      [...(state?.modelRecords || [])].sort((a, b) => {
        if (Boolean(a.isDefault) !== Boolean(b.isDefault)) return a.isDefault ? -1 : 1;
        if (Boolean(a.isFallback) !== Boolean(b.isFallback)) return a.isFallback ? -1 : 1;
        if (a.providerId !== b.providerId) return a.providerId.localeCompare(b.providerId);
        return a.modelId.localeCompare(b.modelId);
      }),
    [state],
  );

  // Presets are already grouped by provider
  const presetProviders = useMemo(() => presets, [presets]);

  const selectedProvider = presetProviders.find((p) => p.id === modelForm.presetProviderId);
  const matchedPresetModel = selectedProvider?.models.find((m) => m.id === modelForm.presetModelInput);
  const isOAuthPreset = selectedProvider?.authKind === 'oauth-device-code';
  const isOAuthAuthed = isOAuthPreset && oauthAuthed[modelForm.presetProviderId];

  // Load OAuth status on mount
  useEffect(() => {
    apiGet<OAuthStatusResponse>('/api/engine/brain/oauth/status')
      .then((resp) => setOAuthAuthed(resp.authenticated || {}))
      .catch(() => { });
  }, []);

  const derivedProviderId = deriveProviderId(modelForm.baseUrl, modelForm.modelId);
  const derivedDisplayName = deriveDisplayName(modelForm.modelId);
  const editingRecord = useMemo(
    () => records.find((record) => record.providerId === modelForm.existingProviderId && record.modelId === modelForm.existingModelId) || null,
    [records, modelForm.existingModelId, modelForm.existingProviderId],
  );

  const cerebellum = state?.cerebellum;
  const brainLocal = state?.brainLocal;
  const dlProgress = cerebellum?.downloadProgress;
  const brainLocalDlProgress = brainLocal?.downloadProgress;

  const loadState = useCallback(async () => {
    setError('');
    try {
      const next = await apiGet<BrainState>('/api/engine/brain');
      setState(next);
      return next;
    } catch (err) {
      setError(err instanceof Error ? err.message : t('brain.loadError'));
      return null;
    } finally {
      setLoading(false);
    }
  }, []);

  // Start / stop polling for download progress
  const startProgressPoll = useCallback(() => {
    if (pollRef.current) return;
    pollRef.current = setInterval(async () => {
      try {
        const next = await apiGet<BrainState>('/api/engine/brain');
        setState(next);
        const cerebellumActive = next.cerebellum?.downloadProgress?.active;
        const brainLocalActive = next.brainLocal?.downloadProgress?.active;
        if (!cerebellumActive && !brainLocalActive) {
          if (pollRef.current) {
            clearInterval(pollRef.current);
            pollRef.current = null;
          }
        }
      } catch {
        // ignore polling errors
      }
    }, 1500);
  }, []);

  const stopProgressPoll = useCallback(() => {
    if (pollRef.current) {
      clearInterval(pollRef.current);
      pollRef.current = null;
    }
  }, []);

  useEffect(() => {
    return () => stopProgressPoll();
  }, [stopProgressPoll]);

  useEffect(() => {
    void loadState();
  }, [loadState]);

  // Start polling when a download is detected as active
  useEffect(() => {
    if (dlProgress?.active || brainLocalDlProgress?.active) {
      startProgressPoll();
    } else {
      stopProgressPoll();
    }
  }, [dlProgress?.active, brainLocalDlProgress?.active, startProgressPoll, stopProgressPoll]);

  const refresh = async () => {
    setLoading(true);
    await loadState();
  };

  const openCreateModel = () => {
    setModelForm(defaultModelForm());
    setOAuthSession(null);
    setOAuthPolling(false);
    setOAuthError('');
    if (oauthPollRef.current) { clearInterval(oauthPollRef.current); oauthPollRef.current = null; }
    setModelModal('create');
  };

  // ─── OAuth device-code flow ───
  const startOAuth = async (presetId: string) => {
    setOAuthError('');
    setOAuthPolling(true);
    try {
      const resp = await apiPost<OAuthDeviceCodeStartResponse>('/api/engine/brain/oauth/start', { presetId });
      setOAuthSession(resp);
      // Open the verification URL in a new tab
      window.open(resp.verificationUrl, '_blank');
      // Start polling
      const pollInterval = (resp.interval || 5) * 1000;
      oauthPollRef.current = setInterval(async () => {
        try {
          const poll = await apiPost<OAuthDeviceCodePollResponse>('/api/engine/brain/oauth/poll', { sessionId: resp.sessionId });
          if (poll.status === 'success') {
            // Auth succeeded
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
          // 'pending' — keep polling
        } catch {
          // ignore poll errors, will retry
        }
      }, pollInterval);
    } catch (err) {
      setOAuthError(err instanceof Error ? err.message : t('brain.oauthStartError'));
      setOAuthPolling(false);
    }
  };

  const logoutOAuth = async (presetId: string) => {
    try {
      await apiPost('/api/engine/brain/oauth/logout', { providerId: presetId });
      setOAuthAuthed((prev) => ({ ...prev, [presetId]: false }));
    } catch {
      // ignore
    }
  };

  // Clean up OAuth polling on unmount
  useEffect(() => {
    return () => {
      if (oauthPollRef.current) { clearInterval(oauthPollRef.current); oauthPollRef.current = null; }
    };
  }, []);

  const openEditModel = (record: BrainModelRecord) => {
    setModelForm({
      mode: 'custom',
      presetProviderId: '',
      presetModelInput: '',
      existingProviderId: record.providerId,
      existingModelId: record.modelId,
      modelId: record.modelId,
      baseUrl: record.baseUrl || '',
      api: record.api || 'openai-completions',
      apiKey: record.apiKey || '',
      reasoning: Boolean(record.reasoning),
      contextWindow: record.contextWindow ? String(record.contextWindow) : '',
      maxTokens: record.maxTokens ? String(record.maxTokens) : '',
    });
    setModelModal('edit');
  };

  const saveModel = async () => {
    setSaving(true);
    setError('');
    try {
      let payload: Record<string, unknown>;
      if (modelForm.mode === 'preset') {
        // Preset mode: derive fields from selected provider + typed model ID
        const reasoning = matchedPresetModel?.reasoning ?? true;
        payload = {
          providerId: modelForm.presetProviderId,
          modelId: modelForm.presetModelInput.trim(),
          displayName: matchedPresetModel?.name || deriveDisplayName(modelForm.presetModelInput),
          baseUrl: selectedProvider?.baseUrl ?? '',
          api: selectedProvider?.api ?? '',
          apiKey: isOAuthPreset ? '' : modelForm.apiKey.trim(),
          reasoning,
          contextWindow: matchedPresetModel?.contextWindow ?? 0,
          maxTokens: matchedPresetModel?.maxTokens ?? 0,
        };
        // For OAuth presets, pass presetId so the backend injects the stored token
        if (isOAuthPreset) {
          payload.presetId = modelForm.presetProviderId;
        }
      } else {
        payload = {
          existingProviderId: modelForm.existingProviderId,
          existingModelId: modelForm.existingModelId,
          providerId: derivedProviderId,
          modelId: modelForm.modelId.trim(),
          displayName: derivedDisplayName,
          baseUrl: modelForm.baseUrl.trim(),
          api: modelForm.api.trim(),
          apiKey: modelForm.apiKey.trim(),
          reasoning: modelForm.reasoning,
          contextWindow: modelForm.contextWindow ? Number(modelForm.contextWindow) : 0,
          maxTokens: modelForm.maxTokens ? Number(modelForm.maxTokens) : 0,
        };
      }
      const next = await apiPost<BrainState>('/api/engine/brain/models/upsert', payload);
      setState(next);
      setModelModal(null);
    } catch (err) {
      setError(err instanceof Error ? err.message : t('brain.saveModelError'));
    } finally {
      setSaving(false);
    }
  };

  const deleteModel = async () => {
    if (!deleteTarget) return;
    setSaving(true);
    setError('');
    try {
      const next = await apiPost<BrainState>('/api/engine/brain/models/delete', {
        providerId: deleteTarget.providerId,
        modelId: deleteTarget.modelId,
      });
      setState(next);
      setDeleteTarget(null);
    } catch (err) {
      setError(err instanceof Error ? err.message : t('brain.deleteModelError'));
    } finally {
      setSaving(false);
    }
  };

  const setDefault = async (record: BrainModelRecord) => {
    setSaving(true);
    setError('');
    try {
      const next = await apiPost<BrainState>('/api/engine/brain/models/default', {
        providerId: record.providerId,
        modelId: record.modelId,
      });
      setState(next);
    } catch (err) {
      setError(err instanceof Error ? err.message : t('brain.setDefaultError'));
    } finally {
      setSaving(false);
    }
  };

  const toggleFallback = async (record: BrainModelRecord) => {
    setSaving(true);
    setError('');
    try {
      const next = await apiPost<BrainState>('/api/engine/brain/models/fallback', {
        providerId: record.providerId,
        modelId: record.modelId,
        enabled: !record.isFallback,
      });
      setState(next);
    } catch (err) {
      setError(err instanceof Error ? err.message : t('brain.setFallbackError'));
    } finally {
      setSaving(false);
    }
  };

  // Cerebellum operations
  const cerebellumStart = async () => {
    setCerebellumPendingAction('starting');
    setCerebellumOperating(true);
    setError('');
    try {
      const next = await apiPost<BrainState>('/api/engine/brain/cerebellum/start', {});
      setState(next);
    } catch (err) {
      setError(err instanceof Error ? err.message : t('brain.startCerebellumError'));
    } finally {
      setCerebellumOperating(false);
      setCerebellumPendingAction(null);
    }
  };

  const cerebellumStop = async () => {
    setCerebellumPendingAction('stopping');
    setCerebellumOperating(true);
    setError('');
    try {
      const next = await apiPost<BrainState>('/api/engine/brain/cerebellum/stop', {});
      setState(next);
    } catch (err) {
      setError(err instanceof Error ? err.message : t('brain.stopCerebellumError'));
    } finally {
      setCerebellumOperating(false);
      setCerebellumPendingAction(null);
    }
  };

  const cerebellumRestart = async () => {
    setCerebellumPendingAction('restarting');
    setCerebellumOperating(true);
    setError('');
    try {
      const next = await apiPost<BrainState>('/api/engine/brain/cerebellum/restart', {});
      setState(next);
    } catch (err) {
      setError(err instanceof Error ? err.message : t('brain.restartCerebellumError'));
    } finally {
      setCerebellumOperating(false);
      setCerebellumPendingAction(null);
    }
  };

  const cerebellumSelectModel = async (modelId: string) => {
    setCerebellumOperating(true);
    setError('');
    try {
      const next = await apiPost<BrainState>('/api/engine/brain/cerebellum/model', { modelId });
      setState(next);
    } catch (err) {
      setError(err instanceof Error ? err.message : t('brain.selectModelError'));
    } finally {
      setCerebellumOperating(false);
    }
  };

  const cerebellumDownloadModel = async (presetId: string) => {
    setError('');
    try {
      const next = await apiPost<BrainState>('/api/engine/brain/cerebellum/download', { presetId });
      setState(next);
      // Polling will be triggered by the useEffect watching dlProgress.active
    } catch (err) {
      setError(err instanceof Error ? err.message : t('brain.downloadStartError'));
    }
  };

  const cerebellumCancelDownload = async () => {
    setError('');
    try {
      const next = await apiPost<BrainState>('/api/engine/brain/cerebellum/download/cancel', {});
      setState(next);
    } catch (err) {
      setError(err instanceof Error ? err.message : t('brain.downloadCancelError'));
    }
  };

  const cerebellumClearModel = async () => {
    setCerebellumOperating(true);
    setError('');
    try {
      const next = await apiPost<BrainState>('/api/engine/brain/cerebellum/model/clear', {});
      setState(next);
    } catch (err) {
      setError(err instanceof Error ? err.message : t('brain.unsetDefaultError'));
    } finally {
      setCerebellumOperating(false);
    }
  };

  const cerebellumDeleteModel = async (modelId: string) => {
    setCerebellumOperating(true);
    setError('');
    try {
      const next = await apiPost<BrainState>('/api/engine/brain/cerebellum/model/delete', { modelId });
      setState(next);
      setLocalDeleteTarget(null);
    } catch (err) {
      setError(err instanceof Error ? err.message : t('brain.deleteModelError'));
    } finally {
      setCerebellumOperating(false);
    }
  };

  // Brain mode switch
  const brainModeSwitch = async (mode: BrainMode) => {
    setModeSwitching(true);
    setError('');
    try {
      const next = await apiPost<BrainState>('/api/engine/brain/mode', { mode });
      setState(next);
    } catch (err) {
      setError(err instanceof Error ? err.message : t('brain.switchModeError'));
    } finally {
      setModeSwitching(false);
    }
  };

  const brainLocalStart = async () => {
    setBrainLocalPendingAction('starting');
    setBrainLocalOperating(true);
    setError('');
    try {
      const next = await apiPost<BrainState>('/api/engine/brain/local/start', {});
      setState(next);
    } catch (err) {
      setError(err instanceof Error ? err.message : t('brain.startLocalError'));
    } finally {
      setBrainLocalOperating(false);
      setBrainLocalPendingAction(null);
    }
  };

  const brainLocalStop = async () => {
    setBrainLocalPendingAction('stopping');
    setBrainLocalOperating(true);
    setError('');
    try {
      const next = await apiPost<BrainState>('/api/engine/brain/local/stop', {});
      setState(next);
    } catch (err) {
      setError(err instanceof Error ? err.message : t('brain.stopLocalError'));
    } finally {
      setBrainLocalOperating(false);
      setBrainLocalPendingAction(null);
    }
  };

  const brainLocalRestart = async () => {
    setBrainLocalPendingAction('restarting');
    setBrainLocalOperating(true);
    setError('');
    try {
      const next = await apiPost<BrainState>('/api/engine/brain/local/restart', {});
      setState(next);
    } catch (err) {
      setError(err instanceof Error ? err.message : t('brain.restartLocalError'));
    } finally {
      setBrainLocalOperating(false);
      setBrainLocalPendingAction(null);
    }
  };

  const brainLocalSelectModel = async (modelId: string) => {
    setBrainLocalOperating(true);
    setError('');
    try {
      const next = await apiPost<BrainState>('/api/engine/brain/local/model', { modelId });
      setState(next);
    } catch (err) {
      setError(err instanceof Error ? err.message : t('brain.selectModelError'));
    } finally {
      setBrainLocalOperating(false);
    }
  };

  const brainLocalDownloadModel = async (presetId: string) => {
    setError('');
    try {
      const next = await apiPost<BrainState>('/api/engine/brain/local/download', { presetId });
      setState(next);
    } catch (err) {
      setError(err instanceof Error ? err.message : t('brain.downloadStartError'));
    }
  };

  const brainLocalCancelDownload = async () => {
    setError('');
    try {
      const next = await apiPost<BrainState>('/api/engine/brain/local/download/cancel', {});
      setState(next);
    } catch (err) {
      setError(err instanceof Error ? err.message : t('brain.downloadCancelError'));
    }
  };

  const brainLocalClearModel = async () => {
    setBrainLocalOperating(true);
    setError('');
    try {
      const next = await apiPost<BrainState>('/api/engine/brain/local/model/clear', {});
      setState(next);
    } catch (err) {
      setError(err instanceof Error ? err.message : t('brain.unsetDefaultError'));
    } finally {
      setBrainLocalOperating(false);
    }
  };

  const brainLocalDeleteModel = async (modelId: string) => {
    setBrainLocalOperating(true);
    setError('');
    try {
      const next = await apiPost<BrainState>('/api/engine/brain/local/model/delete', { modelId });
      setState(next);
      setLocalDeleteTarget(null);
    } catch (err) {
      setError(err instanceof Error ? err.message : t('brain.deleteModelError'));
    } finally {
      setBrainLocalOperating(false);
    }
  };

  const openProxyModal = async (presetId: string) => {
    setProxyRetryPresetId(presetId);
    setProxyModalError('');
    setProxyModalSaving(false);
    try {
      const net = await apiGet<NetworkState>('/api/system/network');
      setProxyModalUrl(net.proxyUrl || '');
    } catch {
      setProxyModalUrl('');
    }
    setProxyModalOpen(true);
  };

  const handleProxySaveAndRetry = async () => {
    setProxyModalSaving(true);
    setProxyModalError('');
    try {
      await apiPost<NetworkState>('/api/system/network/save', { proxyUrl: proxyModalUrl });
      setProxyModalOpen(false);
      // Retry the download with the (now-active) proxy
      await cerebellumDownloadModel(proxyRetryPresetId);
    } catch (err) {
      setProxyModalError(err instanceof Error ? err.message : t('brain.saveProxyError'));
    } finally {
      setProxyModalSaving(false);
    }
  };

  // Open params modal for either brain local or cerebellum
  // Uses catalog defaults for the currently selected model when user hasn't customized params.
  const openParamsModal = (target: 'brainLocal' | 'cerebellum') => {
    const src = target === 'brainLocal' ? brainLocal : cerebellum;
    const sp = src?.sampling;
    // Find catalog defaults for the currently selected model
    const catalogList = src?.catalog || [];
    const selectedModelId = src?.modelId || '';
    const matchedPreset = catalogList.find((p) => {
      const presetModelId = p.filename?.replace(/\.gguf$/i, '') || p.id;
      return presetModelId === selectedModelId || p.id === selectedModelId;
    });
    const dfl = matchedPreset?.defaults;
    const dflSp = dfl?.sampling;
    setParamsForm({
      temp: sp?.temp ?? dflSp?.temp ?? 0.6,
      topP: sp?.topP ?? dflSp?.topP ?? 0.95,
      topK: sp?.topK ?? dflSp?.topK ?? 20,
      minP: sp?.minP ?? dflSp?.minP ?? 0,
      typicalP: sp?.typicalP ?? dflSp?.typicalP ?? 0,
      repeatLastN: sp?.repeatLastN ?? dflSp?.repeatLastN ?? 64,
      penaltyRepeat: sp?.penaltyRepeat ?? dflSp?.penaltyRepeat ?? 1.0,
      penaltyFreq: sp?.penaltyFreq ?? dflSp?.penaltyFreq ?? 0,
      penaltyPresent: sp?.penaltyPresent ?? dflSp?.penaltyPresent ?? 0,
      threads: src?.threads ?? dfl?.threads ?? 4,
      contextSize: src?.contextSize ?? dfl?.contextSize ?? 4096,
      gpuLayers: src?.gpuLayers ?? dfl?.gpuLayers ?? 0,
      enableThinking: src?.enableThinking ?? dfl?.enableThinking ?? true,
    });
    setParamsError('');
    setParamsSaving(false);
    setParamsModalTarget(target);
  };

  const saveParams = async () => {
    if (!paramsModalTarget) return;
    setParamsSaving(true);
    setParamsError('');
    const endpoint = paramsModalTarget === 'brainLocal'
      ? '/api/engine/brain/local/params'
      : '/api/engine/brain/cerebellum/params';
    try {
      const next = await apiPost<BrainState>(endpoint, {
        sampling: {
          temp: paramsForm.temp,
          topP: paramsForm.topP,
          topK: paramsForm.topK,
          minP: paramsForm.minP,
          typicalP: paramsForm.typicalP,
          repeatLastN: paramsForm.repeatLastN,
          penaltyRepeat: paramsForm.penaltyRepeat,
          penaltyFreq: paramsForm.penaltyFreq,
          penaltyPresent: paramsForm.penaltyPresent,
        },
        threads: paramsForm.threads,
        contextSize: paramsForm.contextSize,
        gpuLayers: paramsForm.gpuLayers,
        enableThinking: paramsForm.enableThinking,
      });
      setState(next);
      setParamsModalTarget(null);
    } catch (err) {
      setParamsError(err instanceof Error ? err.message : t('brain.saveParamsError'));
    } finally {
      setParamsSaving(false);
    }
  };

  const catalog = cerebellum?.catalog || [];
  const localModelIds = new Set((cerebellum?.models || []).map((m) => m.id));

  const brainLocalCatalog = brainLocal?.catalog || [];
  const brainLocalModelIds = new Set((brainLocal?.models || []).map((m) => m.id));
  const cerebellumDefaultModel = (cerebellum?.models || []).find((model) => model.id === cerebellum?.modelId) || null;
  const brainLocalDefaultModel = (brainLocal?.models || []).find((model) => model.id === brainLocal?.modelId) || null;
  const cerebellumDefaultModelLabel = cerebellumDefaultModel
    ? `${cerebellumDefaultModel.name}${cerebellumDefaultModel.size ? ` (${cerebellumDefaultModel.size})` : ''}`
    : t('brain.noDefaultModel');
  const brainLocalDefaultModelLabel = brainLocalDefaultModel
    ? `${brainLocalDefaultModel.name}${brainLocalDefaultModel.size ? ` (${brainLocalDefaultModel.size})` : ''}`
    : t('brain.noDefaultModel');
  const cerebellumVisualStatus: CerebellumState['status'] =
    cerebellumPendingAction === 'restarting'
      ? 'starting'
      : cerebellumPendingAction ?? cerebellum?.status ?? 'stopped';
  const brainLocalVisualStatus: LocalModelState['status'] =
    brainLocalPendingAction === 'restarting'
      ? 'starting'
      : brainLocalPendingAction ?? brainLocal?.status ?? 'stopped';

  const cerebellumStatusConfig = {
    running: { color: 'text-emerald-600 dark:text-emerald-400', bg: 'bg-emerald-500', label: t('brain.running') },
    stopped: { color: 'text-zinc-500 dark:text-zinc-400', bg: 'bg-zinc-400', label: t('brain.stopped') },
    starting: { color: 'text-amber-600 dark:text-amber-400', bg: 'bg-amber-500', label: t('brain.starting') },
    stopping: { color: 'text-amber-600 dark:text-amber-400', bg: 'bg-amber-500', label: t('brain.stopping') },
    error: { color: 'text-rose-600 dark:text-rose-400', bg: 'bg-rose-500', label: t('brain.error') },
  };

  // Validation
  const isPresetValid = isOAuthPreset
    ? Boolean(modelForm.presetProviderId && modelForm.presetModelInput.trim() && isOAuthAuthed)
    : Boolean(modelForm.presetProviderId && modelForm.presetModelInput.trim() && modelForm.apiKey.trim());
  const isCustomValid = Boolean(modelForm.modelId.trim() && modelForm.baseUrl.trim() && modelForm.api.trim() && derivedProviderId);
  const canSave = modelForm.mode === 'preset' ? isPresetValid : isCustomValid;

  // Derive current mode from state
  const currentMode: BrainMode = (state?.brainMode === 'local') ? 'local' : 'cloud';
  const showCerebellumBusyAnimation = cerebellumVisualStatus === 'starting' || cerebellumVisualStatus === 'stopping';
  const cerebellumIsRunning = cerebellum?.status === 'running' && !cerebellumPendingAction;
  const showBrainLocalBusyAnimation = brainLocalVisualStatus === 'starting' || brainLocalVisualStatus === 'stopping';
  const brainLocalIsRunning = brainLocal?.status === 'running' && !brainLocalPendingAction;

  // ─── Page header ─────────────────────────────────────────────
  const pageTitle = currentMode === 'local' ? t('brain.modeLocal') : t('brain.modeCloud');
  const pageDesc = currentMode === 'local' ? t('brain.modeLocalDesc') : t('brain.modeCloudDesc');

  return (
    <div className="flex h-full flex-col bg-white text-zinc-900 transition-colors dark:bg-zinc-950 dark:text-zinc-100">
      <PageHeader title={pageTitle} description={pageDesc}>
        <button
          onClick={() => void refresh()}
          className="rounded-lg border border-zinc-200 px-3 py-2 text-sm font-medium text-zinc-700 transition-colors hover:bg-zinc-100 dark:border-zinc-800 dark:text-zinc-200 dark:hover:bg-zinc-800"
        >
          <RefreshCw className="h-4 w-4" />
        </button>
        {currentMode === 'cloud' && (
          <button
            onClick={openCreateModel}
            className="flex items-center gap-2 whitespace-nowrap rounded-lg bg-indigo-600 px-3 py-2 text-sm font-medium text-white transition-colors hover:bg-indigo-500"
          >
            <Plus className="h-4 w-4" />
            {t('brain.addModel')}
          </button>
        )}
      </PageHeader>

      {/* ─── Mode Switcher ─────────────────────────────────────────── */}
      <div className="border-b border-zinc-200 px-6 dark:border-zinc-800">
        <div className="-mb-px flex gap-1 py-2">
          <button
            onClick={() => void brainModeSwitch('cloud')}
            disabled={modeSwitching}
            className={`flex items-center gap-2 rounded-lg px-4 py-2.5 text-sm font-medium transition-colors ${currentMode === 'cloud'
              ? 'bg-indigo-50 text-indigo-700 ring-1 ring-indigo-200 dark:bg-indigo-950/40 dark:text-indigo-300 dark:ring-indigo-800'
              : 'text-zinc-500 hover:bg-zinc-100 hover:text-zinc-700 dark:text-zinc-400 dark:hover:bg-zinc-800 dark:hover:text-zinc-200'
              }`}
          >
            <Cloud className="h-4 w-4" />
            {t('brain.modeCloudShort')}
          </button>
          <button
            onClick={() => void brainModeSwitch('local')}
            disabled={modeSwitching}
            className={`flex items-center gap-2 rounded-lg px-4 py-2.5 text-sm font-medium transition-colors ${currentMode === 'local'
              ? 'bg-indigo-50 text-indigo-700 ring-1 ring-indigo-200 dark:bg-indigo-950/40 dark:text-indigo-300 dark:ring-indigo-800'
              : 'text-zinc-500 hover:bg-zinc-100 hover:text-zinc-700 dark:text-zinc-400 dark:hover:bg-zinc-800 dark:hover:text-zinc-200'
              }`}
          >
            <HardDrive className="h-4 w-4" />
            {t('brain.modeLocalShort')}
          </button>
          {modeSwitching && (
            <span className="flex items-center gap-1.5 text-xs text-amber-600 dark:text-amber-400">
              <Loader2 className="h-3 w-3 animate-spin" />
              {t('brain.modeSwitching')}
            </span>
          )}
        </div>
      </div>

      <div className="flex-1 space-y-8 overflow-y-auto p-6">
        <ErrorAlert message={error} />

        {/* ══════════════════════════════════════════════════════════════════
            Cloud Models (shown in cloud mode)
           ══════════════════════════════════════════════════════════════════ */}
        {currentMode === 'cloud' && (
          <section className="space-y-6">
            <div className="flex items-center justify-between">
              <div>
                <h3 className="mb-1 text-lg font-medium text-zinc-900 dark:text-zinc-100">{t('brain.cloudModels')}</h3>
                <p className="text-sm text-zinc-500 dark:text-zinc-400">{t('brain.cloudDesc')}</p>
              </div>
              <span className="rounded-full bg-zinc-100 px-3 py-1 text-xs font-medium text-zinc-600 dark:bg-zinc-800 dark:text-zinc-300">
                {t('brain.defaultAgent')}: {state?.defaultAgent || 'main'}
              </span>
            </div>

            {loading ? (
              <div className="flex items-center justify-center rounded-xl border border-zinc-200 bg-white p-8 text-zinc-500 dark:border-zinc-800 dark:bg-zinc-900 dark:text-zinc-400">
                <Loader2 className="mr-2 h-5 w-5 animate-spin" />
                {t('brain.loadingModels')}
              </div>
            ) : (
              <div className="space-y-4">
                {records.length === 0 ? (
                  <div className="rounded-xl border border-zinc-200 bg-white p-6 text-sm text-zinc-500 shadow-sm dark:border-zinc-800 dark:bg-zinc-900 dark:text-zinc-400">
                    {t('brain.noModelsConfigured')}
                  </div>
                ) : null}
                {records.map((record) => (
                  <div key={record.key} className="rounded-xl border border-zinc-200 bg-white p-4 shadow-sm dark:border-zinc-800 dark:bg-zinc-900">
                    <div className="flex items-center gap-3 text-sm">
                      <div className="rounded-lg bg-zinc-100 p-2 dark:bg-zinc-800">
                        <Database className="h-4 w-4 text-indigo-600 dark:text-indigo-400" />
                      </div>
                      <div className="min-w-0 flex-1 truncate text-zinc-600 dark:text-zinc-300">
                        <span className="font-medium text-zinc-900 dark:text-zinc-100">{recordLabel(record)}</span>
                        <span className="mx-2 text-zinc-300 dark:text-zinc-600">•</span>
                        <span>{record.providerId}</span>
                        <span className="mx-2 text-zinc-300 dark:text-zinc-600">•</span>
                        <span className="font-mono text-xs">{record.modelId}</span>
                      </div>
                    </div>

                    <div className="mt-3 flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
                      <div className="flex min-w-0 flex-wrap items-center gap-2 text-xs">
                        {record.ready ? (
                          <span className="inline-flex items-center gap-1 rounded-full bg-emerald-500/10 px-2 py-1 font-medium text-emerald-600 dark:text-emerald-400">
                            <CheckCircle2 className="h-3.5 w-3.5" /> {t('brain.networkOk')}
                          </span>
                        ) : (
                          <span className="inline-flex items-center gap-1 rounded-full bg-rose-500/10 px-2 py-1 font-medium text-rose-600 dark:text-rose-400">
                            <AlertCircle className="h-3.5 w-3.5" /> {record.lastError || t('brain.invalidKey')}
                          </span>
                        )}
                        {record.isDefault ? (
                          <span className="inline-flex items-center gap-1 rounded-full bg-amber-500/10 px-2 py-1 font-medium text-amber-600 dark:text-amber-400">
                            <Star className="h-3 w-3" /> {t('brain.default')}
                          </span>
                        ) : null}
                        {record.isFallback ? (
                          <span className="inline-flex items-center gap-1 rounded-full bg-sky-500/10 px-2 py-1 font-medium text-sky-600 dark:text-sky-400">
                            <ShieldAlert className="h-3 w-3" /> {t('brain.fallback')}
                          </span>
                        ) : null}
                        {record.reasoning ? <span className="text-zinc-500 dark:text-zinc-400">{t('brain.reasoning')}</span> : null}
                      </div>

                      <div className="flex flex-nowrap items-center gap-2 overflow-x-auto whitespace-nowrap pb-1 sm:justify-end">
                        <button
                          onClick={() => void setDefault(record)}
                          disabled={saving || Boolean(record.isDefault)}
                          className="shrink-0 rounded-lg border border-zinc-200 px-2.5 py-1.5 text-xs font-medium text-zinc-700 transition-colors hover:bg-zinc-50 disabled:cursor-not-allowed disabled:opacity-50 dark:border-zinc-800 dark:text-zinc-200 dark:hover:bg-zinc-800"
                        >
                          {t('brain.setDefault')}
                        </button>
                        <button
                          onClick={() => void toggleFallback(record)}
                          disabled={saving || Boolean(record.isDefault)}
                          className="shrink-0 rounded-lg border border-zinc-200 px-2.5 py-1.5 text-xs font-medium text-zinc-700 transition-colors hover:bg-zinc-50 disabled:cursor-not-allowed disabled:opacity-50 dark:border-zinc-800 dark:text-zinc-200 dark:hover:bg-zinc-800"
                        >
                          {record.isFallback ? t('brain.unsetFallback') : t('brain.setFallback')}
                        </button>
                        <button
                          onClick={() => openEditModel(record)}
                          className="inline-flex shrink-0 items-center gap-1 rounded-lg border border-zinc-200 px-2.5 py-1.5 text-xs font-medium text-zinc-700 transition-colors hover:bg-zinc-50 dark:border-zinc-800 dark:text-zinc-200 dark:hover:bg-zinc-800"
                        >
                          <Pencil className="h-3.5 w-3.5" />
                          {t('brain.editModel')}
                        </button>
                        <button
                          onClick={() => setDeleteTarget(record)}
                          className="shrink-0 rounded-lg border border-zinc-200 px-2.5 py-1.5 text-xs font-medium text-zinc-700 transition-colors hover:bg-zinc-50 dark:border-zinc-800 dark:text-zinc-200 dark:hover:bg-zinc-800"
                        >
                          <Trash2 className="h-3.5 w-3.5" />
                        </button>
                      </div>
                    </div>
                  </div>
                ))}
              </div>
            )}
          </section>
        )}

        {/* ══════════════════════════════════════════════════════════════════
            Brain Local (shown in local mode)
           ══════════════════════════════════════════════════════════════════ */}
        {currentMode === 'local' && (
          <section className="space-y-6">
            <div className="flex items-center justify-between">
              <div>
                <h3 className="mb-1 text-lg font-medium text-zinc-900 dark:text-zinc-100">{t('brain.brainLocalTitle')}</h3>
                <p className="text-sm text-zinc-500 dark:text-zinc-400">{t('brain.brainLocalDesc')}</p>
              </div>
            </div>

            {loading ? (
              <div className="flex items-center justify-center rounded-xl border border-zinc-200 bg-white p-8 text-zinc-500 dark:border-zinc-800 dark:bg-zinc-900 dark:text-zinc-400">
                <Loader2 className="mr-2 h-5 w-5 animate-spin" />
                {t('brain.loadingModels')}
              </div>
            ) : (
              <div className="rounded-xl border border-zinc-200 bg-white p-5 shadow-sm dark:border-zinc-800 dark:bg-zinc-900">
                <div className="flex flex-col gap-4 xl:flex-row xl:items-center xl:justify-between">
                  <div className="flex min-w-0 items-center gap-4">
                    <div className="rounded-lg bg-indigo-100 p-3 dark:bg-indigo-900/30">
                      <HardDrive className="h-6 w-6 text-indigo-600 dark:text-indigo-400" />
                    </div>
                    <div className="min-w-0">
                      <div className="flex items-center gap-2">
                        <h4 className="font-medium text-zinc-900 dark:text-zinc-100">{t('brain.brainLocalTitle')}</h4>
                        {brainLocalVisualStatus && (
                          <span className={`flex items-center gap-1.5 text-xs font-medium ${cerebellumStatusConfig[brainLocalVisualStatus].color}`}>
                            {showBrainLocalBusyAnimation ? (
                              <Loader2 className="h-3.5 w-3.5 animate-spin" />
                            ) : (
                              <span className={`h-1.5 w-1.5 rounded-full ${cerebellumStatusConfig[brainLocalVisualStatus].bg}`} />
                            )}
                            {cerebellumStatusConfig[brainLocalVisualStatus].label}
                          </span>
                        )}
                      </div>
                      {showBrainLocalBusyAnimation && (
                        <p className="mt-1 text-xs text-amber-600 dark:text-amber-400">
                          {brainLocalPendingAction === 'restarting' ? `${t('brain.restart')}...` : `${cerebellumStatusConfig[brainLocalVisualStatus].label}...`}
                        </p>
                      )}
                      {brainLocal?.lastError && (
                        <p className="mt-1 text-xs text-rose-600 dark:text-rose-400">{brainLocal.lastError}</p>
                      )}
                    </div>
                  </div>

                  <div className="flex w-full flex-col gap-3 sm:flex-row sm:items-start xl:w-auto xl:flex-shrink-0 xl:items-center">
                    <div className="min-w-0 sm:flex-1 xl:w-72 xl:min-w-[18rem] xl:flex-none">
                      <p className="text-[11px] font-medium uppercase tracking-wide text-zinc-500 dark:text-zinc-400">
                        {t('brain.defaultModelLabel')}
                      </p>
                      <p className="mt-1 truncate text-sm text-zinc-900 dark:text-zinc-100">{brainLocalDefaultModelLabel}</p>
                      {brainLocalDefaultModel ? (
                        <p className="mt-0.5 truncate text-xs text-zinc-500 dark:text-zinc-400">{brainLocalDefaultModel.id}</p>
                      ) : null}
                    </div>

                    <div className="flex flex-wrap items-center gap-1 self-start sm:justify-end xl:flex-nowrap">
                      {brainLocalIsRunning ? (
                        <button
                          onClick={() => void brainLocalStop()}
                          disabled={brainLocalOperating}
                          className="flex min-w-[80px] items-center justify-center gap-1.5 whitespace-nowrap rounded-lg border border-zinc-200 px-3 py-2 text-sm font-medium text-zinc-700 transition-colors hover:bg-zinc-50 disabled:opacity-50 dark:border-zinc-700 dark:text-zinc-200 dark:hover:bg-zinc-800"
                        >
                          {brainLocalPendingAction === 'stopping' ? (
                            <Loader2 className="h-3.5 w-3.5 flex-shrink-0 animate-spin" />
                          ) : (
                            <Square className="h-3.5 w-3.5 flex-shrink-0" />
                          )}
                          {brainLocalPendingAction === 'stopping' ? `${t('brain.stopping')}...` : t('brain.stop')}
                        </button>
                      ) : (
                        <button
                          onClick={() => void brainLocalStart()}
                          disabled={brainLocalOperating || !brainLocal?.modelId}
                          className="flex min-w-[80px] items-center justify-center gap-1.5 whitespace-nowrap rounded-lg bg-indigo-600 px-3 py-2 text-sm font-medium text-white transition-colors hover:bg-indigo-500 disabled:opacity-50"
                        >
                          {brainLocalPendingAction === 'starting' || brainLocalPendingAction === 'restarting' ? (
                            <Loader2 className="h-3.5 w-3.5 flex-shrink-0 animate-spin" />
                          ) : (
                            <Play className="h-3.5 w-3.5 flex-shrink-0" />
                          )}
                          {brainLocalPendingAction === 'starting'
                            ? `${t('brain.starting')}...`
                            : brainLocalPendingAction === 'restarting'
                              ? `${t('brain.restart')}...`
                              : t('brain.start')}
                        </button>
                      )}
                      <button
                        onClick={() => void brainLocalRestart()}
                        disabled={brainLocalOperating || brainLocal?.status !== 'running' || !brainLocal?.modelId}
                        className="flex min-w-[80px] items-center justify-center gap-1.5 whitespace-nowrap rounded-lg border border-zinc-200 px-3 py-2 text-sm font-medium text-zinc-700 transition-colors hover:bg-zinc-50 disabled:opacity-50 dark:border-zinc-700 dark:text-zinc-200 dark:hover:bg-zinc-800"
                      >
                        {brainLocalPendingAction === 'restarting' ? (
                          <Loader2 className="h-3.5 w-3.5 flex-shrink-0 animate-spin" />
                        ) : (
                          <RotateCcw className="h-3.5 w-3.5 flex-shrink-0" />
                        )}
                        {brainLocalPendingAction === 'restarting' ? `${t('brain.restart')}...` : t('brain.restart')}
                      </button>
                      <button
                        onClick={() => openParamsModal('brainLocal')}
                        disabled={brainLocalOperating}
                        className="flex items-center justify-center gap-1.5 whitespace-nowrap rounded-lg border border-zinc-200 px-3 py-2 text-sm font-medium text-zinc-700 transition-colors hover:bg-zinc-50 disabled:opacity-50 dark:border-zinc-700 dark:text-zinc-200 dark:hover:bg-zinc-800"
                        title={t('brain.params') || 'Parameters'}
                      >
                        <Settings2 className="h-3.5 w-3.5 flex-shrink-0" />
                      </button>
                    </div>
                  </div>
                </div>
              </div>
            )}

            {!loading ? (
              <div className="space-y-3">
                <h4 className="text-sm font-medium text-zinc-700 dark:text-zinc-300">{t('brain.downloadedModelsTitle')}</h4>
                {(brainLocal?.models || []).length > 0 ? (
                  <div className="space-y-2">
                    {(brainLocal?.models || []).map((model) => {
                      const isDefault = brainLocal?.modelId === model.id;
                      return (
                        <div
                          key={model.id}
                          className="flex flex-col gap-3 rounded-lg border border-zinc-200 bg-white p-3 dark:border-zinc-800 dark:bg-zinc-900 lg:flex-row lg:items-center lg:justify-between"
                        >
                          <div className="min-w-0">
                            <div className="flex items-center gap-2">
                              <span className="truncate text-sm font-medium text-zinc-900 dark:text-zinc-100">{model.name}</span>
                              {isDefault ? (
                                <span className="inline-flex items-center rounded-full bg-amber-500/10 px-2 py-0.5 text-[11px] font-medium text-amber-600 dark:text-amber-400">
                                  {t('brain.default')}
                                </span>
                              ) : null}
                            </div>
                            <div className="mt-1 flex items-center gap-2 text-xs text-zinc-500 dark:text-zinc-400">
                              <span className="font-mono">{model.id}</span>
                              {model.size ? <span>· {model.size}</span> : null}
                            </div>
                          </div>
                          <div className="flex flex-wrap items-center gap-2 lg:justify-end">
                            {isDefault ? (
                              <button
                                onClick={() => void brainLocalClearModel()}
                                disabled={brainLocalOperating}
                                className="rounded-md border border-zinc-300 px-2.5 py-1 text-xs font-medium text-zinc-700 transition-colors hover:bg-zinc-50 disabled:opacity-50 dark:border-zinc-700 dark:text-zinc-200 dark:hover:bg-zinc-800"
                              >
                                {t('brain.unsetDefault')}
                              </button>
                            ) : (
                              <button
                                onClick={() => void brainLocalSelectModel(model.id)}
                                disabled={brainLocalOperating}
                                className="rounded-md bg-indigo-600 px-2.5 py-1 text-xs font-medium text-white transition-colors hover:bg-indigo-500 disabled:opacity-50"
                              >
                                {t('brain.setDefault')}
                              </button>
                            )}
                            <button
                              onClick={() => setLocalDeleteTarget({ scope: 'brainLocal', modelId: model.id, modelName: model.name })}
                              disabled={brainLocalOperating}
                              className="flex items-center gap-1 rounded-md border border-rose-300 px-2.5 py-1 text-xs font-medium text-rose-600 transition-colors hover:bg-rose-50 disabled:opacity-50 dark:border-rose-900/50 dark:text-rose-400 dark:hover:bg-rose-950/30"
                            >
                              <Trash2 className="h-3 w-3" />
                              {t('common.delete')}
                            </button>
                          </div>
                        </div>
                      );
                    })}
                  </div>
                ) : (
                  <div className="rounded-lg border border-zinc-200 bg-zinc-50 p-4 text-sm text-zinc-500 dark:border-zinc-800 dark:bg-zinc-950/50 dark:text-zinc-400">
                    {t('brain.noDownloadedModels')}
                  </div>
                )}
                {brainLocal?.modelsDir ? (
                  <p className="text-xs text-zinc-500 dark:text-zinc-400">{t('brain.modelDirectory', { path: brainLocal.modelsDir })}</p>
                ) : null}
              </div>
            ) : null}

            {/* Brain Local Model Catalog */}
            {!loading && brainLocalCatalog.length > 0 ? (
              <div className="space-y-3">
                <h4 className="text-sm font-medium text-zinc-700 dark:text-zinc-300">{t('brain.catalogTitle')}</h4>
                <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
                  {brainLocalCatalog.map((preset) => {
                    const alreadyDownloaded = brainLocalModelIds.has(preset.filename?.replace(/\.gguf$/i, '') || preset.id);
                    const isThisDownloading = brainLocalDlProgress?.active && brainLocalDlProgress.presetId === preset.id;
                    const presetDescription = resolveLocalizedText(preset.description, language);
                    const pct =
                      isThisDownloading && brainLocalDlProgress.totalBytes > 0
                        ? Math.min(100, Math.round((brainLocalDlProgress.downloadedBytes / brainLocalDlProgress.totalBytes) * 100))
                        : 0;

                    return (
                      <div
                        key={preset.id}
                        className="flex flex-col gap-2 rounded-lg border border-zinc-200 bg-zinc-50 p-3 dark:border-zinc-800 dark:bg-zinc-950/50"
                      >
                        <div className="flex items-center gap-2">
                          <HardDrive className="h-4 w-4 shrink-0 text-indigo-500" />
                          <span className="text-sm font-medium text-zinc-900 dark:text-zinc-100">{preset.name}</span>
                        </div>
                        {presetDescription ? (
                          <p className="text-xs text-zinc-500 dark:text-zinc-400">{presetDescription}</p>
                        ) : null}

                        {isThisDownloading ? (
                          <div className="space-y-1.5">
                            <div className="flex items-center justify-between text-xs text-zinc-500 dark:text-zinc-400">
                              <span className="flex items-center gap-1">
                                <Loader2 className="h-3 w-3 animate-spin" />
                                {t('brain.catalogDownloading')}
                              </span>
                              <span>
                                {brainLocalDlProgress.totalBytes > 0
                                  ? `${formatBytes(brainLocalDlProgress.downloadedBytes)} / ${formatBytes(brainLocalDlProgress.totalBytes)} (${pct}%)`
                                  : formatBytes(brainLocalDlProgress.downloadedBytes)}
                              </span>
                            </div>
                            <div className="h-2 w-full overflow-hidden rounded-full bg-zinc-200 dark:bg-zinc-800">
                              <div
                                className="h-full rounded-full bg-indigo-500 transition-all duration-300"
                                style={{ width: brainLocalDlProgress.totalBytes > 0 ? `${pct}%` : '100%' }}
                              />
                            </div>
                            <div className="flex justify-end">
                              <button
                                onClick={() => void brainLocalCancelDownload()}
                                className="flex items-center gap-1 rounded-md border border-zinc-300 bg-white px-2.5 py-1 text-xs font-medium text-zinc-700 transition-colors hover:bg-zinc-50 dark:border-zinc-600 dark:bg-zinc-800 dark:text-zinc-300 dark:hover:bg-zinc-700"
                              >
                                <Square className="h-3 w-3" />
                                {t('common.cancel')}
                              </button>
                            </div>
                          </div>
                        ) : (
                          <div className="flex items-center justify-between">
                            <span className="text-xs text-zinc-400">{preset.size || '—'}</span>
                            {alreadyDownloaded ? (
                              <span className="flex items-center gap-1 text-xs font-medium text-emerald-600 dark:text-emerald-400">
                                <CheckCircle2 className="h-3.5 w-3.5" />
                                {t('brain.catalogDownloaded')}
                              </span>
                            ) : brainLocalDlProgress?.active ? (
                              <button
                                disabled
                                className="flex items-center gap-1 rounded-md bg-indigo-600 px-2.5 py-1 text-xs font-medium text-white opacity-50"
                              >
                                <Download className="h-3 w-3" />
                                {t('brain.catalogDownload')}
                              </button>
                            ) : brainLocalDlProgress && !brainLocalDlProgress.active && !brainLocalDlProgress.canceled && brainLocalDlProgress.error && brainLocalDlProgress.presetId === preset.id ? (
                              <div className="flex flex-col items-end gap-1.5">
                                <span className="text-xs text-rose-500">{t('brain.downloadFailed')}</span>
                                <div className="flex items-center gap-2">
                                  <button
                                    onClick={() => void openProxyModal(preset.id)}
                                    className="flex items-center gap-1 rounded-md border border-zinc-300 bg-white px-2.5 py-1 text-xs font-medium text-zinc-700 transition-colors hover:bg-zinc-50 dark:border-zinc-600 dark:bg-zinc-800 dark:text-zinc-300 dark:hover:bg-zinc-700"
                                  >
                                    <Globe className="h-3 w-3" />
                                    {t('brain.configureProxy')}
                                  </button>
                                  <button
                                    onClick={() => void brainLocalDownloadModel(preset.id)}
                                    className="flex items-center gap-1 rounded-md bg-indigo-600 px-2.5 py-1 text-xs font-medium text-white transition-colors hover:bg-indigo-500"
                                  >
                                    <Download className="h-3 w-3" />
                                    {t('brain.downloadRetry')}
                                  </button>
                                </div>
                              </div>
                            ) : (
                              <button
                                onClick={() => void brainLocalDownloadModel(preset.id)}
                                className="flex items-center gap-1 rounded-md bg-indigo-600 px-2.5 py-1 text-xs font-medium text-white transition-colors hover:bg-indigo-500"
                              >
                                <Download className="h-3 w-3" />
                                {t('brain.catalogDownload')}
                              </button>
                            )}
                          </div>
                        )}
                      </div>
                    );
                  })}
                </div>
              </div>
            ) : null}
          </section>
        )}

        {/* ══════════════════════════════════════════════════════════════════
            Cerebellum (only visible in cloud mode — not needed for pure local)
           ══════════════════════════════════════════════════════════════════ */}
        {currentMode === 'cloud' && (
          <section className="space-y-6">
            {/* Status & Controls */}
            <div>
              <h3 className="mb-1 text-lg font-medium text-zinc-900 dark:text-zinc-100">{t('brain.cerebellumSection')}</h3>
              <p className="text-sm text-zinc-500 dark:text-zinc-400">{t('brain.cerebellumSectionDesc')}</p>
            </div>

            {loading ? (
              <div className="flex items-center justify-center rounded-xl border border-zinc-200 bg-white p-8 text-zinc-500 dark:border-zinc-800 dark:bg-zinc-900 dark:text-zinc-400">
                <Loader2 className="mr-2 h-5 w-5 animate-spin" />
                {t('brain.loadingModels')}
              </div>
            ) : (
              <div className="rounded-xl border border-zinc-200 bg-white p-5 shadow-sm dark:border-zinc-800 dark:bg-zinc-900">
                <div className="flex flex-col gap-4 xl:flex-row xl:items-center xl:justify-between">
                  <div className="flex min-w-0 items-center gap-4">
                    <div className="rounded-lg bg-indigo-100 p-3 dark:bg-indigo-900/30">
                      <Cpu className="h-6 w-6 text-indigo-600 dark:text-indigo-400" />
                    </div>
                    <div className="min-w-0">
                      <div className="flex items-center gap-2">
                        <h4 className="font-medium text-zinc-900 dark:text-zinc-100">{t('brain.localCerebellum')}</h4>
                        {cerebellumVisualStatus && (
                          <span className={`flex items-center gap-1.5 text-xs font-medium ${cerebellumStatusConfig[cerebellumVisualStatus].color}`}>
                            {showCerebellumBusyAnimation ? (
                              <Loader2 className="h-3.5 w-3.5 animate-spin" />
                            ) : (
                              <span className={`h-1.5 w-1.5 rounded-full ${cerebellumStatusConfig[cerebellumVisualStatus].bg}`} />
                            )}
                            {cerebellumStatusConfig[cerebellumVisualStatus].label}
                          </span>
                        )}
                      </div>
                      {showCerebellumBusyAnimation && (
                        <p className="mt-1 text-xs text-amber-600 dark:text-amber-400">
                          {cerebellumPendingAction === 'restarting' ? `${t('brain.restart')}...` : `${cerebellumStatusConfig[cerebellumVisualStatus].label}...`}
                        </p>
                      )}
                      {cerebellum?.lastError && (
                        <p className="mt-1 text-xs text-rose-600 dark:text-rose-400">{cerebellum.lastError}</p>
                      )}
                    </div>
                  </div>

                  <div className="flex w-full flex-col gap-3 sm:flex-row sm:items-start xl:w-auto xl:flex-shrink-0 xl:items-center">
                    <div className="min-w-0 sm:flex-1 xl:w-72 xl:min-w-[18rem] xl:flex-none">
                      <p className="text-[11px] font-medium uppercase tracking-wide text-zinc-500 dark:text-zinc-400">
                        {t('brain.defaultModelLabel')}
                      </p>
                      <p className="mt-1 truncate text-sm text-zinc-900 dark:text-zinc-100">{cerebellumDefaultModelLabel}</p>
                      {cerebellumDefaultModel ? (
                        <p className="mt-0.5 truncate text-xs text-zinc-500 dark:text-zinc-400">{cerebellumDefaultModel.id}</p>
                      ) : null}
                    </div>

                    <div className="flex flex-wrap items-center gap-1 self-start sm:justify-end xl:flex-nowrap">
                      {cerebellumIsRunning ? (
                        <button
                          onClick={() => void cerebellumStop()}
                          disabled={cerebellumOperating}
                          className="flex min-w-[80px] items-center justify-center gap-1.5 whitespace-nowrap rounded-lg border border-zinc-200 px-3 py-2 text-sm font-medium text-zinc-700 transition-colors hover:bg-zinc-50 disabled:opacity-50 dark:border-zinc-700 dark:text-zinc-200 dark:hover:bg-zinc-800"
                        >
                          {cerebellumPendingAction === 'stopping' ? (
                            <Loader2 className="h-3.5 w-3.5 flex-shrink-0 animate-spin" />
                          ) : (
                            <Square className="h-3.5 w-3.5 flex-shrink-0" />
                          )}
                          {cerebellumPendingAction === 'stopping' ? `${t('brain.stopping')}...` : t('brain.stop')}
                        </button>
                      ) : (
                        <button
                          onClick={() => void cerebellumStart()}
                          disabled={cerebellumOperating || !cerebellum?.modelId}
                          className="flex min-w-[80px] items-center justify-center gap-1.5 whitespace-nowrap rounded-lg bg-indigo-600 px-3 py-2 text-sm font-medium text-white transition-colors hover:bg-indigo-500 disabled:opacity-50"
                        >
                          {cerebellumPendingAction === 'starting' || cerebellumPendingAction === 'restarting' ? (
                            <Loader2 className="h-3.5 w-3.5 flex-shrink-0 animate-spin" />
                          ) : (
                            <Play className="h-3.5 w-3.5 flex-shrink-0" />
                          )}
                          {cerebellumPendingAction === 'starting'
                            ? `${t('brain.starting')}...`
                            : cerebellumPendingAction === 'restarting'
                              ? `${t('brain.restart')}...`
                              : t('brain.start')}
                        </button>
                      )}
                      <button
                        onClick={() => void cerebellumRestart()}
                        disabled={cerebellumOperating || cerebellum?.status !== 'running' || !cerebellum?.modelId}
                        className="flex min-w-[80px] items-center justify-center gap-1.5 whitespace-nowrap rounded-lg border border-zinc-200 px-3 py-2 text-sm font-medium text-zinc-700 transition-colors hover:bg-zinc-50 disabled:opacity-50 dark:border-zinc-700 dark:text-zinc-200 dark:hover:bg-zinc-800"
                      >
                        {cerebellumPendingAction === 'restarting' ? (
                          <Loader2 className="h-3.5 w-3.5 flex-shrink-0 animate-spin" />
                        ) : (
                          <RotateCcw className="h-3.5 w-3.5 flex-shrink-0" />
                        )}
                        {cerebellumPendingAction === 'restarting' ? `${t('brain.restart')}...` : t('brain.restart')}
                      </button>
                      <button
                        onClick={() => openParamsModal('cerebellum')}
                        disabled={cerebellumOperating}
                        className="flex items-center justify-center gap-1.5 whitespace-nowrap rounded-lg border border-zinc-200 px-3 py-2 text-sm font-medium text-zinc-700 transition-colors hover:bg-zinc-50 disabled:opacity-50 dark:border-zinc-700 dark:text-zinc-200 dark:hover:bg-zinc-800"
                        title={t('brain.params') || 'Parameters'}
                      >
                        <Settings2 className="h-3.5 w-3.5 flex-shrink-0" />
                      </button>
                    </div>
                  </div>
                </div>
              </div>
            )}

            {!loading ? (
              <div className="space-y-3">
                <h4 className="text-sm font-medium text-zinc-700 dark:text-zinc-300">{t('brain.downloadedModelsTitle')}</h4>
                {(cerebellum?.models || []).length > 0 ? (
                  <div className="space-y-2">
                    {(cerebellum?.models || []).map((model) => {
                      const isDefault = cerebellum?.modelId === model.id;
                      return (
                        <div
                          key={model.id}
                          className="flex flex-col gap-3 rounded-lg border border-zinc-200 bg-white p-3 dark:border-zinc-800 dark:bg-zinc-900 lg:flex-row lg:items-center lg:justify-between"
                        >
                          <div className="min-w-0">
                            <div className="flex items-center gap-2">
                              <span className="truncate text-sm font-medium text-zinc-900 dark:text-zinc-100">{model.name}</span>
                              {isDefault ? (
                                <span className="inline-flex items-center rounded-full bg-amber-500/10 px-2 py-0.5 text-[11px] font-medium text-amber-600 dark:text-amber-400">
                                  {t('brain.default')}
                                </span>
                              ) : null}
                            </div>
                            <div className="mt-1 flex items-center gap-2 text-xs text-zinc-500 dark:text-zinc-400">
                              <span className="font-mono">{model.id}</span>
                              {model.size ? <span>· {model.size}</span> : null}
                            </div>
                          </div>
                          <div className="flex flex-wrap items-center gap-2 lg:justify-end">
                            {isDefault ? (
                              <button
                                onClick={() => void cerebellumClearModel()}
                                disabled={cerebellumOperating}
                                className="rounded-md border border-zinc-300 px-2.5 py-1 text-xs font-medium text-zinc-700 transition-colors hover:bg-zinc-50 disabled:opacity-50 dark:border-zinc-700 dark:text-zinc-200 dark:hover:bg-zinc-800"
                              >
                                {t('brain.unsetDefault')}
                              </button>
                            ) : (
                              <button
                                onClick={() => void cerebellumSelectModel(model.id)}
                                disabled={cerebellumOperating}
                                className="rounded-md bg-indigo-600 px-2.5 py-1 text-xs font-medium text-white transition-colors hover:bg-indigo-500 disabled:opacity-50"
                              >
                                {t('brain.setDefault')}
                              </button>
                            )}
                            <button
                              onClick={() => setLocalDeleteTarget({ scope: 'cerebellum', modelId: model.id, modelName: model.name })}
                              disabled={cerebellumOperating}
                              className="flex items-center gap-1 rounded-md border border-rose-300 px-2.5 py-1 text-xs font-medium text-rose-600 transition-colors hover:bg-rose-50 disabled:opacity-50 dark:border-rose-900/50 dark:text-rose-400 dark:hover:bg-rose-950/30"
                            >
                              <Trash2 className="h-3 w-3" />
                              {t('common.delete')}
                            </button>
                          </div>
                        </div>
                      );
                    })}
                  </div>
                ) : (
                  <div className="rounded-lg border border-zinc-200 bg-zinc-50 p-4 text-sm text-zinc-500 dark:border-zinc-800 dark:bg-zinc-950/50 dark:text-zinc-400">
                    {t('brain.noDownloadedModels')}
                  </div>
                )}
                {cerebellum?.modelsDir ? (
                  <p className="text-xs text-zinc-500 dark:text-zinc-400">{t('brain.modelDirectory', { path: cerebellum.modelsDir })}</p>
                ) : null}
              </div>
            ) : null}

            {/* Model Catalog – downloadable presets with progress */}
            {!loading && catalog.length > 0 ? (
              <div className="space-y-3">
                <h4 className="text-sm font-medium text-zinc-700 dark:text-zinc-300">{t('brain.catalogTitle')}</h4>
                <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
                  {catalog.map((preset) => {
                    const alreadyDownloaded = localModelIds.has(preset.filename?.replace(/\.gguf$/i, '') || preset.id);
                    const isThisDownloading = dlProgress?.active && dlProgress.presetId === preset.id;
                    const presetDescription = resolveLocalizedText(preset.description, language);
                    const pct =
                      isThisDownloading && dlProgress.totalBytes > 0
                        ? Math.min(100, Math.round((dlProgress.downloadedBytes / dlProgress.totalBytes) * 100))
                        : 0;

                    return (
                      <div
                        key={preset.id}
                        className="flex flex-col gap-2 rounded-lg border border-zinc-200 bg-zinc-50 p-3 dark:border-zinc-800 dark:bg-zinc-950/50"
                      >
                        <div className="flex items-center gap-2">
                          <HardDrive className="h-4 w-4 shrink-0 text-indigo-500" />
                          <span className="text-sm font-medium text-zinc-900 dark:text-zinc-100">{preset.name}</span>
                        </div>
                        {presetDescription ? (
                          <p className="text-xs text-zinc-500 dark:text-zinc-400">{presetDescription}</p>
                        ) : null}

                        {/* Download progress bar */}
                        {isThisDownloading ? (
                          <div className="space-y-1.5">
                            <div className="flex items-center justify-between text-xs text-zinc-500 dark:text-zinc-400">
                              <span className="flex items-center gap-1">
                                <Loader2 className="h-3 w-3 animate-spin" />
                                {t('brain.catalogDownloading')}
                              </span>
                              <span>
                                {dlProgress.totalBytes > 0
                                  ? `${formatBytes(dlProgress.downloadedBytes)} / ${formatBytes(dlProgress.totalBytes)} (${pct}%)`
                                  : formatBytes(dlProgress.downloadedBytes)}
                              </span>
                            </div>
                            <div className="h-2 w-full overflow-hidden rounded-full bg-zinc-200 dark:bg-zinc-800">
                              <div
                                className="h-full rounded-full bg-indigo-500 transition-all duration-300"
                                style={{ width: dlProgress.totalBytes > 0 ? `${pct}%` : '100%' }}
                              />
                            </div>
                            <div className="flex justify-end">
                              <button
                                onClick={() => void cerebellumCancelDownload()}
                                className="flex items-center gap-1 rounded-md border border-zinc-300 bg-white px-2.5 py-1 text-xs font-medium text-zinc-700 transition-colors hover:bg-zinc-50 dark:border-zinc-600 dark:bg-zinc-800 dark:text-zinc-300 dark:hover:bg-zinc-700"
                              >
                                <Square className="h-3 w-3" />
                                {t('common.cancel')}
                              </button>
                            </div>
                          </div>
                        ) : (
                          <div className="flex items-center justify-between">
                            <span className="text-xs text-zinc-400">{preset.size || '—'}</span>
                            {alreadyDownloaded ? (
                              <span className="flex items-center gap-1 text-xs font-medium text-emerald-600 dark:text-emerald-400">
                                <CheckCircle2 className="h-3.5 w-3.5" />
                                {t('brain.catalogDownloaded')}
                              </span>
                            ) : dlProgress?.active ? (
                              <button
                                disabled
                                className="flex items-center gap-1 rounded-md bg-indigo-600 px-2.5 py-1 text-xs font-medium text-white opacity-50"
                              >
                                <Download className="h-3 w-3" />
                                {t('brain.catalogDownload')}
                              </button>
                            ) : dlProgress && !dlProgress.active && !dlProgress.canceled && dlProgress.error && dlProgress.presetId === preset.id ? (
                              <div className="flex flex-col items-end gap-1.5">
                                <span className="text-xs text-rose-500">{t('brain.downloadFailed')}</span>
                                <div className="flex items-center gap-2">
                                  <button
                                    onClick={() => void openProxyModal(preset.id)}
                                    className="flex items-center gap-1 rounded-md border border-zinc-300 bg-white px-2.5 py-1 text-xs font-medium text-zinc-700 transition-colors hover:bg-zinc-50 dark:border-zinc-600 dark:bg-zinc-800 dark:text-zinc-300 dark:hover:bg-zinc-700"
                                  >
                                    <Globe className="h-3 w-3" />
                                    {t('brain.configureProxy')}
                                  </button>
                                  <button
                                    onClick={() => void cerebellumDownloadModel(preset.id)}
                                    className="flex items-center gap-1 rounded-md bg-indigo-600 px-2.5 py-1 text-xs font-medium text-white transition-colors hover:bg-indigo-500"
                                  >
                                    <Download className="h-3 w-3" />
                                    {t('brain.downloadRetry')}
                                  </button>
                                </div>
                              </div>
                            ) : (
                              <button
                                onClick={() => void cerebellumDownloadModel(preset.id)}
                                className="flex items-center gap-1 rounded-md bg-indigo-600 px-2.5 py-1 text-xs font-medium text-white transition-colors hover:bg-indigo-500"
                              >
                                <Download className="h-3 w-3" />
                                {t('brain.catalogDownload')}
                              </button>
                            )}
                          </div>
                        )}
                      </div>
                    );
                  })}
                </div>
              </div>
            ) : null}
          </section>
        )}
      </div>

      <Modal
        isOpen={modelModal !== null}
        onClose={() => setModelModal(null)}
        title={modelModal === 'edit' ? t('brain.editModel') : t('brain.addModel')}
      >
        <div className="space-y-4">
          {/* Mode toggle (create only) */}
          {modelModal === 'create' ? (
            <div>
              <label className="mb-1 block text-sm font-medium text-zinc-700 dark:text-zinc-300">{t('brain.mode')}</label>
              <div className="grid grid-cols-2 gap-2">
                <button
                  onClick={() => setModelForm((f) => ({ ...defaultModelForm(), mode: 'preset' }))}
                  className={`rounded-lg border px-3 py-2 text-sm font-medium transition-colors ${modelForm.mode === 'preset'
                    ? 'border-indigo-500 bg-indigo-50 text-indigo-700 dark:bg-indigo-950/40 dark:text-indigo-300'
                    : 'border-zinc-200 text-zinc-700 hover:bg-zinc-50 dark:border-zinc-800 dark:text-zinc-200 dark:hover:bg-zinc-800'
                    }`}
                >
                  {t('brain.preset')}
                </button>
                <button
                  onClick={() => setModelForm((f) => ({ ...defaultModelForm(), mode: 'custom' }))}
                  className={`rounded-lg border px-3 py-2 text-sm font-medium transition-colors ${modelForm.mode === 'custom'
                    ? 'border-indigo-500 bg-indigo-50 text-indigo-700 dark:bg-indigo-950/40 dark:text-indigo-300'
                    : 'border-zinc-200 text-zinc-700 hover:bg-zinc-50 dark:border-zinc-800 dark:text-zinc-200 dark:hover:bg-zinc-800'
                    }`}
                >
                  {t('brain.custom')}
                </button>
              </div>
            </div>
          ) : null}

          {/* ── Preset mode ── */}
          {modelForm.mode === 'preset' && modelModal === 'create' ? (
            <>
              {/* Provider selection */}
              <div>
                <label className="mb-1 block text-sm font-medium text-zinc-700 dark:text-zinc-300">{t('brain.presetProvider')}</label>
                <Select
                  value={modelForm.presetProviderId}
                  onChange={(val) =>
                    setModelForm((f) => ({ ...f, presetProviderId: val, presetModelInput: '' }))
                  }
                  placeholder={t('brain.selectProvider')}
                  options={presetProviders.map((p) => {
                    const displayLabel = (language === 'zh' && p.labelZh) ? p.labelZh : p.label;
                    return {
                      value: p.id,
                      label: `${displayLabel} (${p.id})`,
                      labelNode: (
                        <span className="flex items-center gap-2">
                          <span>{displayLabel} <span className="text-zinc-400">({p.id})</span></span>
                          {p.free && (
                            <span className="inline-flex items-center rounded-full bg-emerald-100 px-1.5 py-0.5 text-[10px] font-semibold leading-none text-emerald-700 dark:bg-emerald-900/40 dark:text-emerald-400">
                              {t('onboarding.step1.free')}
                            </span>
                          )}
                        </span>
                      ),
                    };
                  })}
                  searchable
                  searchPlaceholder={t('brain.selectProvider')}
                />
              </div>

              {/* API Key / OAuth + Model ID — shown after provider is selected */}
              {modelForm.presetProviderId ? (
                <>
                  {/* Authentication: OAuth device-code or API key */}
                  {isOAuthPreset ? (
                    <div>
                      <label className="mb-1 block text-sm font-medium text-zinc-700 dark:text-zinc-300">{t('brain.oauthAuth')}</label>
                      {isOAuthAuthed ? (
                        <div className="flex items-center gap-2 rounded-lg border border-emerald-200 bg-emerald-50 px-3 py-2.5 text-sm text-emerald-700 dark:border-emerald-900/50 dark:bg-emerald-950/30 dark:text-emerald-300">
                          <CheckCircle2 className="h-4 w-4" />
                          <span className="flex-1">{t('brain.oauthAuthorized')}</span>
                          <button
                            onClick={() => void logoutOAuth(modelForm.presetProviderId)}
                            className="rounded px-2 py-0.5 text-xs text-emerald-600 hover:bg-emerald-100 dark:text-emerald-400 dark:hover:bg-emerald-900/40"
                            title={t('brain.oauthLogout')}
                          >
                            <LogOut className="h-3.5 w-3.5" />
                          </button>
                        </div>
                      ) : oauthPolling ? (
                        <div className="space-y-2">
                          <div className="flex items-center gap-2 rounded-lg border border-amber-200 bg-amber-50 px-3 py-2.5 text-sm text-amber-700 dark:border-amber-900/50 dark:bg-amber-950/30 dark:text-amber-300">
                            <Loader2 className="h-4 w-4 animate-spin" />
                            <span>{t('brain.oauthWaiting')}</span>
                          </div>
                          {oauthSession?.userCode ? (
                            <div className="rounded-lg border border-zinc-200 bg-zinc-50 p-3 text-center dark:border-zinc-800 dark:bg-zinc-950/50">
                              <div className="mb-1 text-xs text-zinc-500 dark:text-zinc-400">{t('brain.oauthUserCode')}</div>
                              <div className="font-mono text-lg font-bold text-zinc-900 dark:text-zinc-100">{oauthSession.userCode}</div>
                              <a
                                href={oauthSession.verificationUrl}
                                target="_blank"
                                rel="noopener noreferrer"
                                className="mt-1 inline-flex items-center gap-1 text-xs text-indigo-600 hover:underline dark:text-indigo-400"
                              >
                                {t('brain.oauthOpenBrowser')} <ExternalLink className="h-3 w-3" />
                              </a>
                            </div>
                          ) : null}
                        </div>
                      ) : (
                        <div className="space-y-2">
                          <button
                            onClick={() => void startOAuth(modelForm.presetProviderId)}
                            className="flex w-full items-center justify-center gap-2 rounded-lg border border-indigo-500 bg-indigo-50 px-3 py-2.5 text-sm font-medium text-indigo-700 transition-colors hover:bg-indigo-100 dark:bg-indigo-950/40 dark:text-indigo-300 dark:hover:bg-indigo-950/60"
                          >
                            <LogIn className="h-4 w-4" />
                            {t('brain.oauthLogin')}
                          </button>
                          {oauthError ? (
                            <div className="rounded-lg border border-rose-200 bg-rose-50 px-3 py-2 text-xs text-rose-600 dark:border-rose-900/50 dark:bg-rose-950/30 dark:text-rose-400">
                              {oauthError}
                            </div>
                          ) : null}
                        </div>
                      )}
                    </div>
                  ) : (
                    <div>
                      <label className="mb-1 block text-sm font-medium text-zinc-700 dark:text-zinc-300">{t('brain.apiKey')}</label>
                      <input
                        value={modelForm.apiKey}
                        onChange={(e) => setModelForm((f) => ({ ...f, apiKey: e.target.value }))}
                        className="w-full rounded-lg border border-zinc-200 bg-zinc-50 px-3 py-2.5 text-sm text-zinc-900 outline-none transition-all focus:border-indigo-500 focus:ring-1 focus:ring-indigo-500 dark:border-zinc-800 dark:bg-zinc-950 dark:text-zinc-200"
                        placeholder={t('brain.apiKeyPlaceholder')}
                        autoComplete="off"
                      />
                    </div>
                  )}

                  <div>
                    <label className="mb-1 block text-sm font-medium text-zinc-700 dark:text-zinc-300">{t('brain.presetModelId')}</label>
                    <Select
                      value={modelForm.presetModelInput}
                      onChange={(val) => setModelForm((f) => ({ ...f, presetModelInput: val }))}
                      placeholder={t('brain.presetModelIdPlaceholder')}
                      options={(selectedProvider?.models || []).map((m) => ({ value: m.id, label: `${m.name} (${m.id})` }))}
                      searchable
                      searchPlaceholder={t('brain.presetModelIdPlaceholder')}
                      allowCustomValue
                    />
                  </div>

                  {/* Matched preset info */}
                  {matchedPresetModel ? (
                    <div className="rounded-lg border border-indigo-200 bg-indigo-50 p-3 text-xs text-indigo-700 dark:border-indigo-900/50 dark:bg-indigo-950/30 dark:text-indigo-300">
                      <div className="mb-1 font-medium">{t('brain.matchedPreset')}: {matchedPresetModel.name}</div>
                      <div className="flex flex-wrap gap-x-4 gap-y-1 text-indigo-600 dark:text-indigo-400">
                        <span>{t('brain.contextMetric', { value: String(matchedPresetModel.contextWindow || '-') })}</span>
                        <span>{t('brain.maxMetric', { value: String(matchedPresetModel.maxTokens || '-') })}</span>
                        <span>{matchedPresetModel.reasoning ? t('brain.reasoning') + ' ✓' : ''}</span>
                        <span className="font-mono">{selectedProvider?.baseUrl}</span>
                      </div>
                    </div>
                  ) : modelForm.presetModelInput.trim() ? (
                    <div className="rounded-lg border border-zinc-200 bg-zinc-50 p-3 text-xs text-zinc-500 dark:border-zinc-800 dark:bg-zinc-950/50 dark:text-zinc-400">
                      <div className="font-mono">{selectedProvider?.baseUrl}</div>
                      <div className="mt-0.5">{selectedProvider?.api}</div>
                    </div>
                  ) : null}
                </>
              ) : null}
            </>
          ) : (
            /* ── Custom mode (also used for edit) ── */
            <>
              {modelModal === 'edit' && editingRecord ? (
                <div className="rounded-xl border border-zinc-200 bg-zinc-50 p-4 dark:border-zinc-800 dark:bg-zinc-950/50">
                  <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
                    <div>
                      <div className="flex items-center gap-2">
                        <h4 className="text-sm font-semibold text-zinc-900 dark:text-zinc-100">{recordLabel(editingRecord)}</h4>
                        {editingRecord.isDefault ? (
                          <span className="inline-flex items-center rounded-full bg-amber-500/10 px-2 py-0.5 text-[11px] font-medium text-amber-600 dark:text-amber-400">{t('brain.default')}</span>
                        ) : null}
                        {editingRecord.isFallback ? (
                          <span className="inline-flex items-center rounded-full bg-sky-500/10 px-2 py-0.5 text-[11px] font-medium text-sky-600 dark:text-sky-400">{t('brain.fallback')}</span>
                        ) : null}
                      </div>
                      <p className="mt-1 text-xs text-zinc-500 dark:text-zinc-400">{editingRecord.providerId} · <span className="font-mono">{editingRecord.modelId}</span></p>
                    </div>
                    {editingRecord.ready ? (
                      <span className="inline-flex items-center gap-1 rounded-full bg-emerald-500/10 px-2 py-1 text-xs font-medium text-emerald-600 dark:text-emerald-400">
                        <CheckCircle2 className="h-3.5 w-3.5" /> {t('brain.networkOk')}
                      </span>
                    ) : (
                      <span className="inline-flex items-center gap-1 rounded-full bg-rose-500/10 px-2 py-1 text-xs font-medium text-rose-600 dark:text-rose-400">
                        <AlertCircle className="h-3.5 w-3.5" /> {editingRecord.lastError || t('brain.invalidKey')}
                      </span>
                    )}
                  </div>

                  <div className="mt-4 grid gap-3 sm:grid-cols-2">
                    <div className="rounded-lg border border-zinc-200 bg-white p-3 text-xs dark:border-zinc-800 dark:bg-zinc-900">
                      <div className="text-zinc-400">{t('brain.baseUrl')}</div>
                      <div className="mt-1 break-all text-zinc-700 dark:text-zinc-300">{editingRecord.baseUrl || '—'}</div>
                    </div>
                    <div className="rounded-lg border border-zinc-200 bg-white p-3 text-xs dark:border-zinc-800 dark:bg-zinc-900">
                      <div className="text-zinc-400">{t('brain.apiKind')}</div>
                      <div className="mt-1 text-zinc-700 dark:text-zinc-300">{editingRecord.api || '—'}</div>
                    </div>
                    <div className="rounded-lg border border-zinc-200 bg-white p-3 text-xs dark:border-zinc-800 dark:bg-zinc-900">
                      <div className="text-zinc-400">{t('brain.limits')}</div>
                      <div className="mt-1 text-zinc-700 dark:text-zinc-300">{t('brain.contextMetric', { value: String(editingRecord.contextWindow || '-') })} · {t('brain.maxMetric', { value: String(editingRecord.maxTokens || '-') })}</div>
                    </div>
                    <div className="rounded-lg border border-zinc-200 bg-white p-3 text-xs dark:border-zinc-800 dark:bg-zinc-900">
                      <div className="text-zinc-400">{t('brain.apiKey')}</div>
                      <div className="mt-1 font-mono text-zinc-700 dark:text-zinc-300">{maskKey(editingRecord.apiKey) || t('brain.notConfigured')}</div>
                    </div>
                  </div>
                </div>
              ) : null}

              <div className="grid gap-3 sm:grid-cols-2">
                <div className="sm:col-span-2">
                  <label className="mb-1 block text-sm font-medium text-zinc-700 dark:text-zinc-300">{t('brain.baseUrl')}</label>
                  <input
                    value={modelForm.baseUrl}
                    onChange={(e) => setModelForm((f) => ({ ...f, baseUrl: e.target.value }))}
                    className="w-full rounded-lg border border-zinc-200 bg-zinc-50 px-3 py-2.5 text-sm text-zinc-900 outline-none transition-all focus:border-indigo-500 focus:ring-1 focus:ring-indigo-500 dark:border-zinc-800 dark:bg-zinc-950 dark:text-zinc-200"
                    placeholder={t('brain.baseUrlPlaceholder')}
                  />
                </div>
                <div>
                  <label className="mb-1 block text-sm font-medium text-zinc-700 dark:text-zinc-300">{t('brain.apiKind')}</label>
                  <Select
                    value={modelForm.api}
                    onChange={(val) => setModelForm((f) => ({ ...f, api: val }))}
                    options={API_KIND_OPTIONS}
                  />
                </div>
                <div>
                  <label className="mb-1 block text-sm font-medium text-zinc-700 dark:text-zinc-300">{t('brain.customModelId')}</label>
                  <input
                    value={modelForm.modelId}
                    onChange={(e) => setModelForm((f) => ({ ...f, modelId: e.target.value }))}
                    className="w-full rounded-lg border border-zinc-200 bg-zinc-50 px-3 py-2.5 text-sm text-zinc-900 outline-none transition-all focus:border-indigo-500 focus:ring-1 focus:ring-indigo-500 dark:border-zinc-800 dark:bg-zinc-950 dark:text-zinc-200"
                    placeholder={t('brain.customModelPlaceholder')}
                  />
                </div>
                <div className="sm:col-span-2">
                  <label className="mb-1 block text-sm font-medium text-zinc-700 dark:text-zinc-300">{t('brain.apiKey')}</label>
                  <input
                    value={modelForm.apiKey}
                    onChange={(e) => setModelForm((f) => ({ ...f, apiKey: e.target.value }))}
                    className="w-full rounded-lg border border-zinc-200 bg-zinc-50 px-3 py-2.5 text-sm text-zinc-900 outline-none transition-all focus:border-indigo-500 focus:ring-1 focus:ring-indigo-500 dark:border-zinc-800 dark:bg-zinc-950 dark:text-zinc-200"
                    placeholder={t('brain.apiKeyPlaceholder')}
                    autoComplete="off"
                  />
                </div>
                <div>
                  <label className="mb-1 block text-sm font-medium text-zinc-700 dark:text-zinc-300">{t('brain.contextWindow')}</label>
                  <input
                    value={modelForm.contextWindow}
                    onChange={(e) => setModelForm((f) => ({ ...f, contextWindow: e.target.value }))}
                    className="w-full rounded-lg border border-zinc-200 bg-zinc-50 px-3 py-2.5 text-sm text-zinc-900 outline-none transition-all focus:border-indigo-500 focus:ring-1 focus:ring-indigo-500 dark:border-zinc-800 dark:bg-zinc-950 dark:text-zinc-200"
                    placeholder={t('brain.contextWindowPlaceholder')}
                    type="number"
                    min="0"
                  />
                </div>
                <div>
                  <label className="mb-1 block text-sm font-medium text-zinc-700 dark:text-zinc-300">{t('brain.maxTokens')}</label>
                  <input
                    value={modelForm.maxTokens}
                    onChange={(e) => setModelForm((f) => ({ ...f, maxTokens: e.target.value }))}
                    className="w-full rounded-lg border border-zinc-200 bg-zinc-50 px-3 py-2.5 text-sm text-zinc-900 outline-none transition-all focus:border-indigo-500 focus:ring-1 focus:ring-indigo-500 dark:border-zinc-800 dark:bg-zinc-950 dark:text-zinc-200"
                    placeholder={t('brain.maxTokensPlaceholder')}
                    type="number"
                    min="0"
                  />
                </div>
                <div className="sm:col-span-2 rounded-lg border border-zinc-200 bg-zinc-50 px-3 py-2.5 dark:border-zinc-800 dark:bg-zinc-950/50">
                  <label className="flex items-center gap-2 text-sm text-zinc-700 dark:text-zinc-300">
                    <input
                      type="checkbox"
                      checked={modelForm.reasoning}
                      onChange={(e) => setModelForm((f) => ({ ...f, reasoning: e.target.checked }))}
                      className="rounded"
                    />
                    {t('brain.reasoningEnabled')}
                  </label>
                </div>
              </div>

              {/* Auto-generated info */}
              {(derivedProviderId || derivedDisplayName) ? (
                <div className="rounded-lg border border-zinc-200 bg-zinc-50 p-3 dark:border-zinc-800 dark:bg-zinc-950/50">
                  <div className="mb-1.5 text-xs font-medium uppercase tracking-wide text-zinc-400">{t('brain.autoGeneratedInfo')}</div>
                  <div className="grid grid-cols-2 gap-2 text-xs text-zinc-600 dark:text-zinc-400">
                    <div>
                      <span className="text-zinc-400">{t('brain.derivedProviderId')}: </span>
                      <span className="font-mono text-zinc-700 dark:text-zinc-300">{derivedProviderId || '—'}</span>
                    </div>
                    <div>
                      <span className="text-zinc-400">{t('brain.derivedDisplayName')}: </span>
                      <span className="font-medium text-zinc-700 dark:text-zinc-300">{derivedDisplayName || '—'}</span>
                    </div>
                  </div>
                </div>
              ) : null}
            </>
          )}

          <div className="flex justify-end gap-2 pt-2">
            <button
              onClick={() => setModelModal(null)}
              className="rounded-lg px-4 py-2 text-sm font-medium text-zinc-700 transition-colors hover:bg-zinc-100 dark:text-zinc-300 dark:hover:bg-zinc-800"
            >
              {t('common.cancel')}
            </button>
            <button
              onClick={() => void saveModel()}
              disabled={saving || !canSave}
              className="rounded-lg bg-indigo-600 px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-indigo-500 disabled:cursor-not-allowed disabled:opacity-50"
            >
              {saving ? t('common.saving') : modelModal === 'edit' ? t('tasks.update') : t('tasks.create')}
            </button>
          </div>
        </div>
      </Modal>

      <ConfirmDialog
        isOpen={deleteTarget !== null}
        onClose={() => setDeleteTarget(null)}
        onConfirm={() => void deleteModel()}
        title={t('common.deleteConfirmTitle')}
        message={t('common.deleteConfirmMessage')}
        confirmText={t('common.delete')}
        loading={saving}
      />
      <ConfirmDialog
        isOpen={localDeleteTarget !== null}
        onClose={() => setLocalDeleteTarget(null)}
        onConfirm={() => {
          if (!localDeleteTarget) return;
          if (localDeleteTarget.scope === 'brainLocal') {
            void brainLocalDeleteModel(localDeleteTarget.modelId);
          } else {
            void cerebellumDeleteModel(localDeleteTarget.modelId);
          }
        }}
        title={t('common.deleteConfirmTitle')}
        message={t('common.deleteConfirmMessage')}
        confirmText={t('common.delete')}
        loading={saving || cerebellumOperating || brainLocalOperating}
      />

      {/* Proxy configuration modal — shown when download fails */}
      <Modal
        isOpen={proxyModalOpen}
        onClose={() => setProxyModalOpen(false)}
        title={t('brain.proxyModalTitle')}
      >
        <div className="space-y-4 pt-2">
          <p className="text-sm text-zinc-500 dark:text-zinc-400">{t('brain.proxyModalDesc')}</p>
          {proxyModalError && (
            <div className="rounded-lg bg-rose-50 px-3 py-2 text-xs text-rose-600 dark:bg-rose-900/20 dark:text-rose-400">
              {proxyModalError}
            </div>
          )}
          <div>
            <label className="mb-1.5 block text-xs font-medium text-zinc-700 dark:text-zinc-300">
              {t('network.proxyUrl')}
            </label>
            <input
              type="text"
              value={proxyModalUrl}
              onChange={(e) => setProxyModalUrl(e.target.value)}
              placeholder={t('network.proxyPlaceholder')}
              className="w-full rounded-lg border border-zinc-300 bg-white px-3 py-2 text-sm text-zinc-900 placeholder:text-zinc-400 focus:border-indigo-500 focus:outline-none focus:ring-1 focus:ring-indigo-500 dark:border-zinc-700 dark:bg-zinc-800 dark:text-zinc-100 dark:placeholder:text-zinc-500"
            />
          </div>
          <div className="flex justify-end gap-3">
            <button
              type="button"
              onClick={() => setProxyModalOpen(false)}
              className="rounded-lg border border-zinc-300 bg-white px-4 py-2 text-sm font-medium text-zinc-700 transition-colors hover:bg-zinc-50 dark:border-zinc-600 dark:bg-zinc-800 dark:text-zinc-300 dark:hover:bg-zinc-700"
            >
              {t('common.cancel')}
            </button>
            <button
              type="button"
              onClick={() => void handleProxySaveAndRetry()}
              disabled={proxyModalSaving}
              className="inline-flex items-center gap-2 rounded-lg bg-indigo-600 px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-indigo-500 disabled:opacity-50"
            >
              {proxyModalSaving && <Loader2 className="h-4 w-4 animate-spin" />}
              {t('brain.proxySaveRetry')}
            </button>
          </div>
        </div>
      </Modal>

      {/* Model Parameters Modal */}
      <Modal
        isOpen={paramsModalTarget !== null}
        onClose={() => setParamsModalTarget(null)}
        title={paramsModalTarget === 'brainLocal' ? (t('brain.brainLocalParams') || 'Brain Local Parameters') : (t('brain.cerebellumParams') || 'Cerebellum Parameters')}
      >
        <div className="space-y-4 pt-2">
          {paramsError && (
            <div className="rounded-lg bg-rose-50 px-3 py-2 text-xs text-rose-600 dark:bg-rose-900/20 dark:text-rose-400">
              {paramsError}
            </div>
          )}

          {/* Runtime Parameters */}
          <div className="space-y-3">
            <h4 className="text-xs font-semibold uppercase tracking-wider text-zinc-500 dark:text-zinc-400">
              {t('brain.runtimeParams') || 'Runtime Parameters'}
            </h4>
            {paramsModalTarget === 'brainLocal' ? (
              <label className="flex items-center justify-between rounded-lg border border-zinc-200 bg-zinc-50 px-3 py-2 text-sm text-zinc-700 dark:border-zinc-800 dark:bg-zinc-900/50 dark:text-zinc-200">
                <span>{t('brain.reasoningEnabled') || 'Reasoning enabled'}</span>
                <input
                  type="checkbox"
                  checked={paramsForm.enableThinking}
                  onChange={(e) => setParamsForm((f) => ({ ...f, enableThinking: e.target.checked }))}
                  className="h-4 w-4 rounded border-zinc-300 text-indigo-600 focus:ring-indigo-500 dark:border-zinc-700 dark:bg-zinc-800"
                />
              </label>
            ) : null}
            <div className="grid grid-cols-3 gap-3">
              <div>
                <label className="mb-1 block text-xs font-medium text-zinc-700 dark:text-zinc-300">
                  {t('brain.threads') || 'Threads'}
                </label>
                <input
                  type="number" min={1} max={64} step={1}
                  value={paramsForm.threads}
                  onChange={(e) => setParamsForm((f) => ({ ...f, threads: parseInt(e.target.value) || 1 }))}
                  className="w-full rounded-lg border border-zinc-300 bg-white px-3 py-1.5 text-sm text-zinc-900 focus:border-indigo-500 focus:outline-none focus:ring-1 focus:ring-indigo-500 dark:border-zinc-700 dark:bg-zinc-800 dark:text-zinc-100"
                />
              </div>
              <div>
                <label className="mb-1 block text-xs font-medium text-zinc-700 dark:text-zinc-300">
                  {t('brain.contextSize') || 'Context Size'}
                </label>
                <input
                  type="number" min={512} max={131072} step={512}
                  value={paramsForm.contextSize}
                  onChange={(e) => setParamsForm((f) => ({ ...f, contextSize: parseInt(e.target.value) || 2048 }))}
                  className="w-full rounded-lg border border-zinc-300 bg-white px-3 py-1.5 text-sm text-zinc-900 focus:border-indigo-500 focus:outline-none focus:ring-1 focus:ring-indigo-500 dark:border-zinc-700 dark:bg-zinc-800 dark:text-zinc-100"
                />
              </div>
              <div>
                <label className="mb-1 block text-xs font-medium text-zinc-700 dark:text-zinc-300">
                  {t('brain.gpuLayers') || 'GPU Layers'}
                </label>
                <input
                  type="number" min={-1} max={999} step={1}
                  value={paramsForm.gpuLayers}
                  onChange={(e) => setParamsForm((f) => ({ ...f, gpuLayers: parseInt(e.target.value) || 0 }))}
                  className="w-full rounded-lg border border-zinc-300 bg-white px-3 py-1.5 text-sm text-zinc-900 focus:border-indigo-500 focus:outline-none focus:ring-1 focus:ring-indigo-500 dark:border-zinc-700 dark:bg-zinc-800 dark:text-zinc-100"
                />
                <span className="mt-0.5 block text-[10px] text-zinc-400">{t('brain.gpuAll')}</span>
              </div>
            </div>
          </div>

          {/* Sampling Parameters */}
          <div className="space-y-3">
            <h4 className="text-xs font-semibold uppercase tracking-wider text-zinc-500 dark:text-zinc-400">
              {t('brain.samplingParams')}
            </h4>
            <div className="grid grid-cols-3 gap-3">
              <div>
                <label className="mb-1 block text-xs font-medium text-zinc-700 dark:text-zinc-300">{t('brain.temperature')}</label>
                <input
                  type="number" min={0} max={2} step={0.01}
                  value={paramsForm.temp}
                  onChange={(e) => setParamsForm((f) => ({ ...f, temp: parseFloat(e.target.value) || 0 }))}
                  className="w-full rounded-lg border border-zinc-300 bg-white px-3 py-1.5 text-sm text-zinc-900 focus:border-indigo-500 focus:outline-none focus:ring-1 focus:ring-indigo-500 dark:border-zinc-700 dark:bg-zinc-800 dark:text-zinc-100"
                />
              </div>
              <div>
                <label className="mb-1 block text-xs font-medium text-zinc-700 dark:text-zinc-300">{t('brain.topP')}</label>
                <input
                  type="number" min={0} max={1} step={0.01}
                  value={paramsForm.topP}
                  onChange={(e) => setParamsForm((f) => ({ ...f, topP: parseFloat(e.target.value) || 0 }))}
                  className="w-full rounded-lg border border-zinc-300 bg-white px-3 py-1.5 text-sm text-zinc-900 focus:border-indigo-500 focus:outline-none focus:ring-1 focus:ring-indigo-500 dark:border-zinc-700 dark:bg-zinc-800 dark:text-zinc-100"
                />
              </div>
              <div>
                <label className="mb-1 block text-xs font-medium text-zinc-700 dark:text-zinc-300">{t('brain.topK')}</label>
                <input
                  type="number" min={0} max={500} step={1}
                  value={paramsForm.topK}
                  onChange={(e) => setParamsForm((f) => ({ ...f, topK: parseInt(e.target.value) || 0 }))}
                  className="w-full rounded-lg border border-zinc-300 bg-white px-3 py-1.5 text-sm text-zinc-900 focus:border-indigo-500 focus:outline-none focus:ring-1 focus:ring-indigo-500 dark:border-zinc-700 dark:bg-zinc-800 dark:text-zinc-100"
                />
              </div>
              <div>
                <label className="mb-1 block text-xs font-medium text-zinc-700 dark:text-zinc-300">{t('brain.minP')}</label>
                <input
                  type="number" min={0} max={1} step={0.01}
                  value={paramsForm.minP}
                  onChange={(e) => setParamsForm((f) => ({ ...f, minP: parseFloat(e.target.value) || 0 }))}
                  className="w-full rounded-lg border border-zinc-300 bg-white px-3 py-1.5 text-sm text-zinc-900 focus:border-indigo-500 focus:outline-none focus:ring-1 focus:ring-indigo-500 dark:border-zinc-700 dark:bg-zinc-800 dark:text-zinc-100"
                />
              </div>
              <div>
                <label className="mb-1 block text-xs font-medium text-zinc-700 dark:text-zinc-300">{t('brain.typicalP')}</label>
                <input
                  type="number" min={0} max={1} step={0.01}
                  value={paramsForm.typicalP}
                  onChange={(e) => setParamsForm((f) => ({ ...f, typicalP: parseFloat(e.target.value) || 0 }))}
                  className="w-full rounded-lg border border-zinc-300 bg-white px-3 py-1.5 text-sm text-zinc-900 focus:border-indigo-500 focus:outline-none focus:ring-1 focus:ring-indigo-500 dark:border-zinc-700 dark:bg-zinc-800 dark:text-zinc-100"
                />
              </div>
              <div>
                <label className="mb-1 block text-xs font-medium text-zinc-700 dark:text-zinc-300">{t('brain.repeatLastN')}</label>
                <input
                  type="number" min={0} max={4096} step={1}
                  value={paramsForm.repeatLastN}
                  onChange={(e) => setParamsForm((f) => ({ ...f, repeatLastN: parseInt(e.target.value) || 0 }))}
                  className="w-full rounded-lg border border-zinc-300 bg-white px-3 py-1.5 text-sm text-zinc-900 focus:border-indigo-500 focus:outline-none focus:ring-1 focus:ring-indigo-500 dark:border-zinc-700 dark:bg-zinc-800 dark:text-zinc-100"
                />
              </div>
              <div>
                <label className="mb-1 block text-xs font-medium text-zinc-700 dark:text-zinc-300">{t('brain.penaltyRepeat')}</label>
                <input
                  type="number" min={0} max={2} step={0.01}
                  value={paramsForm.penaltyRepeat}
                  onChange={(e) => setParamsForm((f) => ({ ...f, penaltyRepeat: parseFloat(e.target.value) || 0 }))}
                  className="w-full rounded-lg border border-zinc-300 bg-white px-3 py-1.5 text-sm text-zinc-900 focus:border-indigo-500 focus:outline-none focus:ring-1 focus:ring-indigo-500 dark:border-zinc-700 dark:bg-zinc-800 dark:text-zinc-100"
                />
              </div>
              <div>
                <label className="mb-1 block text-xs font-medium text-zinc-700 dark:text-zinc-300">{t('brain.penaltyFreq')}</label>
                <input
                  type="number" min={0} max={2} step={0.01}
                  value={paramsForm.penaltyFreq}
                  onChange={(e) => setParamsForm((f) => ({ ...f, penaltyFreq: parseFloat(e.target.value) || 0 }))}
                  className="w-full rounded-lg border border-zinc-300 bg-white px-3 py-1.5 text-sm text-zinc-900 focus:border-indigo-500 focus:outline-none focus:ring-1 focus:ring-indigo-500 dark:border-zinc-700 dark:bg-zinc-800 dark:text-zinc-100"
                />
              </div>
              <div>
                <label className="mb-1 block text-xs font-medium text-zinc-700 dark:text-zinc-300">{t('brain.penaltyPresent')}</label>
                <input
                  type="number" min={0} max={2} step={0.01}
                  value={paramsForm.penaltyPresent}
                  onChange={(e) => setParamsForm((f) => ({ ...f, penaltyPresent: parseFloat(e.target.value) || 0 }))}
                  className="w-full rounded-lg border border-zinc-300 bg-white px-3 py-1.5 text-sm text-zinc-900 focus:border-indigo-500 focus:outline-none focus:ring-1 focus:ring-indigo-500 dark:border-zinc-700 dark:bg-zinc-800 dark:text-zinc-100"
                />
              </div>
            </div>
          </div>

          <p className="text-xs text-zinc-400 dark:text-zinc-500">
            {t('brain.paramsNote')}
          </p>

          <div className="flex justify-end gap-3 pt-1">
            <button
              type="button"
              onClick={() => setParamsModalTarget(null)}
              className="rounded-lg border border-zinc-300 bg-white px-4 py-2 text-sm font-medium text-zinc-700 transition-colors hover:bg-zinc-50 dark:border-zinc-600 dark:bg-zinc-800 dark:text-zinc-300 dark:hover:bg-zinc-700"
            >
              {t('common.cancel')}
            </button>
            <button
              type="button"
              onClick={() => void saveParams()}
              disabled={paramsSaving}
              className="inline-flex items-center gap-2 rounded-lg bg-indigo-600 px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-indigo-500 disabled:opacity-50"
            >
              {paramsSaving && <Loader2 className="h-4 w-4 animate-spin" />}
              {t('common.save') || 'Save'}
            </button>
          </div>
        </div>
      </Modal>
    </div>
  );
}
