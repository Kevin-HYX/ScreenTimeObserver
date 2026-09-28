// 不启动浏览器，验证时间轴与截图的交互逻辑及异步竞争。
const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs');
const vm=require('node:vm');
function setup(){
 const elements=new Map(),images=[];
 function element(){return {value:'',textContent:'',hidden:false,disabled:false,style:{},dataset:{},children:[],classList:{toggle(){}},append(...v){this.children.push(...v)},replaceChildren(...v){this.children=v},listeners:{},addEventListener(k,v){this.listeners[k]=v},setAttribute(k,v){this[k]=v},replaceWith(other){elements.set(this.id||'shot',other)},removeAttribute(k){delete this[k]},close(){},showModal(){},getBoundingClientRect(){return {width:800}},getContext(){return {scale(){},fillRect(){},save(){},beginPath(){},rect(){},clip(){},moveTo(){},lineTo(){},stroke(){},restore(){}}}};}
 const document={hidden:false,fullscreenElement:null,addEventListener(){},getElementById(id){if(!elements.has(id))elements.set(id,Object.assign(element(),{id}));return elements.get(id)},createElement:element,querySelector:element};
 const context=vm.createContext({document,window:{devicePixelRatio:1},console,Date,AbortSignal,fetch:()=>new Promise(()=>{}),setInterval(){},setTimeout(){return 1},clearTimeout(){},ResizeObserver:class{observe(){}},Image:class{constructor(){Object.assign(this,element());images.push(this)}}});
 vm.runInContext(fs.readFileSync(__dirname+'/dashboard/app.js','utf8'),context);
 return {run:s=>vm.runInContext(s,context),elements,images};
}
test('范围截取不会改变原始区间，概览与应用排行一致',()=>{
 const t=setup();t.run(`state.day={start:0,until:180,bad_lines:0,segments:[{start:0,end:60,dur_sec:60,kind:'active',process:'a'},{start:60,end:120,dur_sec:60,kind:'paused',process:''},{start:120,end:180,dur_sec:60,kind:'media',process:'b'}]};state.range=[30,150];renderStats();`);
 assert.equal(t.run('slices().reduce((n,s)=>n+s.dur_sec,0)'),120);
 assert.equal(t.elements.get('effective').textContent,'1m');
 assert.equal(t.run('state.day.segments[0].start'),0);
 assert.equal(t.elements.get('apps').children.length,2);
});
test('七日图按应用拆分总时长，清除筛选后恢复单色柱',()=>{
 const t=setup();
 t.run(`state.day={date:'2026-09-28',start:0,end:86400,until:3600,shots:[],segments:[{start:0,end:1800,dur_sec:1800,kind:'active',process:'a'},{start:1800,end:3600,dur_sec:1800,kind:'media',process:'b'}]};state.weekDays=[{date:'2026-09-27',totals:{active:3600,media:1800,unknown:60},apps:[{name:'a',sec:1200},{name:'b',sec:4200}]},{date:'2026-09-28',totals:{active:1800,media:1800,unknown:0},apps:[{name:'a',sec:1800},{name:'b',sec:1800}]}];layoutWeekBars=()=>{};renderWeekChart();`);
 assert.equal(t.elements.get('week').children[0].children[1].children.length,1);
 assert.equal(t.elements.get('weekLegend').children.length,1);
 t.run("setAppFilter('a')");
 const first=t.elements.get('week').children[0],stack=first.children[1];
 assert.equal(first.children[0].textContent,'1h 30m');
 assert.equal(stack.children[0].className,'week-selected');
 assert.ok(Math.abs(parseFloat(stack.children[0].style.height)-1200/5400*100)<.001);
 assert.equal(stack.children[1].className,'week-other');
 assert.ok(Math.abs(parseFloat(stack.children[1].style.height)-4200/5400*100)<.001);
 assert.match(first.title,/a 20m，其他应用 1h 10m/);
 assert.equal(t.elements.get('weekLegend').children.length,2);
 t.run("setAppFilter('')");
 assert.equal(t.elements.get('week').children[0].children[1].children.length,1);
 assert.equal(t.elements.get('weekLegend').children.length,1);
 const html=fs.readFileSync(__dirname+'/dashboard/index.html','utf8');
 const weekPanel=html.match(/<article class="panel week-panel">[\s\S]*?<\/article>/)[0];
 assert.doesNotMatch(weekPanel,/媒体播放/);
});
test('点击应用立即重绘时间轴，再次点击和清除筛选恢复全部时段',()=>{
 const t=setup();t.run(`state.day={start:0,end:86400,until:180,shots:[],segments:[{start:0,end:120,dur_sec:120,kind:'active',process:'msedge.exe'},{start:120,end:180,dur_sec:60,kind:'media',process:'b'}]};renderStats();`);
 const draws=[];const ctx={scale(){},fillRect(x,y,w,h){draws.push({x,w,alpha:this.globalAlpha??1})},save(){},beginPath(){},rect(){},clip(){},moveTo(){},lineTo(){},stroke(){},restore(){}};
 t.run(`$('timeline')`);t.elements.get('timeline').getContext=()=>ctx;
 t.elements.get('apps').children[0].onclick();
 assert.equal(t.run('state.app'),'msedge.exe');assert.equal(draws.at(-2).alpha,1);assert.equal(draws.at(-1).alpha,0.06);
 assert.match(t.elements.get('timelineLabel').textContent,/Microsoft Edge/);assert.equal(t.elements.get('rows').children.length,1);
 const positions=draws.slice(-2).map(d=>[d.x,d.w]);
 t.elements.get('apps').children[0].onclick();assert.equal(t.run('state.app'),'');assert.equal(draws.at(-1).alpha,1);assert.deepEqual(draws.slice(-2).map(d=>[d.x,d.w]),positions);
 t.elements.get('apps').children[1].onclick();assert.equal(draws.at(-2).alpha,0.06);assert.equal(draws.at(-1).alpha,1);
 t.elements.get('clearFilter').onclick();assert.equal(t.run('state.app'),'');assert.equal(draws.at(-2).alpha,1);assert.equal(draws.at(-1).alpha,1);assert.equal(t.elements.get('timelineLabel').textContent,'全天活动');
});
test('快速拖动时旧截图的异步返回不能覆盖新截图',()=>{
 const t=setup();t.run(`state.day={shots:[{ts:100,file:'a.png'},{ts:160,file:'b.png'}],segments:[{start:0,end:200,kind:'active'}]};state.cursor=110;renderShot({kind:'active'});state.cursor=170;renderShot({kind:'active'});`);
 assert.equal(t.images.length,2);t.images[1].onload();assert.equal(t.elements.get('shot').src,'/shot/b.png');t.images[0].onload();assert.equal(t.elements.get('shot').src,'/shot/b.png');
});
test('回顾暂停或空洞时清除截图，边界之前的图片不能透传',()=>{
 const t=setup();t.run(`state.day={shots:[{ts:100,file:'a.png'}],segments:[{start:0,end:120,kind:'active'},{start:120,end:150,kind:'paused'},{start:150,end:200,kind:'active'}]};state.cursor=160;renderShot({kind:'active'});`);
 assert.equal(t.images.length,0);assert.equal(t.elements.get('expandShot').disabled,true);
 t.run(`state.cursor=110;renderShot({kind:'active'});renderShot({kind:'paused'});`);t.images[0].onload();assert.equal(t.elements.get('shot').hidden,true);
});
test('缺图、截图过旧、未来时间不会显示无关画面',()=>{
 const t=setup();t.run(`state.day={shots:[],segments:[]};state.cursor=Date.now()/1000;renderShot({kind:'active'});`);assert.equal(t.images.length,0);
 t.run(`state.day.shots=[{ts:state.cursor-100,file:'old.png'}];renderShot({kind:'active'});`);assert.equal(t.images.length,0);
 t.run(`state.day.shots=[{ts:state.cursor+1,file:'future.png'}];renderShot({kind:'active'});`);assert.equal(t.images.length,0);
});
test('时间指针在区间边界切换状态，并限制到已发生的时间',()=>{
 const t=setup();t.run(`state.day={start:0,end:86400,until:120,segments:[{start:0,end:60,kind:'active',process:'a',dur_sec:60},{start:60,end:120,kind:'paused',dur_sec:60}]};selectTime(60);`);assert.equal(t.elements.get('kind').textContent,'人工暂停');t.run('selectTime(80000)');assert.ok(t.run('state.cursor')<120);
});

