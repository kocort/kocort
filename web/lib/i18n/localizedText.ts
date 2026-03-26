import type { LocalizedText } from '@/lib/api';

type SupportedLanguage = 'zh' | 'en';

function resolvePageLanguage(fallbackLanguage: SupportedLanguage): SupportedLanguage {
    if (typeof document !== 'undefined') {
        const lang = document.documentElement.lang.trim().toLowerCase();
        if (lang.startsWith('zh')) {
            return 'zh';
        }
        if (lang.startsWith('en')) {
            return 'en';
        }
    }
    return fallbackLanguage;
}

export function resolveLocalizedText(text?: LocalizedText, fallbackLanguage: SupportedLanguage = 'en'): string {
    if (!text) {
        return '';
    }
    if (typeof text === 'string') {
        return text;
    }

    const pageLanguage = resolvePageLanguage(fallbackLanguage);
    if (pageLanguage === 'zh') {
        return text.zh?.trim() || text.en?.trim() || '';
    }
    return text.en?.trim() || text.zh?.trim() || '';
}