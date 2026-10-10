<script setup lang="ts">
import { ref,watch } from 'vue'
import { getMonitorLimits,setMonitorLimits } from '../api/managedAgents'
import { uiText } from '../locales/ui'
const props=defineProps<{projectKey:string}>()
const emit=defineEmits<{updated:[]}>()
const attempts=ref<number|string>(''),failures=ref<number|string>(''),error=ref(''),notice=ref(''),busy=ref(false),loading=ref(true)
let generation=0
watch(()=>props.projectKey,async()=>{const g=++generation;loading.value=true;error.value='';try{const s=await getMonitorLimits(props.projectKey);if(g!==generation)return;attempts.value=s.max_attempts??'';failures.value=s.max_consecutive_failures??''}catch(e){if(g===generation)error.value=e instanceof Error?e.message:String(e)}finally{if(g===generation)loading.value=false}},{immediate:true})

async function save(){busy.value=true;error.value='';try{await setMonitorLimits(props.projectKey,{max_attempts:attempts.value===''?null:Number(attempts.value),max_consecutive_failures:failures.value===''?null:Number(failures.value)});notice.value=uiText('停止条件を保存しました');emit('updated')}catch(e){error.value=e instanceof Error?e.message:String(e)}finally{busy.value=false}}
</script>
<template><section class="monitor-settings"><h2>{{$ui('実行の停止条件')}}</h2><p>{{$ui('空欄の条件では自動停止しません。')}}</p><p v-if="error" role="alert">{{error}}</p><p v-if="notice" role="status">{{notice}}</p><form @submit.prevent="save"><label>{{$ui('同一チケットの試行上限')}}<input v-model="attempts" :disabled="loading || busy" type="number" min="1" max="2147483647" step="1" /></label><label>{{$ui('連続失敗上限')}}<input v-model="failures" :disabled="loading || busy" type="number" min="1" max="2147483647" step="1" /></label><button type="submit" :disabled="loading || busy">{{$ui('保存')}}</button></form></section></template>
<style scoped>.monitor-settings{max-width:40rem;padding:1rem 0;min-width:0}form{display:grid;gap:.6rem}label{display:grid;gap:.25rem}input{box-sizing:border-box;width:100%;min-width:0;padding:.4rem;border:1px solid var(--pb-border);background:var(--pb-surface);color:inherit}button{justify-self:start;padding:.4rem .7rem}</style>
