'use strict';
const $ = id => document.getElementById(id);
const labels = {active:'活跃操作',media:'媒体播放',idle:'空闲',locked:'锁屏',suspended:'休眠',paused:'人工暂停',unknown:'未采集'};
const names = {'msedge.exe':'Microsoft Edge','chrome.exe':'Google Chrome','Code.exe':'Visual Studio Code','explorer.exe':'文件资源管理器','collector.exe':'屏幕时间采集器','WindowsTerminal.exe':'Windows Terminal'};
const state={screenshotMaxAge:90,screenshotRetentionSec:86400,day:null,cursor:0,range:null,app:'',limit:80,allApps:false,zoom:24,viewStart:0,generation:0,shotGeneration:0,shotFile:'',follow:true,shotCache:new Map(),weekKey:'',weekDays:[]};
const duration = sec => { const value=Math.max(0,sec||0); if(value>0&&value<60)return '<1m'; const m=Math.floor(value/60); return m>=60?`${Math.floor(m/60)}h ${String(m%60).padStart(2,'0')}m`:`${m}m`; };
const time = (ts,seconds=false) => new Date(ts*1000).toLocaleTimeString('zh-CN',{hour12:false,hour:'2-digit',minute:'2-digit',...(seconds?{second:'2-digit'}:{})});
const appName = p => names[p]||p||'未识别应用';
function localDate(d=new Date()){return `${d.getFullYear()}-${String(d.getMonth()+1).padStart(2,'0')}-${String(d.getDate()).padStart(2,'0')}`;}
function dateShift(date,days){const d=new Date(date+'T12:00:00');d.setDate(d.getDate()+days);return localDate(d);}
function node(tag,text,cls){const n=document.createElement(tag);if(text!==undefined)n.textContent=text;if(cls)n.className=cls;return n;}
async function api(path){const response=await fetch(path,{cache:'no-store',signal:AbortSignal.timeout(10000)});if(!response.ok)throw new Error(await response.text());return response.json();}
function notice(message){$('notice').textContent=message;$('notice').hidden=!message;}
function slices(){const d=state.day;if(!d)return [];let lo=state.range?.[0]??d.start,hi=state.range?.[1]??d.until;return d.segments.filter(s=>s.end>lo&&s.start<hi).map(s=>({...s,start:Math.max(s.start,lo),end:Math.min(s.end,hi),dur_sec:Math.min(s.end,hi)-Math.max(s.start,lo)}));}
function renderStats(){const totals={},apps=new Map();for(const s of slices()){totals[s.kind]=(totals[s.kind]||0)+s.dur_sec;if(['active','media'].includes(s.kind)){const name=s.process||'未识别应用';const v=apps.get(name)||{name,active:0,media:0};v[s.kind]+=s.dur_sec;apps.set(name,v);}}
 $('effective').textContent=duration((totals.active||0)+(totals.media||0));for(const k of ['active','idle'])$(k).textContent=duration(totals[k]);
 $('coverage').textContent=`${state.range?'所选范围':'当日'}：有效使用 ${duration((totals.active||0)+(totals.media||0))} · 暂停 ${duration(totals.paused)} · 休眠 ${duration(totals.suspended)} · 锁屏 ${duration(totals.locked)} · 未采集 ${duration(totals.unknown)}${state.day.bad_lines?` · ${state.day.bad_lines} 条记录异常，相关统计可能不完整`:''}`;
 const sorted=[...apps.values()].sort((a,b)=>(b.active+b.media)-(a.active+a.media));const total=(totals.active||0)+(totals.media||0);$('apps').replaceChildren();
 for(const a of sorted.slice(0,state.allApps?undefined:8)){const row=node('button',undefined,'app-row'+(state.app===a.name?' selected':''));const top=node('div',undefined,'app-name');top.append(node('strong',appName(a.name)),node('span',`${duration(a.active+a.media)} · ${total?Math.round((a.active+a.media)/total*100):0}%`));const bar=node('div',undefined,'bar'),fill=node('div',undefined,'bar-fill');fill.style.width=`${sorted.length?(a.active+a.media)/(sorted[0].active+sorted[0].media)*100:0}%`;const active=node('b'),media=node('em');active.style.width=`${a.active/(a.active+a.media)*100}%`;media.style.flex='1';fill.append(active,media);bar.append(fill);row.append(top,bar);row.onclick=()=>setAppFilter(state.app===a.name?'':a.name);$('apps').append(row);}
 renderRecallApps(sorted);
 if(!sorted.length)$('apps').append(node('p','该范围没有有效使用记录','empty'));
 $('allApps').hidden=sorted.length<=8;$('allApps').textContent=state.allApps?'收起应用':'展开全部应用';$('clearFilter').hidden=!state.app;
}
function setAppFilter(app){
 state.app=app;state.limit=80;state.follow=false;
 renderStats();renderRows();selectTime(state.cursor);renderTimeline();
}
function renderRecallApps(apps){
 const list=$('recallApps'),scroll=list.scrollTop;
 list.replaceChildren();
 $('recallClearFilter').setAttribute('aria-pressed',String(!state.app));
 const max=Math.max(1,...apps.map(a=>a.active+a.media));
 for(const a of apps){
  const row=node('button',undefined,'recall-app-row'+(state.app===a.name?' selected':''));
  row.title=appName(a.name);row.setAttribute('aria-pressed',String(state.app===a.name));
  const top=node('div',undefined,'recall-app-name');
  top.append(node('strong',appName(a.name)),node('span',duration(a.active+a.media)));
  const bar=node('div',undefined,'bar'),fill=node('div',undefined,'bar-fill'),active=node('b'),media=node('em');
  fill.style.width=(a.active+a.media)/max*100+'%';active.style.width=a.active/(a.active+a.media)*100+'%';media.style.flex='1';
  fill.append(active,media);bar.append(fill);row.append(top,bar);
  row.onclick=()=>setAppFilter(state.app===a.name?'':a.name);
  list.append(row);
 }
 if(!apps.length)list.append(node('p','暂无应用记录','muted'));
 list.scrollTop=scroll;
}
function renderRows(){const search=$('search').value.toLocaleLowerCase(),kind=$('kindFilter').value;const list=slices().filter(s=>(!state.app||(s.process||'未识别应用')===state.app)&&(!kind||s.kind===kind)&&(!search||`${appName(s.process)} ${s.title}`.toLocaleLowerCase().includes(search)));$('rows').replaceChildren();
 for(const s of list.slice(0,state.limit)){const tr=node('tr');const first=node('td',`${time(s.start)}–${time(s.end)}`);const desc=node('td');desc.append(node('strong',s.process?appName(s.process):labels[s.kind]),node('p',s.title||'—'));const kindCell=node('td');kindCell.append(node('span',labels[s.kind]||s.kind,'badge'));tr.append(first,desc,kindCell,node('td',duration(s.dur_sec)));tr.onclick=()=>{state.follow=false;selectTime(s.start,true);$('cursorTime').scrollIntoView({block:'center',behavior:'smooth'});};$('rows').append(tr);}
 if(!list.length){const tr=node('tr'),td=node('td','没有匹配的活动');td.colSpan=4;tr.append(td);$('rows').append(tr);}$('more').hidden=list.length<=state.limit;
}
function currentSegment(ts){const list=state.day?.segments||[];let lo=0,hi=list.length;while(lo<hi){const mid=(lo+hi)>>1;if(list[mid].start<=ts)lo=mid+1;else hi=mid;}const s=list[lo-1];return s&&ts<s.end?s:null;}
// 拖动与键盘共用可显示截图集合，跳过不可展示时段和已知读取失败的图片。
function recallShots(){
 return (state.day?.shots||[]).filter(shot=>{
  if(shot.ts<state.day.start||shot.ts>=state.day.until||state.shotCache.get(shot.file)?.failed)return false;
  const s=currentSegment(shot.ts);
  return s&&['active','media','idle'].includes(s.kind)&&(!state.app||(s.process||'未识别应用')===state.app);
 });
}
function nearestRecallTime(ts){
 let nearest=ts,distance=Infinity;
 const end=Math.min(state.day.until,state.viewStart+state.zoom*3600);
 for(const shot of recallShots()){
  if(shot.ts<state.viewStart||shot.ts>=end)continue;
  const delta=Math.abs(shot.ts-ts);
  if(delta<distance){distance=delta;nearest=shot.ts;}
 }
 return nearest;
}
function filteredRecallTime(ts){return state.app?nearestRecallTime(ts):ts;}
function stepRecallShot(direction){
 if(!state.day)return;
 const shots=recallShots();
 const shot=direction>0?shots.find(s=>s.ts>state.cursor):shots.findLast(s=>s.ts<state.cursor);
 if(!shot)return;
 state.follow=false;
 const outside=shot.ts<state.viewStart||shot.ts>=state.viewStart+state.zoom*3600;
 selectTime(shot.ts,outside,false);
}
function updateViewport(center){const d=state.day;if(!d)return;const span=Math.min(state.zoom*3600,d.end-d.start);state.viewStart=Math.max(d.start,Math.min(d.end-span,center-span/2));$('scrubber').min=String(state.viewStart);$('scrubber').max=String(Math.min(d.until,d.end,state.viewStart+span));$('scrubber').disabled=d.until<=state.viewStart;renderTimeline();}
function renderTimeline(){const d=state.day;if(!d)return;const canvas=$('timeline'),rect=canvas.getBoundingClientRect(),scale=window.devicePixelRatio||1;canvas.width=Math.max(1,Math.round(rect.width*scale));canvas.height=40*scale;const ctx=canvas.getContext('2d');ctx.scale(scale,scale);const w=rect.width,span=Math.min(state.zoom*3600,d.end-d.start),start=state.viewStart;ctx.fillStyle='#f4f7fb';ctx.fillRect(0,0,w,40);
 for(const s of d.segments){const left=Math.max(start,s.start),right=Math.min(start+span,s.end);if(right<=left)continue;const x=(left-start)/span*w,width=Math.max(.5,(right-left)/span*w);ctx.globalAlpha=(state.app && (s.process||'未识别应用') !== state.app) ? 0.06 : 1;ctx.fillStyle=s.kind==='active'?'#1266cc':({media:'#79b8ff',idle:'#d3deeb',locked:'#bac8d9',suspended:'#a1aec0',paused:'#ead393',unknown:'#fafcff'}[s.kind]||'#d3deeb');ctx.fillRect(x,0,width,40);if(s.kind==='unknown'||s.kind==='media'){ctx.save();ctx.beginPath();ctx.rect(x,0,width,40);ctx.clip();ctx.strokeStyle=s.kind==='unknown'?'#b7c4d4':'#ffffff80';ctx.lineWidth=1;for(let n=x-40;n<x+width;n+=8){ctx.beginPath();ctx.moveTo(n,40);ctx.lineTo(n+40,0);ctx.stroke();}ctx.restore();}}
 $('ticks').replaceChildren();for(let i=0;i<=6;i++){$('ticks').append(node('span',i===6&&start+span===d.end?'24:00':time(start+span*i/6)));}
 $('timelineLabel').textContent=(state.zoom===24?'全天活动':'放大回顾 · '+(state.zoom>=1?state.zoom+' 小时':state.zoom*60+' 分钟'))+(state.app?' · '+appName(state.app):'');
 // 原生滑块的可选上限保留全天视野，使手柄位置与画布精确对应。
 $('scrubber').max=String(start+span);$('scrubber').value=String(state.cursor);
}
function clearShot(message){state.shotGeneration++;state.shotFile='';$('shot').hidden=true;$('shotEmpty').hidden=false;$('shotEmpty').textContent=message;$('expandShot').disabled=true;$('shotTime').textContent='';$('shotAge').textContent='';}
function renderShot(s){const d=state.day,ts=state.cursor;
 if(!s||!['active','media','idle'].includes(s.kind)){clearShot(s?`${labels[s.kind]}时段，不展示桌面截图`:'该时间尚无活动记录');return;}
 const shots=d.shots;let lo=0,hi=shots.length;while(lo<hi){const mid=(lo+hi)>>1;if(shots[mid].ts<=ts)lo=mid+1;else hi=mid;}const shot=shots[lo-1];
 if(state.app&&((s.process||'未识别应用')!==state.app||shot&&(currentSegment(shot.ts)?.process||'未识别应用')!==state.app)){clearShot('此时间附近没有以该应用为前台的截图');return;}
 // 不越过空洞、暂停、锁屏、休眠边界；普通前台切换允许显示相近快照并标注时间差。
 const boundary=d.segments.some(v=>v.end>shot?.ts&&v.start<=ts&&['paused','unknown','suspended','locked'].includes(v.kind));
 if(!shot||ts-shot.ts>state.screenshotMaxAge||boundary){clearShot(Date.now()/1000-ts>state.screenshotRetentionSec?'该时段截图已按保留策略清理':'此时间附近没有可用截图');return;}
 $('shotTime').textContent='拍摄于 '+time(shot.ts,true);$('shotAge').textContent=`距所选时间 ${Math.floor(ts-shot.ts)} 秒`;
 if(state.shotFile===shot.file){
 const pending=state.shotCache.get(shot.file);
 if(pending&&!pending.ready){$('shotTime').textContent=$('shot').hidden?'正在加载':'正在加载 · 暂显示上一张';$('shotAge').textContent='';}
 warmRecallImages();return;
}
 state.shotFile=shot.file;++state.shotGeneration;
  const hasPrevious=!$('shot').hidden;
 $('shotEmpty').hidden=hasPrevious;$('shotEmpty').textContent='正在读取桌面截图…';$('expandShot').disabled=true;
 $('shotTime').textContent=hasPrevious?'正在加载 · 暂显示上一张':'正在加载';$('shotAge').textContent='';
 const entry=loadRecallImage(shot.file);
 if(entry.failed){clearShot('截图已清理或暂时无法读取');return;}
 if(entry.ready)presentRecallImage(shot.file,entry);
 warmRecallImages();
}

