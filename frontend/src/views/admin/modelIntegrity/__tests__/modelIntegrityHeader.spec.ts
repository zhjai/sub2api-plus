import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'
import zh from '@/i18n/locales/zh'
import en from '@/i18n/locales/en'

const here = dirname(fileURLToPath(import.meta.url))
const routerSource = readFileSync(resolve(here, '../../../../router/index.ts'), 'utf8')
const testsViewSource = readFileSync(resolve(here, '../ModelIntegrityTestsView.vue'), 'utf8')
const schedulingViewSource = readFileSync(resolve(here, '../ModelIntegritySchedulingView.vue'), 'utf8')

// The global header renders route meta descriptions in a non-shrinking block,
// so a long subtitle squeezes the header's right-hand links. The header gets a
// one-line summary; the full explanation stays in the page shell.
describe('model integrity header subtitles', () => {
  const pages = ['tests', 'scheduling'] as const

  it('routes use the short header subtitle, not the full page description', () => {
    for (const page of pages) {
      expect(routerSource).toContain(`descriptionKey: 'admin.modelIntegrity.${page}.headerDescription'`)
      expect(routerSource).not.toContain(`descriptionKey: 'admin.modelIntegrity.${page}.description'`)
    }
  })

  it('page shells keep the full explanatory description', () => {
    expect(testsViewSource).toContain("t('admin.modelIntegrity.tests.description')")
    expect(schedulingViewSource).toContain("t('admin.modelIntegrity.scheduling.description')")
  })

  it('keeps localized header subtitles short and distinct from the page copy', () => {
    for (const page of pages) {
      const zhCopy = zh.admin.modelIntegrity[page]
      const enCopy = en.admin.modelIntegrity[page]
      expect(zhCopy.headerDescription.length).toBeGreaterThan(0)
      expect(zhCopy.headerDescription.length).toBeLessThanOrEqual(24)
      expect(enCopy.headerDescription.length).toBeGreaterThan(0)
      expect(enCopy.headerDescription.length).toBeLessThanOrEqual(80)
      expect(zhCopy.headerDescription.length).toBeLessThan(zhCopy.description.length)
      expect(enCopy.headerDescription.length).toBeLessThan(enCopy.description.length)
    }
  })
})

// Each locale names the evaluate action in its own language, and every
// English message that refers to it uses the same English name.
describe('model integrity evaluate action copy', () => {
  const collect = (value: unknown): string[] =>
    typeof value === 'string' ? [value] : value && typeof value === 'object' ? Object.values(value).flatMap(collect) : []

  it('is “Evaluate now” in English and 立即评估 in Chinese', () => {
    expect(en.admin.modelIntegrity.scheduling.evaluation.run).toBe('Evaluate now')
    expect(zh.admin.modelIntegrity.scheduling.evaluation.run).toBe('立即评估')
    expect(en.admin.modelIntegrity.scheduling.quality.pending).toContain('“Evaluate now”')
    expect(en.admin.modelIntegrity.scheduling.board.none).toContain('“Evaluate now”')
    expect(en.admin.modelIntegrity.scheduling.records.empty).toContain('“Evaluate now”')
  })

  it('leaves no Chinese text in the English model integrity copy', () => {
    const chinese = collect(en.admin.modelIntegrity).filter(text => /[一-鿿]/.test(text))
    expect(chinese).toEqual([])
  })
})
