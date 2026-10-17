<script setup lang="ts">
/** メインメニューの単色アイコン（GuiDesign.md 4.1）。色は親から継承する。 */
defineProps<{
  name: 'projects' | 'dashboard' | 'backlog' | 'gantt' | 'clock' | 'search' | 'insights' | 'docs' | 'project-settings' | 'accounts' | 'audit' | 'app-settings'
}>()
</script>

<template>
  <svg
    :data-menu-icon="name"
    viewBox="0 0 16 16"
    width="16"
    height="16"
    fill="none"
    stroke="currentColor"
    :stroke-width="name === 'app-settings' ? 1.2 : 1.4"
    stroke-linecap="round"
    stroke-linejoin="round"
    aria-hidden="true"
    focusable="false"
  >
    <g v-if="name === 'projects'">
      <path d="M2 5h5l2 2h5v7H2z" />
    </g>
    <g v-else-if="name === 'dashboard'">
      <path d="M2 2h5v5H2zM10 2h4v3h-4zM2 10h5v4H2zM10 8h4v6h-4z" />
    </g>
    <g v-else-if="name === 'backlog'">
      <path d="M2 3l1 1 2-2M7 3h7M2 8l1 1 2-2M7 8h7M2 13l1 1 2-2M7 13h7" />
    </g>
    <g v-else-if="name === 'gantt'">
      <path d="M2 2v12h12M4 4h5M7 8h6M5 12h5" />
    </g>
    <g v-else-if="name === 'clock'">
      <circle cx="8" cy="8" r="6" />
      <path d="M8 4v4l3 2" />
    </g>
    <g v-else-if="name === 'search'">
      <circle cx="6.5" cy="6.5" r="4.5" />
      <path d="m10 10 4 4" />
    </g>
    <g v-else-if="name === 'insights'">
      <path d="M2 2v12h12M5 11V8M9 11V5M13 11V2" />
    </g>
    <g v-else-if="name === 'docs'">
      <path d="M3 1h7l3 3v11H3zM10 1v4h3M5 8h6M5 11h5" />
    </g>
    <g v-else-if="name === 'project-settings'">
      <path d="M2 3h12M2 8h12M2 13h12" />
      <circle cx="5" cy="3" r="2" />
      <circle cx="11" cy="8" r="2" />
      <circle cx="6" cy="13" r="2" />
    </g>
    <g v-else-if="name === 'accounts'">
      <circle cx="6" cy="5" r="3" />
      <path d="M1 14v-1a5 5 0 0 1 10 0v1M11 2a3 3 0 0 1 0 6M13 10a4 4 0 0 1 2 4" />
    </g>
    <g v-else-if="name === 'audit'">
      <path d="M8 1 2 3v5c0 3 3 5 6 7 3-2 6-4 6-7V3zM5 8l2 2 4-4" />
    </g>
    <g v-else-if="name === 'app-settings'">
      <!-- 同心円上の円弧を45度ずつ繰り返した8枚歯。中心穴も同じ中心を使う。 -->
      <path d="M12.80417 6.01005 A5.2 5.2 0 0 1 13.04554 6.74201 L14.59821 6.83656 A6.7 6.7 0 0 1 14.59821 9.16344 L13.04554 9.25799 A5.2 5.2 0 0 1 12.80417 9.98995 A5.2 5.2 0 0 1 12.45727 10.67820 L13.48832 11.84296 A6.7 6.7 0 0 1 11.84296 13.48832 L10.67820 12.45727 A5.2 5.2 0 0 1 9.98995 12.80417 A5.2 5.2 0 0 1 9.25799 13.04554 L9.16344 14.59821 A6.7 6.7 0 0 1 6.83656 14.59821 L6.74201 13.04554 A5.2 5.2 0 0 1 6.01005 12.80417 A5.2 5.2 0 0 1 5.32180 12.45727 L4.15704 13.48832 A6.7 6.7 0 0 1 2.51168 11.84296 L3.54273 10.67820 A5.2 5.2 0 0 1 3.19583 9.98995 A5.2 5.2 0 0 1 2.95446 9.25799 L1.40179 9.16344 A6.7 6.7 0 0 1 1.40179 6.83656 L2.95446 6.74201 A5.2 5.2 0 0 1 3.19583 6.01005 A5.2 5.2 0 0 1 3.54273 5.32180 L2.51168 4.15704 A6.7 6.7 0 0 1 4.15704 2.51168 L5.32180 3.54273 A5.2 5.2 0 0 1 6.01005 3.19583 A5.2 5.2 0 0 1 6.74201 2.95446 L6.83656 1.40179 A6.7 6.7 0 0 1 9.16344 1.40179 L9.25799 2.95446 A5.2 5.2 0 0 1 9.98995 3.19583 A5.2 5.2 0 0 1 10.67820 3.54273 L11.84296 2.51168 A6.7 6.7 0 0 1 13.48832 4.15704 L12.45727 5.32180 A5.2 5.2 0 0 1 12.80417 6.01005 Z" />
      <circle cx="8" cy="8" r="2.3" />
    </g>
  </svg>
</template>

<style scoped>
svg {
  display: block;
  flex: none;
}
</style>
