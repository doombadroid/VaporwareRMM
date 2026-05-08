import { Inter } from 'next/font/google'
import { ReactNode } from "react";
import type { Metadata } from "next";
import { ThemeProvider } from "next-themes";
import { Toaster } from "sonner";
import { BrandingProvider } from "@/components/BrandingProvider";
import { CurrentUserProvider } from "@/components/CurrentUserProvider";
import "./globals.css";

const inter = Inter({ subsets: ['latin'], variable: '--font-inter' })

export const metadata: Metadata = {
  title: { default: 'vaporRMM', template: '%s · vaporRMM' },
  description: 'Self-hosted Remote Monitoring & Management',
};

interface RootLayoutProps {
  children: ReactNode;
}

export default function RootLayout({ children }: RootLayoutProps) {
  return (
    <html lang="en" suppressHydrationWarning className={inter.variable}>
      <body className="min-h-screen antialiased font-sans">
        <ThemeProvider attribute="class" defaultTheme="dark" enableSystem>
          <BrandingProvider>
            <CurrentUserProvider>
              {children}
              <Toaster position="top-right" richColors />
            </CurrentUserProvider>
          </BrandingProvider>
        </ThemeProvider>
      </body>
    </html>
  );
}
