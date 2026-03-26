'use client';

import { useEffect } from 'react';
import { useI18n } from '@/lib/i18n/I18nContext';

/**
 * Client component that dynamically updates document title and meta description
 * based on the current i18n language setting.
 */
export function DynamicMeta() {
    const { t } = useI18n();

    useEffect(() => {
        document.title = t('meta.title');

        const metaDescription = document.querySelector('meta[name="description"]');
        if (metaDescription) {
            metaDescription.setAttribute('content', t('meta.description'));
        }
    }, [t]);

    return null;
}
