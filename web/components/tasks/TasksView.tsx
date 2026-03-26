'use client';

import { useEffect, useMemo, useState } from 'react';
import { Calendar, Loader2, Pencil, Plus, RefreshCw, Trash2, XCircle } from 'lucide-react';
import { useI18n } from '@/lib/i18n/I18nContext';
import { ErrorAlert, Modal, PageHeader, Select, ConfirmDialog } from '@/components/ui';
import { apiGet, apiPost, type TaskRecord } from '@/lib/api';
import { DEFAULT_SESSION_KEY, DEFAULT_CHANNEL, DEFAULT_TO } from '@/lib/constants';

type TasksResponse = {
  tasks: TaskRecord[];
};

type TaskFormState = {
  id?: string;
  title: string;
  message: string;
  taskType: 'reminder' | 'background';
  scheduleKind: 'at' | 'every' | 'cron';
  runAt: string;
  everySeconds: string;
  cronExpr: string;
  timezone: string;
  wakeMode: 'now' | 'next-heartbeat';
};


function sortTasks(tasks: TaskRecord[]): TaskRecord[] {
  return [...tasks].sort((a, b) => {
    const left = a.updatedAt || a.createdAt || '';
    const right = b.updatedAt || b.createdAt || '';
    return right.localeCompare(left);
  });
}

function formatWhen(value: string | undefined, t: (key: any, params?: Record<string, string>) => string): string {
  if (!value) return t('tasks.immediately');
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleString();
}

function formatSchedule(task: TaskRecord, t: (key: any, params?: Record<string, string>) => string): string {
  switch ((task.scheduleKind || '').trim()) {
    case 'every': {
      const seconds = task.scheduleEveryMs ? Math.max(1, Math.round(task.scheduleEveryMs / 1000)) : task.intervalSeconds || 0;
      return seconds > 0 ? t('tasks.everySecondsLabel', { seconds: String(seconds) }) : t('tasks.recurring');
    }
    case 'cron':
      return task.scheduleExpr
        ? t('tasks.cronLabel', { expr: task.scheduleExpr, timezone: task.scheduleTz ? ` (${task.scheduleTz})` : '' })
        : t('tasks.cronShort');
    case 'at':
    default:
      return formatWhen(task.nextRunAt || task.scheduleAt, t);
  }
}

function toDateTimeLocal(value?: string): string {
  if (!value) return '';
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return '';
  const local = new Date(date.getTime() - date.getTimezoneOffset() * 60000);
  return local.toISOString().slice(0, 16);
}

function taskToForm(task?: TaskRecord): TaskFormState {
  if (!task) {
    return {
      title: '',
      message: '',
      taskType: 'reminder',
      scheduleKind: 'at',
      runAt: '',
      everySeconds: '',
      cronExpr: '',
      timezone: Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC',
      wakeMode: 'next-heartbeat',
    };
  }
  const taskType =
    task.payloadKind === 'systemEvent' && task.sessionTarget === 'main' ? 'reminder' : 'background';
  const scheduleKind = (task.scheduleKind === 'every' || task.scheduleKind === 'cron' ? task.scheduleKind : 'at') as 'at' | 'every' | 'cron';
  return {
    id: task.id,
    title: task.title || '',
    message: task.message || '',
    taskType,
    scheduleKind,
    runAt: toDateTimeLocal(task.scheduleAt || task.nextRunAt),
    everySeconds: task.scheduleEveryMs ? String(Math.max(1, Math.round(task.scheduleEveryMs / 1000))) : task.intervalSeconds ? String(task.intervalSeconds) : '',
    cronExpr: task.scheduleExpr || '',
    timezone: task.scheduleTz || Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC',
    wakeMode: task.wakeMode === 'now' ? 'now' : 'next-heartbeat',
  };
}

