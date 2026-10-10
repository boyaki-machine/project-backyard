<script setup lang="ts">
import { ref, watch } from 'vue'
import { getMonitor, resetMonitor, saveAttributes } from '../api/managedAgents'
import type { MonitorState } from '../api/managedAgents'
import { uiText } from '../locales/ui'
const props=defineProps<{id:string;projectKey?:string;trustLevel:number;capabilities?:string[]}>()
const emit=defineEmits<{updated:[]}>()
const trust=ref(props.trustLevel), labels=ref((props.capabilities??[]).join(', ')), reason=ref(''), busy=ref(false), error=ref(''),notice=ref(''),monitor=ref<MonitorState|null>(null)
let generation=0
watch(()=>props.id,async()=>{const g=++generation;trust.value=props.trustLevel;labels.value=(props.capabilities??[]).join(', ');monitor.value=null;error.value='';try{const result=await getMonitor(props.id,props.projectKey);if(g===generation)monitor.value=result}catch(e){if(g===generation)error.value=e instanceof Error?e.message:String(e)}},{immediate:true})
async function save(){busy.value=true;error.value='';try{await saveAttributes(props.id,trust.value,labels.value.trim()?labels.value.split(',').map(v=>v.trim()):[],props.projectKey);notice.value=uiText('評価属性を保存しました');emit('updated')}catch(e){error.value=e instanceof Error?e.message:String(e)}finally{busy.value=false}}
async function reset(){busy.value=true;error.value='';try{monitor.value=await resetMonitor(props.id,reason.value,props.projectKey);reason.value='';notice.value=uiText('実行監視を解除しました')}catch(e){error.value=e instanceof Error?e.message:String(e)}finally{busy.value=false}}
</script>
<template>
 <section class="agent-controls">
  <h3>{{$ui('評価と実行監視')}}</h3>
  <p v-if="error" role="alert">{{error}}</p><p v-if="notice" role="status">{{notice}}</p>
  <form @submit.prevent="save"><label>{{$ui('信頼度')}}<select v-model.number="trust"><option v-for="n in [0,1,2,3]" :key="n" :value="n">{{n}}</option></select></label><label>{{$ui('能力ラベル（カンマ区切り）')}}<input v-model="labels" /></label><p class="hint">{{$ui('評価属性によって権限は自動で増えません。')}}</p><button type="submit" :disabled="busy">{{$ui('保存')}}</button></form>
  <template v-if="monitor"><p>{{$ui('同一チケットの最大試行数')}}: {{monitor.attempts}} · {{$ui('連続失敗')}}: {{monitor.consecutive_failures}}</p><p>{{monitor.stopped?$ui('停止条件に到達'):$ui('実行可能')}}</p><p v-if="monitor.legacy_unknown" class="hint">{{$ui('過去の未観測の失敗回数は不明です。')}}</p><form @submit.prevent="reset"><label>{{$ui('解除理由')}}<textarea v-model="reason" maxlength="2000" required /></label><button type="submit" :disabled="busy || !reason.trim()">{{$ui('監視を解除')}}</button></form></template>
 </section>
</template>
<style scoped>
.agent-controls{max-width:40rem;min-width:0;padding:1rem;border:1px solid var(--pb-border);border-radius:var(--pb-radius);margin-top:1rem;overflow-wrap:anywhere}h3{margin-top:0}form{display:grid;gap:.6rem;min-width:0}label{display:grid;gap:.25rem}input,textarea,select{box-sizing:border-box;width:100%;min-width:0;padding:.4rem;border:1px solid var(--pb-border);border-radius:var(--pb-radius);background:var(--pb-surface);color:inherit;font:inherit}button{justify-self:start;padding:.4rem .7rem}.hint{color:var(--pb-text-muted);font-size:.85rem}
</style>
