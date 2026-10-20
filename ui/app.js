import {dictionaries} from './i18n.js';
import {MultiSelect} from './multi-select.js';
import {ConfigPicker,selectionStats} from './config-picker.js';
import {presenceIndex,presenceKey,presenceText,presenceClass,presenceCount,recipientKey} from './panel-presence.js';
const app=document.querySelector('#app'),notice=document.querySelector('#notice');
const base=location.pathname.endsWith('/')?location.pathname:location.pathname+'/';
let language=localStorage.getItem('subbridge-language')||'en';
if(!dictionaries[language])language='en';
const state={auth:false,csrf:'',page:location.hash.slice(1)||'overview',config:null,source:null,panel:null,management:null,current:null,password:'',token:'',adminPassword:'',paused:false,sourceID:'default',groups:[],configGroup:'all',configSearch:'',configSort:'name-asc',configMenuOpen:location.hash==='#configs',catalog:null,catalogKey:'',catalogStatus:'idle',catalogError:''};
let pickers=[],catalogRequest=null;
let configPicker=null;
let presenceRequest=null,pollRunning=false;
Object.assign(state,{presence:null,presenceStatus:'idle',presenceError:'',presenceIndex:new Map(),selectionPending:false});
const t=key=>dictionaries[language][key]||key;
const esc=value=>String(value??'').replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
const paths={brand:'M10 13a5 5 0 0 0 7 .2l4-4a5 5 0 0 0-7-7l-2 2 M14 11a5 5 0 0 0-7-.2l-4 4a5 5 0 0 0 7 7l2-2',overview:'m3 10 9-7 9 7 M5 9v12h5v-7h4v7h5V9',source:'M7 3h10a2 2 0 0 1 2 2v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2 M9 7h5 M9 11h2 M9 16h6 M15 18h2',xui:'M5 3h14a2 2 0 0 1 2 2v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2 M5 15h14a2 2 0 0 1 2 2v2a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-2a2 2 0 0 1 2-2 M7 7h.01 M11 7h.01 M7 18h.01 M11 18h.01',settings:'m9 3-.7 2.1-2 .9-2-.4L2.8 8l1.3 1.7-.2 2.3-1.5 1.5 1.3 2.8 2.2.1 1.7 1.5.5 2.1h3l1-1.8 2.2-.6 1.9 1 2.2-2-1-1.9.6-2.1 1.8-1V8.8l-2.2-.5L18 6.5l-.1-2.1-2.7-1.2-1.6 1.5-2.2.2L9 3 M14.8 11.5a3.3 3.3 0 1 1-6.6 0 3.3 3.3 0 0 1 6.6 0',logout:'M10 4H4v16h6 M8 12h13 m-4-4 4 4-4 4',copy:'M9 8h11v13H9z M5 16H3V3h11v2',back:'m14 5-7 7 7 7'};
const icon=name=>`<svg class="icon" viewBox="0 0 24 24" aria-hidden="true"><path d="${paths[name]}"/></svg>`;
const languages=()=>`<div class="languages" aria-label="Language"><button type="button" data-language="en" class="${language==='en'?'active':''}" aria-pressed="${language==='en'}">EN</button><button type="button" data-language="ru" class="${language==='ru'?'active':''}" aria-pressed="${language==='ru'}">RU</button></div>`;
function toast(message){notice.textContent=message;notice.hidden=false;clearTimeout(toast.timer);toast.timer=setTimeout(()=>notice.hidden=true,4500);}
async function api(path,body,options={}){
 const response=await fetch(base+'api/'+path,{method:body===undefined?'GET':'POST',signal:options.signal,headers:{'Accept-Language':language,...(body===undefined?{}:{'Content-Type':'application/json','X-CSRF-Token':state.csrf})},body:body===undefined?undefined:JSON.stringify(body)});
 const result=await response.json().catch(()=>({}));
 if(!response.ok){if(response.status===401&&path!=='login'){state.auth=false;render();}throw Error(result.error||t('error'));}return result;
}
function field(key,id,value='',extra='',placeholder=''){return `<div class="field"><label for="${id}">${esc(t(key))}</label><input id="${id}" name="${id}" value="${esc(value)}" placeholder="${esc(placeholder)}" ${extra}></div>`;}
function select(key,id,value,options,translateOptions=true){return `<div class="field"><label for="${id}">${esc(t(key))}</label><select id="${id}" name="${id}">${options.map(([v,label])=>`<option value="${esc(v)}" ${value===v?'selected':''}>${esc(translateOptions?t(label):label)}</option>`).join('')}</select></div>`;}
function section(title,content){return `<section class="section"><h2>${esc(t(title))}</h2>${content}</section>`;}
function panelDefault(){return {enabled:false,url:'',auth_mode:'password',username:'',api_token_env:'SUBBRIDGE_PANEL_TOKEN',username_env:'SUBBRIDGE_PANEL_USERNAME',password_env:'SUBBRIDGE_PANEL_PASSWORD',all_clients:false,emails:[],inbound_ids:[],sync_seconds:60,name_prefix:'',delivery_mode:'direct',config_selection:{sources:[],nodes:[]}};}
async function loadConfig(reset='all'){const result=await api('config');state.config=result.config;if(reset==='all'||reset==='source')state.source=structuredClone(result.config);if(reset==='all'||reset==='xui')state.panel=structuredClone(result.config.panel||panelDefault());}
async function loadAll(){await Promise.all([loadConfig(),api('management').then(v=>state.management=v),api('state').then(v=>state.current=v),api('configurations').then(v=>state.groups=v.groups)]);}
function capture(){
 const form=document.querySelector('form[data-page]');if(!form)return;const fd=new FormData(form),get=key=>fd.get(key)??'',num=key=>Number(get(key));
 if(state.page==='source'){
  const c=state.source,entry=activeSource();Object.assign(entry.source,{url:get('sourceURL'),hwid:get('hwid').trim(),hwid_attempts:num('attempts'),user_agent:get('userAgent'),app_version:get('appVersion'),device_os:get('os'),os_version:get('osVersion'),device_model:get('deviceModel')});if(state.sourceID==='default')c.source_name=get('sourceName');else c.sources.find(s=>s.id===state.sourceID).name=get('sourceName');c.refresh_seconds=num('interval');c.export_profile=get('exportProfile');c.listen=get('subscriptionListen');c.public_url=get('publicURL');
 }else if(state.page==='settings'){
  const m=state.management;Object.assign(m,{ip:get('bindIP').trim(),port:num('panelPort'),path:get('panelPath').trim(),ssh_host:get('server').trim(),ssh_port:num('sshPort'),ssh_user:get('sshUser').trim(),username:get('username')});state.adminPassword=get('newPassword');
 }else if(state.page==='xui'){
  const previous=connectionKey(),p=state.panel;Object.assign(p,{enabled:fd.has('enabled'),url:get('panelURL').replace(/\/+$/,''),auth_mode:get('authMode'),username:get('username'),all_clients:fd.has('allClients'),sync_seconds:num('syncInterval'),name_prefix:get('prefix'),delivery_mode:get('deliveryMode')||'direct'});state.password=get('password');state.token=get('apiToken');if(previous!==connectionKey())resetCatalog();
 }
}
function sourceEntries(c=state.source){return [{id:'default',name:c.source_name||t('primarySource'),source:c.source},...(c.sources||[])];}
function activeSource(){const entries=sourceEntries();return entries.find(s=>s.id===state.sourceID)||entries[0];}
function sourcePage(){
 const c=state.source,entry=activeSource(),s=entry.source;state.sourceID=entry.id;
 return `<form data-page="source"><div class="source-toolbar">${select('chooseSource','sourceSelect',entry.id,sourceEntries().map(s=>[s.id,s.name]),false)}<div class="actions"><button type="button" data-add-source>${esc(t('addSource'))}</button><button type="button" data-remove-source>${esc(t('removeSource'))}</button></div></div><div class="sections">${section('sourceSection',`<div class="formgrid"><div class="full">${field('sourceName','sourceName',entry.name,'maxlength="256"')}</div><div class="full">${field('sourceURL','sourceURL',s.url,'maxlength="32768"','https://provider.example/subscription')}</div>${field('hwid','hwid',s.hwid,'maxlength="512"',t('automatic'))}${field('attempts','attempts',s.hwid_attempts||3,'type="number" min="1" max="20" required')}<p class="hint full">${esc(t('hwidHint'))}</p></div>`)}${section('requestProfile',`<div class="formgrid">${field('userAgent','userAgent',s.user_agent)}${field('appVersion','appVersion',s.app_version)}${field('os','os',s.device_os)}${field('osVersion','osVersion',s.os_version,'',t('optional'))}<div class="full">${field('deviceModel','deviceModel',s.device_model,'',t('optional'))}</div></div>`)}${section('sharedSettings',`<div class="formgrid">${field('interval','interval',c.refresh_seconds,'type="number" min="30" max="86400" required')}${select('exportProfile','exportProfile',c.export_profile,[['original','original'],['v2rayn','compatible']])}</div><p class="hint">${esc(t('sharedHint'))}</p>`)}</div><div class="actions"><button class="primary" type="submit">${esc(t('saveSource'))}</button><button type="button" data-command="refresh">${esc(t('refresh'))}</button></div><details><summary>${esc(t('advanced'))}</summary><div class="formgrid">${field('subscriptionListen','subscriptionListen',c.listen,'required')}${field('publicURL','publicURL',c.public_url,'required')}</div><p class="hint">${esc(t('retryHint'))}</p></details></form>`;
}

