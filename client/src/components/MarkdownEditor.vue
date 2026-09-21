<script setup lang="ts">
/**
 * Markdown の編集欄（`GuiDesign.md` 6.1 / 5.5「説明欄」）。
 *
 * **ソースとプレビューを縦に積む。** 横に並べない——チケット詳細はペインとして
 * 開き、幅が 750px 前後に制限される（2.2.1）。半分にすると各 350px になり、
 * `Requirements.md` 7章が対応すると定めた**表とコードブロックがどちらも
 * 読めない幅**になる。縦なら幅を保ったまま両方が見える。
 *
 * **`basicSetup` ではなく `minimalSetup` を使う。** `basicSetup` は行番号・
 * 折りたたみガター・括弧の自動補完まで入れる**コードエディタ向けの束**であり、
 * 散文を書く欄には合わない（行番号が本文の左に並ぶ）。`minimalSetup` は
 * 取り消し履歴と既定のキー割り当てだけを持つ。
 *
 * **描画は `lib/markdown.ts` を必ず通す**（`dompurify`）。本文の書き手は
 * 人だけではない（`Requirements.md` 10章）。
 */
import { markdown } from '@codemirror/lang-markdown'
import { EditorView, minimalSetup } from 'codemirror'
import { computed, onBeforeUnmount, onMounted, useTemplateRef, watch } from 'vue'

import { renderMarkdown } from '../lib/markdown'

const props = withDefaults(
  defineProps<{
    modelValue: string
    /** 開いた直後にカーソルを置く。インライン編集の入口はクリックなので既定 true */
    autofocus?: boolean
    /**
     * ソースの下にプレビューを積むか。
     *
     * **Docs（`GuiDesign.md` 5.10）は可視化ペインを独立して持つので `false` にする**
     * ——編集器の中にプレビューを二重に持たない。ただし**3ペインが並ばない幅では
     * `true` に戻す**（あちらの可視化ペインが畳まれるため、ここが唯一の描画になる）。
     */
    preview?: boolean
  }>(),
  { autofocus: true, preview: true },
)

const emit = defineEmits<{
  'update:modelValue': [value: string]
  /** `Esc`（5.5「編集の単位」の取り消し）。**戻す値は呼び出し側が持つ** */
  cancel: []
}>()

const host = useTemplateRef<HTMLDivElement>('host')
let view: EditorView | null = null

/** 描画済みの HTML。**`preview` が false のときは作らない**（無駄な描画を毎打鍵で走らせない） */
const previewHtml = computed(() => (props.preview ? renderMarkdown(props.modelValue) : ''))

onMounted(() => {
  if (host.value === null) return
  view = new EditorView({
    parent: host.value,
    doc: props.modelValue,
    extensions: [
      minimalSetup,
      markdown(),
      // **折り返す。** 散文の欄で横スクロールが出ると、書いている行が
      // 視野から消える
      EditorView.lineWrapping,
      EditorView.updateListener.of((u) => {
        if (!u.docChanged) return
        emit('update:modelValue', u.state.doc.toString())
      }),
    ],
  })
  if (props.autofocus) view.focus()
})

onBeforeUnmount(() => {
  view?.destroy()
  view = null
})

/**
 * 外から値が変わったとき（409 で取り直した・`Esc` で戻した）に追随する。
 *
 * **自分の入力で来た変化は無視する**——`updateListener` が投げた値がそのまま
 * 返ってくるので、比べずに `dispatch` するとカーソルが毎打鍵で先頭へ飛ぶ。
 */
watch(
  () => props.modelValue,
  (next) => {
    if (view === null) return
    const current = view.state.doc.toString()
    if (current === next) return
    view.dispatch({ changes: { from: 0, to: current.length, insert: next } })
  },
)

/** 呼び出し側から焦点を戻せるようにする（保存に失敗して編集モードへ留めるとき） */
defineExpose({
  focus: (): void => view?.focus(),
})
</script>

<template>
  <div class="md-editor" @keydown.escape.stop="emit('cancel')">
    <div ref="host" class="source"></div>

    <!-- プレビュー。**打つたびに追随する**（5.5「説明欄」のライブプレビュー）。
         Docs（5.10）は可視化ペインを別に持つので、ここは出さない -->
    <template v-if="preview">
      <div class="preview-label">{{ $ui('プレビュー') }}</div>
      <!-- eslint-disable-next-line vue/no-v-html -- lib/markdown.ts の dompurify を通っている -->
      <div v-if="previewHtml" class="markdown-body preview" v-html="previewHtml"></div>
      <p v-else class="preview empty">{{ $ui('まだ何も書かれていません') }}</p>
    </template>
  </div>
</template>

<style scoped>
.md-editor {
  display: flex;
  flex-direction: column;
  min-width: 0;
}

/* CodeMirror の外枠。**高さは中身で伸ばす**——固定にすると、短い本文でも
   大きな空白が残り、長い本文では二重スクロールになる */
.source {
  overflow: hidden;
  border: 1px solid var(--pb-border);
  border-radius: var(--pb-radius);
  background: var(--pb-bg);
}

.preview-label {
  margin: var(--pb-space-3) 0 var(--pb-space-1);
  color: var(--pb-text-muted);
  font-size: 12px;
}

.preview {
  padding: var(--pb-space-3);
  border: 1px dashed var(--pb-line);
  border-radius: var(--pb-radius);
}

.preview.empty {
  margin: 0;
  color: var(--pb-text-muted);
  font-size: 13px;
}
</style>

<style>
/* CodeMirror が挿す DOM は scoped の属性を持たないので、ここだけ素で当てる。
   **クラス名は `cm-` で始まるライブラリ側のもの**なので、PB の他の画面と
   衝突しない（`.section` で踏んだ衝突と同じ轍を踏まないための確認） */
/* **書ける高さを最初から用意する。** 中身で伸びる作りなので、1行の本文だと
   1行ぶんしか出ない——**PB で最も打鍵回数の多い入力欄**（`Design.md` 3.1）に
   1行の窓を出すのは、素の `textarea` より狭い */
.md-editor .cm-editor {
  min-height: 180px;
  max-height: 420px;
  font-size: 13px;
}

.md-editor .cm-editor.cm-focused {
  outline: none;
}

/* 等幅にする。**`base.css` の code / pre と同じ並び**にそろえる
   （別の並びを書くと、ソースとプレビューのコードで字形が変わる） */
.md-editor .cm-scroller {
  font-family:
    ui-monospace,
    SFMono-Regular,
    Menlo,
    Consolas,
    'Noto Sans Mono',
    monospace;
  line-height: 1.6;
}

.md-editor .cm-content {
  padding: var(--pb-space-3);
  caret-color: var(--pb-text);
}

.md-editor .cm-line {
  padding: 0;
}

/* 選択範囲と行の強調はテーマの色に寄せる（8.2 の輝度で階層を作る） */
.md-editor .cm-selectionBackground,
.md-editor .cm-editor ::selection {
  background: var(--pb-active) !important;
}

.md-editor .cm-cursor {
  border-left-color: var(--pb-text);
}
</style>
