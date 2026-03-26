import { useEffect, useState } from 'react';

/**
 * Synchronises a boolean `isDark` state with the `dark` class on `<html>`.
 * Listens via MutationObserver so it stays in sync when the theme toggle
 * (in page.tsx) changes the class list.
 */
export function useThemeSync(): boolean {
    const [isDark, setIsDark] = useState(false);

    useEffect(() => {
        const syncTheme = () =>
            setIsDark(document.documentElement.classList.contains('dark'));
        syncTheme();

        const observer = new MutationObserver(syncTheme);
        observer.observe(document.documentElement, {
            attributes: true,
            attributeFilter: ['class'],
        });
        return () => observer.disconnect();
    }, []);

    return isDark;
}
