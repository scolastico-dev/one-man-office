(() => {
  'use strict';
  const omo = document.currentScript.omo;
  omo.initialize(() => {
    const pending = new Map();
    let sequence = 0;
    function active() {
      if (omo.signal.aborted) throw new Error('VS Code frontend disabled; polling aborted.');
    }
    function pause() {
      active();
      return new Promise((resolve, reject) => {
        const abort = () => { clearTimeout(timer); reject(new Error('VS Code frontend disabled; polling aborted.')); };
        const timer = setTimeout(() => { omo.signal.removeEventListener('abort', abort); resolve(); }, 1500);
        omo.signal.addEventListener('abort', abort, { once: true });
      });
    }
    function checkDeadline(started) {
      active();
      if (Date.now() - started > 35 * 60 * 1000) throw new Error('Editor installation timed out. Check the plugin log and task output before retrying.');
    }
    async function runHook(action, payload, started) {
      for (;;) {
        checkDeadline(started);
        try {
          return await omo.runHook(action, payload);
        } catch (error) {
          // This exact refusal guarantees the hook never ran. Every other
          // failure, including a lost response, must be surfaced without retry.
          if (error?.status !== 409 || error?.code !== 'conflict' || error?.message !== 'an action of plugin vscode is already running') throw error;
          await pause();
        }
      }
    }
    function open(context, place) {
      active();
      const payload = { project: context.project };
      if (place === 'ticket') payload.ticket = context.ticket?.number;
      const key = `${payload.ticket ?? 'project'}:${payload.project ?? ''}`;
      if (pending.has(key)) return pending.get(key);
      const panel = `opening-${++sequence}`;
      const work = (async () => {
        const action = place === 'ticket' ? 'ticket' : 'project';
        const started = Date.now();
        try {
          // The board has already reserved the new tab during the gesture.
          // Never retry a lost action response: it may have started the editor.
          let result = await runHook(`open-${action}`, payload, started);
          while (result.message?.startsWith('VS Code pending:')) {
            active();
            omo.addPanel({ id: panel, place: place === 'ticket' ? 'ticket' : 'sidebar', title: 'Opening VS Code',
              text: `${payload.project ?? 'Selected folder'}: ${result.message.slice(17).trim()}` });
            checkDeadline(started);
            await pause();
            active();
            result = await runHook(`status-${action}`, payload, started);
          }
          active();
          return result;
        } finally {
          pending.delete(key);
          if (!omo.signal.aborted) omo.removePanel(panel);
        }
      })();
      pending.set(key, work);
      return work;
    }
    omo.addButton({ id: 'open-ticket', label: 'Open in VS Code', place: 'ticket', per_worktree: true,
      run: context => open(context, 'ticket') });
    omo.addButton({ id: 'open-project', label: 'Open in VS Code', place: 'project',
      run: context => open(context, 'project') });
    return () => pending.clear();
  });
})();
