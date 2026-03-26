'use client';

import { useEffect, useMemo, useState } from 'react';
import { MessageSquare, Pencil, Plus, RefreshCw, Trash2 } from 'lucide-react';
import { useI18n } from '@/lib/i18n/I18nContext';
import { ErrorAlert, Modal, PageHeader, Select, ConfirmDialog } from '@/components/ui';
import { apiGet, apiPost, type ChannelsState, type ChannelDriverSchema, type ChannelConfigField } from '@/lib/api';
import { WeixinQRLogin } from './WeixinQRLogin';

type ChannelEntry = NonNullable<ChannelsState['config']['entries']>[string];
type TranslateFn = (key: any, params?: Record<string, string>) => string;

// 动态字段值存储
type ChannelDraft = {
  id: string;
  type: string;
  enabled: boolean;
  agent: string;
  fields: Record<string, string | boolean>; // 动态字段值
};

const EMPTY_DRAFT: ChannelDraft = {
  id: '',
  type: 'generic',
  enabled: true,
  agent: 'main',
  fields: {},
};

// 从entry中提取driver类型
function readChannelDriver(entry?: ChannelEntry): string {
  return String(entry?.config?.driver || entry?.config?.type || 'generic').trim().toLowerCase();
}

// 从entry构建draft
function entryToDraft(id: string, entry?: ChannelEntry, schema?: ChannelDriverSchema): ChannelDraft {
  if (!entry) return { ...EMPTY_DRAFT, id };

  const type = readChannelDriver(entry);
  const draft: ChannelDraft = {
    id,
    type,
    enabled: entry.enabled ?? true,
    agent: String(entry.agent || 'main'),
    fields: {},
  };

  if (!schema) return draft;

  // 根据schema从entry中提取字段值
  const defaultAccount = String(entry.defaultAccount || 'main').trim();
  const account = (entry.accounts?.[defaultAccount] || {}) as Record<string, unknown>;

  schema.fields.forEach((field) => {
    const key = field.key;
    let value: string | boolean = field.defaultValue || '';

    if (field.type === 'checkbox') {
      value = false;
    }

    // 优先从account中取值，然后config，最后entry根字段
    if (field.group === 'account' && account[key] !== undefined) {
      value = String(account[key]);
    } else if (entry.config?.[key] !== undefined) {
      value = String(entry.config[key]);
    } else if (key === 'defaultTo' && entry.defaultTo) {
      value = String(entry.defaultTo);
    } else if (key === 'defaultAccount' && entry.defaultAccount) {
      value = String(entry.defaultAccount);
    } else if (key === 'inboundToken' && entry.config?.inboundToken) {
      value = String(entry.config.inboundToken);
    }

    draft.fields[key] = value;
  });

  return draft;
}

// 从draft构建entry
function draftToEntry(draft: ChannelDraft, schema?: ChannelDriverSchema): ChannelEntry {
  const base: ChannelEntry = {
    enabled: draft.enabled,
    agent: draft.agent.trim() || 'main',
    config: { driver: draft.type },
  };

  if (!schema) return base;

  // 根据schema的字段分组构建entry结构
  const accountFields: Record<string, unknown> = {};
  const configFields: Record<string, unknown> = {};

  schema.fields.forEach((field) => {
    const key = field.key;
    const value = draft.fields[key];

    if (value === undefined || value === '') return;

    if (field.group === 'account') {
      accountFields[key] = value;
    } else if (key === 'defaultTo') {
      base.defaultTo = String(value).trim();
    } else if (key === 'defaultAccount') {
      base.defaultAccount = String(value).trim();
    } else if (key === 'inboundToken') {
      configFields[key] = value;
    } else {
      configFields[key] = value;
    }
  });

  // 如果有账户字段，构建accounts结构
  if (Object.keys(accountFields).length > 0) {
    const accountId = String(draft.fields.defaultAccount || 'main').trim() || 'main';
    base.defaultAccount = accountId;
    base.accounts = {
      [accountId]: accountFields,
    };
  }

  // 合并config字段
  if (Object.keys(configFields).length > 0) {
    base.config = { ...base.config, ...configFields };
  }

  return base;
}

function translateChannelSchemaText(
  t: TranslateFn,
  key: string,
  fallback: string,
  params?: Record<string, string>
): string {
  const translated = t(key as any, params);
  return translated === key ? fallback : translated;
}

function getSchemaName(
  t: TranslateFn,
  schema: ChannelDriverSchema
): string {
  return translateChannelSchemaText(t, `channels.schema.${schema.id}.name`, schema.name);
}