test('持续拖动立即请求截图，不等待松手；回拖复用已加载图片',()=>{
 const t=setup();t.run(`state.day={start:0,end:86400,until:300,shots:[{ts:100,file:'a.png'},{ts:160,file:'b.png'}],segments:[{start:0,end:300,dur_sec:300,kind:'active'}]};selectTime(110);`);
 assert.equal(t.images.length,2);t.images[0].onload();
 assert.equal(t.images.length,2,'预读相邻截图');t.images[1].onload();
 t.run('selectTime(170)');assert.equal(t.elements.get('shot').src,'/shot/b.png');
 t.run('selectTime(110)');assert.equal(t.elements.get('shot').src,'/shot/a.png');assert.equal(t.images.length,2,'同一张图不重复下载');
});
test('图片缓存有界，快速跨时段拖动淘汰无用请求',()=>{
 const t=setup();t.run(`state.day={shots:[],segments:[]};for(let i=0;i<140;i++){state.shotFile='image'+i;loadRecallImage(state.shotFile);}`);
 assert.equal(t.run('state.shotCache.size'),12);assert.equal(t.images[0].src,'');
});
test('全屏按钮进入回顾容器，退出保留指针并同步按钮状态',async()=>{
 const t=setup();t.run(`$('recall').requestFullscreen=async()=>{document.fullscreenElement=$('recall')};document.exitFullscreen=async()=>{document.fullscreenElement=null};state.cursor=123;`);
 await t.run('toggleRecallFullscreen()');t.run('syncRecallFullscreen()');
 assert.equal(t.elements.get('fullscreenRecall')['aria-pressed'],'true');
 await t.run('toggleRecallFullscreen()');t.run('syncRecallFullscreen()');
 assert.equal(t.elements.get('fullscreenRecall')['aria-pressed'],'false');assert.equal(t.run('state.cursor'),123);
});
test('全屏请求被拒绝时展示原因',async()=>{
 const t=setup();t.run(`$('recall').requestFullscreen=async()=>{throw new Error('权限拒绝')};`);await t.run('toggleRecallFullscreen()');
 assert.equal(t.elements.get('fullscreenError').hidden,false);assert.match(t.elements.get('fullscreenError').textContent,/权限拒绝/);
});

