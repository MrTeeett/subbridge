const escape = value => String(value ?? '').replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));

// Search and sorting change the visible rows; selection always spans all rows.
export class MultiSelect {
 constructor(root, config) {
  this.root=root;this.config=config;this.selected=new Set(config.selected.map(String));
  this.items=[];this.query='';this.order='name-asc';this.disabled=false;
  this.collator=new Intl.Collator(config.language,{numeric:true,sensitivity:'base'});
  this.controller=new AbortController();const signal=this.controller.signal;
  const {id,labels}=config;
  root.classList.add('multi-select');
  root.innerHTML=`<span id="${id}-label">${escape(config.label)}</span><button type="button" id="${id}" class="picker-trigger" aria-haspopup="dialog" aria-expanded="false" aria-labelledby="${id}-label ${id}-summary"><span id="${id}-summary"></span><span aria-hidden="true">⌄</span></button><div class="picker-popup" role="dialog" aria-labelledby="${id}-label" hidden><div class="picker-tools"><label class="sr-only" for="${id}-search">${escape(labels.search)}</label><input id="${id}-search" type="search" placeholder="${escape(labels.search)}" autocomplete="off"><label class="sr-only" for="${id}-sort">${escape(labels.sort)}</label><select id="${id}-sort">${config.orders.map(([key,label])=>`<option value="${key}">${escape(label)}</option>`).join('')}</select></div><div class="picker-actions"><button type="button" data-pick-visible>${escape(labels.selectVisible)}</button><button type="button" data-pick-clear>${escape(labels.clear)}</button><span class="picker-count" role="status" aria-live="polite"></span></div><div class="picker-options"></div></div>`;
  this.trigger=root.querySelector('.picker-trigger');this.popup=root.querySelector('.picker-popup');
  this.search=root.querySelector('input[type=search]');this.sort=root.querySelector('select');
  this.trigger.addEventListener('click',()=>this.setOpen(this.popup.hidden),{signal});
  this.search.addEventListener('input',()=>{this.query=this.search.value;this.renderOptions();},{signal});
  this.sort.addEventListener('change',()=>{this.order=this.sort.value;this.renderOptions();},{signal});
  root.addEventListener('change',event=>{
   const value=event.target.dataset.choice;if(value===undefined)return;
   if(event.target.checked)this.selected.add(value);else this.selected.delete(value);
   this.changed(value);
  },{signal});
  root.querySelector('[data-pick-visible]').addEventListener('click',()=>{for(const row of this.visible())this.selected.add(row.value);this.changed();},{signal});
  root.querySelector('[data-pick-clear]').addEventListener('click',()=>{this.selected.clear();this.changed();},{signal});
  document.addEventListener('pointerdown',event=>{if(!root.contains(event.target))this.setOpen(false);},{signal});
  root.addEventListener('keydown',event=>{
   if(event.key==='Escape'&&!this.popup.hidden){event.preventDefault();event.stopPropagation();this.setOpen(false);this.trigger.focus();}
   if(event.key==='Enter'&&event.target===this.search){event.preventDefault();root.querySelector('[data-choice]')?.focus();}
   if(['ArrowDown','ArrowUp'].includes(event.key)&&event.target.matches('[data-choice],input[type=search]')){
    const rows=[this.search,...root.querySelectorAll('[data-choice]')];const index=rows.indexOf(event.target);
    event.preventDefault();rows[Math.max(0,Math.min(rows.length-1,index+(event.key==='ArrowDown'?1:-1)))]?.focus();
   }
  },{signal});
  this.update(config.items,config.disabled);
 }
 destroy(){this.controller.abort();}
 setOpen(open){
  if(this.disabled)open=false;this.popup.hidden=!open;this.trigger.setAttribute('aria-expanded',String(open));this.root.classList.remove('opens-up');
  if(open){
   this.popup.style.removeProperty('--picker-space');
   const position=this.trigger.getBoundingClientRect(),height=this.popup.getBoundingClientRect().height,below=innerHeight-position.bottom-12,above=position.top-12;
   const upward=innerWidth>760&&below<height&&above>below;
   if(upward)this.root.classList.add('opens-up');
   this.popup.style.setProperty('--picker-space',`${Math.max(160,upward?above:below)}px`);this.search.focus();
  }
 }
 update(items,disabled=false){this.items=items.map(row=>({...row,value:String(row.value)}));this.disabled=disabled;this.trigger.disabled=disabled;if(disabled)this.setOpen(false);this.renderOptions();}
 rows(){
  const rows=[...this.items],known=new Set(rows.map(row=>row.value));
  for(const value of this.selected)if(!known.has(value))rows.push({value,label:this.config.missingLabel?.(value)||value,detail:this.config.labels.missing,missing:true,number:Number(value)||0});
  return rows;
 }
 visible(){
  const terms=this.query.toLocaleLowerCase(this.config.language).normalize('NFKC').trim().split(/\s+/).filter(Boolean);
  return this.rows().filter(row=>terms.every(term=>(row.label+' '+(row.detail||'')+' '+(row.search||'')).toLocaleLowerCase(this.config.language).normalize('NFKC').includes(term))).sort((a,b)=>{
   let compare=0;
   if(this.order==='selected')compare=Number(this.selected.has(b.value))-Number(this.selected.has(a.value));
   if(this.order.startsWith('id-'))compare=(a.number||0)-(b.number||0);
   if(this.order.startsWith('port-'))compare=(a.port||0)-(b.port||0);
   if(this.order==='name-desc')compare=-this.collator.compare(a.label,b.label);
   if(this.order.endsWith('-desc')&&this.order!=='name-desc')compare=-compare;
   return compare||this.collator.compare(a.label,b.label)||this.collator.compare(a.value,b.value);
  });
 }
 changed(focused){this.config.onChange([...this.selected]);this.renderOptions(focused);}
 renderOptions(focused){
  const {id,labels}=this.config,rows=this.visible(),all=this.rows();
  const single=this.selected.size===1?all.find(row=>this.selected.has(row.value)):null;
  this.root.querySelector(`#${id}-summary`).textContent=single?.label||(this.selected.size?`${labels.selected}: ${this.selected.size}`:labels.choose);
  this.root.querySelector('.picker-count').textContent=`${rows.length} / ${all.length} · ${labels.selected}: ${this.selected.size}`;
  this.root.querySelector('[data-pick-visible]').disabled=this.disabled||!rows.length;
  this.root.querySelector('[data-pick-clear]').disabled=this.disabled||!this.selected.size;
  this.root.querySelector('.picker-options').innerHTML=rows.length?rows.map(row=>`<label class="picker-option ${this.selected.has(row.value)?'is-selected':''}"><input type="checkbox" data-choice="${escape(row.value)}" ${this.selected.has(row.value)?'checked':''} ${this.disabled?'disabled':''}><span><span class="picker-option-name">${escape(row.label)}</span><span class="picker-option-detail ${row.missing?'missing':''}">${escape(row.detail||'')}</span></span></label>`).join(''):`<p class="picker-empty">${escape(this.query?labels.noResults:labels.empty)}</p>`;
  if(focused!==undefined)(this.root.querySelector(`[data-choice="${CSS.escape(focused)}"]`)||this.search).focus({preventScroll:true});
 }
}
