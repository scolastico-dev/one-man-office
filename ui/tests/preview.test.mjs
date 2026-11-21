import {readFileSync} from 'node:fs';
import vm from 'node:vm';
import {test} from 'node:test';
import assert from 'node:assert/strict';
test('preview documents have no native wrapper and retain ordinary page input',()=>{
  const window={__TAURI__:{core:{invoke(){throw Error('native API reached')}}}};
  const events=[];
  const context={window,document:{addEventListener(...args){events.push(args)}}};
  vm.runInNewContext(readFileSync(new URL('../src-tauri/src/preview.js',import.meta.url),'utf8'),context);
  assert.equal(window.__TAURI__,undefined);
  assert.deepEqual(events,[]);
});
