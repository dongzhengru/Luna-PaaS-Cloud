<script setup lang="ts">
import { onMounted, onUnmounted, ref, watch } from 'vue'
import { EditorState } from '@codemirror/state'
import {
  EditorView,
  keymap,
  lineNumbers,
  highlightActiveLine,
  drawSelection,
} from '@codemirror/view'
import { defaultKeymap, history, historyKeymap, indentWithTab } from '@codemirror/commands'
import {
  defaultHighlightStyle,
  syntaxHighlighting,
  indentOnInput,
  bracketMatching,
} from '@codemirror/language'
import { yaml } from '@codemirror/lang-yaml'
const value = defineModel<string>({ default: '' })
const host = ref<HTMLElement>()
let editor: EditorView | undefined
const theme = EditorView.theme({
  '&': {
    fontSize: '13px',
    borderRadius: '0.5rem',
    overflow: 'hidden',
    border: '1px solid var(--color-border)',
  },
  '&.cm-focused': { outline: '2px solid var(--color-ring)', outlineOffset: '-1px' },
  '.cm-scroller': {
    minHeight: '360px',
    maxHeight: '600px',
    fontFamily: 'var(--font-mono, monospace)',
  },
  '.cm-content': { padding: '12px 0' },
  '.cm-gutters': {
    backgroundColor: 'var(--color-muted)',
    color: 'var(--color-muted-foreground)',
    borderRight: '1px solid var(--color-border)',
  },
  '.cm-activeLine': { backgroundColor: 'color-mix(in srgb, var(--color-muted) 40%, transparent)' },
})
onMounted(() => {
  editor = new EditorView({
    parent: host.value,
    state: EditorState.create({
      doc: value.value,
      extensions: [
        lineNumbers(),
        highlightActiveLine(),
        drawSelection(),
        history(),
        indentOnInput(),
        bracketMatching(),
        yaml(),
        syntaxHighlighting(defaultHighlightStyle),
        keymap.of([...defaultKeymap, ...historyKeymap, indentWithTab]),
        theme,
        EditorView.contentAttributes.of({ 'aria-label': 'Compose YAML 配置', spellcheck: 'false' }),
        EditorView.updateListener.of((update) => {
          if (update.docChanged) value.value = update.state.doc.toString()
        }),
      ],
    }),
  })
})
watch(value, (text) => {
  if (editor && text !== editor.state.doc.toString())
    editor.dispatch({ changes: { from: 0, to: editor.state.doc.length, insert: text } })
})
onUnmounted(() => editor?.destroy())
</script>
<template><div ref="host" class="min-w-0" /></template>
