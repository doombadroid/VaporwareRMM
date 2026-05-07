import { Inter } from 'next/font/google'
import { ReactNode } from "react";
import { ThemeProvider } from "next-themes";
import { Toaster } from "sonner";
import { BrandingProvider } from "@/components/BrandingProvider";
import "./globals.css";

const inter = Inter({ subsets: ['latin'], variable: '--font-inter' })

interface RootLayoutProps {
  children: ReactNode;
}

export default function RootLayout({ children }: RootLayoutProps) {
  return (
    <html lang="en" suppressHydrationWarning className={inter.variable}>
      <body className="min-h-screen antialiased font-sans">
        <ThemeProvider attribute="class" defaultTheme="dark" enableSystem>
          <BrandingProvider>
            {children}
            <Toaster position="top-right" richColors />
          </BrandingProvider>
        </ThemeProvider>
      </body>
    </html>
  );
}