function getSchemaDescription(
  t: TranslateFn,
  schema: ChannelDriverSchema
): string {
  return translateChannelSchemaText(t, `channels.schema.${schema.id}.description`, schema.description || '');
}

function getFieldLabel(
  t: TranslateFn,
  schemaId: string,
  field: ChannelConfigField
): string {
  return translateChannelSchemaText(t, `channels.schema.${schemaId}.field.${field.key}.label`, field.label);
}

function getFieldPlaceholder(
  t: TranslateFn,
  schemaId: string,
  field: ChannelConfigField
): string {
  return translateChannelSchemaText(t, `channels.schema.${schemaId}.field.${field.key}.placeholder`, field.placeholder || '');
}

function getFieldHelp(
  t: TranslateFn,
  schemaId: string,
  field: ChannelConfigField
): string {
  return translateChannelSchemaText(t, `channels.schema.${schemaId}.field.${field.key}.help`, field.help || '');
}

function getFieldOptions(
  t: TranslateFn,
  schemaId: string,
  field: ChannelConfigField
) {
  return (field.options || []).map((option) => ({
    value: option.value,
    label: translateChannelSchemaText(
      t,
      `channels.schema.${schemaId}.field.${field.key}.option.${option.value}.label`,
      option.label
    ),
  }));
}

// 动态字段渲染组件
function DynamicField({
  schemaId,
  field,
  value,
  onChange,
}: {
  schemaId: string;
  field: ChannelConfigField;
  value: string | boolean;
  onChange: (value: string | boolean) => void;
}) {
  const { t } = useI18n();
  const baseClassName = 'w-full bg-zinc-50 dark:bg-zinc-950 border border-zinc-200 dark:border-zinc-800 rounded-lg p-2.5 text-sm text-zinc-900 dark:text-zinc-200 focus:border-indigo-500 focus:ring-1 focus:ring-indigo-500 transition-all outline-none';
  const placeholder = getFieldPlaceholder(t, schemaId, field);

  switch (field.type) {
    case 'text':
      return (
        <input
          type="text"
          value={String(value)}
          onChange={(e) => onChange(e.target.value)}
          placeholder={placeholder}
          className={baseClassName}
        />
      );
    case 'password':
      return (
        <input
          type="password"
          value={String(value)}
          onChange={(e) => onChange(e.target.value)}
          placeholder={placeholder}
          className={baseClassName}
        />
      );
    case 'select':
      return (
        <Select
          value={String(value)}
          onChange={(val) => onChange(val)}
          options={getFieldOptions(t, schemaId, field)}
        />
      );
    case 'checkbox':
      return (
        <input
          type="checkbox"
          checked={Boolean(value)}
          onChange={(e) => onChange(e.target.checked)}
          className="rounded border-zinc-300 dark:border-zinc-700"
        />
      );
    case 'number':
      return (
        <input
          type="number"
          value={String(value)}
          onChange={(e) => onChange(e.target.value)}
          placeholder={placeholder}
          className={baseClassName}
        />
      );
    default:
      return null;
  }
}