function connectionKey(){const p=state.panel||{};return JSON.stringify([p.url,p.auth_mode,p.username,p.api_token_env,p.username_env,p.password_env,state.password,state.token]);}
function resetCatalog(){catalogRequest?.abort();catalogRequest=null;state.catalog=null;state.catalogKey='';state.catalogStatus='idle';state.catalogError='';}
function catalogItems(kind){
 const data=state.catalog||{clients:[],inbounds:[]};
 if(kind==='inbounds')return data.inbounds.map(row=>({value:row.id,label:row.remark||`#${row.id}`,detail:`#${row.id} · ${row.protocol.toUpperCase()} · :${row.port}`,number:row.id,port:row.port}));
 const inbounds=new Map(data.inbounds.map(row=>[row.id,row.remark||`#${row.id}`]));
 return data.clients.map(row=>({value:row.email,label:row.email,detail:(row.inboundIds||[]).map(id=>`#${id} ${inbounds.get(id)||''}`).join(' · ')}));
}
function refreshCatalogView(){
 const button=document.querySelector('[data-load-catalog]');if(!button)return;
 button.disabled=state.catalogStatus==='loading';button.textContent=t(state.catalogStatus==='loading'?'loadingLists':state.catalog?'reloadLists':'connectLists');
 const status=document.querySelector('#catalog-status');status.className='hint'+(state.catalogError?' inline-error':'');
 status.textContent=state.catalogError||(state.catalog?`${t('connected')} · ${t('emails')}: ${state.catalog.clients.length} · ${t('inbounds')}: ${state.catalog.inbounds.length}`:t('connectHint'));
 pickers.forEach((picker,i)=>picker.update(catalogItems(i?'inbounds':'clients'),state.panel.all_clients));
}
function mountPickers(){
 const labels={choose:t('chooseItems'),search:t('searchItems'),sort:t('sort'),selected:t('selectedCount'),selectVisible:t('selectVisible'),clear:t('clearSelection'),missing:t('missingSelection'),noResults:t('noResults'),empty:t('connectHint')};
 const orders=[['name-asc',t('nameAsc')],['name-desc',t('nameDesc')],['selected',t('selectedFirst')]];
 pickers=[new MultiSelect(document.querySelector('#client-picker'),{id:'emails',label:t('emails'),language,labels,orders,selected:state.panel.emails||[],items:catalogItems('clients'),disabled:state.panel.all_clients,onChange:values=>{state.panel.emails=values;}}),new MultiSelect(document.querySelector('#inbound-picker'),{id:'inbounds',label:t('inbounds'),language,labels,orders:[...orders,['id-asc',t('idAsc')],['id-desc',t('idDesc')],['port-asc',t('portAsc')],['port-desc',t('portDesc')]],selected:state.panel.inbound_ids||[],items:catalogItems('inbounds'),disabled:state.panel.all_clients,missingLabel:value=>`#${value}`,onChange:values=>{state.panel.inbound_ids=values.map(Number);}})];
 refreshCatalogView();
}
async function loadCatalog(){
 capture();const key=connectionKey();if(state.catalogStatus==='loading')return;
 catalogRequest?.abort();const request=new AbortController();catalogRequest=request;state.catalogStatus='loading';state.catalogError='';refreshCatalogView();
 try{const panel={...state.panel,config_selection:null};const data=await api('panel-options',{panel,password:state.password,token:state.token},{signal:request.signal});if(catalogRequest!==request||key!==connectionKey())return;state.catalog=data;state.catalogKey=key;state.catalogStatus='ready';}
 catch(error){if(request.signal.aborted)return;state.catalogStatus='error';state.catalogError=error.message||t('error');}
 finally{if(catalogRequest===request){catalogRequest=null;refreshCatalogView();}}
}

