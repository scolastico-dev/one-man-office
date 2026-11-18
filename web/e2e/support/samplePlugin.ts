import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import { REPO_DIR, type Omo } from './omo'
import { makePlugin } from './plugin'

/** One external-style sample composes declarations, public assets, routes,
 * startup, separate task hooks and authenticated forward navigation. */
export async function installSample(omo: Omo) {
  const manifest = {
    name: 'sample', version: '1.0.0', description: 'Complete extension lifecycle sample',
    default_config: { token: 'fixture-secret-value', mode: 'one' },
    config_fields: [{ key: 'mode', label: 'Sample mode', type: 'select', choices: [{ value: 'one', label: 'One' }, { value: 'two', label: 'Two' }] }],
    frontend: { script: 'frontend.js', assets: 'assets' },
    buttons: [
      { place: 'ticket', label: 'Start sample ticket', action: 'ticket' },
      { place: 'sidebar', label: 'Sample status', action: 'status' },
      { place: 'project', label: 'Sample repository', action: 'project' },
    ],
    hooks: [
      { event: 'omo_started', lua: 'startup.lua' },
      ...['ticket', 'project', 'status', 'stop', 'open'].map(name => ({ event: 'manual', name, description: 'Sample ' + name, scope: name === 'ticket' ? 'ticket' : 'global', hidden: true, lua: 'action.lua' })),
    ],
  }
  const entry = `(() => {
    const omo = document.currentScript.omo;
    omo.initialize(() => {
      let routes = 0;
      omo.addButton({id:'list',label:'Dynamic status',place:'sidebar',action:'status'});
      omo.addButton({id:'open',label:'Open sample',place:'sidebar',action:'open'});
      omo.addButton({id:'stop',label:'Stop sample',place:'ticket',run:()=>omo.runHook('stop')});
      return omo.onRouteChange(context => {
        routes++;
        for (const place of ['sidebar','ticket']) omo.addPanel({id:place,place,title:'Sample route',text:context.page+' route '+routes,image:omo.assetUrl('mark.svg')});
      });
    });
  })();`
  const repo = makePlugin(join(omo.home, 'sample-source'), manifest, {
    'frontend.js': entry,
    'assets/mark.svg': '<svg xmlns="http://www.w3.org/2000/svg" width="12" height="12"><rect width="12" height="12" fill="green"/></svg>',
    'startup.lua': 'assert(omo.store_set("starts", (omo.store_get("starts") or 0)+1))',
    'worker.cjs': 'console.log("Credential: " + process.env.SAMPLE_TOKEN); console.log("Visible: \\u202e text");\n' + readFileSync(join(REPO_DIR, 'web/e2e/fixtures/editor.cjs'), 'utf8'),
    'action.lua': `local action=event.data.action
      if action=='status' then
        local ts=assert(omo.task_list()); local running=0
        for _,t in ipairs(ts) do if t.state=='running' then running=running+1 end end
        return {message='starts='..tostring(omo.store_get('starts') or 0)..' running='..running..' mode='..config.mode}
      end
      if action=='stop' then for _,t in ipairs(assert(omo.task_list())) do if t.state=='running' then assert(omo.task_stop(t.name)) end end;return {message='Stopped from another hook'} end
      if action=='open' then return {forward_id=assert(omo.forward_list())[1].id} end
      if action=='project' then return {message=event.data.project.kind..': '..event.data.project.path} end
      local ticket=event.data.ticket.number
      local port=assert(omo.free_loopback_port())
      local t=assert(omo.task_start{name='worker-'..ticket,command=${JSON.stringify(process.execPath)},args={'worker.cjs','--host','127.0.0.1','--port',tostring(port),'--default-folder',${JSON.stringify(omo.home)},'--without-connection-token'},ticket=ticket,env={SAMPLE_TOKEN=config.token}})
      assert(omo.forward_add{name='Sample worker',port=port,ticket=ticket,task_id=t.id,strip_prefix=true})
      return {message='Started '..t.name}`,
  })
  await omo.api('POST', '/api/plugins', { name: 'sample', source: repo.source, acknowledge: 'community' })
  return { repo, manifest, entry }
}
