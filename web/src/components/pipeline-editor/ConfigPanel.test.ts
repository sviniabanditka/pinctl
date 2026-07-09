import { createPinia, setActivePinia } from 'pinia'
import { mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it } from 'vitest'
import ConfigPanel from './ConfigPanel.vue'
import { useEditorStore } from '../../stores/editor'
import type { FlowNode } from '../../lib/graphMapping'

function expressionNode(inputs: { name: string; type: 'float64' | 'bool' }[]): FlowNode {
  return {
    id: 'n1',
    type: 'expression',
    position: { x: 0, y: 0 },
    data: {
      nodeType: 'expression',
      config: { expression: 'a', inputs, outputType: 'bool' },
    },
  }
}

describe('ConfigPanel: expression client-side validation', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  it('rejects an input named after a builtin, with a clear message', () => {
    const editor = useEditorStore()
    editor.nodes = [expressionNode([{ name: 'min', type: 'float64' }])]
    const wrapper = mount(ConfigPanel, { props: { selectedNodeId: 'n1' } })

    const text = wrapper.find('.expression-field .issue-list.error').text()
    expect(text).toContain('"min" is a reserved builtin name.')
  })

  it.each(['true', 'false'])('rejects the keyword "%s" as an input name', (name) => {
    const editor = useEditorStore()
    editor.nodes = [expressionNode([{ name, type: 'float64' }])]
    const wrapper = mount(ConfigPanel, { props: { selectedNodeId: 'n1' } })

    const text = wrapper.find('.expression-field .issue-list.error').text()
    expect(text).toContain(`"${name}" is a reserved keyword name.`)
  })

  it('accepts an ordinary, non-reserved input name', () => {
    const editor = useEditorStore()
    editor.nodes = [expressionNode([{ name: 'temperature', type: 'float64' }])]
    const wrapper = mount(ConfigPanel, { props: { selectedNodeId: 'n1' } })

    const issueList = wrapper.find('.expression-field .issue-list.error')
    expect(issueList.exists()).toBe(false)
  })

  it('still flags duplicate input names as a separate error', () => {
    const editor = useEditorStore()
    editor.nodes = [
      expressionNode([
        { name: 'a', type: 'float64' },
        { name: 'a', type: 'float64' },
      ]),
    ]
    const wrapper = mount(ConfigPanel, { props: { selectedNodeId: 'n1' } })

    const text = wrapper.find('.expression-field .issue-list.error').text()
    expect(text).toContain('Duplicate input name "a".')
  })
})