test('应用筛选吸附匹配时段，拒绝其他应用前台截图，取消后恢复自由回顾',()=>{
 const t=setup();t.run("state.day={start:0,end:86400,until:300,shots:[{ts:90,file:'b.png'},{ts:160,file:'a.png'}],segments:[{start:0,end:100,kind:'active',process:'b',dur_sec:100},{start:100,end:200,kind:'active',process:'a',dur_sec:100},{start:200,end:300,kind:'active',process:'b',dur_sec:100}]};state.app='a';selectTime(50);");
 assert.equal(t.run('state.cursor'),160);assert.equal(t.images.length,1);
 t.run('selectTime(170)');assert.equal(t.images.length,1);t.images[0].onload();assert.equal(t.elements.get('shot').src,'/shot/a.png');
 t.run('selectTime(250)');assert.ok(t.run('state.cursor')<200);assert.equal(t.elements.get('process').textContent,'a');
 t.run("state.app='missing';selectTime(150)");assert.equal(t.elements.get('shot').hidden,true);
 t.run("state.app='';selectTime(250)");assert.equal(t.run('state.cursor'),250);
});

test('滚轮以鼠标时间为锚点缩放，保留应用筛选和回顾指针，并限制缩放边界',()=>{
 const t=setup();t.run("state.day={start:0,end:86400,until:86400,segments:[]};state.cursor=50000;state.app='a';$('timeline').getBoundingClientRect=()=>({left:100,width:800});");
 t.run("zoomTimelineWithWheel({deltaY:-100,clientX:300,preventDefault(){}})");
 assert.equal(t.run('state.zoom'),12);assert.equal(t.run('state.viewStart+0.25*state.zoom*3600'),21600);
 assert.equal(t.run('state.cursor'),50000);assert.equal(t.run('state.app'),'a');assert.equal(t.elements.get('zoom').value,'12');
 t.run("for(let i=0;i<20;i++)zoomTimelineWithWheel({deltaY:-100,clientX:300,preventDefault(){}})");
 assert.equal(t.run('state.zoom'),.25);assert.match(t.elements.get('timelineLabel').textContent,/15 分钟/);
 t.run("zoomTimelineWithWheel({ctrlKey:true,deltaY:100,clientX:300,preventDefault(){throw Error('不应拦截浏览器缩放')}})");
 assert.equal(t.run('state.zoom'),.25);
 t.run("for(let i=0;i<20;i++)zoomTimelineWithWheel({deltaY:100,clientX:900,preventDefault(){}})");
 assert.equal(t.run('state.zoom'),24);assert.equal(t.run('state.viewStart'),0);
});

