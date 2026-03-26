'use client';

import { useState } from 'react';
import { Database, FileText, Folder, Globe, LayoutDashboard, Wrench } from 'lucide-react';
import { useI18n } from '@/lib/i18n/I18nContext';
import { DashboardView } from './DashboardView';
import { SandboxView } from './SandboxView';
import { DataView } from './DataView';
import { AuditLogsView } from './AuditLogsView';
import { ToolsView } from './ToolsView';
import { NetworkSettingsView } from './NetworkSettingsView';

type SystemTab = 'dashboard' | 'sandbox' | 'data' | 'tools' | 'network' | 'audit';

const TAB_META: Record<SystemTab, { icon: typeof LayoutDashboard }> = {
  dashboard: { icon: LayoutDashboard },
  sandbox: { icon: Folder },
  data: { icon: Database },
  tools: { icon: Wrench },
  network: { icon: Globe },
  audit: { icon: FileText },
};

export function SystemSettingsView() {
  const { t } = useI18n();
  const [activeTab, setActiveTab] = useState<SystemTab>('dashboard');

  return (
    <div className="flex h-full min-h-0 flex-col bg-white text-zinc-900 transition-colors dark:bg-zinc-950 dark:text-zinc-100">
      <div className="border-b border-zinc-200 bg-white px-6 py-4 pr-20 dark:border-zinc-800 dark:bg-zinc-950">
        <div className="flex flex-wrap items-center gap-2">
          {(['dashboard', 'sandbox', 'data', 'tools', 'network', 'audit'] as SystemTab[]).map((tab) => {
            const Icon = TAB_META[tab].icon;
            const isActive = activeTab === tab;
            return (
              <button
                key={tab}
                type="button"
                onClick={() => setActiveTab(tab)}
                className={
                  isActive
                    ? 'inline-flex items-center gap-2 rounded-lg bg-zinc-900 px-3 py-2 text-sm font-medium text-white transition-colors dark:bg-zinc-100 dark:text-zinc-900'
                    : 'inline-flex items-center gap-2 rounded-lg px-3 py-2 text-sm font-medium text-zinc-600 transition-colors hover:bg-zinc-100 hover:text-zinc-900 dark:text-zinc-400 dark:hover:bg-zinc-900 dark:hover:text-zinc-100'
                }
              >
                <Icon className="h-4 w-4" />
                <span>{t(`system.tabs.${tab}` as const)}</span>
              </button>
            );
          })}
        </div>
      </div>

      <div className="min-h-0 flex-1">
        {activeTab === 'dashboard' ? <DashboardView /> : null}
        {activeTab === 'sandbox' ? <SandboxView /> : null}
        {activeTab === 'data' ? <DataView /> : null}
        {activeTab === 'tools' ? <ToolsView /> : null}
        {activeTab === 'network' ? <NetworkSettingsView /> : null}
        {activeTab === 'audit' ? <AuditLogsView /> : null}
      </div>
    </div>
  );
}
