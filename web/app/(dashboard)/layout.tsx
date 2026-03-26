'use client';

import { useState, useEffect, useCallback } from 'react';
import { Sidebar } from '@/components/layout/Sidebar';
import { OnboardingView } from '@/components/onboarding/OnboardingView';
import { ConfigProvider, theme as antdTheme } from 'antd';
import { Sun, Moon } from 'lucide-react';
import { useI18n } from '@/lib/i18n/I18nContext';
import { apiGet } from '@/lib/api';

type SetupStatusResponse = { needsSetup: boolean; hasModels: boolean };

export default function DashboardLayout({ children }: { children: React.ReactNode }) {
    const { t } = useI18n();
    const [showOnboarding, setShowOnboarding] = useState<boolean | null>(null);
    const [isDark, setIsDark] = useState(() => {
        if (typeof window === 'undefined') return false;
        const stored = window.localStorage.getItem('ui-theme');
        if (stored === 'dark') return true;
        if (stored === 'light') return false;
        return window.matchMedia('(prefers-color-scheme: dark)').matches;
    });

    useEffect(() => {
        if (isDark) {
            document.documentElement.classList.add('dark');
        } else {
            document.documentElement.classList.remove('dark');
        }
        window.localStorage.setItem('ui-theme', isDark ? 'dark' : 'light');
    }, [isDark]);

    useEffect(() => {
        apiGet<SetupStatusResponse>('/api/setup/status')
            .then((resp) => setShowOnboarding(resp.needsSetup))
            .catch(() => setShowOnboarding(false));
    }, []);

    const handleOnboardingComplete = useCallback(() => {
        setShowOnboarding(false);
    }, []);

    return (
        <ConfigProvider
            theme={{
                algorithm: isDark ? antdTheme.darkAlgorithm : antdTheme.defaultAlgorithm,
                token: {
                    colorPrimary: '#4f46e5',
                    borderRadius: 14,
                },
            }}
        >
            {/* Onboarding */}
            {showOnboarding === true && (
                <OnboardingView onComplete={handleOnboardingComplete} />
            )}

            {/* Main app shell */}
            {showOnboarding === false && (
                <div className="flex h-screen w-full bg-white dark:bg-zinc-950 text-zinc-900 dark:text-zinc-100 overflow-hidden font-sans selection:bg-indigo-500/30 transition-colors">
                    <Sidebar />

                    <main className="relative flex min-h-0 flex-1 flex-col overflow-hidden bg-white transition-colors dark:bg-zinc-950">
                        <div className="absolute top-4 right-6 z-50">
                            <button
                                onClick={() => setIsDark(!isDark)}
                                className="p-2 rounded-lg bg-zinc-100 dark:bg-zinc-900 text-zinc-600 dark:text-zinc-400 hover:bg-zinc-200 dark:hover:bg-zinc-800 transition-colors border border-zinc-200 dark:border-zinc-800 shadow-sm"
                                title={t('common.toggleTheme')}
                            >
                                {isDark ? <Sun className="w-4 h-4" /> : <Moon className="w-4 h-4" />}
                            </button>
                        </div>
                        {children}
                    </main>
                </div>
            )}

            {/* Loading state */}
            {showOnboarding === null && (
                <div className="flex items-center justify-center h-screen bg-white dark:bg-zinc-950">
                    <div className="w-8 h-8 border-2 border-indigo-600 border-t-transparent rounded-full animate-spin" />
                </div>
            )}
        </ConfigProvider>
    );
}