function selectedConfigurationsSummary(){
 if(state.presence?.available)return `${t('inPanel')}: ${presenceCount(state.presence)}`;
 if(state.presenceStatus==='loading')return t('checkingPanel');
 const stats=selectionStats(state.panel.config_selection,state.groups);
 return `${t(state.panel.config_selection==null?'allSelectedConfigs':'selectedCount')}: ${stats.count}${stats.missing?' · '+t('missingCount')+': '+stats.missing:''}`;
}
function refreshSelectedConfigurations(){const summary=document.querySelector('[data-config-selection-summary]');if(summary)summary.textContent=selectedConfigurationsSummary();}
function refreshIntegrationStatus(){
 const root=document.querySelector('#integration-status');if(!root)return;
 const s=state.current||{};let message='',error=false;
 if(s.sync_issue){message=s.sync_issue;error=true;}
 else if(state.presenceError){message=t('presenceUnavailable')+' '+state.presenceError;error=true;}
 else if(state.selectionPending)message=t('syncing');
 else if(state.presence?.available){message=`${t('inPanel')}: ${presenceCount(state.presence)} · ${t('syncedClients')}: ${state.presence.clients} · ${t('checkedAt')}: ${new Date(state.presence.checked_at).toLocaleTimeString(language)}`;if(state.config.panel?.enabled===false)message+=' · '+t('autoSyncDisabled');}
 else if(state.presenceStatus==='loading')message=t('checkingPanel');
 else if(state.presence?.configured&&state.presence.mode==='subscription')message=t('presenceURLMode');
 else if(s.activity==='sync')message=t('syncing');
 else if(state.config.panel?.enabled===false||!state.config.panel){message=t('integrationDisabled');if(s.last_sync&&!s.last_sync.startsWith('0001'))message=`${t('syncedConfigs')}: ${s.synced_nodes} · ${t('syncedClients')}: ${s.synced_clients} · ${t('autoSyncDisabled')}`;}
 else if(s.activity)message=t(s.activity==='sync'?'syncing':'refreshing');
 else if(s.last_sync&&!s.last_sync.startsWith('0001'))message=`${t('syncedConfigs')}: ${s.synced_nodes} · ${t('syncedClients')}: ${s.synced_clients} · ${new Date(s.last_sync).toLocaleString(language)}`;
 else message=t('syncPending');
 root.innerHTML=`<div class="notice ${error?'error':''}">${esc(message)}</div>`;
}
function presenceConnectionKey(){const p=state.config?.panel;return JSON.stringify(p?[p.url,p.auth_mode,p.username,p.delivery_mode,p.emails,p.inbound_ids,p.all_clients]:null);}
async function loadPresence(force=false,signal){
 if(presenceRequest&&!force)return presenceRequest;
 const connection=presenceConnectionKey();state.presenceStatus='loading';refreshIntegrationStatus();
 const request=(async()=>{try{
  const value=await api('panel-state'+(force?'?refresh=1':''),undefined,{signal});
  if(!state.auth||connection!==presenceConnectionKey())return value;
  const changed=JSON.stringify([state.presence?.available,state.presence?.clients,state.presence?.nodes])!==JSON.stringify([value.available,value.clients,value.nodes]);
  state.presence=value;state.presenceIndex=presenceIndex(value);state.presenceStatus='ready';state.presenceError='';configPicker?.updatePresence(value);refreshSelectedConfigurations();refreshIntegrationStatus();if(changed&&state.page==='configs')renderConfigurationResults();return value;
 }catch(error){
  if(!signal?.aborted&&connection===presenceConnectionKey()){state.presenceStatus='error';state.presenceError=error.message||t('error');state.presence=state.presence?{...state.presence,available:false}:null;configPicker?.updatePresence(state.presence,true);refreshIntegrationStatus();if(state.page==='configs')renderConfigurationResults();}
  throw error;
 }finally{if(presenceRequest===request)presenceRequest=null;const button=document.querySelector('[data-check-presence]');if(button)button.disabled=false;}})();presenceRequest=request;return request;
}
function configurationPresenceBadge(source,node){const p=state.presence;if(!p?.configured||p.mode!=='direct')return '';const row=state.presenceIndex.get(presenceKey(source,node.id));return `<span class="presence-badge ${presenceClass(p,row)}">${esc(presenceText(p,row,t))}</span>`;}
function openConfigPicker(){
 capture();configPicker?.destroy();
 const saved=state.config.panel,canSync=!!(saved?.url&&(saved.all_clients||saved.emails?.length||saved.inbound_ids?.length));
 configPicker=new ConfigPicker({groups:state.groups,selection:state.panel.config_selection,language,t,
  presence:state.presence,requirePresence:canSync&&(saved.delivery_mode||'direct')==='direct',
  saveHint:t(canSync?'selectionSaveHint':'selectionDraftHint'),
  load:async signal=>{const result=await api('configurations',undefined,{signal});state.groups=result.groups;if(canSync)await loadPresence(true,signal);refreshSelectedConfigurations();return result.groups;},
  onApply:async (selection,changes)=>{
   if(canSync){await api('config-selection',{selection,changes});state.config.panel.config_selection=structuredClone(selection);state.selectionPending=true;state.presence=null;state.presenceStatus='loading';const choose=document.querySelector('[data-choose-configs]');if(choose)choose.disabled=true;refreshIntegrationStatus();}
   state.panel.config_selection=selection;refreshSelectedConfigurations();toast(t(canSync?'selectionQueued':'selectionDraftHint'));if(canSync)setTimeout(poll,250);
  },onClose:()=>{configPicker=null;}});
}