test('筛选吸附跳过无截图的应用片段，视野内没有匹配截图时不强行跳转',()=>{
 const t=setup();t.run("state.day={start:0,end:86400,until:3600,shots:[{ts:300,file:'a.png'},{ts:960,file:'b.png'}],segments:[{start:0,end:900,kind:'active',process:'a'},{start:900,end:970,kind:'active',process:'b'},{start:970,end:1000,kind:'active',process:'a'},{start:1000,end:3600,kind:'active',process:'b'}]};state.app='a';state.zoom=.25;state.viewStart=900;selectTime(980);");
 assert.equal(t.run('state.cursor'),980,'没有截图的短片段不成为吸附目标');
 assert.equal(t.images.length,0);assert.equal(t.elements.get('shotEmpty').hidden,false);
 t.run("state.day.shots.push({ts:990,file:'match.png'});selectTime(980)");
 assert.equal(t.run('state.cursor'),990);assert.equal(t.images.length,2);t.images[0].onload();assert.equal(t.elements.get('shot').src,'/shot/match.png');
 t.run("state.day.shots=[];selectTime(985)");assert.equal(t.run('state.cursor'),985);assert.equal(t.elements.get('shot').hidden,true);
});

test('全屏应用列表与普通排行双向同步，支持全部应用及清除筛选',()=>{
 const t=setup();t.run("state.day={start:0,end:86400,until:1200,shots:[],segments:Array.from({length:12},(_,i)=>({start:i*100,end:(i+1)*100,dur_sec:100,kind:'active',process:'app'+i}))};renderStats();");
 assert.equal(t.elements.get('apps').children.length,8);assert.equal(t.elements.get('recallApps').children.length,12);
 t.elements.get('recallApps').scrollTop=60;t.elements.get('recallApps').children[9].onclick();
 assert.equal(t.run('state.app'),'app9');assert.equal(t.elements.get('recallApps').children[9]['aria-pressed'],'true');assert.equal(t.elements.get('recallApps').scrollTop,60);
 assert.match(t.elements.get('timelineLabel').textContent,/app9/);
 t.elements.get('apps').children[0].onclick();assert.equal(t.elements.get('recallApps').children[0]['aria-pressed'],'true');
 t.elements.get('recallClearFilter').onclick();assert.equal(t.run('state.app'),'');assert.equal(t.elements.get('recallClearFilter')['aria-pressed'],'true');assert.equal(t.elements.get('clearFilter').hidden,true);
});

