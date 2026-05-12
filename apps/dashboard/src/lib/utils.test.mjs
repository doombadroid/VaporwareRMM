// Vanilla node:test unit test for slugifyAppName / brandAppNameError.
// The dashboard has no Jest/Vitest runner; this file uses the
// standard library so `node --test src/lib/utils.test.mjs` runs it
// without introducing a new dependency. Mirrors the source logic
// exactly — keep in sync if utils.ts changes.

import { test } from 'node:test'
import assert from 'node:assert/strict'

function formatBytes(n) {
  if (!n || n <= 0) return "—"
  const TB = 1e12
  const GB = 1e9
  const MB = 1e6
  if (n >= TB) return `${(n / TB).toFixed(1)} TB`
  if (n >= GB) return `${(n / GB).toFixed(1)} GB`
  if (n >= MB) return `${(n / MB).toFixed(1)} MB`
  return `${n} B`
}

test('formatBytes picks unit and renders em-dash for zero/null', () => {
  assert.equal(formatBytes(0), '—')
  assert.equal(formatBytes(null), '—')
  assert.equal(formatBytes(undefined), '—')
  assert.equal(formatBytes(-1), '—')
  assert.equal(formatBytes(500), '500 B')
  assert.equal(formatBytes(1_500_000), '1.5 MB')
  assert.equal(formatBytes(8_589_934_592), '8.6 GB')           // 8 GB RAM
  assert.equal(formatBytes(137_438_953_472), '137.4 GB')       // 128 GB RAM (real device)
  assert.equal(formatBytes(2_199_023_255_552), '2.2 TB')       // 2 TB disk (real device)
})

const BRAND_APP_NAME_REGEX = /^[A-Za-z0-9._-]{1,64}$/

function brandAppNameError(value) {
  if (!value) return ''
  if (value.length > 64) return 'Must be 64 characters or fewer.'
  if (!BRAND_APP_NAME_REGEX.test(value)) {
    return 'Letters, numbers, dashes, underscores, and periods only — no spaces or symbols.'
  }
  return ''
}

function slugifyAppName(input) {
  if (!input) return ''
  let s = input.toLowerCase()
  s = s.replace(/\s+/g, '-')
  s = s.replace(/[^a-z0-9._-]+/g, '')
  s = s.replace(/-+/g, '-')
  s = s.replace(/^-+|-+$/g, '')
  if (s.length > 64) s = s.slice(0, 64)
  s = s.replace(/-+$/g, '')
  return s
}

test('slugify converts company name to valid app_name', () => {
  const cases = [
    ['T&C IT Systems', 'tc-it-systems'],
    ['Smith & Jones, LLC', 'smith-jones-llc'],
    ["O'Reilly Media", 'oreilly-media'],
    ['Tesla, Inc. ($TSLA)', 'tesla-inc.-tsla'],
    ['   spaced   ', 'spaced'],
    ['MIXED-CaSe_2025', 'mixed-case_2025'],
    ['vaporRMM', 'vaporrmm'],
    ['multiple    spaces    here', 'multiple-spaces-here'],
    ['---leading-and-trailing---', 'leading-and-trailing'],
    ['', ''],
    ['!!!@#$%', ''],
  ]
  for (const [input, want] of cases) {
    const got = slugifyAppName(input)
    assert.equal(got, want, `slugifyAppName(${JSON.stringify(input)}) = ${JSON.stringify(got)}, want ${JSON.stringify(want)}`)
    if (want) {
      assert.ok(
        BRAND_APP_NAME_REGEX.test(got),
        `slug ${JSON.stringify(got)} must match server-side regex`,
      )
    }
  }
})

test('slugify truncates to 64 chars and strips trailing dash from a cut run', () => {
  const long = 'a'.repeat(60) + ' ' + 'b'.repeat(20)
  const s = slugifyAppName(long)
  assert.ok(s.length <= 64, `length ${s.length} exceeds 64`)
  assert.ok(!s.endsWith('-'), `slug ${JSON.stringify(s)} ends with dash`)
  assert.ok(BRAND_APP_NAME_REGEX.test(s))
})

test('client-side validation rejects spaces in app_name before submission', () => {
  assert.equal(brandAppNameError(''), '', 'empty is allowed (server fills default)')
  assert.equal(brandAppNameError('vaporrmm'), '', 'plain identifier accepted')
  assert.equal(brandAppNameError('My RMM'), 'Letters, numbers, dashes, underscores, and periods only — no spaces or symbols.')
  assert.equal(brandAppNameError('Smith & Jones'), 'Letters, numbers, dashes, underscores, and periods only — no spaces or symbols.')
  assert.equal(brandAppNameError('a'.repeat(65)), 'Must be 64 characters or fewer.')
})

// "app_name does not auto-update once user has manually edited it"
// is a component-level behavior expressed in SetupWizard /
// BrandingModal via the appNameTouched flag — there's no isolated
// pure function to test. The contract is: while appNameTouched ==
// false, onCompanyNameChange writes slugifyAppName(value) into
// app_name; once true, it does not. Simulate the state machine here
// to lock the contract.
test('app_name does not auto-update once user has manually edited it', () => {
  // State machine the component runs:
  let state = { app_name: '', company_name: '', appNameTouched: false }
  const onCompanyNameChange = (value) => {
    state = { ...state, company_name: value }
    if (!state.appNameTouched) {
      const slug = slugifyAppName(value)
      if (slug) state.app_name = slug
    }
  }
  const onAppNameChange = (value) => {
    state = { ...state, app_name: value, appNameTouched: value !== '' }
  }

  // First-time setup: company name fills app_name automatically.
  onCompanyNameChange('T&C IT Systems')
  assert.equal(state.app_name, 'tc-it-systems', 'initial auto-fill from company name')

  // User edits app_name directly — auto-sync disengages.
  onAppNameChange('custom-name')
  assert.equal(state.appNameTouched, true)
  assert.equal(state.app_name, 'custom-name')

  // Subsequent company-name edits MUST NOT touch app_name.
  onCompanyNameChange('Smith & Jones IT')
  assert.equal(state.app_name, 'custom-name', 'app_name retained after manual edit')

  // Clearing app_name re-engages the sync.
  onAppNameChange('')
  assert.equal(state.appNameTouched, false)
  onCompanyNameChange('Foo Bar')
  assert.equal(state.app_name, 'foo-bar', 'sync re-engaged after clear')
})
