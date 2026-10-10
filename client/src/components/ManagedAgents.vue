<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import ConfirmDialog from './ConfirmDialog.vue'
import AgentControls from './AgentControls.vue'
import { uiText } from '../locales/ui'
import { formatDateTime } from '../lib/datetime'
import { listManagedAgents, disableManagedAgent } from '../api/managedAgents'
import { listUsers } from '../api/users'
interface AgentRow { id:string; display_name:string; is_active:boolean; model_name:string|null; owner_name:string; project_key:string; created_at:number; trust_level:number; capabilities:string[] }
const props=defineProps<{projectKey?:string}>()
const items=ref<AgentRow[]>([]), page=ref(1), totalPages=ref(0), loading=ref(false), busy=ref(false), error=ref(''), notice=ref('')
const target=ref<AgentRow|null>(null), controls=ref<AgentRow|null>(null)
const stopMessage=computed(()=>target.value ? uiText('「{name}」（所有者：{owner}、PJ：{project}）を停止します。全トークンを失効し、履歴は保持します。', {name:target.value.display_name,owner:target.value.owner_name,project:target.value.project_key}) : '')
let generation=0
async function load(){
 const version=++generation;loading.value=true;error.value=''
 try {
  if(props.projectKey){
   const result=await listManagedAgents(props.projectKey,page.value)
   if(version!==generation)return
   items.value=result.items.map(a=>({...a,capabilities:a.capabilities??[],owner_name:a.owner.display_name,project_key:props.projectKey!}));totalPages.value=result.total_pages
  }else{
   const result=await listUsers({kind:'agent',page:page.value})
   if(version!==generation)return
   items.value=result.items.map(a=>({id:a.id,display_name:a.display_name,is_active:a.is_active,model_name:a.agent?.model_name??null,owner_name:a.agent?.owner?.display_name??'',project_key:a.agent?.project_key??'',created_at:a.created_at,trust_level:a.agent?.trust_level??1,capabilities:a.agent?.capabilities??[]}));totalPages.value=result.total_pages
  }
 }catch(e){if(version===generation)error.value=e instanceof Error?e.message:String(e)}finally{if(version===generation)loading.value=false}
}
watch(()=>props.projectKey,()=>{page.value=1;void load()},{immediate:true})
async function stop(){
 if(!target.value || busy.value)return
 busy.value=true;error.value=''
 try{await disableManagedAgent(target.value.project_key,target.value.id);target.value=null;notice.value=uiText('エージェントを停止しました');await load()}
 catch(e){error.value=e instanceof Error?e.message:String(e)}finally{busy.value=false}
}
</script>
<template>
 <section class="managed-agents" aria-label="agents">
  <p v-if="error" role="alert">{{error}}</p><p v-if="notice" role="status">{{notice}}</p>
  <p v-if="loading">{{$ui('読み込み中…')}}</p>
  <div class="table-scroll"><table>
   <thead><tr><th>{{$ui('名前')}}</th><th>{{$ui('所有者')}}</th><th>{{$ui('モデル')}}</th><th v-if="!projectKey">{{$ui('プロジェクト')}}</th><th>{{$ui('状態')}}</th><th>{{$ui('登録')}}</th><th>{{$ui('操作')}}</th></tr></thead>
   <tbody><tr v-for="a in items" :key="a.id"><td>{{a.display_name}}</td><td>{{a.owner_name}}</td><td>{{a.model_name??'—'}}</td><td v-if="!projectKey">{{a.project_key}}</td><td>{{a.is_active?$ui('有効'):$ui('無効')}}</td><td>{{formatDateTime(a.created_at)}}</td><td><button v-if="a.project_key" type="button" @click="controls=a">{{$ui('評価と実行監視')}}</button><button v-if="a.is_active && a.project_key" type="button" class="danger" :disabled="busy" @click="target=a">{{$ui('停止')}}</button></td></tr></tbody>
  </table></div>
  <p v-if="!loading && items.length===0">{{$ui('エージェントがありません')}}</p>
  <div class="pager"><button type="button" :disabled="page<=1 || loading" @click="page--;load()">{{$ui('前へ')}}</button><span>{{page}} / {{Math.max(1,totalPages)}}</span><button type="button" :disabled="page>=totalPages || loading" @click="page++;load()">{{$ui('次へ')}}</button></div>
  <AgentControls v-if="controls" :key="controls.id" :id="controls.id" :project-key="controls.project_key" :trust-level="controls.trust_level" :capabilities="controls.capabilities" @updated="load" />
  <ConfirmDialog v-if="target" :title="$ui('エージェントを停止')" :message="stopMessage" :confirm-label="$ui('停止')" danger :busy="busy" @confirm="stop" @cancel="target=null" />
 </section>
</template>
<style scoped>
.managed-agents{min-width:0;max-width:100%;padding:1rem 0}.table-scroll{overflow-x:auto;max-width:100%}table{width:100%;border-collapse:collapse;text-align:left}th,td{padding:.65rem;white-space:nowrap;border-bottom:1px solid var(--pb-border)}.pager{display:flex;align-items:center;gap:1rem;margin-top:1rem}
</style>
