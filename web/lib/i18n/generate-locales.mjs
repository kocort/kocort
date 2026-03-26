import fs from 'node:fs';
import path from 'node:path';

const srcPath = path.join(process.cwd(), 'web', 'lib', 'i18n', 'translations.ts');
let text = fs.readFileSync(srcPath, 'utf8');
text = text.replace(/^export const translations\s*=\s*/, 'const translations = ');
text = text.replace(/\nexport type Language[\s\S]*$/, '\nmodule.exports = { translations };\n');
const moduleObj = { exports: {} };
const fn = new Function('module', 'exports', text);
fn(moduleObj, moduleObj.exports);
const { translations } = moduleObj.exports;

const outDir = path.join(process.cwd(), 'web', 'lib', 'i18n', 'locales');
fs.mkdirSync(outDir, { recursive: true });
for (const lang of ['en', 'zh']) {
    const sorted = Object.fromEntries(Object.entries(translations[lang]).sort(([a], [b]) => a.localeCompare(b)));
    fs.writeFileSync(path.join(outDir, `${lang}.json`), `${JSON.stringify(sorted, null, 2)}\n`);
}

const barrel = `import en from './locales/en.json';\nimport zh from './locales/zh.json';\n\nexport const translations = {\n  en,\n  zh,\n} as const;\n\nexport type Language = keyof typeof translations;\nexport type TranslationKey = keyof typeof en;\n`;
fs.writeFileSync(srcPath, barrel);