test('预加载立即启动且并发有界，解码完成前不展示，缓存命中直接复用',async()=>{
 const t=setup();t.run("state.day={start:0,end:86400,until:2000,shots:Array.from({length:20},(_,i)=>({ts:i*60,file:i+'.png'})),segments:[{start:0,end:2000,kind:'active',process:'a'}]};selectTime(600);");
 assert.equal(t.images.length,3,'当前图片与两个后台预读立即启动');
 let resolveDecode;t.images[0].decode=()=>new Promise(r=>resolveDecode=r);t.images[0].onload();
 assert.equal(t.run("state.shotCache.get('10.png').ready"),false);
 resolveDecode();await Promise.resolve();assert.equal(t.run("state.shotCache.get('10.png').ready"),true);
 const done=new Set();
 for(let pass=0;pass<20;pass++)for(const img of [...t.images])if(!done.has(img)&&img!==t.images[0]){done.add(img);img.onload();}
 assert.ok(t.run('state.shotCache.size')<=12);
 assert.equal(t.run('[...state.shotCache.values()].filter(e=>e.ready).length'),11);
 const count=t.images.length;t.run('selectTime(660)');assert.equal(t.elements.get('shot').src,'/shot/11.png');
 assert.equal(t.elements.get('shotEmpty').hidden,true);assert.ok(t.images.length<=count+2);
});
test('全屏日期控件调用统一日期入口，不主动退出全屏',async()=>{
 const t=setup();t.run("var requested=[];loadDate=async d=>{requested.push(d)};$('date').value='2026-09-19';document.fullscreenElement=$('recall');$('recallDate').value='2026-09-17';");
 await t.elements.get('recallPrevDate').onclick();await t.elements.get('recallNextDate').onclick();await t.elements.get('recallDate').onchange();await t.elements.get('recallToday').onclick();
 assert.equal(t.run("requested.join(',')"),'2026-09-18,2026-09-20,2026-09-17,'+t.run('localDate()'));
 assert.equal(t.run("document.fullscreenElement===$('recall')"),true);
});

test('新截图加载时保留旧图，解码完成再替换；无图和读取失败立即清空',()=>{
 const t=setup();t.run("state.day={start:0,end:86400,until:500,shots:[{ts:100,file:'a.png'},{ts:160,file:'b.png'},{ts:220,file:'c.png'}],segments:[{start:0,end:400,kind:'active',process:'a'},{start:400,end:500,kind:'paused'}]};selectTime(110);");
 t.images[0].onload();const old=t.elements.get('shot');assert.equal(old.src,'/shot/a.png');
 t.run('selectTime(170)');assert.equal(t.elements.get('shot'),old);assert.equal(old.hidden,false);assert.equal(t.elements.get('shotEmpty').hidden,true);assert.match(t.elements.get('shotTime').textContent,/暂显示上一张/);
 t.run('selectTime(175)');assert.match(t.elements.get('shotTime').textContent,/暂显示上一张/);
 t.images.find(i=>i.src==='/shot/b.png').onload();assert.equal(t.elements.get('shot').src,'/shot/b.png');assert.doesNotMatch(t.elements.get('shotTime').textContent,/加载/);
 t.run('selectTime(230)');assert.equal(t.elements.get('shot').hidden,false);
 t.images.find(i=>i.src==='/shot/c.png').onerror();assert.equal(t.elements.get('shot').hidden,true);
 t.run('selectTime(110)');assert.equal(t.elements.get('shot').hidden,false);
 t.run('selectTime(390)');assert.equal(t.elements.get('shot').hidden,true);
 t.run('selectTime(110);selectTime(410)');assert.equal(t.elements.get('shot').hidden,true);
});

