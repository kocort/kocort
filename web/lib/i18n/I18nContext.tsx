'use client';

import React, { createContext, useContext, useState, useEffect, ReactNode } from 'react';
import { apiGet } from '@/lib/api';
import { translations, Language, TranslationKey } from './translations';

export type LanguagePreference = 'system' | Language;

type NetworkPreferences = {
  language?: LanguagePreference;
};

interface I18nContextProps {
  language: Language;
  languagePreference: LanguagePreference;
  setLanguage: (lang: LanguagePreference) => void;
  t: (key: TranslationKey, params?: Record<string, string>) => string;
}

const I18nContext = createContext<I18nContextProps | undefined>(undefined);

function resolveSystemLanguage(): Language {
  if (typeof navigator === 'undefined') {
    return 'en';
  }
  const browserLang = navigator.language.toLowerCase();
  return browserLang.startsWith('zh') ? 'zh' : 'en';
}

export function I18nProvider({ children }: { children: ReactNode }) {
  const [languagePreference, setLanguagePreference] = useState<LanguagePreference>('system');
  const [mounted, setMounted] = useState(false);

  useEffect(() => {
    let cancelled = false;

    const loadPreference = async () => {
      try {
        const prefs = await apiGet<NetworkPreferences>('/api/system/network');
        if (!cancelled) {
          const next = prefs.language === 'zh' || prefs.language === 'en' ? prefs.language : 'system';
          setLanguagePreference(next);
        }
      } catch {
        if (!cancelled) {
          setLanguagePreference('system');
        }
      } finally {
        if (!cancelled) {
          setMounted(true);
        }
      }
    };

    void loadPreference();
    return () => {
      cancelled = true;
    };
  }, []);

  const language = languagePreference === 'system' ? resolveSystemLanguage() : languagePreference;

  useEffect(() => {
    if (typeof document !== 'undefined') {
      document.documentElement.lang = language === 'zh' ? 'zh-CN' : 'en';
    }
  }, [language]);

  const t = (key: TranslationKey, params?: Record<string, string>): string => {
    let text = translations[language][key] || translations['en'][key] || key;
    if (params) {
      Object.entries(params).forEach(([k, v]) => {
        text = text.replace(`{${k}}`, v);
      });
    }
    return text;
  };

  if (!mounted) {
    return null; // Prevent hydration mismatch
  }

  return (
    <I18nContext.Provider value={{ language, languagePreference, setLanguage: setLanguagePreference, t }}>
      {children}
    </I18nContext.Provider>
  );
}

export function useI18n() {
  const context = useContext(I18nContext);
  if (!context) {
    throw new Error('useI18n must be used within an I18nProvider');
  }
  return context;
}
