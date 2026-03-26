import type { Metadata } from 'next';
import '@ant-design/x-markdown/dist/x-markdown.css';
import '@ant-design/x-markdown/themes/light.css';
import '@ant-design/x-markdown/themes/dark.css';
import './globals.css'; // Global styles
import { I18nProvider } from '@/lib/i18n/I18nContext';
import { DynamicMeta } from '@/components/DynamicMeta';

export const metadata: Metadata = {
  title: 'Kocort — Desktop AI Agent Assistant',
  description: 'Desktop AI Agent Assistant — Dual-Brain Architecture · Zero CLI · Cross-Platform Support',
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en">
      <body className="font-sans antialiased" suppressHydrationWarning>
        <I18nProvider>
          <DynamicMeta />
          {children}
        </I18nProvider>
      </body>
    </html>
  );
}