function configurationsPage(){return `<div class="config-toolbar"><div class="field"><label for="configSearch">${esc(t('searchConfigs'))}</label><input type="search" id="configSearch" value="${esc(state.configSearch)}" placeholder="${esc(t('searchConfigsHint'))}"></div>${select('sort','configSort',state.configSort,[['name-asc','nameAsc'],['name-desc','nameDesc'],['address','sortAddress'],['protocol','sortProtocol']])}<button type="button" data-command="refresh">${esc(t('refresh'))}</button></div><div id="configuration-results"></div>`;}
function renderConfigurationResults(){
 const root=document.querySelector('#configuration-results');if(!root)return;
 document.querySelectorAll('.nav-subscriptions [data-group] .nav-count').forEach(el=>{el.textContent=state.groups.find(g=>g.id===el.closest('[data-group]').dataset.group)?.nodes.length||0;});
 const collator=new Intl.Collator(language,{numeric:true,sensitivity:'base'}),terms=state.configSearch.toLocaleLowerCase(language).trim().split(/\s+/).filter(Boolean);
 const groups=state.groups.filter(g=>state.configGroup==='all'||state.configGroup===g.id);
 root.innerHTML=groups.map((group,index)=>{
  const name=group.name||sourceEntries(state.config).find(s=>s.id===group.id)?.name||`${t('source')} ${index+1}`;
  const nodes=group.nodes.map((node,index)=>({...node,index})).filter(node=>terms.every(term=>(name+' '+node.name+' '+node.protocol+' '+node.address+' '+node.port).toLocaleLowerCase(language).includes(term))).sort((a,b)=>{
   const field=state.configSort==='address'?'address':state.configSort==='protocol'?'protocol':'name';return (state.configSort==='name-desc'?-1:1)*collator.compare(a[field],b[field]);
  });
  return `<section class="section config-group" data-config-source="${esc(group.id)}"><div class="config-group-header"><div><h2>${esc(name)} <span class="count-badge">${nodes.length} / ${group.nodes.length}</span></h2><p class="hint">${esc(t('lastUpdate'))}: ${group.updated_at&&!group.updated_at.startsWith('0001')?esc(new Date(group.updated_at).toLocaleString(language)):esc(t('never'))}</p></div><div class="actions"><button type="button" data-group-copy="${esc(group.id)}">${esc(t('copyURL'))}</button><button type="button" data-group-download="${esc(group.id)}">${esc(t('download'))}</button></div></div>${group.issue?`<div class="notice error">${esc(group.issue)}</div>`:''}<div class="config-nodes">${nodes.map(node=>`<article class="config-node"><div class="config-node-header"><div class="config-node-title"><span class="protocol-badge">${esc(node.protocol.toUpperCase())}</span> ${configurationPresenceBadge(group.id,node)}<h3>${esc(node.name)}</h3><p>${esc(node.address)}${node.port?':'+esc(node.port):''}</p></div><button type="button" data-node-group="${esc(group.id)}" data-node-copy="${node.index}">${icon('copy')}${esc(t('copy'))}</button></div><details><summary>${esc(t('showURI'))}</summary><code class="node-uri">${esc(node.uri)}</code></details></article>`).join('')||`<p class="empty">${esc(t(terms.length?'noResults':'emptyNodes'))}</p>`}</div></section>`;
 }).join('')||`<div class="notice">${esc(t('emptyNodes'))}</div>`;
 root.querySelectorAll('[data-node-copy]').forEach(button=>button.addEventListener('click',()=>{const group=state.groups.find(g=>g.id===button.dataset.nodeGroup);clipboard(group.nodes[Number(button.dataset.nodeCopy)].uri).catch(error=>toast(error.message));}));
 root.querySelectorAll('[data-group-copy]').forEach(button=>button.addEventListener('click',()=>clipboard(state.groups.find(g=>g.id===button.dataset.groupCopy).subscription_url).catch(error=>toast(error.message))));
 root.querySelectorAll('[data-group-download]').forEach(button=>button.addEventListener('click',()=>downloadConfigurations(button.dataset.groupDownload)));
}
function downloadConfigurations(source){const a=document.createElement('a');a.href=base+'api/download?protocol=all&source='+encodeURIComponent(source);a.download='subbridge-configs.txt';a.click();}
function navigation(){
 const groupButton=`<button type="button" data-config-toggle class="${state.page==='configs'?'selected':''}" aria-expanded="${state.configMenuOpen}">${icon('source')}<span>${esc(t('configs'))}</span><span class="nav-arrow" aria-hidden="true">${state.configMenuOpen?'⌃':'⌄'}</span></button>`;
 const groups=state.config?sourceEntries(state.config).filter(s=>s.source.url||(!state.config.sources?.length&&s.id==='default')):[];
 const children=state.configMenuOpen?`<div class="nav-subscriptions"><button type="button" data-page="configs" data-group="all" class="${state.page==='configs'&&state.configGroup==='all'?'selected':''}">${esc(t('allSources'))}</button>${groups.map(source=>`<button type="button" data-page="configs" data-group="${esc(source.id)}" class="${state.page==='configs'&&state.configGroup===source.id?'selected':''}"><span>${esc(source.name)}</span><span class="nav-count">${state.groups.find(g=>g.id===source.id)?.nodes.length||0}</span></button>`).join('')}</div>`:'';
 return ['overview','source','configs','xui','settings'].map(page=>page==='configs'?`<div class="nav-group">${groupButton}${children}</div>`:(page==='settings'?'<div class="divider"></div>':'')+`<button type="button" data-page="${page}" class="${state.page===page?'selected':''}" ${state.page===page?'aria-current="page"':''}>${icon(page)}<span class="nav-label">${esc(t(page))}</span></button>`).join('');
}
function sshCommand(){const m=state.management;let target=m.ip;if(target==='0.0.0.0')target='127.0.0.1';if(target==='::')target='::1';if(target.includes(':'))target='['+target+']';return `ssh -p ${m.ssh_port} -L ${m.port}:${target}:${m.port} ${m.ssh_user}@${m.ssh_host||'server.example.com'}`;}
function settingsPage(){const m=state.management;return `<form data-page="settings"><div class="sections">${section('network',`<div class="formgrid three">${field('bindIP','bindIP',m.ip,'required')}${field('panelPort','panelPort',m.port,'type="number" min="1" max="65535" required')}${field('panelPath','panelPath',m.path,'required')}</div><p class="hint">${esc(t('loopbackHint'))}</p>`)}${section('ssh',`<div class="formgrid three">${field('server','server',m.ssh_host,'','server.example.com')}${field('sshPort','sshPort',m.ssh_port,'type="number" min="1" max="65535" required')}${field('sshUser','sshUser',m.ssh_user,'required')}</div><div class="code"><code id="ssh-command">${esc(sshCommand())}</code><button type="button" data-copy-ssh aria-label="${esc(t('copy'))}">${icon('copy')}</button></div>`)}${section('account',`<div class="formgrid">${field('username','username',m.username,'autocomplete="username" required')}${field('newPassword','newPassword',state.adminPassword,'type="password" autocomplete="new-password" minlength="8"',t('keepPassword'))}</div>`)}</div><div class="actions"><button class="primary" type="submit">${esc(t('saveSettings'))}</button><span class="hint">${esc(t('immediate'))}</span></div></form>`;}
function xuiPage(){const p=state.panel,enabled=p.enabled!==false;return `<form data-page="xui"><div class="sections">${section('xui',`<label class="check"><input type="checkbox" name="enabled" ${enabled?'checked':''}>${esc(t('enabled'))}</label><div class="formgrid gap-top"><div class="full">${field('panelURL','panelURL',p.url,enabled?'required':'','https://panel.example:2053/secret-prefix')}<p class="hint">${esc(t('baseHint'))}</p></div>${select('authMode','authMode',p.auth_mode||'environment',[['password','passwordAuth'],['token','tokenAuth'],['environment','environmentAuth']])}${p.auth_mode==='password'?field('username','username',p.username,'autocomplete="off"')+field('password','password',state.password,'type="password" autocomplete="new-password"',t('keepPassword')):p.auth_mode==='token'?field('apiToken','apiToken',state.token,'type="password" autocomplete="new-password"',t('keepToken')):''}</div><div class="actions"><button type="button" data-load-catalog>${esc(t('connectLists'))}</button><span id="catalog-status" class="hint" role="status"></span></div>`)}${section('recipients',`<label class="check"><input type="checkbox" name="allClients" ${p.all_clients?'checked':''}>${esc(t('allClients'))}</label><div class="formgrid gap-top"><div class="field" id="client-picker"></div><div class="field" id="inbound-picker"></div><p class="hint full">${esc(t('selectionUnion'))}</p>${field('syncInterval','syncInterval',p.sync_seconds||60,'type="number" min="30" max="86400" required')}</div><p class="hint">${esc(t('recipientsHint'))}</p>`)}${section('exportedConfigs',`${select('deliveryMode','deliveryMode',p.delivery_mode||'direct',[['direct','directMode'],['subscription','subscriptionMode']])}<p class="hint">${esc(t(p.delivery_mode==='subscription'?'subscriptionDeliveryHint':'directDeliveryHint'))}</p><div class="configuration-selection-summary gap-top"><div><p data-config-selection-summary>${esc(selectedConfigurationsSummary())}</p><p class="hint">${esc(t('configurationSelectionHint'))}</p></div><div class="actions"><button type="button" data-check-presence>${esc(t('checkPanel'))}</button><button type="button" data-choose-configs>${esc(t('chooseConfigs'))}</button></div></div><div class="formgrid gap-top">${field('prefix','prefix',p.name_prefix)}<p class="hint">${esc(t('prefixHint'))}</p></div><p class="hint">${esc(t('directHint'))}</p>`)}</div><div id="integration-status" role="status"></div><div class="actions"><button class="primary" type="submit">${esc(t('saveXui'))}</button><button type="button" data-command="sync" ${enabled?'':'disabled'}>${esc(t('sync'))}</button></div></form>`;}
function overviewPage(){const s=state.current||{},hasNodes=s.nodes>0;let status=t(s.status||'unavailable');if(s.activity)status=t(s.activity==='sync'?'syncing':'refreshing');const date=s.last_success&&!s.last_success.startsWith('0001')?new Date(s.last_success).toLocaleString(language):t('never');return `${s.status==='unconfigured'?`<div class="notice">${esc(t('unconfigured'))}</div>`:''}<div class="stats"><div><div class="stat-label">${esc(t('status'))}</div><div class="stat-value ${s.status==='ok'?'good':''}" data-status>${esc(status)}</div></div><div><div class="stat-label">${esc(t('nodes'))}</div><div class="stat-value" data-nodes>${esc(s.nodes||0)}</div></div><div><div class="stat-label">${esc(t('lastUpdate'))}</div><div class="stat-value">${esc(date)}</div></div></div>${s.issue?`<div class="notice error">${esc(s.issue)}</div>`:''}${s.sync_issue?`<div class="notice error">${esc(s.sync_issue)}</div>`:''}<div class="sections">${section('output',`<div class="subscription-line"><input aria-label="${esc(t('copyURL'))}" value="${esc(s.subscription_url)}" readonly><button type="button" data-copy-url>${icon('copy')}${esc(t('copy'))}</button></div><p class="protocols">${Object.entries(s.protocols||{}).map(([k,v])=>esc(k.toUpperCase())+' · '+v).join(' / ')||esc(t('emptyNodes'))}</p><div class="actions"><button type="button" data-command="refresh" ${s.activity?'disabled':''}>${esc(t('refresh'))}</button><button type="button" data-download="all" ${!hasNodes?'disabled':''}>${esc(t('download'))}</button><button type="button" data-download="vless" ${!hasNodes?'disabled':''}>${esc(t('downloadVless'))}</button></div>`)}${section('identity',`<div class="stat-value">${esc(s.hwid||'—')}</div><p class="hint">${esc(t(s.hwid_auto?'autoMode':'manualMode'))}${s.attempts?' · '+esc(t('attempts'))+': '+s.attempts:''}</p>`)}${s.warnings?.length?section('warnings',`<ul class="plainlist">${s.warnings.map(v=>`<li>${esc(v)}</li>`).join('')}</ul>`):''}${section('logs',`<pre class="logs">${esc((s.logs||[]).join('\n')||t('noLogs'))}</pre><p class="hint">${esc(t('logsOriginal'))}</p>`)}</div>`;}
function render(){
 configPicker?.destroy();
 if(state.auth&&['configs','xui'].includes(state.page)&&state.presenceStatus==='idle')queueMicrotask(()=>loadPresence().catch(()=>{}));
 pickers.forEach(picker=>picker.destroy());pickers=[];
 document.documentElement.lang=language;document.title='SubBridge · '+t(state.page);app.setAttribute('aria-busy','false');
 if(!state.auth){app.innerHTML=`<main class="login-page"><div class="loginbox"><div class="brand">${icon('brand')}SubBridge</div><h1>${esc(t('welcome'))}</h1><p class="subtitle">${esc(t('loginHint'))}</p><form id="login-form">${field('username','login-user','','required autocomplete="username"')}${field('password','login-password','','type="password" required autocomplete="current-password"')}<div id="login-error" class="inline-error" role="alert"></div><button type="submit" class="primary">${esc(t('login'))}</button></form>${languages()}</div></main>`;bind();return;}
 if(!['overview','source','configs','xui','settings'].includes(state.page))state.page='overview';
 const title=t(state.page+'Title'),subtitle=t(state.page+'Sub');
 app.innerHTML=`<div class="shell"><aside class="sidebar"><div class="brand">${icon('brand')}SubBridge</div><nav aria-label="SubBridge">${navigation()}</nav><div class="sidefooter">v${esc(state.current?.version||'1.1.0')}</div></aside><main class="main ${state.page}-page"><div class="topbar"><div class="breadcrumb">${state.page==='settings'?icon('back')+esc(t('settingsBreadcrumb')):''}</div><div class="topright">${languages()}<button type="button" class="logout" data-logout>${icon('logout')}${esc(t('logout'))}</button></div></div><header class="pageheader"><h1>${esc(title)}</h1><p class="subtitle">${esc(subtitle)}</p></header><div id="page-notice"></div>${state.page==='settings'?settingsPage():state.page==='source'?sourcePage():state.page==='xui'?xuiPage():state.page==='configs'?configurationsPage():overviewPage()}</main></div>`;bind();
 if(state.page==='configs')renderConfigurationResults();
 if(state.page==='xui'){
  api('configurations').then(result=>{state.groups=result.groups;if(state.page==='xui')refreshSelectedConfigurations();}).catch(()=>{});
  if(state.catalogKey&&state.catalogKey!==connectionKey())resetCatalog();mountPickers();refreshIntegrationStatus();
  const saved=state.config.panel;
  if(state.catalogStatus==='idle'&&saved?.url&&saved.url===state.panel.url&&saved.auth_mode===state.panel.auth_mode&&!state.password&&!state.token)queueMicrotask(()=>{if(state.page==='xui'&&state.catalogStatus==='idle')loadCatalog();});
 }
}
async function clipboard(value){try{await navigator.clipboard.writeText(value);}catch{const node=document.createElement('textarea');node.value=value;document.body.append(node);node.select();if(!document.execCommand('copy')){node.remove();throw Error(t('error'));}node.remove();}toast(t('copied'));}
function pageNotice(message,error=false){const el=document.querySelector('#page-notice');if(el)el.innerHTML=`<div class="notice ${error?'error':''}" role="status">${esc(message)}</div>`;}
async function submitPage(event){event.preventDefault();capture();const button=event.submitter;button.disabled=true;
 try{
  if(state.page==='settings'){
   const result=await api('management',{config:state.management,password:state.adminPassword});state.adminPassword='';
   if(result.address_changed){state.paused=true;pageNotice(t('changedAddress'));return;}
   if(result.credentials_changed){state.auth=false;state.csrf='';render();toast(t('credentialsChanged'));return;}
   render();toast(t('saved'));return;
  }
  const next=structuredClone(state.config);
  if(state.page==='source'){for(const key of ['source','source_name','sources','listen','public_url','refresh_seconds','export_profile'])next[key]=state.source[key];}
  if(state.page==='xui')next.panel=state.panel;
  const recipientsChanged=state.page==='xui'&&recipientKey(state.config.panel)!==recipientKey(next.panel),hasRecipients=!!(next.panel?.url&&(next.panel.all_clients||next.panel.emails?.length||next.panel.inbound_ids?.length));
  await api('config',{config:next,password:state.page==='xui'?state.password:'',token:state.page==='xui'?state.token:''});
  if(state.page==='xui'){state.password='';state.token='';state.presence=null;state.presenceStatus='idle';state.presenceError='';}await loadConfig(state.page);state.current=await api('state');state.groups=(await api('configurations')).groups;render();
  if(recipientsChanged&&hasRecipients&&state.page==='xui'){toast(t('recipientsSavedApply'));openConfigPicker();}
  else toast(t(state.page==='xui'&&state.config.panel?.enabled===false?'savedDisabled':'saved'));
 }catch(error){pageNotice(error.message||t('error'),true);}finally{button.disabled=false;}
}
function bind(){
 document.querySelectorAll('[data-language]').forEach(el=>el.addEventListener('click',()=>{capture();language=el.dataset.language;localStorage.setItem('subbridge-language',language);clearTimeout(toast.timer);notice.hidden=true;render();if(state.auth&&!state.paused)api('state').then(v=>{state.current=v;if(state.page==='overview')render();}).catch(()=>{});}));
 document.querySelectorAll('nav [data-page]').forEach(el=>el.addEventListener('click',()=>{capture();state.page=el.dataset.page;if(state.page==='configs')state.configGroup=el.dataset.group||'all';history.replaceState(null,'','#'+state.page);render();scrollTo(0,0);}));
 document.querySelector('[data-config-toggle]')?.addEventListener('click',()=>{capture();state.configMenuOpen=!state.configMenuOpen;state.page='configs';state.configGroup='all';history.replaceState(null,'','#configs');render();scrollTo(0,0);});
 document.querySelector('[data-logout]')?.addEventListener('click',async()=>{try{await api('logout',{});}catch{}state.auth=false;state.csrf='';state.password='';state.token='';state.groups=[];state.presence=null;state.presenceStatus='idle';state.presenceError='';state.presenceIndex=new Map();resetCatalog();render();});
 document.querySelector('#login-form')?.addEventListener('submit',async event=>{event.preventDefault();const button=event.submitter;button.disabled=true;try{const result=await api('login',{username:document.querySelector('#login-user').value,password:document.querySelector('#login-password').value});state.auth=true;state.csrf=result.csrf;await loadAll();render();}catch(error){document.querySelector('#login-error').textContent=error.message||t('error');}finally{button.disabled=false;}});
 document.querySelector('form[data-page]')?.addEventListener('submit',submitPage);
 document.querySelector('#authMode')?.addEventListener('change',()=>{capture();render();});
 document.querySelector('#deliveryMode')?.addEventListener('change',()=>{capture();render();});
 document.querySelector('input[name="enabled"]')?.addEventListener('change',()=>{capture();render();});
 document.querySelector('input[name="allClients"]')?.addEventListener('change',()=>{capture();refreshCatalogView();});
 document.querySelector('[data-load-catalog]')?.addEventListener('click',loadCatalog);
 document.querySelector('[data-choose-configs]')?.addEventListener('click',openConfigPicker);
 document.querySelector('[data-check-presence]')?.addEventListener('click',async event=>{event.currentTarget.disabled=true;try{await loadPresence(true);}catch{}});
 document.querySelectorAll('form[data-page="xui"] #panelURL,form[data-page="xui"] #username,form[data-page="xui"] #password,form[data-page="xui"] #apiToken').forEach(input=>input.addEventListener('input',()=>{capture();refreshCatalogView();}));
 document.querySelector('#sourceSelect')?.addEventListener('change',event=>{capture();state.sourceID=event.target.value;render();});
 document.querySelector('[data-add-source]')?.addEventListener('click',()=>{capture();const entries=state.source.sources||[];if(entries.length>=31){toast(t('sourceLimit'));return;}const id=Array.from(crypto.getRandomValues(new Uint8Array(16)),byte=>byte.toString(16).padStart(2,'0')).join('');state.source.sources=[...entries,{id,name:`${t('sourceLabel')} ${entries.length+2}`,source:{url:'',hwid:'',hwid_attempts:3,user_agent:'v2raytun/android',app_version:'5.26.83',device_os:'Android'}}];state.sourceID=id;render();document.querySelector('#sourceName')?.focus();});
 document.querySelector('[data-remove-source]')?.addEventListener('click',()=>{if(!confirm(t('removeSourceConfirm')))return;capture();if(state.sourceID==='default'){state.source.source.url='';state.source.source_name='';}else state.source.sources=state.source.sources.filter(source=>source.id!==state.sourceID);state.sourceID=state.source.sources?.[0]?.id||'default';render();});
 document.querySelector('#configSearch')?.addEventListener('input',event=>{state.configSearch=event.target.value;renderConfigurationResults();});
 document.querySelector('#configSort')?.addEventListener('change',event=>{state.configSort=event.target.value;renderConfigurationResults();});
 document.querySelector('form[data-page="settings"]')?.addEventListener('input',()=>{capture();document.querySelector('#ssh-command').textContent=sshCommand();});
 document.querySelector('[data-copy-ssh]')?.addEventListener('click',()=>clipboard(sshCommand()).catch(e=>toast(e.message)));
 document.querySelector('[data-copy-url]')?.addEventListener('click',()=>clipboard(state.current.subscription_url).catch(e=>toast(e.message)));
 document.querySelectorAll('[data-command]').forEach(button=>button.addEventListener('click',async()=>{button.disabled=true;try{await api(button.dataset.command,{});toast(t('queued'));setTimeout(poll,250);}catch(error){pageNotice(error.message,true);}finally{button.disabled=false;}}));
 document.querySelectorAll('[data-download]').forEach(button=>button.addEventListener('click',()=>{const a=document.createElement('a');a.href=base+'api/download?protocol='+button.dataset.download+'&profile='+encodeURIComponent(state.config.export_profile);a.download='subbridge-configs.txt';a.click();}));
}
async function poll(){
 if(!state.auth||state.paused||pollRunning)return;pollRunning=true;
 try{
  const next=await api('state');state.current=next;
  let force=false;if(state.selectionPending&&(next.sync_issue||next.last_sync&&!next.last_sync.startsWith('0001'))){state.selectionPending=false;state.presenceStatus='idle';force=true;const choose=document.querySelector('[data-choose-configs]');if(choose)choose.disabled=false;}
  if(state.page==='overview')render();else if(state.page==='xui')refreshIntegrationStatus();
  if(state.page==='configs'||state.page==='xui'){
   const groups=(await api('configurations')).groups;if(JSON.stringify(groups)!==JSON.stringify(state.groups)){state.groups=groups;if(state.page==='configs')renderConfigurationResults();else{refreshSelectedConfigurations();configPicker?.updateGroups(groups);}}
   if(!state.selectionPending)await loadPresence(force).catch(()=>{});
  }
 }catch{}finally{pollRunning=false;if(state.selectionPending)setTimeout(poll,400);}
}
async function boot(){render();try{const session=await api('session');state.auth=session.authenticated;state.csrf=session.csrf;if(state.auth)await loadAll();render();}catch(error){toast(error.message||t('error'));}setInterval(poll,4000);}
addEventListener('hashchange',()=>{capture();state.page=location.hash.slice(1)||'overview';render();});boot();
