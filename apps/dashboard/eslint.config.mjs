// Flat-config ESLint setup for Next.js 15. Without this `next lint` drops
// into an interactive configuration wizard which hangs CI.
//
// We use the FlatCompat shim because eslint-config-next is still shipped as
// a legacy preset (extends:["next/core-web-vitals","next/typescript"]) and
// hasn't moved to flat config yet. The shim disappears once Next ships a
// native flat-config export.

import { dirname } from 'node:path'
import { fileURLToPath } from 'node:url'
import { FlatCompat } from '@eslint/eslintrc'

const __filename = fileURLToPath(import.meta.url)
const __dirname = dirname(__filename)

const compat = new FlatCompat({
  baseDirectory: __dirname,
})

export default [
  ...compat.extends('next/core-web-vitals', 'next/typescript'),
  {
    // Build outputs + vendor + ephemeral test artifacts. Without this
    // ESLint scans the static export under out/ which has thousands of
    // formatting errors in minified vendor bundles.
    ignores: [
      '.next/**',
      'out/**',
      'dist/**',
      'build/**',
      'node_modules/**',
      'playwright-report/**',
      'test-results/**',
      'public/**',
      'coverage/**',
      'tsconfig.tsbuildinfo',
      '**/*.tsbuildinfo',
    ],
  },
  {
    rules: {
      // The dashboard uses `any` deliberately in a few places where the
      // JSON shape comes from the Go API and we don't want to duplicate
      // the type. Demote from error to warn so CI doesn't fail on legacy
      // code while we tighten types.
      '@typescript-eslint/no-explicit-any': 'warn',
      // Allow unused parameters that start with _ (placeholder pattern).
      '@typescript-eslint/no-unused-vars': ['warn', {
        argsIgnorePattern: '^_',
        varsIgnorePattern: '^_',
      }],
    },
  },
]
