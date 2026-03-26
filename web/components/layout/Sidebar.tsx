'use client';

import { useEffect, useState } from 'react';
import Link from 'next/link';
import { usePathname } from 'next/navigation';
import {
  MessageSquare,
  CheckSquare,
  Brain,
  Zap,
  Send,
  Settings,
  PanelLeftClose,
  PanelLeftOpen,
} from 'lucide-react';
import { useI18n } from '@/lib/i18n/I18nContext';
import { cn } from '@/lib/utils';
import { apiGet, type DashboardSnapshot } from '@/lib/api';

type StatusType = 'online' | 'offline' | 'loading';

type SystemStatus = {
  gateway: StatusType;
  brain: StatusType;
  brainLabel: string;
  brainMode: string;
  cerebellum: StatusType;
  model: StatusType;
};

export function Sidebar() {
  const { t } = useI18n();
  const pathname = usePathname();
  const [collapsed, setCollapsed] = useState(false);
  const [status, setStatus] = useState<SystemStatus>({
    gateway: 'loading',
    brain: 'loading',
    brainLabel: '',
    brainMode: 'cloud',
    cerebellum: 'loading',
    model: 'loading',
  });

  const groups = [
    {
      labelKey: 'sidebar.groupWorkspace',
      items: [
        { id: 'chat', href: '/chat', labelKey: 'sidebar.chat', icon: MessageSquare },
        { id: 'tasks', href: '/tasks', labelKey: 'sidebar.tasks', icon: CheckSquare },
      ],
    },
    {
      labelKey: 'sidebar.groupAgent',
      items: [
        { id: 'capabilities', href: '/capabilities', labelKey: 'sidebar.capabilities', icon: Zap },
        { id: 'brain', href: '/brain', labelKey: 'sidebar.models', icon: Brain },
        { id: 'channels', href: '/channels', labelKey: 'sidebar.channels', icon: Send },
      ],
    },
  ] as const;

  useEffect(() => {
    let cancelled = false;

    const fetchStatus = async () => {
      try {
        const data = await apiGet<DashboardSnapshot>('/api/system/dashboard');
        if (cancelled) return;

        const providers = data?.providers || [];
        const hasReadyProvider = providers.some(p => p.ready);

        // Brain status: in local mode check brainLocalStatus, in cloud mode check providers
        const brainMode = data?.brainMode || 'cloud';
        const isLocal = brainMode === 'local';
        const brainLocalRunning = data?.brainLocalStatus === 'running';
        const cerebellumRunning = data?.cerebellumStatus === 'running';

        let brainStatus: StatusType;
        if (isLocal) {
          brainStatus = brainLocalRunning ? 'online' : 'offline';
        } else {
          brainStatus = hasReadyProvider ? 'online' : providers.length > 0 ? 'offline' : 'offline';
        }

        setStatus({
          gateway: data?.runtime?.healthy ? 'online' : 'offline',
          brain: brainStatus,
          brainLabel: isLocal ? t('sidebar.brainLocal' as any) : t('sidebar.brainCloud' as any),
          brainMode: brainMode,
          cerebellum: cerebellumRunning ? 'online' : 'offline',
          model: hasReadyProvider ? 'online' : providers.length > 0 ? 'offline' : 'offline',
        });
      } catch {
        if (cancelled) return;
        setStatus({
          gateway: 'offline',
          brain: 'offline',
          brainLabel: '',
          brainMode: 'cloud',
          cerebellum: 'offline',
          model: 'offline',
        });
      }
    };

    void fetchStatus();
    const interval = setInterval(fetchStatus, 10000);

    return () => {
      cancelled = true;
      clearInterval(interval);
    };
  }, [t]);

  const statusColors: Record<StatusType, string> = {
    online: 'bg-emerald-500',
    offline: 'bg-zinc-400',
    loading: 'bg-amber-500 animate-pulse',
  };

  const statusDots = status.brainMode === 'local'
    ? [{ key: 'brain' as const, labelKey: 'sidebar.brainStatus' as const }]
    : [
      { key: 'brain' as const, labelKey: 'sidebar.brainStatus' as const },
      { key: 'cerebellum' as const, labelKey: 'sidebar.cerebellumStatus' as const },
    ];

  // Aggregate status: in local mode only brain matters; in cloud mode
  // it's the worst of brain + cerebellum.
  const overallStatus: StatusType =
    status.brainMode === 'local'
      ? status.brain
      : status.brain === 'offline' || status.cerebellum === 'offline'
        ? 'offline'
        : status.brain === 'loading' || status.cerebellum === 'loading'
          ? 'loading'
          : 'online';

  return (
    <div
      className={cn(
        'flex h-full flex-col border-r border-zinc-200 bg-zinc-50 text-zinc-600 transition-[width] duration-200 ease-in-out dark:border-zinc-800 dark:bg-zinc-950 dark:text-zinc-300',
        collapsed ? 'w-14' : 'w-[9.5rem]',
      )}
    >
      {/* Logo row */}
      <div className={cn('flex h-14 items-center border-b border-zinc-200/60 dark:border-zinc-800/60', collapsed ? 'justify-center px-0' : 'gap-2 px-2')}>
        {!collapsed && (
          <img src="/logo.svg" alt="Logo" className="h-10 w-10 flex-shrink-0" />
        )}
        {!collapsed && (
          <span className="flex-1 text-sm font-semibold tracking-tight text-zinc-900 dark:text-zinc-100">
            {t('sidebar.title')}
          </span>
        )}
        <button
          onClick={() => setCollapsed((c) => !c)}
          className="rounded-md p-1.5 text-zinc-400 transition-colors hover:bg-zinc-200/60 hover:text-zinc-600 dark:hover:bg-zinc-800/50 dark:hover:text-zinc-300"
          title={collapsed ? t('sidebar.expand') : t('sidebar.collapse')}
        >
          {collapsed
            ? <PanelLeftOpen className="h-4 w-4" />
            : <PanelLeftClose className="h-4 w-4" />
          }
        </button>
      </div>

      {/* Nav groups */}
      <nav className="flex-1 overflow-y-auto overflow-x-hidden py-3">
        {groups.map((group, gi) => (
          <div key={gi} className={cn('px-2', gi > 0 && 'mt-4')}>
            {/* Group label — hidden when collapsed */}
            {!collapsed && (
              <div className="mb-1 px-1.5 text-[9.5px] font-semibold uppercase tracking-widest text-zinc-400 select-none dark:text-zinc-500">
                {t(group.labelKey as any)}
              </div>
            )}
            {/* Divider when collapsed */}
            {collapsed && gi > 0 && (
              <div className="mx-auto mb-2 h-px w-6 bg-zinc-200 dark:bg-zinc-800" />
            )}
            <div className="space-y-0.5">
              {group.items.map((item) => {
                const Icon = item.icon;
                const isActive = pathname === item.href;
                const label = t(item.labelKey as any);
                return (
                  <Link
                    key={item.id}
                    href={item.href}
                    title={collapsed ? label : undefined}
                    className={cn(
                      'flex w-full items-center rounded-lg py-2 text-sm font-medium transition-colors',
                      collapsed ? 'justify-center px-0' : 'gap-2.5 px-2.5',
                      isActive
                        ? 'bg-indigo-50 text-indigo-600 dark:bg-indigo-950/40 dark:text-indigo-400'
                        : 'hover:bg-zinc-200/50 hover:text-zinc-900 dark:hover:bg-zinc-800/30 dark:hover:text-zinc-100',
                    )}
                  >
                    <Icon
                      className={cn(
                        'h-4 w-4 shrink-0',
                        isActive ? 'text-indigo-500 dark:text-indigo-400' : 'text-zinc-400 dark:text-zinc-500',
                      )}
                    />
                    {!collapsed && label}
                  </Link>
                );
              })}
            </div>
          </div>
        ))}
      </nav>

      {/* Bottom: Settings + Status */}
      <div className="border-t border-zinc-200/60 px-2 py-2.5 dark:border-zinc-800/60">
        <Link
          href="/system"
          title={collapsed ? t('sidebar.systemSettings' as any) : undefined}
          className={cn(
            'flex w-full items-center rounded-lg py-1.5 text-sm transition-colors',
            collapsed ? 'justify-center px-0' : 'gap-2.5 px-2',
            pathname === '/system'
              ? 'text-indigo-600 dark:text-indigo-400'
              : 'text-zinc-500 hover:text-zinc-900 dark:text-zinc-400 dark:hover:text-zinc-100',
          )}
        >
          <Settings className="h-3.5 w-3.5 shrink-0" />
          {!collapsed && <span className="font-medium">{t('sidebar.systemSettings' as any)}</span>}
        </Link>

        {/* Status indicators */}
        {collapsed ? (
          // Collapsed: single aggregate dot centered
          <div className="mt-2 flex justify-center pb-0.5">
            <span className={cn('h-1.5 w-1.5 rounded-full', statusColors[overallStatus])} />
          </div>
        ) : (
          // Expanded: individual dots with brain mode label
          <div className="mt-1.5 flex items-center gap-2.5 px-2 pb-0.5">
            {statusDots.map(({ key, labelKey }) => (
              <div key={key} className="flex items-center gap-1">
                <span className={cn('h-1 w-1 rounded-full', statusColors[status[key]])} />
                <span className="text-[10px] text-zinc-400 dark:text-zinc-500">
                  {t(labelKey)}
                  {key === 'brain' && status.brainLabel ? ` (${status.brainLabel})` : ''}
                </span>
              </div>
            ))}
          </div>
        )}
      </div>
    </div>
  );
}
