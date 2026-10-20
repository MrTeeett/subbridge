import {presenceIndex,presenceKey,observedSelection,presenceText,presenceClass} from './panel-presence.js';
const escape=value=>String(value??'').replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
const key=(source,id)=>source+':'+id;
const matches=(value,query,language)=>query.normalize('NFKC').toLocaleLowerCase(language).trim().split(/\s+/).every(term=>value.normalize('NFKC').toLocaleLowerCase(language).includes(term));

export function selectionStats(selection,groups){
 const chosen=new Set(),known=new Set(),sources=new Set(selection?.sources||[]),refs=new Set((selection?.nodes||[]).map(n=>key(n.source_id,n.node_id)));
 for(const group of groups)for(const node of group.nodes){const id=key(group.id,node.id);known.add(id);if(selection==null||sources.has(group.id)||refs.has(id))chosen.add(node.id);}
 const missing=(selection?.nodes||[]).filter(n=>!known.has(key(n.source_id,n.node_id))).length+(selection?.sources||[]).filter(id=>!groups.some(g=>g.id===id)).length;
 return {count:chosen.size,missing};
}

export class ConfigPicker{
 constructor({groups,selection,language,t,load,onApply,onClose,saveHint,presence,requirePresence=false}){
  Object.assign(this,{groups,language,t,load,onApply,onClose});this.selection=selection==null?null:{sources:[...(selection.sources||[])],nodes:structuredClone(selection.nodes||[])};this.presence=presence;this.presenceIndex=presenceIndex(presence);this.requirePresence=requirePresence;this.presenceError=false;this.editedNodes=new Set();this.editedSources=new Set();this.allEdited=false;if(requirePresence&&presence?.available)this.selection=observedSelection(presence);this.source='all';this.query='';this.sourceQuery='';this.order='name-asc';this.sourceOrder='name-asc';this.selectedOnly=false;this.closed=false;
  this.collator=new Intl.Collator(language,{numeric:true,sensitivity:'base'});this.controller=new AbortController();const signal=this.controller.signal;
  this.previousFocus=document.activeElement;this.dialog=document.createElement('dialog');this.dialog.className='configuration-dialog';this.dialog.setAttribute('aria-labelledby','configuration-dialog-title');
  const label=key=>escape(t(key));
  this.dialog.innerHTML=`<div class="configuration-dialog-header"><div><h2 id="configuration-dialog-title">${label('chooseConfigs')}</h2><p class="hint">${label('chooseConfigsHint')}</p></div><button type="button" data-close aria-label="${label('close')}">×</button></div><div class="configuration-dialog-mode"><label class="check"><input type="checkbox" data-all-sources ${this.selection==null?'checked':''}>${label('allConvertedConfigs')}</label><span data-selection-status role="status" aria-live="polite"></span></div><div class="configuration-dialog-layout"><aside class="configuration-source-nav"><label class="sr-only" for="selection-source-search">${label('searchSubscriptions')}</label><input type="search" id="selection-source-search" placeholder="${label('searchSubscriptions')}"><label class="sr-only" for="selection-source-sort">${label('sortSubscriptions')}</label><select id="selection-source-sort"><option value="name-asc">${label('nameAsc')}</option><option value="name-desc">${label('nameDesc')}</option></select><div data-source-list></div></aside><div class="configuration-dialog-content"><div class="configuration-selection-tools"><div class="field"><label for="selection-config-search">${label('searchConfigs')}</label><input type="search" id="selection-config-search" placeholder="${label('searchConfigsHint')}"></div><div class="field"><label for="selection-config-sort">${label('sort')}</label><select id="selection-config-sort">${[['name-asc','nameAsc'],['name-desc','nameDesc'],['selected','selectedFirst'],['address','sortAddress'],['protocol','sortProtocol'],['port-asc','portAsc'],['port-desc','portDesc']].map(([v,k])=>`<option value="${v}">${label(k)}</option>`).join('')}</select></div></div><div class="configuration-selection-actions"><label class="check"><input type="checkbox" data-only-selected>${label('onlySelected')}</label><button type="button" data-reload>${label('reloadConfigs')}</button></div><div class="configuration-selection-actions"><button type="button" data-select-visible>${label('selectVisible')}</button><button type="button" data-clear-visible>${label('deselectVisible')}</button><button type="button" data-clear-all>${label('clearSelection')}</button></div><p class="hint" data-load-status role="status"></p><div class="configuration-selection-results" data-selection-results></div></div></div><div class="configuration-dialog-footer"><p class="hint">${escape(saveHint||t('selectionSaveHint'))}</p><div class="actions"><button type="button" data-cancel>${label('cancel')}</button><button type="button" class="primary" data-apply>${label('applySelection')}</button></div></div>`;
  document.body.append(this.dialog);document.body.classList.add('dialog-open');
  this.dialog.addEventListener('close',()=>this.destroy(),{signal});this.dialog.querySelector('[data-close]').addEventListener('click',()=>this.destroy(),{signal});this.dialog.querySelector('[data-cancel]').addEventListener('click',()=>this.destroy(),{signal});
  this.dialog.querySelector('[data-apply]').addEventListener('click',()=>this.apply(),{signal});
  this.dialog.addEventListener('cancel',e=>{if(this.saving)e.preventDefault();},{signal});
  this.dialog.querySelector('[data-all-sources]').addEventListener('change',e=>{this.allEdited=true;this.selection=e.target.checked?null:{sources:[],nodes:[]};this.update();},{signal});
  this.dialog.querySelector('#selection-config-search').addEventListener('input',e=>{this.query=e.target.value;this.update();},{signal});
  this.dialog.querySelector('#selection-source-search').addEventListener('input',e=>{this.sourceQuery=e.target.value;this.update();},{signal});
  this.dialog.querySelector('#selection-config-sort').addEventListener('change',e=>{this.order=e.target.value;this.update();},{signal});
  this.dialog.querySelector('#selection-source-sort').addEventListener('change',e=>{this.sourceOrder=e.target.value;this.update();},{signal});
  this.dialog.querySelector('[data-only-selected]').addEventListener('change',e=>{this.selectedOnly=e.target.checked;this.update();},{signal});
  this.dialog.querySelector('[data-select-visible]').addEventListener('click',()=>{const rows=this.visible();this.manual();for(const {group,node} of rows)if(!this.isChosen(group.id,node.id))this.setNode(group.id,node.id,true);this.update();},{signal});
  this.dialog.querySelector('[data-clear-visible]').addEventListener('click',()=>{const rows=this.visible();this.manual();for(const {group,node} of rows)this.setNode(group.id,node.id,false);this.update();},{signal});
  this.dialog.querySelector('[data-clear-all]').addEventListener('click',()=>{this.allEdited=true;this.selection={sources:[],nodes:[]};this.update();},{signal});
  this.dialog.querySelector('[data-reload]').addEventListener('click',()=>this.reload(),{signal});
  this.dialog.addEventListener('click',e=>{const button=e.target.closest('[data-select-source]');if(!button)return;this.source=button.dataset.selectSource;this.update();this.dialog.querySelector('[data-selection-results]').scrollTop=0;},{signal});
  this.dialog.addEventListener('change',e=>{
   if(e.target.matches('[data-whole-source]')){const id=e.target.dataset.wholeSource;this.editedSources.add(id);this.manual();this.selection.sources=this.selection.sources.filter(s=>s!==id);this.selection.nodes=this.selection.nodes.filter(n=>n.source_id!==id);if(e.target.checked)this.selection.sources.push(id);this.update();}
   if(e.target.matches('[data-config-choice]')){const id=e.target.dataset.configChoice,source=e.target.dataset.source;this.manual();this.setNode(source,id,e.target.checked);this.update();this.dialog.querySelector(`[data-config-choice="${CSS.escape(id)}"][data-source="${CSS.escape(source)}"]`)?.focus({preventScroll:true});}
  },{signal});
  this.update();this.dialog.showModal();this.dialog.querySelector('#selection-config-search').focus();this.reload();
 }
 manual(){if(this.selection==null)this.selection={sources:this.groups.map(g=>g.id),nodes:[]};}
 isChosen(source,id){return this.selection==null||this.selection.sources.includes(source)||this.selection.nodes.some(n=>n.source_id===source&&n.node_id===id);}
 setNode(source,id,chosen,edited=true){
  if(edited){this.editedNodes.add(presenceKey(source,id));const connection=this.presenceIndex.get(presenceKey(source,id))?.connection_id;if(connection)for(const n of this.presence?.nodes||[])if(n.connection_id===connection&&(n.source_id!==source||n.node_id!==id)){this.setNode(n.source_id,n.node_id,chosen,false);this.editedNodes.add(presenceKey(n.source_id,n.node_id));}}
  if(this.selection.sources.includes(source)){
   this.selection.sources=this.selection.sources.filter(s=>s!==source);
   for(const node of this.groups.find(g=>g.id===source)?.nodes||[])if(!this.selection.nodes.some(n=>n.source_id===source&&n.node_id===node.id))this.selection.nodes.push({source_id:source,node_id:node.id});
  }
  this.selection.nodes=this.selection.nodes.filter(n=>n.source_id!==source||n.node_id!==id);
  if(chosen)this.selection.nodes.push({source_id:source,node_id:id});
 }
 allGroups(){
  const groups=this.groups.map(g=>({...g,nodes:g.nodes.map(n=>({...n,available:true}))})),byID=new Map(groups.map(g=>[g.id,g]));
  const ensure=id=>{if(!byID.has(id)){const group={id,name:this.t('missingSubscription')+' · '+id,nodes:[],missing:true};byID.set(id,group);groups.push(group);}return byID.get(id);};
  for(const id of this.selection?.sources||[])ensure(id);
  for(const ref of this.selection?.nodes||[]){const group=ensure(ref.source_id);if(!group.nodes.some(n=>n.id===ref.node_id))group.nodes.push({id:ref.node_id,name:this.t('missingConfig')+' · '+ref.node_id.slice(0,12),protocol:'',address:'',port:'',available:false});}
  return groups.sort((a,b)=>(this.sourceOrder==='name-desc'?-1:1)*this.collator.compare(this.name(a),this.name(b)));
 }
 name(group){return group.name||(group.id==='default'?this.t('primarySource'):group.id);}
 visible(){
  const rows=[];
  for(const group of this.allGroups()){
   if(this.source!=='all'&&this.source!==group.id)continue;
   for(const node of group.nodes)if((!this.selectedOnly||this.isChosen(group.id,node.id))&&matches([this.name(group),node.name,node.protocol,node.address,node.port].join(' '),this.query,this.language))rows.push({group,node});
  }
  return rows.sort((a,b)=>{
   let comparison=0;
   if(this.order==='selected')comparison=Number(this.isChosen(b.group.id,b.node.id))-Number(this.isChosen(a.group.id,a.node.id));
   if(this.order.startsWith('port-'))comparison=(Number(a.node.port)||0)-(Number(b.node.port)||0);
   const field=this.order==='address'?'address':this.order==='protocol'?'protocol':'name';
   return comparison*(this.order==='port-desc'?-1:1)||(this.order==='name-desc'?-1:1)*this.collator.compare(a.node[field],b.node[field])||this.collator.compare(a.node.id,b.node.id);
  });
 }
 update(){
  if(this.closed)return;this.keepManual();const e=escape,t=this.t,groups=this.allGroups(),rows=this.visible(),stats=selectionStats(this.selection,this.groups);
  const all=this.dialog.querySelector('[data-all-sources]'),current=this.groups.flatMap(g=>g.nodes.map(n=>({source:g.id,id:n.id})));all.checked=this.selection==null||current.length>0&&current.every(n=>this.isChosen(n.source,n.id));all.indeterminate=!all.checked&&current.some(n=>this.isChosen(n.source,n.id));
  this.dialog.querySelector('[data-selection-status]').textContent=t('selectedCount')+': '+stats.count+(stats.missing?' · '+t('missingCount')+': '+stats.missing:'');
  const sourceButton=(id,name,count)=>`<button type="button" data-select-source="${e(id)}" class="${this.source===id?'selected':''}" aria-pressed="${this.source===id}"><span>${e(name)}</span><span class="nav-count">${e(count)}</span></button>`;
  this.dialog.querySelector('[data-source-list]').innerHTML=sourceButton('all',t('allSources'),this.groups.reduce((sum,g)=>sum+g.nodes.length,0))+groups.filter(g=>matches(this.name(g),this.sourceQuery,this.language)).map(g=>sourceButton(g.id,this.name(g),g.nodes.filter(n=>n.available).length)).join('');
  const visibleGroups=groups.filter(g=>(this.source==='all'||g.id===this.source)&&(rows.some(r=>r.group.id===g.id)||!this.query&&!this.selectedOnly));
  this.dialog.querySelector('[data-selection-results]').innerHTML=visibleGroups.map(group=>{
   const nodes=rows.filter(row=>row.group.id===group.id).map(row=>row.node),whole=this.selection==null||this.selection.sources.includes(group.id)||group.nodes.length>0&&group.nodes.every(n=>this.isChosen(group.id,n.id));
   return `<section class="configuration-selection-group" data-selection-source="${e(group.id)}"><div class="configuration-selection-group-header"><h3>${e(this.name(group))} <span class="count-badge">${nodes.length} / ${group.nodes.length}</span></h3><label class="check"><input type="checkbox" data-whole-source="${e(group.id)}" ${whole?'checked':''}>${e(t('wholeSubscription'))}</label></div>${group.issue?`<p class="hint inline-error">${e(group.issue)}</p>`:''}${nodes.map(node=>`<label class="configuration-choice ${this.isChosen(group.id,node.id)?'is-selected':''}"><input type="checkbox" data-config-choice="${e(node.id)}" data-source="${e(group.id)}" ${this.isManual(group.id,node.id)?'disabled':''} ${this.isChosen(group.id,node.id)?'checked':''}><span><span class="configuration-choice-title">${e(node.name)}${!node.available?` <span class="missing">${e(t('unavailableConfig'))}</span>`:''}</span>${this.presence?.configured?`<span class="presence-badge ${presenceClass(this.presence,this.presenceIndex.get(presenceKey(group.id,node.id)))}">${e(presenceText(this.presence,this.presenceIndex.get(presenceKey(group.id,node.id)),t))}</span>`:''}<span class="configuration-choice-meta"><span class="protocol-badge">${e(node.protocol.toUpperCase())}</span> ${e(node.address)}${node.port?':'+e(node.port):''}</span></span></label>`).join('')||`<p class="hint">${e(t('emptyNodes'))}</p>`}</section>`;
  }).join('')||`<p class="empty">${e(t('noResults'))}</p>`;
  this.dialog.querySelector('[data-select-visible]').disabled=!rows.length;this.dialog.querySelector('[data-clear-visible]').disabled=!rows.some(({group,node})=>this.isChosen(group.id,node.id));
  this.dialog.querySelector('[data-apply]').disabled=this.requirePresence&&(this.loading||this.presenceError||!this.presence?.available||!this.presence.clients);
  for(const el of this.dialog.querySelectorAll('[data-config-choice]')){const row=this.presenceIndex.get(presenceKey(el.dataset.source,el.dataset.configChoice));el.indeterminate=!!(this.presence?.available&&row?.present&&row.present<this.presence.clients&&!this.editedNodes.has(presenceKey(el.dataset.source,el.dataset.configChoice))&&!this.allEdited);}
  if(this.saving)this.dialog.querySelectorAll('button,input,select').forEach(el=>el.disabled=true);
 }
 changes(){
  if(!this.requirePresence)return undefined;
  const changes=[];for(const group of this.groups)for(const node of group.nodes){const id=presenceKey(group.id,node.id),chosen=this.isChosen(group.id,node.id),present=this.presenceIndex.get(id)?.present||0,partial=chosen&&present>0&&present<this.presence.clients;if(!this.allEdited&&!this.editedSources.has(group.id)&&!this.editedNodes.has(id)&&!partial)continue;if(chosen!==!!present||partial)changes.push({source_id:group.id,node_id:node.id,selected:chosen});}
  return changes;
 }
 async apply(){
  if(this.saving||this.closed||this.requirePresence&&(this.presenceError||!this.presence?.available))return;this.saving=true;
  const controls=[...this.dialog.querySelectorAll('button,input,select')].map(el=>[el,el.disabled]),status=this.dialog.querySelector('[data-load-status]'),button=this.dialog.querySelector('[data-apply]');
  controls.forEach(([el])=>el.disabled=true);button.textContent=this.t('busy');status.textContent=this.t('applyingSelection');status.className='hint';
  try{await this.onApply(this.selection==null?null:structuredClone(this.selection),this.changes());this.destroy();}
  catch(error){if(!this.closed){status.textContent=error.message||this.t('error');status.className='hint inline-error';}}
  finally{this.saving=false;if(!this.closed){controls.forEach(([el,disabled])=>el.disabled=disabled);button.textContent=this.t('applySelection');this.dialog.querySelector('[data-reload]').disabled=!!this.loading;this.update();}}
 }
 async reload(){
  if(this.loading||this.closed)return;this.loading=true;this.update();const button=this.dialog.querySelector('[data-reload]'),status=this.dialog.querySelector('[data-load-status]');button.disabled=true;status.textContent=this.t('loadingConfigs');status.className='hint';
  try{const groups=await this.load(this.controller.signal);if(this.closed)return;this.groups=groups;if(!this.saving)status.textContent=groups.some(g=>g.nodes.length)?this.t('configsLoaded'):this.t('emptyConfigsHint');this.update();}
  catch(error){if(!this.closed&&!this.controller.signal.aborted){this.presenceError=true;status.textContent=error.message||this.t('error');status.className='hint inline-error';}}
  finally{if(!this.closed){this.loading=false;button.disabled=!!this.saving;this.update();}}
 }
 isManual(source,id){const n=this.presenceIndex.get(presenceKey(source,id));return !!(this.presence?.available&&this.presence.clients>0&&n?.present>0&&n.manual===this.presence.clients&&!n.managed);}
 keepManual(){if(this.selection==null)return;for(const n of this.presence?.nodes||[])if(this.isManual(n.source_id,n.node_id)&&!this.isChosen(n.source_id,n.node_id))this.selection.nodes.push({source_id:n.source_id,node_id:n.node_id});}
 updatePresence(value,error=false){
  const unchanged=JSON.stringify([this.presence?.available,this.presence?.clients,this.presence?.nodes,this.presenceError])===JSON.stringify([value?.available,value?.clients,value?.nodes,error]);
  this.presence=value;this.presenceIndex=presenceIndex(value);this.presenceError=error;if(unchanged)return;
  if(this.requirePresence&&value?.available&&!this.allEdited){
   if(!this.editedNodes.size&&!this.editedSources.size)this.selection=observedSelection(value);
   else{this.manual();for(const group of this.groups)for(const node of group.nodes)if(!this.editedNodes.has(presenceKey(group.id,node.id))&&!this.editedSources.has(group.id))this.setNode(group.id,node.id,!!this.presenceIndex.get(presenceKey(group.id,node.id))?.present,false);}
  }
  this.update();
 }
 updateGroups(groups){this.groups=groups;this.update();}
 destroy(){if(this.closed)return;this.closed=true;this.controller.abort();this.dialog.close();this.dialog.remove();document.body.classList.remove('dialog-open');this.previousFocus?.focus({preventScroll:true});this.onClose?.();}
}