// 最多缓存12张全尺寸图片，后台最多同时预读两张；就绪表示解码完成。
function loadRecallImage(file){
 let entry=state.shotCache.get(file);
 if(entry){if(file===state.shotFile)entry.img.fetchPriority='high';state.shotCache.delete(file);state.shotCache.set(file,entry);return entry;}
 const img=new Image();img.decoding='async';img.fetchPriority=file===state.shotFile?'high':'low';
 entry={img,ready:false};state.shotCache.set(file,entry);
 while(state.shotCache.size>12){
  const old=[...state.shotCache.keys()].find(k=>k!==state.shotFile&&k!==file);
  if(!old)break;const removed=state.shotCache.get(old);state.shotCache.delete(old);
  if(!removed.ready)removed.img.src='';
 }
  const finish=()=>{if(state.shotCache.get(file)!==entry)return;entry.ready=true;if(state.shotFile===file)presentRecallImage(file,entry);warmRecallImages();};
 const fail=()=>{if(state.shotCache.get(file)!==entry)return;entry.failed=true;if(state.shotFile===file)clearShot('截图已清理或暂时无法读取');warmRecallImages();};
 img.onload=()=>{if(state.shotCache.get(file)!==entry)return;if(typeof img.decode==='function')img.decode().then(finish,fail);else finish();};
 img.onerror=fail;
 img.src='/shot/'+encodeURIComponent(file);return entry;
}
function presentRecallImage(file,entry){
 if(state.shotFile!==file)return;
 const img=entry.img;img.id='shot';img.alt='所选时间的桌面截图';img.hidden=false;
 if($('shot')!==img)$('shot').replaceWith(img);
  $('shotEmpty').hidden=true;$('expandShot').disabled=false;
 const displayed=state.day?.shots.find(s=>s.file===file);
 if(displayed){$('shotTime').textContent='拍摄于 '+time(displayed.ts,true);$('shotAge').textContent='距所选时间 '+Math.floor(state.cursor-displayed.ts)+' 秒';}
}
function warmRecallImages(){
 if(!state.shotFile||!state.day)return;
 const shots=state.day.shots||[],index=shots.findIndex(s=>s.file===state.shotFile);
 if(index<0)return;
 const matches=shots.filter(s=>{
  if(!state.app)return true;
  const segment=currentSegment(s.ts);
  return segment&&(segment.process||'未识别应用')===state.app&&['active','media','idle'].includes(segment.kind);
 });
 const center=shots[index].ts;
 const nearby=matches.filter(s=>s.file!==state.shotFile).sort((a,b)=>Math.abs(a.ts-center)-Math.abs(b.ts-center)).slice(0,10);
 // 远距离跳转时停止不再相关的预读，不让旧请求堵住新的回顾目标。
 const wanted=new Set([state.shotFile,...nearby.map(s=>s.file)]);
 for(const [file,entry] of state.shotCache){
  if(!entry.ready&&!entry.failed&&!wanted.has(file)){state.shotCache.delete(file);entry.img.src='';}
 }
 let pending=[...state.shotCache].filter(([file,e])=>file!==state.shotFile&&!e.ready&&!e.failed).length;
 for(const shot of nearby){
  if(pending>=2)break;
  if(state.shotCache.has(shot.file))continue;
  loadRecallImage(shot.file);pending++;
 }
}
function selectTime(ts,recenter=false,snap=true){const d=state.day;if(!d)return;ts=snap?filteredRecallTime(Number(ts)):Number(ts);if(ts===null){clearShot('该应用没有可回顾的活动');return;}state.cursor=Math.max(d.start,Math.min(Math.max(d.start,d.until-.001),Number(ts)));if(recenter)updateViewport(state.cursor);$('scrubber').value=String(state.cursor);$('cursorTime').textContent=time(state.cursor,true);const s=currentSegment(state.cursor);$('kind').textContent=s?labels[s.kind]:'暂无记录';$('process').textContent=s?.process?appName(s.process):(s?labels[s.kind]:'没有可回顾的活动');$('title').textContent=s?.title||({paused:'你主动暂停了采集，此段不记录活动细节。',unknown:'这段时间没有足够记录，不计入应用使用。',suspended:'电脑处于休眠状态。',locked:'电脑已锁屏。',idle:'一段没有键鼠操作、也没有音频活动的时间。'}[s?.kind]||'');$('segmentInfo').textContent=s?`${time(s.start)} — ${time(s.end)} · ${duration(s.dur_sec)}`:'';renderShot(s);}
// 七日图柱高按卡片实测可用高度换算：卡片被同排面板拉高时柱子随之伸展，贴住卡片底部；-7 为 .week-col .stack 的 margin-top。
function layoutWeekBars(){const box=$('week');if(!box)return;for(const col of box.children){const stack=col.querySelector('.stack');if(!stack||!stack.dataset)continue;const ratio=Number(stack.dataset.ratio||0);let others=0;for(const el of col.children)if(el!==stack)others+=el.offsetHeight;const avail=col.clientHeight-others-7;if(!(avail>10))continue;stack.style.height=Math.max(2,ratio*avail)+'px';}}
async function renderWeek(date,force=false){const key=date+':'+Math.floor(Date.now()/120000);if(!force&&key===state.weekKey)return;state.weekKey=key;const generation=state.generation;const dates=Array.from({length:7},(_,i)=>dateShift(date,i-6));const results=await Promise.allSettled(dates.map(d=>api('/api/day?date='+d)));if(generation!==state.generation){state.weekKey='';return;}state.weekDays=results.map((r,i)=>r.status==='fulfilled'?r.value:{date:dates[i],error:true});const max=Math.max(3600,...state.weekDays.map(d=>(d.totals?.active||0)+(d.totals?.media||0)));$('week').replaceChildren();
 for(const d of state.weekDays){const a=d.totals?.active||0,m=d.totals?.media||0;const col=node('div',undefined,'week-col'+(d.date===date?' selected':''));col.title=d.error?'读取失败':`有效使用 ${duration(a+m)}，未采集 ${duration(d.totals?.unknown)}`;col.append(node('span',d.error?'读取失败':duration(a+m)));const stack=node('div',undefined,'stack');stack.dataset.ratio=String(max?(a+m)/max:0);const active=node('span',undefined,'active'),media=node('span',undefined,'media');active.style.height=(a+m?a/(a+m)*100:0)+'%';media.style.height=(a+m?m/(a+m)*100:0)+'%';stack.append(active,media);col.append(stack,node('span',d.date.slice(5).replace('-','/'),'day'),node('small',d.error?'—':`缺 ${duration(d.totals?.unknown)}`));$('week').append(col);}
 $('weekNote').textContent='';layoutWeekBars();
}
async function loadDate(date,preserve=false){if(!/^\d{4}-\d{2}-\d{2}$/.test(date))return;const generation=++state.generation;
 try{const d=await api('/api/day?date='+date);if(generation!==state.generation)return;state.day=d;$('date').value=date;$('recallDate').value=date;$('nextDate').disabled=date>=localDate();$('recallNextDate').disabled=date>=localDate();if(!preserve){state.range=null;state.app='';state.limit=80;state.follow=true;$('resetRange').hidden=true;$('rangeLabel').textContent='全天';$('lightbox').close();clearShot('正在读取回顾信息…');}
 const last=d.segments.filter(s=>['active','media'].includes(s.kind)).at(-1);const cursor=preserve&&!state.follow?state.cursor:(date===localDate()?Math.max(d.start,d.until-1):(last?Math.max(last.start,last.end-1):d.start));updateViewport(preserve?state.viewStart+state.zoom*1800:cursor);selectTime(cursor);renderStats();renderRows();notice(d.bad_lines?`有 ${d.bad_lines} 条记录无法正常解析或时间异常；当前结果可能不完整。`:'');$('updated').textContent='更新于 '+time(Date.now()/1000,true)+'';renderWeek(date).catch(()=>{$('weekNote').textContent='趋势读取失败，请稍后重试';});
 }catch(e){if(generation===state.generation)notice('读取失败：'+e.message+'。保留上次显示的结果。');}
}
async function refreshStatus(){try{const s=await api('/api/status');const fresh=Date.now()-Date.parse(s.updated)<40000;$('status').textContent=!fresh?'采集状态已过期':s.paused?'采集已暂停':s.hook_ok?'正在采集':'采集需检查';$('date').max=s.today;$('recallDate').max=s.today;return true;}catch{$('status').textContent='采集器已离线';notice('无法连接本机采集器。当前结果已保留，启动采集器后即可继续刷新。');return false;}}
$('scrubber').addEventListener('input',e=>{state.follow=false;selectTime(nearestRecallTime(Number(e.target.value)),false,false);});
$('scrubber').addEventListener('keydown',e=>{if(e.key==='ArrowLeft'||e.key==='ArrowRight'){e.preventDefault();stepRecallShot(e.key==='ArrowLeft'?-1:1);}});
// 滚轮只改变视野，不改变回顾指针；鼠标下的时间在缩放前后保持同一位置。
function zoomTimelineWithWheel(event){
 if(!state.day||event.ctrlKey||!event.deltaY)return;
 event.preventDefault();
 const levels=[24,12,6,3,1,.5,.25],index=levels.indexOf(state.zoom);
 const next=levels[Math.max(0,Math.min(levels.length-1,index+(event.deltaY<0?1:-1)))];
 if(next===state.zoom)return;
 const rect=$('timeline').getBoundingClientRect();
 const ratio=Math.max(0,Math.min(1,(event.clientX-rect.left)/Math.max(1,rect.width)));
 const oldSpan=Math.min(state.zoom*3600,state.day.end-state.day.start);
 const anchor=state.viewStart+ratio*oldSpan;
 state.zoom=next;$('zoom').value=String(next);
 const span=Math.min(next*3600,state.day.end-state.day.start);
 updateViewport(anchor+(0.5-ratio)*span);
}
document.querySelector('.timeline-wrap').addEventListener('wheel',zoomTimelineWithWheel,{passive:false});
$('zoom').onchange=()=>{state.zoom=Number($('zoom').value);updateViewport(state.cursor);};
$('live').onclick=()=>{if($('date').value!==localDate()){loadDate(localDate());return;}state.follow=true;loadDate(localDate(),true);};
$('recallDate').onchange=()=>loadDate($('recallDate').value);
$('recallPrevDate').onclick=()=>loadDate(dateShift($('date').value,-1));
$('recallNextDate').onclick=()=>loadDate(dateShift($('date').value,1));
$('recallToday').onclick=()=>loadDate(localDate());
$('date').onchange=()=>loadDate($('date').value);$('today').onclick=()=>loadDate(localDate());$('prevDate').onclick=()=>loadDate(dateShift($('date').value,-1));$('nextDate').onclick=()=>loadDate(dateShift($('date').value,1));
$('allApps').onclick=()=>{state.allApps=!state.allApps;renderStats();};$('clearFilter').onclick=()=>setAppFilter('');$('recallClearFilter').onclick=()=>setAppFilter('');
for(const id of ['search','kindFilter'])$(id).addEventListener('input',()=>{state.limit=80;renderRows();});$('more').onclick=()=>{state.limit+=80;renderRows();};
$('applyRange').onclick=()=>{if(!state.day)return;const parse=v=>{const [h,m]=v.split(':').map(Number);return h*3600+m*60;};const from=parse($('from').value),to=$('to').value==='23:59'?86400:parse($('to').value);if(!Number.isFinite(from)||!Number.isFinite(to)||to<=from){notice('结束时间需要晚于开始时间。');return;}state.range=[state.day.start+from,state.day.start+to];state.limit=80;$('resetRange').hidden=false;$('rangeLabel').textContent=$('from').value+'–'+$('to').value;notice('');renderStats();renderRows();};
$('resetRange').onclick=()=>{state.range=null;$('resetRange').hidden=true;$('rangeLabel').textContent='全天';renderStats();renderRows();};
$('expandShot').onclick=()=>{if($('shot').hidden)return;$('largeShot').src=$('shot').src;$('lightbox').showModal();};$('closeShot').onclick=()=>$('lightbox').close();$('fitShot').onclick=()=>document.querySelector('.image-scroll').classList.toggle('original');
new ResizeObserver(()=>renderTimeline()).observe($('timeline'));new ResizeObserver(()=>layoutWeekBars()).observe($('week'));
$('date').value=localDate();refreshStatus();loadDate(localDate());setInterval(async()=>{if(document.hidden)return;const online=await refreshStatus();if(online&&state.day?.date===localDate())loadDate(state.day.date,true);},30000);