export function ChannelsView() {
  const { t } = useI18n();
  const [state, setState] = useState<ChannelsState | null>(null);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const [isModalOpen, setIsModalOpen] = useState(false);
  const [editingId, setEditingId] = useState('');
  const [draft, setDraft] = useState<ChannelDraft>(EMPTY_DRAFT);
  const [deleteTarget, setDeleteTarget] = useState<{ id: string; name: string } | null>(null);

  const loadState = async () => {
    setError('');
    try {
      const next = await apiGet<ChannelsState>('/api/integrations/channels');
      setState(next);
    } catch (err) {
      setError(err instanceof Error ? err.message : t('channels.loadError'));
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    void loadState();
  }, []);

  const persistChannels = async (entries: Record<string, ChannelEntry>) => {
    if (!state) return;
    setSaving(true);
    setError('');
    try {
      const next = await apiPost<ChannelsState>('/api/integrations/channels/save', {
        channels: {
          ...state.config,
          entries,
        },
      });
      setState(next);
    } catch (err) {
      setError(err instanceof Error ? err.message : t('channels.saveError'));
    } finally {
      setSaving(false);
    }
  };

  const currentSchema = useMemo(() => {
    return state?.schemas.find((s) => s.id === draft.type);
  }, [state?.schemas, draft.type]);

  const generateChannelName = (type: string) => {
    const rand = String(Math.floor(Math.random() * 100)).padStart(2, '0');
    return `${type}${rand}`;
  };

  const openCreateModal = () => {
    setEditingId('');
    const defaultSchema = state?.schemas.find((s) => s.id === 'weixin') || state?.schemas[0];
    const defaultType = defaultSchema?.id || 'weixin';
    const newDraft = { ...EMPTY_DRAFT, type: defaultType, id: generateChannelName(defaultType) };
    setDraft(newDraft);
    setIsModalOpen(true);
  };

  const openEditModal = (id: string, entry: ChannelEntry) => {
    setEditingId(id);
    const driver = readChannelDriver(entry);
    const schema = state?.schemas.find((s) => s.id === driver);
    setDraft(entryToDraft(id, entry, schema));
    setIsModalOpen(true);
  };

  const handleTypeChange = (newType: string) => {
    const schema = state?.schemas.find((s) => s.id === newType);
    const newFields: Record<string, string | boolean> = {};

    schema?.fields.forEach((field) => {
      newFields[field.key] = field.type === 'checkbox' ? false : (field.defaultValue || '');
    });

    setDraft((current) => ({
      ...current,
      type: newType,
      // 新增时自动生成渠道名称
      ...(!editingId ? { id: generateChannelName(newType) } : {}),
      fields: newFields,
    }));
  };

  const handleSaveChannel = async () => {
    if (!state) return;
    const channelID = draft.id.trim();
    if (!channelID) return;

    // 验证必填字段
    const schema = currentSchema;
    if (schema) {
      const missingRequired = schema.fields.some(
        (f) => f.required && !draft.fields[f.key]
      );
      if (missingRequired) {
        setError(t('channels.requiredFieldsError'));
        return;
      }
    }

    const entries: Record<string, ChannelEntry> = {
      ...((state.config.entries || {}) as Record<string, ChannelEntry>),
    };

    if (editingId && editingId !== channelID) {
      delete entries[editingId];
    }

    entries[channelID] = draftToEntry(draft, schema);
    await persistChannels(entries);
    setIsModalOpen(false);
    setEditingId('');
    setDraft({ ...EMPTY_DRAFT });
  };

  const deleteChannel = async () => {
    if (!state || !deleteTarget) return;
    const entries: Record<string, ChannelEntry> = {
      ...((state.config.entries || {}) as Record<string, ChannelEntry>),
    };
    delete entries[deleteTarget.id];
    setDeleteTarget(null);
    await persistChannels(entries);
  };

  const channels = useMemo(
    () =>
      Object.entries(state?.config.entries || {}).map(([id, entry]) => {
        const integration = (state?.integrations || []).find((item) => item.id === id);
        const driver = readChannelDriver(entry);
        const schema = state?.schemas.find((s) => s.id === driver);
        const defaultAccount = String(entry.defaultAccount || 'main').trim() || 'main';
        const account = (entry.accounts?.[defaultAccount] || {}) as Record<string, unknown>;

        // 构建详细信息显示
        let detail = entry.defaultTo || t('channels.notConfigured');
        if (schema) {
          const displayFields = schema.fields.filter((f) => f.group === 'account' || f.key === 'defaultTo');
          if (displayFields.length > 0) {
            const values = displayFields.map((f) => {
              const val = account[f.key] || entry.config?.[f.key] || entry.defaultTo;
              return val ? String(val).substring(0, 20) : '';
            }).filter(Boolean);
            detail = values.join(' · ') || t('channels.notConfigured');
          }
        }

        return {
          id,
          entry,
          driver,
          schemaName: schema ? getSchemaName(t, schema) : driver,
          status: integration?.enabled || entry.enabled ? 'active' : 'inactive',
          detail,
        };
      }),
    [state, t]
  );

  const canSave = draft.id.trim() && currentSchema?.fields.every(
    (f) => !f.required || draft.fields[f.key]
  );

  return (
    <div className="flex flex-col h-full bg-white dark:bg-zinc-950 text-zinc-900 dark:text-zinc-100 transition-colors">
      <PageHeader title={t('channels.title')} description={t('channels.desc')}>
        <button
          onClick={() => void loadState()}
          className="rounded-lg border border-zinc-200 px-3 py-2 text-sm font-medium text-zinc-700 transition-colors hover:bg-zinc-100 dark:border-zinc-800 dark:text-zinc-200 dark:hover:bg-zinc-800"
        >
          <RefreshCw className="h-4 w-4" />
        </button>
      </PageHeader>

      <div className="flex-1 overflow-y-auto p-6">
        <ErrorAlert message={error} className="mb-4" />
        <div className="space-y-6">
          <div className="flex justify-between items-center">
            <h3 className="text-lg font-medium text-zinc-900 dark:text-zinc-100">
              {t('channels.connected')}
            </h3>
            <button
              onClick={openCreateModal}
              className="flex items-center gap-2 whitespace-nowrap bg-indigo-600 hover:bg-indigo-500 text-white px-3 py-1.5 rounded-lg text-sm font-medium transition-colors"
            >
              <Plus className="w-4 h-4" />
              {t('channels.addChannel')}
            </button>
          </div>

          {loading ? (
            <div className="rounded-xl border border-zinc-200 bg-white p-6 text-sm text-zinc-500 shadow-sm dark:border-zinc-800 dark:bg-zinc-900 dark:text-zinc-400">
              {t('channels.loading')}
            </div>
          ) : (
            <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
              {channels.map((channel) => (
                <div
                  key={channel.id}
                  className={`bg-white dark:bg-zinc-900 border border-zinc-200 dark:border-zinc-800 rounded-xl p-5 flex flex-col gap-4 shadow-sm ${channel.status === 'inactive' ? 'opacity-60' : ''
                    }`}
                >
                  <div className="flex items-start gap-4 min-w-0">
                    <div className="w-10 h-10 flex-shrink-0 rounded-lg flex items-center justify-center bg-indigo-500/10 text-indigo-600 dark:text-indigo-300">
                      <MessageSquare className="w-5 h-5" />
                    </div>
                    <div className="min-w-0 flex-1">
                      <h4 className="font-medium text-zinc-900 dark:text-zinc-100 truncate">{channel.id}</h4>
                      <p className="text-sm text-zinc-500 dark:text-zinc-400 break-words line-clamp-2">{channel.detail}</p>
                      <p className="mt-1 text-xs uppercase tracking-wider text-zinc-400">
                        {channel.schemaName}
                      </p>
                    </div>
                  </div>
                  <div className="flex items-center justify-between gap-2 pt-2 border-t border-zinc-100 dark:border-zinc-800">
                    {channel.status === 'active' ? (
                      <span className="flex items-center gap-1 text-xs font-medium text-emerald-600 dark:text-emerald-400 bg-emerald-500/10 dark:bg-emerald-400/10 px-2 py-1 rounded-full whitespace-nowrap">
                        <div className="w-1.5 h-1.5 rounded-full bg-emerald-500 dark:bg-emerald-400" />
                        {t('channels.active')}
                      </span>
                    ) : (
                      <span className="text-xs text-zinc-500 dark:text-zinc-400 font-medium whitespace-nowrap">
                        {t('cap.disabled')}
                      </span>
                    )}
                    <div className="flex items-center gap-1 flex-shrink-0">
                      <button
                        onClick={() => openEditModal(channel.id, channel.entry)}
                        className="p-1.5 text-zinc-400 hover:text-indigo-600 dark:hover:text-indigo-400 transition-colors rounded-md hover:bg-zinc-100 dark:hover:bg-zinc-800"
                        title={t('common.edit')}
                      >
                        <Pencil className="w-4 h-4" />
                      </button>
                      <button
                        onClick={() => setDeleteTarget({ id: channel.id, name: channel.id })}
                        className="p-1.5 text-zinc-400 hover:text-rose-600 dark:hover:text-rose-400 transition-colors rounded-md hover:bg-zinc-100 dark:hover:bg-zinc-800"
                        title={t('common.delete')}
                      >
                        <Trash2 className="w-4 h-4" />
                      </button>
                    </div>
                  </div>
                </div>
              ))}
            </div>
          )}
        </div>
      </div>

      <Modal
        isOpen={isModalOpen}
        onClose={() => setIsModalOpen(false)}
        title={editingId ? t('channels.editChannel') : t('channels.addChannel')}
      >
        <div className="space-y-4">
          {/* 渠道类型和名称 */}
          <div className="grid grid-cols-2 gap-4">
            <div>
              <label className="block text-sm font-medium text-zinc-700 dark:text-zinc-300 mb-1">
                {t('channels.channelType')}
              </label>
              <Select
                value={draft.type}
                onChange={handleTypeChange}
                options={(state?.schemas || []).map((s) => ({
                  value: s.id,
                  label: getSchemaName(t, s),
                }))}
              />
              {currentSchema?.description && (
                <p className="mt-1 text-xs text-zinc-500 dark:text-zinc-400">
                  {getSchemaDescription(t, currentSchema)}
                </p>
              )}
            </div>
            <div>
              <label className="block text-sm font-medium text-zinc-700 dark:text-zinc-300 mb-1">
                {t('channels.channelName')}
              </label>
              <input
                type="text"
                value={draft.id}
                onChange={(e) => setDraft((current) => ({ ...current, id: e.target.value }))}
                placeholder={t('channels.channelNamePlaceholder')}
                className="w-full bg-zinc-50 dark:bg-zinc-950 border border-zinc-200 dark:border-zinc-800 rounded-lg p-2.5 text-sm text-zinc-900 dark:text-zinc-200 focus:border-indigo-500 focus:ring-1 focus:ring-indigo-500 transition-all outline-none"
              />
            </div>
          </div>

          {/* Agent和启用状态 - 仅编辑时可见 */}
          {editingId && (
            <div className="grid grid-cols-2 gap-4">
              <div>
                <label className="block text-sm font-medium text-zinc-700 dark:text-zinc-300 mb-1">
                  {t('channels.agent')}
                </label>
                <input
                  type="text"
                  value={draft.agent}
                  onChange={(e) => setDraft((current) => ({ ...current, agent: e.target.value }))}
                  placeholder={t('channels.agentPlaceholder')}
                  className="w-full bg-zinc-50 dark:bg-zinc-950 border border-zinc-200 dark:border-zinc-800 rounded-lg p-2.5 text-sm text-zinc-900 dark:text-zinc-200 focus:border-indigo-500 focus:ring-1 focus:ring-indigo-500 transition-all outline-none"
                />
              </div>
              <label className="flex items-center gap-2 text-sm font-medium text-zinc-700 dark:text-zinc-300 pt-8">
                <input
                  type="checkbox"
                  checked={draft.enabled}
                  onChange={(e) => setDraft((current) => ({ ...current, enabled: e.target.checked }))}
                />
                {t('channels.enabled')}
              </label>
            </div>
          )}

          {/* 微信扫码登录 */}
          {draft.type === 'weixin' && !editingId && (
            <WeixinQRLogin
              baseUrl={String(draft.fields['baseUrl'] || '')}
              autoStart
              onLoginSuccess={(token, baseUrl) => {
                setDraft((current) => ({
                  ...current,
                  fields: {
                    ...current.fields,
                    token,
                    ...(baseUrl ? { baseUrl } : {}),
                  },
                }));
              }}
            />
          )}

          {/* 动态渲染schema字段 */}
          {currentSchema?.fields
            .filter((field) => {
              // 新增微信渠道时，隐藏 token/baseUrl/pollTimeoutSeconds（由扫码自动填入）
              if (!editingId && draft.type === 'weixin') {
                const hiddenOnCreate = ['token', 'baseUrl', 'pollTimeoutSeconds'];
                if (hiddenOnCreate.includes(field.key)) return false;
              }
              return true;
            })
            .map((field) => (
              <div key={field.key}>
                <label className="block text-sm font-medium text-zinc-700 dark:text-zinc-300 mb-1">
                  {getFieldLabel(t, currentSchema.id, field)}
                  {field.required && <span className="text-rose-500 ml-1">*</span>}
                </label>
                <DynamicField
                  schemaId={currentSchema.id}
                  field={field}
                  value={draft.fields[field.key] ?? (field.type === 'checkbox' ? false : field.defaultValue || '')}
                  onChange={(value) =>
                    setDraft((current) => ({
                      ...current,
                      fields: { ...current.fields, [field.key]: value },
                    }))
                  }
                />
                {getFieldHelp(t, currentSchema.id, field) && (
                  <p className="mt-1 text-xs text-zinc-500 dark:text-zinc-400">{getFieldHelp(t, currentSchema.id, field)}</p>
                )}
              </div>
            ))}

          {/* 保存按钮 */}
          <div className="pt-4 flex justify-end gap-2">
            <button
              onClick={() => setIsModalOpen(false)}
              className="px-4 py-2 text-sm font-medium text-zinc-700 dark:text-zinc-300 hover:bg-zinc-100 dark:hover:bg-zinc-800 rounded-lg transition-colors"
            >
              {t('common.cancel')}
            </button>
            <button
              onClick={() => void handleSaveChannel()}
              disabled={!canSave || saving}
              className="px-4 py-2 text-sm font-medium text-white bg-indigo-600 hover:bg-indigo-500 rounded-lg transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
            >
              {saving ? t('common.saving') : editingId ? t('common.save') : t('channels.addChannel')}
            </button>
          </div>
        </div>
      </Modal>

      <ConfirmDialog
        isOpen={deleteTarget !== null}
        onClose={() => setDeleteTarget(null)}
        onConfirm={() => void deleteChannel()}
        title={t('common.deleteConfirmTitle')}
        message={t('common.deleteConfirmMessage')}
        confirmText={t('common.delete')}
        loading={saving}
      />
    </div>
  );
}
