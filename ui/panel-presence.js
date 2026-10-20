export const presenceKey=(source,id)=>source+':'+id;
export const recipientKey=panel=>JSON.stringify([!!panel?.all_clients,[...(panel?.emails||[])].sort(),[...(panel?.inbound_ids||[])].sort((a,b)=>a-b)]);
export const presenceIndex=value=>new Map((value?.nodes||[]).map(n=>[presenceKey(n.source_id,n.node_id),n]));
export function observedSelection(value){return value?.available?{sources:[],nodes:value.nodes.filter(n=>n.present>0).map(n=>({source_id:n.source_id,node_id:n.node_id}))}:undefined;}
export function presenceCount(value){return new Set((value?.nodes||[]).filter(n=>n.present>0).map(n=>n.connection_id||presenceKey(n.source_id,n.node_id))).size;}
export function presenceText(value,row,t){
 if(!value?.configured)return '';
 if(!value.available||!row)return t('presenceUnknown');
 if(!row.present)return t('presenceAbsent');
 if(row.present<value.clients)return `${t('presencePartial')} ${row.present}/${value.clients}`;
 if(!row.active)return t('presenceDisabled');
 if(row.manual&&!row.managed)return t('presenceManual');
 return t('presenceAdded');
}
export function presenceClass(value,row){return !value?.available||!row?'unknown':!row.present?'absent':row.present<value.clients||!row.active?'partial':'present';}