export function TasksView() {
  const { t } = useI18n();
  const [tasks, setTasks] = useState<TaskRecord[]>([]);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const [modalMode, setModalMode] = useState<'create' | 'edit' | null>(null);
  const [form, setForm] = useState<TaskFormState>(taskToForm());
  const [deleteTarget, setDeleteTarget] = useState<TaskRecord | null>(null);

  const isModalOpen = modalMode !== null;
  const isEditing = modalMode === 'edit';

  const scheduledTasks = useMemo(() => sortTasks(tasks), [tasks]);

  const loadTasks = async () => {
    setError('');
    try {
      const response = await apiGet<TasksResponse>('/api/workspace/tasks');
      setTasks(sortTasks(response.tasks || []));
    } catch (err) {
      setError(err instanceof Error ? err.message : t('tasks.loadError'));
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    void loadTasks();
  }, []);

  const openCreate = () => {
    setForm(taskToForm());
    setModalMode('create');
  };

  const openEdit = (task: TaskRecord) => {
    setForm(taskToForm(task));
    setModalMode('edit');
  };

  const closeModal = () => {
    setModalMode(null);
    setForm(taskToForm());
  };

  const updateForm = (patch: Partial<TaskFormState>) => {
    setForm((current) => ({ ...current, ...patch }));
  };

  const buildTaskPayload = () => ({
    id: form.id,
    title: form.title.trim(),
    message: form.message.trim(),
    sessionKey: DEFAULT_SESSION_KEY,
    channel: DEFAULT_CHANNEL,
    to: DEFAULT_TO,
    deliver: true,
    deliveryMode: 'announce',
    payloadKind: form.taskType === 'reminder' ? 'systemEvent' : 'agentTurn',
    sessionTarget: form.taskType === 'reminder' ? 'main' : 'isolated',
    wakeMode: form.taskType === 'reminder' ? form.wakeMode : 'now',
    scheduleKind: form.scheduleKind,
    scheduleAt: form.scheduleKind === 'at' && form.runAt ? new Date(form.runAt).toISOString() : undefined,
    scheduleEveryMs: form.scheduleKind === 'every' && form.everySeconds ? Number(form.everySeconds) * 1000 : 0,
    scheduleExpr: form.scheduleKind === 'cron' ? form.cronExpr.trim() : '',
    scheduleTz: form.scheduleKind === 'cron' ? form.timezone.trim() : '',
  });

  const handleSave = async () => {
    if (!form.message.trim()) return;
    setSaving(true);
    setError('');
    try {
      if (isEditing && form.id) {
        await apiPost<TaskRecord>('/api/workspace/tasks/update', buildTaskPayload());
      } else {
        await apiPost<TaskRecord>('/api/workspace/tasks', buildTaskPayload());
      }
      closeModal();
      await loadTasks();
    } catch (err) {
      setError(err instanceof Error ? err.message : t('tasks.saveError'));
    } finally {
      setSaving(false);
    }
  };

  const handleCancel = async (id: string) => {
    try {
      await apiPost<TaskRecord>('/api/workspace/tasks/cancel', { id });
      await loadTasks();
    } catch (err) {
      setError(err instanceof Error ? err.message : t('tasks.cancelError'));
    }
  };

  const handleDelete = async () => {
    if (!deleteTarget) return;
    setSaving(true);
    try {
      await apiPost<TaskRecord>('/api/workspace/tasks/delete', { id: deleteTarget.id });
      setDeleteTarget(null);
      await loadTasks();
    } catch (err) {
      setError(err instanceof Error ? err.message : t('tasks.deleteError'));
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="flex h-full flex-col bg-white text-zinc-900 transition-colors dark:bg-zinc-950 dark:text-zinc-100">
      <PageHeader title={t('tasks.title')} description={t('tasks.desc')}>
        <button
          onClick={() => void loadTasks()}
          className="rounded-lg border border-zinc-200 px-3 py-2 text-sm font-medium text-zinc-700 transition-colors hover:bg-zinc-100 dark:border-zinc-800 dark:text-zinc-200 dark:hover:bg-zinc-800"
        >
          <RefreshCw className="h-4 w-4" />
        </button>
        <button
          onClick={openCreate}
          className="flex items-center gap-2 whitespace-nowrap rounded-lg bg-indigo-600 px-3 py-2 text-sm font-medium text-white transition-colors hover:bg-indigo-500"
        >
          <Plus className="h-4 w-4" />
          {t('tasks.createTask')}
        </button>
      </PageHeader>

      <div className="flex-1 overflow-y-auto p-6">
        <ErrorAlert message={error} className="mb-4" />

        {loading ? (
          <div className="flex h-full items-center justify-center text-zinc-500 dark:text-zinc-400">
            <Loader2 className="mr-2 h-5 w-5 animate-spin" />
            {t('tasks.loading')}
          </div>
        ) : scheduledTasks.length === 0 ? (
          <div className="rounded-2xl border border-zinc-200 bg-white p-6 text-center shadow-sm dark:border-zinc-800 dark:bg-zinc-900">
            <Calendar className="mx-auto mb-4 h-12 w-12 text-zinc-400 dark:text-zinc-600" />
            <h3 className="mb-2 text-lg font-medium text-zinc-900 dark:text-zinc-100">{t('tasks.noTasks')}</h3>
            <p className="text-sm text-zinc-500 dark:text-zinc-400">{t('tasks.noTasksDesc')}</p>
          </div>
        ) : (
          <div className="space-y-4">
            {scheduledTasks.map((task) => (
              <div key={task.id} className="rounded-2xl border border-zinc-200 bg-white p-5 shadow-sm dark:border-zinc-800 dark:bg-zinc-900">
                <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between sm:gap-4">
                  <div className="min-w-0 space-y-2">
                    <div className="flex items-center gap-2">
                      <span className="rounded-full bg-zinc-100 px-2 py-1 text-xs font-medium uppercase tracking-wide text-zinc-600 dark:bg-zinc-800 dark:text-zinc-300">
                        {task.status}
                      </span>
                      <span className="text-xs text-zinc-500 dark:text-zinc-400">{task.payloadKind === 'systemEvent' ? t('tasks.reminder') : t('tasks.backgroundRun')}</span>
                    </div>
                    <h3 className="text-lg font-medium text-zinc-900 dark:text-zinc-100">{task.title || t('tasks.untitled')}</h3>
                    <p className="text-sm text-zinc-600 dark:text-zinc-300 line-clamp-2" title={task.message}>{task.message}</p>
                    <div className="space-y-1 text-xs text-zinc-500 dark:text-zinc-400">
                      <p>{t('tasks.schedule')}: {formatSchedule(task, t)}</p>
                      {task.channel || task.to ? <p>{t('tasks.target')}: {task.channel || t('common.unknown')} / {task.to || t('common.default')}</p> : null}
                      {task.lastError ? <p className="text-rose-500 dark:text-rose-300">{t('tasks.lastError')}: {task.lastError}</p> : null}
                    </div>
                  </div>
                  <div className="flex shrink-0 items-center gap-2 flex-wrap">
                    <button
                      onClick={() => openEdit(task)}
                      className="flex items-center gap-1 whitespace-nowrap rounded-lg border border-zinc-200 px-3 py-2 text-sm font-medium text-zinc-700 transition-colors hover:bg-zinc-50 dark:border-zinc-800 dark:text-zinc-200 dark:hover:bg-zinc-800"
                    >
                      <Pencil className="h-4 w-4" />
                      {t('common.edit')}
                    </button>
                    {task.status === 'scheduled' || task.status === 'queued' || task.status === 'running' ? (
                      <button
                        onClick={() => void handleCancel(task.id)}
                        className="flex items-center gap-1 whitespace-nowrap rounded-lg border border-rose-200 px-3 py-2 text-sm font-medium text-rose-600 transition-colors hover:bg-rose-50 dark:border-rose-900/40 dark:text-rose-300 dark:hover:bg-rose-950/20"
                      >
                        <XCircle className="h-4 w-4" />
                        {t('common.cancel')}
                      </button>
                    ) : null}
                    <button
                      onClick={() => setDeleteTarget(task)}
                      className="flex items-center gap-1 whitespace-nowrap rounded-lg border border-zinc-200 px-3 py-2 text-sm font-medium text-zinc-700 transition-colors hover:bg-zinc-50 dark:border-zinc-800 dark:text-zinc-200 dark:hover:bg-zinc-800"
                    >
                      <Trash2 className="h-4 w-4" />
                      {t('common.delete')}
                    </button>
                  </div>
                </div>
              </div>
            ))}
          </div>
        )}
      </div>

      <Modal isOpen={isModalOpen} onClose={closeModal} title={isEditing ? t('tasks.editTask') : t('tasks.createTask')}>
        <div className="space-y-4">
          <div>
            <label className="mb-1 block text-sm font-medium text-zinc-700 dark:text-zinc-300">{t('tasks.titleField')}</label>
            <input
              value={form.title}
              onChange={(e) => updateForm({ title: e.target.value })}
              className="w-full rounded-lg border border-zinc-200 bg-zinc-50 px-3 py-2.5 text-sm text-zinc-900 outline-none transition-all focus:border-indigo-500 focus:ring-1 focus:ring-indigo-500 dark:border-zinc-800 dark:bg-zinc-950 dark:text-zinc-200"
              placeholder={t('tasks.reminder')}
            />
          </div>
          <div>
            <label className="mb-1 block text-sm font-medium text-zinc-700 dark:text-zinc-300">{t('tasks.message')}</label>
            <textarea
              value={form.message}
              onChange={(e) => updateForm({ message: e.target.value })}
              className="h-28 w-full rounded-lg border border-zinc-200 bg-zinc-50 px-3 py-2.5 text-sm text-zinc-900 outline-none transition-all focus:border-indigo-500 focus:ring-1 focus:ring-indigo-500 dark:border-zinc-800 dark:bg-zinc-950 dark:text-zinc-200"
              placeholder={t('tasks.message')}
            />
          </div>
          <div>
            <label className="mb-1 block text-sm font-medium text-zinc-700 dark:text-zinc-300">{t('tasks.taskType')}</label>
            <Select
              value={form.taskType}
              onChange={(val) => updateForm({ taskType: val as 'reminder' | 'background' })}
              options={[
                { value: 'reminder', label: t('tasks.reminderType') },
                { value: 'background', label: t('tasks.backgroundType') },
              ]}
            />
          </div>
          <div>
            <label className="mb-1 block text-sm font-medium text-zinc-700 dark:text-zinc-300">{t('tasks.scheduleType')}</label>
            <Select
              value={form.scheduleKind}
              onChange={(val) => updateForm({ scheduleKind: val as 'at' | 'every' | 'cron' })}
              options={[
                { value: 'at', label: t('tasks.runOnce') },
                { value: 'every', label: t('tasks.repeatEvery') },
                { value: 'cron', label: t('tasks.cronExpr') },
              ]}
            />
          </div>
          {form.scheduleKind === 'at' ? (
            <div>
              <label className="mb-1 block text-sm font-medium text-zinc-700 dark:text-zinc-300">{t('tasks.runAt')}</label>
              <input
                type="datetime-local"
                value={form.runAt}
                onChange={(e) => updateForm({ runAt: e.target.value })}
                className="w-full rounded-lg border border-zinc-200 bg-zinc-50 px-3 py-2.5 text-sm text-zinc-900 outline-none transition-all focus:border-indigo-500 focus:ring-1 focus:ring-indigo-500 dark:border-zinc-800 dark:bg-zinc-950 dark:text-zinc-200"
              />
            </div>
          ) : null}
          {form.scheduleKind === 'every' ? (
            <div>
              <label className="mb-1 block text-sm font-medium text-zinc-700 dark:text-zinc-300">{t('tasks.repeatSeconds')}</label>
              <input
                type="number"
                min="1"
                value={form.everySeconds}
                onChange={(e) => updateForm({ everySeconds: e.target.value })}
                className="w-full rounded-lg border border-zinc-200 bg-zinc-50 px-3 py-2.5 text-sm text-zinc-900 outline-none transition-all focus:border-indigo-500 focus:ring-1 focus:ring-indigo-500 dark:border-zinc-800 dark:bg-zinc-950 dark:text-zinc-200"
                placeholder={t('tasks.repeatSecondsPlaceholder')}
              />
            </div>
          ) : null}
          {form.scheduleKind === 'cron' ? (
            <>
              <div>
                <label className="mb-1 block text-sm font-medium text-zinc-700 dark:text-zinc-300">{t('tasks.cronExpr')}</label>
                <input
                  value={form.cronExpr}
                  onChange={(e) => updateForm({ cronExpr: e.target.value })}
                  className="w-full rounded-lg border border-zinc-200 bg-zinc-50 px-3 py-2.5 text-sm text-zinc-900 outline-none transition-all focus:border-indigo-500 focus:ring-1 focus:ring-indigo-500 dark:border-zinc-800 dark:bg-zinc-950 dark:text-zinc-200"
                  placeholder={t('tasks.cronPlaceholder')}
                />
              </div>
              <div>
                <label className="mb-1 block text-sm font-medium text-zinc-700 dark:text-zinc-300">{t('tasks.timezone')}</label>
                <input
                  value={form.timezone}
                  onChange={(e) => updateForm({ timezone: e.target.value })}
                  className="w-full rounded-lg border border-zinc-200 bg-zinc-50 px-3 py-2.5 text-sm text-zinc-900 outline-none transition-all focus:border-indigo-500 focus:ring-1 focus:ring-indigo-500 dark:border-zinc-800 dark:bg-zinc-950 dark:text-zinc-200"
                  placeholder={t('tasks.timezonePlaceholder')}
                />
              </div>
            </>
          ) : null}
          {form.taskType === 'reminder' ? (
            <div>
              <label className="mb-1 block text-sm font-medium text-zinc-700 dark:text-zinc-300">{t('tasks.wakeMode')}</label>
              <Select
                value={form.wakeMode}
                onChange={(val) => updateForm({ wakeMode: val as 'now' | 'next-heartbeat' })}
                options={[
                  { value: 'next-heartbeat', label: t('tasks.nextHeartbeat') },
                  { value: 'now', label: t('tasks.wakeImmediately') },
                ]}
              />
            </div>
          ) : null}
          <div className="flex justify-end gap-2 pt-4">
            <button
              onClick={closeModal}
              className="rounded-lg px-4 py-2 text-sm font-medium text-zinc-700 transition-colors hover:bg-zinc-100 dark:text-zinc-300 dark:hover:bg-zinc-800"
            >
              {t('common.cancel')}
            </button>
            <button
              onClick={() => void handleSave()}
              disabled={!form.message.trim() || saving}
              className="rounded-lg bg-indigo-600 px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-indigo-500 disabled:cursor-not-allowed disabled:opacity-50"
            >
              {saving ? t('common.saving') : isEditing ? t('tasks.update') : t('tasks.create')}
            </button>
          </div>
        </div>
      </Modal>

      <ConfirmDialog
        isOpen={deleteTarget !== null}
        onClose={() => setDeleteTarget(null)}
        onConfirm={() => void handleDelete()}
        title={t('common.deleteConfirmTitle')}
        message={t('common.deleteConfirmMessage')}
        confirmText={t('common.delete')}
        loading={saving}
      />
    </div>
  );
}
