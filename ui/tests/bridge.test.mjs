import { readFileSync } from 'node:fs';
import vm from 'node:vm';
import { test } from 'node:test';
import assert from 'node:assert/strict';
const source = readFileSync(new URL('../src-tauri/src/bridge.js', import.meta.url), 'utf8');
function load(origin = 'https://board.example', pathname = '/', native = true) {
 const entries = new Map(); const events = [];
 const window = { location: { origin, pathname }, Notification: function BrowserNotification() {}, localStorage: { setItem(k,v) { entries.set(k,v) }, removeItem(k) {entries.delete(k)} }, addEventListener(...args) {events.push(args)} };
 window.window = window; window.top = window;
 const config = { origin:'https://board.example', auth:'token', secret:'a'.repeat(64), native };
 vm.runInNewContext(`(${source})(${JSON.stringify(config)})`, {window, document:{readyState:'loading',addEventListener(...args){events.push(args)}}});
 return {window,entries,events};
}
test('authenticates the board without putting its token in a URL', () => {
 const result=load(); assert.equal(result.entries.get('omo.token'),'a'.repeat(64));
 assert.equal(result.window.location.origin,'https://board.example');
});
test('only a configured board document receives credentials and native suppression', () => {
 for (const [origin,path] of [['https://evil.example','/'],['https://board.example','/proxy/abc/'],['https://board.example','/_omo/proxy/bootstrap']]) {
 const result=load(origin,path);assert.equal(result.entries.size,0);assert.equal(result.window.__OMO_NATIVE_UI__,undefined);assert.equal(typeof result.window.Notification,'function');
 }
});
test('active native delivery suppresses browser duplicates without changing stored browser choice', () => {
 const result=load();assert.equal(result.window.__OMO_NATIVE_UI__.notifications,true);assert.equal(result.window.Notification,undefined);assert.equal(result.entries.has('omo.desktopNotifications'),false);
 const fallback=load('https://board.example','/',false);assert.equal(fallback.window.__OMO_NATIVE_UI__.notifications,false);assert.equal(typeof fallback.window.Notification,'function');
});
test('the bridge never intercepts keyboard or clipboard events', () => {
 const {events}=load();assert.deepEqual(events.map(e=>e[0]),['DOMContentLoaded']);
});
test('blocked browser storage uses the existing token fragment fallback once', () => {
  const run = (hash) => {
    const moved=[];
    const window={location:{origin:'https://board.example',pathname:'/',search:'',hash,replace(value){moved.push(value)}},localStorage:{setItem(){throw new Error('blocked')}},Notification:function(){}};
    window.top=window;
    const document={readyState:'loading',addEventListener(){}};
    vm.runInNewContext(`(${source})(${JSON.stringify({origin:'https://board.example',auth:'token',secret:'a'.repeat(64),native:true})})`,{window,document});
    return {moved,window};
  };
  assert.deepEqual(run('').moved,['/#'+'a'.repeat(64)]);
  const repeat=run('#'+'a'.repeat(64));
  assert.deepEqual(repeat.moved,[]);
  assert.equal(repeat.window.__OMO_NATIVE_UI__.notifications,true);
});
test('mobile token bootstrap stays in the loaded configured document', () => {
 const moved=[];
 const window={location:{origin:'https://board.example',pathname:'/',search:'',hash:'',replace(value){moved.push(value)}},localStorage:{setItem(){}},Notification:function(){}};
 window.top=window;
 vm.runInNewContext(`(${source})(${JSON.stringify({origin:'https://board.example',auth:'token',secret:'a'.repeat(64),native:false,capture:true})})`,{window,document:{readyState:'loading',addEventListener(){}}});
 assert.equal(window.location.hash,'a'.repeat(64));
 assert.deepEqual(moved,[]);
});