test('未筛选拖动吸附截图，键盘跨越空洞和视野跳转；筛选只跳指定应用',()=>{
 const t=setup();t.run("state.day={start:0,end:86400,until:4000,shots:[{ts:100,file:'a'},{ts:120,file:'paused'},{ts:500,file:'b'},{ts:2000,file:'c'},{ts:2100,file:'failed'}],segments:[{start:0,end:110,kind:'active',process:'a'},{start:110,end:200,kind:'paused'},{start:200,end:1000,kind:'media',process:'b'},{start:1000,end:4000,kind:'active',process:'a'}]};state.shotCache.set('failed',{failed:true});state.zoom=.25;state.viewStart=0;state.cursor=100;");
 assert.equal(t.run('nearestRecallTime(480)'),500);
 t.elements.get('scrubber').listeners.input({target:{value:'480'}});assert.equal(t.run('state.cursor'),500);let prevented=false;t.elements.get('scrubber').listeners.keydown({key:'ArrowLeft',preventDefault(){prevented=true}});assert.equal(prevented,true);assert.equal(t.run('state.cursor'),100);
 assert.equal(t.run('nearestRecallTime(130)'),100);
 t.run('stepRecallShot(1)');assert.equal(t.run('state.cursor'),500);
 t.run('stepRecallShot(1)');assert.equal(t.run('state.cursor'),2000);assert.ok(t.run('state.viewStart')>0);
 t.run('stepRecallShot(1)');assert.equal(t.run('state.cursor'),2000,'末尾不回绕，也不跳已知失败图片');
 t.run('stepRecallShot(-1)');assert.equal(t.run('state.cursor'),500);
 t.run("state.app='a';stepRecallShot(-1)");assert.equal(t.run('state.cursor'),100);
 t.run('stepRecallShot(1)');assert.equal(t.run('state.cursor'),2000);
 t.run("state.day.shots=[];stepRecallShot(-1)");assert.equal(t.run('state.cursor'),2000);
});

test('设置弹窗缩短保存期需确认，频率变化同步截图可用时长',()=>{
 const t=setup();t.run("settingsSnapshot={screenshot_retention_hours:24};$('retentionHours').value='12';updateRetentionWarning();");
 assert.equal(t.elements.get('shorterWarning').hidden,false);assert.equal(t.elements.get('confirmShorter').required,true);
 t.run("$('retentionHours').value='48';updateRetentionWarning();applyScreenshotSettings({screenshot_interval_sec:300,screenshot_retention_hours:48});");
 assert.equal(t.elements.get('shorterWarning').hidden,true);assert.equal(t.run('state.screenshotMaxAge'),450);assert.equal(t.run('state.screenshotRetentionSec'),172800);
});

test('活跃时间颜色只由状态决定，ChatGPT 与 Edge 一致且区别于媒体播放',()=>{
 const t=setup();t.run(`state.day={start:0,end:86400,until:180,segments:[{start:0,end:60,kind:'active',process:'ChatGPT.exe'},{start:60,end:120,kind:'active',process:'msedge.exe'},{start:120,end:180,kind:'media',process:'msedge.exe'}]};$('timeline');`);
 const colors=[];const ctx={scale(){},fillRect(){colors.push(this.fillStyle)},save(){},beginPath(){},rect(){},clip(){},moveTo(){},lineTo(){},stroke(){},restore(){}};
 t.elements.get('timeline').getContext=()=>ctx;t.run('renderTimeline()');
 assert.equal(colors[1],colors[2]);assert.notEqual(colors[1],colors[3]);assert.equal(colors[1],'#1266cc');
});
test('不足一分钟的媒体统计不能显示为零',()=>{
 const t=setup();t.run(`state.day={start:0,until:4,segments:[{start:0,end:4,kind:'media',process:'msedge.exe'}]};renderStats();`);
 assert.equal(t.elements.get('effective').textContent,'<1m');assert.equal(t.run('duration(0)'),'0m');
});

test('概览移除媒体播放卡片，渲染不再访问已删除元素',()=>{
 const html=fs.readFileSync(__dirname+'/dashboard/index.html','utf8');
 assert.doesNotMatch(html,/id="media"/);
 const metrics=html.match(/<section class="metrics"[\s\S]*?<\/section>/)[0];
 assert.equal((metrics.match(/<article /g)||[]).length,3);
 const t=setup();t.run(`const getElement=document.getElementById;document.getElementById=id=>{if(id==='media')throw Error('媒体指标已删除');return getElement(id)};state.day={start:0,until:120,segments:[{start:0,end:60,kind:'active',process:'a'},{start:60,end:120,kind:'media',process:'b'}]};renderStats();`);
 assert.equal(t.elements.get('effective').textContent,'2m');assert.equal(t.elements.get('active').textContent,'1m');
});