async function toggleRecallFullscreen(){
 $('fullscreenError').hidden=true;
 try{
  if(document.fullscreenElement===$('recall'))await document.exitFullscreen();
  else if($('recall').requestFullscreen)await $('recall').requestFullscreen();
  else throw new Error('当前浏览器不支持全屏，请使用浏览器的 F11 全屏功能');
 }catch(error){$('fullscreenError').textContent='无法进入全屏：'+error.message;$('fullscreenError').hidden=false;}
}
function syncRecallFullscreen(){
 const active=document.fullscreenElement===$('recall');
 $('fullscreenRecall').textContent=active?'退出全屏 ⛶':'全屏回顾 ⛶';
 $('fullscreenRecall').setAttribute('aria-pressed',String(active));
 renderTimeline();
}
$('fullscreenRecall').onclick=toggleRecallFullscreen;
document.addEventListener('fullscreenchange',syncRecallFullscreen);
let settingsSnapshot=null;
function applyScreenshotSettings(value){
 state.screenshotMaxAge=Math.max(90,value.screenshot_interval_sec*1.5);
 state.screenshotRetentionSec=value.screenshot_retention_hours*3600;
}
function updateRetentionWarning(){
 const shorter=settingsSnapshot&&Number($('retentionHours').value)<settingsSnapshot.screenshot_retention_hours;
 $('shorterWarning').hidden=!shorter;$('confirmShorter').required=!!shorter;
 if(!shorter)$('confirmShorter').checked=false;
}
$('retentionHours').addEventListener('input',updateRetentionWarning);
$('openSettings').onclick=async()=>{
 $('settingsMessage').textContent='正在读取…';$('saveSettings').disabled=true;$('settingsDialog').showModal();$('confirmShorter').checked=false;
 try{
  settingsSnapshot=await api('/api/settings');applyScreenshotSettings(settingsSnapshot);
  $('retentionHours').value=settingsSnapshot.screenshot_retention_hours;$('intervalSeconds').value=settingsSnapshot.screenshot_interval_sec;
  updateRetentionWarning();$('settingsMessage').textContent='';$('saveSettings').disabled=false;
 }catch(e){$('settingsMessage').textContent='读取设置失败：'+e.message;}
};
$('closeSettings').onclick=()=>$('settingsDialog').close();
$('settingsForm').addEventListener('submit',async event=>{
 event.preventDefault();$('saveSettings').disabled=true;
 try{
  const response=await fetch('/api/settings',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({
   screenshot_interval_sec:Number($('intervalSeconds').value),
   screenshot_retention_hours:Number($('retentionHours').value),
   confirm_shorter_retention:$('confirmShorter').checked
  }),signal:AbortSignal.timeout(10000)});
  if(!response.ok)throw new Error(await response.text());
  settingsSnapshot=await response.json();applyScreenshotSettings(settingsSnapshot);updateRetentionWarning();
  $('settingsMessage').textContent='已保存，设置已生效。';
 }catch(e){$('settingsMessage').textContent='保存失败：'+e.message;}
 finally{$('saveSettings').disabled=false;}
});
api('/api/settings').then(applyScreenshotSettings).catch(()=>{});