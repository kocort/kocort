import en from './locales/en.json';
import zh from './locales/zh.json';

export const translations = {
  en,
  zh,
} as const;

export type Language = keyof typeof translations;
export type TranslationKey = keyof typeof en;
