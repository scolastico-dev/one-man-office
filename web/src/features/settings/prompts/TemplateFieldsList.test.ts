import { mount } from '@vue/test-utils'
import { expect, test } from 'vitest'
import { EXPORT_FIELDS, PROJECT_FIELDS, TEMPLATE_FIELDS } from './templateFields'
import TemplateFields from './TemplateFields.vue'

test('every field is listed: the top level, a project inside range .Projects, and .Export of an export job', () => {
  const w = mount(TemplateFields)
  const names = w.findAll('dt').map((d) => d.text())
  expect(names).toEqual([...TEMPLATE_FIELDS, ...PROJECT_FIELDS, ...EXPORT_FIELDS].map((f) => f.name))
  expect(w.get('[data-export-fields]').text()).toContain('The command that opens the pull request')
  expect(w.text()).toContain('An export job has {{.Export}} with:')
})
