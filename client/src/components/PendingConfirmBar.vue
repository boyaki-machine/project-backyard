<script setup lang="ts">
/**
 * 未確認の設定変更を知らせるフッター（`GuiDesign.md` 2.6）。
 *
 * **どの画面にいても見える。** アプリケーション設定ページの中だけに置くと、
 * **設定を変えたあと別の画面へ移ったとき、確認ボタンがどこにも無い状態で
 * 期限が過ぎる。**
 *
 * **重ね順はモーダルより下。** 上に置くと削除確認などの操作を邪魔する。
 *
 * **閉じる操作は持たない。** 閉じたら気づけない。
 *
 * **色は段階を付ける**（`GuiDesign.md` 8.4）。面は黄のまま、**残り60秒を切ったら
 * 枠線と数字を赤**にする——danger は文字・枠線、warning は面という役割分担を保つ。
 */
import { usePendingStore } from '../stores/pending'
import Avatar from './Avatar.vue'

const pendingStore = usePendingStore()
</script>

<template>
  <div v-if="pendingStore.pending" class="bar" :class="{ urgent: pendingStore.urgent }" role="alert">
    <div class="inner">
      <p class="head">
        <strong>{{ $ui('設定の変更を確認してください') }}</strong>
        <span class="remain">{{ $ui('あと') }} {{ pendingStore.remainSeconds }} {{ $ui('秒') }}</span>
      </p>
      <p class="body">
        <template v-if="pendingStore.changedByMe">{{ $ui('あなたが') }}</template>
        <template v-else-if="pendingStore.pending.changed_by">
          <Avatar :name="pendingStore.pending.changed_by.display_name" :kind="pendingStore.pending.changed_by.kind" :id="pendingStore.pending.changed_by.id" :size="18" /> {{ pendingStore.pending.changed_by.display_name }} {{ $ui('が') }} </template>
        {{ pendingStore.pending.keys.join(', ') }} {{ $ui('を変えました。') }} <strong>{{ $ui('この設定で画面へ入れていることを確かめて、右のボタンを押してください。') }}</strong> {{ $ui('押さないまま期限が過ぎると') }}<strong>{{ $ui('元の設定へ戻ります') }}</strong>。
      </p>
      <p v-if="pendingStore.error" class="err">{{ pendingStore.error }}</p>
    </div>
    <button
      type="button"
      class="primary"
      :disabled="pendingStore.confirming"
      @click="pendingStore.confirm()"
    >
      {{ pendingStore.confirming ? $ui("確認中…") : $ui("アクセスできました") }}
    </button>
  </div>
</template>

<style scoped>
/*
 * **モーダル（z-index 100）より下、メニューのオーバーレイ（30）より上。**
 * モーダルは一時的な操作であり、閉じれば見える。
 */
.bar {
  position: fixed;
  z-index: 40;
  right: 0;
  bottom: 0;
  left: 0;
  display: flex;
  gap: var(--pb-space-4);
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  padding: var(--pb-space-3) var(--pb-space-4);
  /* **面は黄**（8.4.1。警告は明るい黄の面に暗い文字を載せる） */
  background: var(--pb-warning-bg);
  border-top: 2px solid var(--pb-warning-border);
  box-shadow: var(--pb-shadow-2);
}
/* **残りが少ないときだけ赤**（8.4：danger は文字・枠線で表現する） */
.bar.urgent {
  border-top-color: var(--pb-danger);
}
.bar.urgent .remain {
  color: var(--pb-danger);
  font-weight: 700;
}
.inner {
  min-width: 0;
  flex: 1;
}
.head {
  margin: 0 0 var(--pb-space-1);
  display: flex;
  gap: var(--pb-space-3);
  align-items: baseline;
  flex-wrap: wrap;
}
.remain {
  font-variant-numeric: tabular-nums;
}
.body {
  margin: 0;
}
.err {
  margin: var(--pb-space-1) 0 0;
  color: var(--pb-danger);
}
.bar button {
  flex: none;
}
</style>
