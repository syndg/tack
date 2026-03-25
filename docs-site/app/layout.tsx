import { RootProvider } from 'fumadocs-ui/provider/next';
import './global.css';

export const metadata = {
  title: 'Deck Documentation',
  description: 'One objective in. Reviewed, tested, merged code out.',
};

export default function Layout({ children }: LayoutProps<'/'>) {
  return (
    <html lang="en" suppressHydrationWarning>
      <body className="flex min-h-screen flex-col">
        <RootProvider>{children}</RootProvider>
      </body>
    </html>
  );
}
