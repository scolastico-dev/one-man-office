'use strict';

(function (root, factory) {
  if (typeof module === 'object' && module.exports) module.exports = factory(root, root.document, require('./helpers.js'));
  else factory(root, root.document, root.FilebrowserHelpers);
})(typeof globalThis === 'object' ? globalThis : this, function (root, document, initialHelpers) {
  const DEFAULT_CONFIG = Object.freeze({
    download_warn_bytes: 52428800,
    download_max_bytes: 1073741824,
    upload_warn_bytes: 52428800,
    upload_max_bytes: 1073741824,
  });

  function createFilebrowser(win = root, doc = document) {
    const omo = win?.omo || {};
    let helpers = initialHelpers || win?.FilebrowserHelpers;
    let started = false;
    let initCount = 0;
    let probeCount = 0;
    let supported = true;
    let ready = Promise.resolve();
    let config = {...DEFAULT_CONFIG};
    let state = {projects: [], instances: []};
    let roots = [];
    let homePath = '/';
    let homeResolved = false;
    let currentPath = '/';
    let pickerMode = false;
    let sortField = 'name';
    let sortDirection = 'asc';
    let currentEntries = [];
    let listGeneration = 0;
    let actionElements = [];
    let activeTransfer = null;

    const LARGE_TRANSFER_BYTES = 4 * 1024 * 1024;

    const byID = key => {
      const id = omo.ids?.[key] || key;
      return doc?.getElementById?.(id) || null;
    };
    const projectInput = () => doc?.getElementById?.('project-path') || null;
    const append = (parent, ...children) => {
      if (parent?.append) parent.append(...children);
      else children.forEach(child => parent?.appendChild?.(child));
    };
    const clear = element => {
      if (element?.replaceChildren) element.replaceChildren();
      else if (element) element.textContent = '';
    };
    const text = (element, value) => { if (element) element.textContent = String(value ?? ''); };
    const safeMessage = message => {
      const value = String(message || '');
      return omo.token ? value.split(String(omo.token)).join('[redacted]') : value;
    };

    function fallbackHelpers() {
      return {
        normalizePath: path => String(path || '').startsWith('/') ? ('/' + String(path).split('/').filter(part => part && part !== '.').reduce((parts, part) => { if (part === '..') parts.pop(); else parts.push(part); return parts; }, []).join('/')) || '/' : '',
        parentPath: path => { const normalized = path || '/'; return normalized === '/' ? '/' : normalized.slice(0, normalized.lastIndexOf('/')) || '/'; },
        breadcrumbs: path => [{label: '/', path}],
        accumulateOutput: (events, stream = 'stdout') => events.filter(event => event?.stream === stream).map(event => String(event.data || '')).join(''),
        accumulateStdout: events => events.filter(event => event?.stream === 'stdout').map(event => String(event.data || '')).join(''),
        parseNullListing: (output, type = 'file') => ({entries: String(output || '').split('\0').filter(Boolean).map(path => ({name: path.slice(path.lastIndexOf('/') + 1), type})), skippedNewlineNames: false}),
        parseListingWithNotice: output => ({entries: String(output || '').split(/\r?\n/).filter(Boolean).map(name => ({name: name.replace(/\/$/, ''), type: name.endsWith('/') ? 'directory' : 'file'})), skippedNewlineNames: false}),
        sortEntries: entries => entries,
        buildRoots: (value, home) => [{label: 'Home', path: home}],
        validateFolderComponent: value => value ? '' : 'Enter one non-empty folder name without slashes.',
        parseByteCount: output => Number(String(output).match(/\d+/)?.[0] || '') || null,
        joinPath: (dir, name) => !name || /[\/\0\r\n]/.test(name) || name === '.' || name === '..' ? '' : (dir === '/' ? '' : dir) + '/' + name,
      };
    }

    async function loadHelpers() {
      if (helpers) return helpers;
      if (!doc?.createElement || !doc?.head?.append) return helpers = fallbackHelpers();
      await new Promise((resolve, reject) => {
        const script = doc.createElement('script');
        const source = doc.currentScript?.src || '/plugins/filebrowser/web/main.js';
        script.src = source.replace(/main\.js(?:\?.*)?$/, 'helpers.js');
        script.async = false;
        script.onload = resolve;
        script.onerror = () => reject(new Error('Failed to load filebrowser helpers.'));
        doc.head.append(script);
      });
      return helpers = win.FilebrowserHelpers || fallbackHelpers();
    }

    function normalizeConfig(value) {
      const source = value && typeof value === 'object' ? value : {};
      const threshold = (key, fallback) => {
        const raw = source[key];
        const number = Number(raw);
        return typeof raw === 'number' && Number.isSafeInteger(number) && number >= 0 ? number : fallback;
      };
      return {
        download_warn_bytes: threshold('download_warn_bytes', DEFAULT_CONFIG.download_warn_bytes),
        download_max_bytes: threshold('download_max_bytes', DEFAULT_CONFIG.download_max_bytes),
        upload_warn_bytes: threshold('upload_warn_bytes', DEFAULT_CONFIG.upload_warn_bytes),
        upload_max_bytes: threshold('upload_max_bytes', DEFAULT_CONFIG.upload_max_bytes),
      };
    }

    function make(tag, className, label) {
      const element = doc?.createElement?.(tag);
      if (!element) return null;
      if (className) element.className = className;
      if (label !== undefined) text(element, label);
      return element;
    }

    function setMessage(message, kind = '') {
      const area = doc?.getElementById?.('filebrowser-message');
      text(area, safeMessage(message));
      if (area) area.dataset.kind = kind;
    }

    function registerAction(element, action) {
      if (element) { element.dataset.filebrowserAction = action; actionElements.push(element); if (!supported) element.disabled = true; }
      return element;
    }

    function transferAdvice(direction, name, size) {
      const amount = `${size} bytes`;
      return `This ${direction} is ${amount}. Huge files are not recommended through the omo company dashboard; fetch them directly, for example with ssh/scp. Continue?`;
    }

    function beginTransfer(label, total = 0, determinate = total > LARGE_TRANSFER_BYTES) {
      const status = doc?.getElementById?.('filebrowser-progress-status');
      const progress = doc?.getElementById?.('filebrowser-progress');
      const cancel = doc?.getElementById?.('filebrowser-cancel-transfer');
      text(status, safeMessage(label));
      if (progress) {
        progress.hidden = false;
        progress.max = determinate ? total : 1;
        if (determinate) progress.value = 0;
        else progress.removeAttribute?.('value');
      }
      if (cancel) cancel.hidden = false;
    }

    function updateTransferProgress(done, total) {
      const progress = doc?.getElementById?.('filebrowser-progress');
      if (progress && total > LARGE_TRANSFER_BYTES) { progress.max = total; progress.value = Math.min(done, total); }
    }

    function clearTransfer() {
      const status = doc?.getElementById?.('filebrowser-progress-status');
      const progress = doc?.getElementById?.('filebrowser-progress');
      const cancel = doc?.getElementById?.('filebrowser-cancel-transfer');
      text(status, '');
      if (progress) { progress.hidden = true; progress.value = 0; progress.removeAttribute?.('value'); }
      if (cancel) cancel.hidden = true;
      activeTransfer = null;
    }

    function makeTransferController() {
      const AbortControllerConstructor = win?.AbortController || root?.AbortController;
      if (!AbortControllerConstructor) return {signal: undefined, abort() { this.aborted = true; }};
      const controller = new AbortControllerConstructor();
      return {signal: controller.signal, abort: () => controller.abort()};
    }

    function injectBrowseButton() {
      const input = projectInput();
      if (!input || doc?.getElementById?.('filebrowser-browse')) return;
      const button = make('button', 'filebrowser-browse', 'Browse...');
      if (!button) return;
      button.type = 'button';
      button.id = 'filebrowser-browse';
      button.onclick = () => openBrowser(true).catch(error => setMessage(error.message, 'warning'));
      if (input.parentNode?.insertBefore) input.parentNode.insertBefore(button, input.nextSibling);
      else input.parentNode?.appendChild?.(button);
    }

    function injectUI() {
      if (!doc?.getElementById?.('filebrowser-style') && doc?.head && doc?.createElement) {
        const style = make('link');
        style.id = 'filebrowser-style'; style.rel = 'stylesheet'; style.href = '/plugins/filebrowser/web/style.css';
        append(doc.head, style);
      }
      injectBrowseButton();
      const sidebar = byID('sidebar');
      const toolbar = byID('toolbar');
      const main = byID('main');
      if (!doc?.getElementById?.('filebrowser-panel') && sidebar) {
        const panel = make('section', 'panel filebrowser-panel');
        panel.id = 'filebrowser-panel';
        const details = make('details', 'filebrowser-details');
        const summary = make('summary', '', 'Files');
        const hint = make('p', 'filebrowser-hint', 'Browse trusted project paths or the whole disk.');
        append(details, summary, hint);
        append(panel, details);
        append(sidebar, panel);
      }
      if (!doc?.getElementById?.('filebrowser-button') && toolbar) {
        const button = make('button', '', 'Files');
        button.id = 'filebrowser-button';
        button.type = 'button';
        button.onclick = () => openBrowser(false).catch(error => setMessage(error.message, 'warning'));
        append(toolbar, button);
      }
      if (!doc?.getElementById?.('filebrowser-overlay') && main) {
        const overlay = make('dialog', 'panel filebrowser-overlay');
        overlay.id = 'filebrowser-overlay';
        overlay.hidden = true;
        const heading = make('div', 'filebrowser-heading');
        const title = make('h2', '', 'Files');
        const close = make('button', '', 'Close');
        close.type = 'button'; close.id = 'filebrowser-close'; close.onclick = closeBrowser;
        append(heading, title, close);
        const controls = make('div', 'filebrowser-controls');
        const rootSelect = make('select'); rootSelect.id = 'filebrowser-root'; rootSelect.onchange = () => navigate(rootSelect.value);
        const path = make('input'); path.id = 'filebrowser-path'; path.type = 'text'; path.placeholder = '/absolute/path';
        const go = make('button', '', 'Go'); go.type = 'button'; go.onclick = () => navigateFromInput();
        path.onkeydown = event => { if (event.key === 'Enter') { event.preventDefault(); navigateFromInput(); } };
        const hidden = make('label', 'filebrowser-hidden');
        const checkbox = make('input'); checkbox.type = 'checkbox'; checkbox.id = 'filebrowser-show-hidden'; checkbox.onchange = () => listDirectory();
        append(hidden, checkbox, ' Show hidden files');
        const refresh = registerAction(make('button', '', 'Refresh'), 'refresh'); refresh.type = 'button'; refresh.id = 'filebrowser-refresh'; refresh.onclick = () => refreshCurrent();
        const uploadLabel = make('label', 'filebrowser-upload-label', 'Upload');
        const upload = registerAction(make('input'), 'upload'); upload.type = 'file'; upload.id = 'filebrowser-upload'; upload.multiple = true;
        upload.onchange = () => uploadFiles(upload.files).finally(() => { upload.value = ''; });
        append(uploadLabel, upload); append(controls, rootSelect, path, go, hidden, refresh, uploadLabel);
        const crumbs = make('nav', 'filebrowser-breadcrumbs'); crumbs.id = 'filebrowser-breadcrumbs'; crumbs.setAttribute?.('aria-label', 'Path breadcrumbs');
        const message = make('p', 'filebrowser-message'); message.id = 'filebrowser-message'; message.setAttribute?.('role', 'status');
        const progress = make('progress', 'filebrowser-progress'); progress.id = 'filebrowser-progress'; progress.hidden = true; progress.max = 1; progress.value = 0;
        const progressStatus = make('p', 'filebrowser-progress-status'); progressStatus.id = 'filebrowser-progress-status'; progressStatus.setAttribute?.('role', 'status');
        const cancelTransfer = make('button', '', 'Cancel transfer'); cancelTransfer.type = 'button'; cancelTransfer.id = 'filebrowser-cancel-transfer'; cancelTransfer.hidden = true; cancelTransfer.onclick = () => activeTransfer?.abort?.();
        const note = make('p', 'filebrowser-note', 'Names containing line breaks cannot be displayed.'); note.id = 'filebrowser-newline-note';
        const table = make('table', 'filebrowser-table');
        const thead = make('thead'); const headerRow = make('tr');
        for (const [field, label] of [['name', 'Name'], ['type', 'Type'], ['size', 'Size']]) {
          const th = make('th'); const button = make('button', 'filebrowser-sort', label); button.type = 'button';
          button.id = `filebrowser-sort-${field}`;
          button.onclick = () => { if (sortField === field) sortDirection = sortDirection === 'asc' ? 'desc' : 'asc'; else { sortField = field; sortDirection = 'asc'; } renderRows(currentEntries); };
          append(th, button); append(headerRow, th);
        }
        append(thead, headerRow); const body = make('tbody'); body.id = 'filebrowser-rows'; append(table, thead, body);
        const pickerActions = make('div', 'filebrowser-picker-actions');
        const newFolder = registerAction(make('button', '', 'New folder'), 'mkdir'); newFolder.type = 'button'; newFolder.id = 'filebrowser-new-folder'; newFolder.onclick = () => createFolder();
        const cancel = make('button', '', 'Cancel'); cancel.type = 'button'; cancel.onclick = closeBrowser;
        const select = make('button', '', 'Select'); select.type = 'button'; select.id = 'filebrowser-select'; select.onclick = selectPicker;
        append(pickerActions, newFolder, cancel, select);
        append(overlay, heading, controls, crumbs, message, note, progressStatus, progress, cancelTransfer, table, pickerActions);
        append(main, overlay);
      }
    }

    function setUnsupported() {
      supported = false;
      const panel = doc?.getElementById?.('filebrowser-panel');
      const details = panel?.querySelector?.('details');
      if (details) details.open = true;
      const warning = doc?.getElementById?.('filebrowser-warning') || make('p', 'filebrowser-warning');
      if (warning) { warning.id = 'filebrowser-warning'; text(warning, 'The file manager is not supported on Windows'); append(details || panel, warning); }
      for (const element of actionElements) element.disabled = true;
      for (const element of doc?.querySelectorAll?.('#filebrowser-browse, #filebrowser-button, #filebrowser-refresh, #filebrowser-upload, .filebrowser-download, #filebrowser-new-folder') || []) element.disabled = true;
    }

    async function execute(command, args, options = {}) {
      if (typeof omo.execute !== 'function') throw new Error('The file manager is unavailable.');
      return omo.execute(command, args, {...options, cwd: 'home'});
    }

    async function probe() {
      probeCount++;
      try { await execute('uname', ['-s']); }
      catch { setUnsupported(); }
    }

    async function resolveHome() {
      if (homeResolved) return homePath;
      homeResolved = true;
      const events = [];
      try { await execute('pwd', [], {onOutput: event => events.push(event)}); const value = helpers.accumulateStdout(events).trim().split(/\r?\n/)[0]; if (helpers.normalizePath(value)) homePath = helpers.normalizePath(value); }
      catch { /* Root remains a safe fallback when pwd is unavailable. */ }
      return homePath;
    }

    async function fetchState() {
      const fetcher = win?.fetch || root?.fetch;
      if (typeof fetcher !== 'function') return state;
      const headers = {};
      if (omo.token) headers.Authorization = 'Bearer ' + omo.token;
      const response = await fetcher('/api/state', {headers, cache: 'no-store'});
      if (!response.ok) throw new Error(await response.text());
      return state = await response.json();
    }

    function renderRoots() {
      const select = doc?.getElementById?.('filebrowser-root');
      if (!select) return;
      clear(select);
      for (const rootPath of roots) {
        const option = make('option', '', rootPath.label); option.value = rootPath.path; option.selected = rootPath.path === currentPath;
        append(select, option);
      }
    }

    function renderBreadcrumbs() {
      const container = doc?.getElementById?.('filebrowser-breadcrumbs');
      if (!container) return;
      clear(container);
      for (const crumb of helpers.breadcrumbs(currentPath)) {
        const button = make('button', '', crumb.label); button.type = 'button'; button.onclick = () => navigate(crumb.path); append(container, button);
      }
    }

    async function findEntries(type, events) {
      const output = [];
      const args = [currentPath, '-mindepth', '1', '-maxdepth', '1'];
      if (type === 'directory') args.push('-type', 'd');
      else args.push('!', '-type', 'd');
      args.push('-print0');
      await execute('find', args, {onOutput: event => { events.push(event); output.push(event); }});
      return helpers.parseNullListing(helpers.accumulateStdout(output), type);
    }

    async function listDirectory() {
      if (!supported) return;
      const generation = ++listGeneration;
      const events = [];
      const hidden = doc?.getElementById?.('filebrowser-show-hidden')?.checked;
      try {
        const directories = await findEntries('directory', events);
        const parsed = pickerMode ? directories : await findEntries('file', events);
        const entries = [...parsed.entries, ...(pickerMode ? [] : directories.entries)];
        const skippedNewlineNames = parsed.skippedNewlineNames || directories.skippedNewlineNames;
        const visibleEntries = hidden ? entries : entries.filter(entry => !entry.name.startsWith('.'));
        for (const entry of visibleEntries) {
          if (entry.type === 'directory') { entry.size = null; continue; }
          const sizeEvents = [];
          try { await execute('wc', ['-c', helpers.joinPath(currentPath, entry.name)], {onOutput: event => sizeEvents.push(event)}); entry.size = helpers.parseByteCount(helpers.accumulateStdout(sizeEvents)); }
          catch { entry.size = null; }
        }
        if (generation !== listGeneration) return;
        setMessage(skippedNewlineNames ? 'Some names were skipped because they contain line breaks.' : '', skippedNewlineNames ? 'warning' : '');
        currentEntries = visibleEntries;
        renderRows(currentEntries);
      } catch (error) {
        if (generation !== listGeneration) return;
        const stderr = helpers.accumulateOutput(events, 'stderr');
        setMessage(stderr.trim() || safeMessage(error.message), 'warning');
        currentEntries = [];
        renderRows(currentEntries);
      }
    }

    async function commandOutput(command, args, signal) {
      const events = [];
      await execute(command, args, {signal, onOutput: event => events.push(event)});
      return helpers.accumulateStdout(events);
    }

    async function downloadFile(filePath) {
      if (!supported) return;
      if (!filePath) throw new Error('The download destination is not a safe file path.');
      const name = filePath.slice(filePath.lastIndexOf('/') + 1);
      const controller = makeTransferController();
      activeTransfer = controller;
      try {
        try { await execute('test', ['-f', filePath], {signal: controller.signal}); }
        catch { throw new Error('The selected download source is not a regular file.'); }
        const size = helpers.parseByteCount(await commandOutput('wc', ['-c', filePath], controller.signal));
        if (size == null) throw new Error('Unable to determine the download size.');
        const decision = helpers.transferThreshold(size, config.download_warn_bytes, config.download_max_bytes);
        if (decision === 'reject') throw new Error(`The download exceeds the configured download limit of ${config.download_max_bytes} bytes.`);
        if (decision === 'warn' && !await dialogRequest('confirm', transferAdvice('download', name, size))) return;
        beginTransfer(`Downloading ${name}`, size);
        const parts = [];
        let decodedBytes = 0;
        const decoder = helpers.createBase64Decoder(bytes => { parts.push(bytes); decodedBytes += bytes.length; updateTransferProgress(decodedBytes, size); });
        await execute('base64', [filePath], {signal: controller.signal, onOutput: event => { if (event?.stream === 'stdout') decoder.push(event.data); }});
        decoder.finish();
        const BlobConstructor = win?.Blob || root?.Blob;
        const URLConstructor = win?.URL || root?.URL;
        if (typeof BlobConstructor !== 'function' || !URLConstructor?.createObjectURL) throw new Error('Browser downloads are unavailable.');
        const blob = new BlobConstructor(parts, {type: 'application/octet-stream'});
        const objectURL = URLConstructor.createObjectURL(blob);
        const link = make('a');
        link.href = objectURL; link.download = name; link.hidden = true;
        append(doc?.body, link);
        try { link.click?.(); }
        finally { link.remove?.(); URLConstructor.revokeObjectURL?.(objectURL); }
      } finally {
        clearTransfer();
      }
    }

    async function destinationExists(path, signal) {
      try { await execute('test', ['-e', path], {signal}); return true; }
      catch { return false; }
    }

    async function uploadFiles(files) {
      if (!supported) return;
      const selected = Array.from(files || []);
      if (!selected.length) return;
      const controller = makeTransferController();
      activeTransfer = controller;
      try {
        for (let index = 0; index < selected.length; index++) {
          const file = selected[index];
          const name = String(file?.name || '');
          const size = Number(file?.size);
          beginTransfer(`Uploading ${name} (${index + 1}/${selected.length})`, size, false);
          try {
            const invalidName = helpers.validateFileComponent(name);
            if (invalidName) throw new Error(invalidName);
            if (!Number.isSafeInteger(size) || size < 0) throw new Error('The selected file has an invalid size.');
            const destination = helpers.joinPath(currentPath, name);
            if (!destination) throw new Error('The upload destination is not a safe file path.');
            const decision = helpers.transferThreshold(size, config.upload_warn_bytes, config.upload_max_bytes);
            if (decision === 'reject') throw new Error(`The upload exceeds the configured upload limit of ${config.upload_max_bytes} bytes.`);
            if (decision === 'warn' && !await dialogRequest('confirm', transferAdvice('upload', name, size))) continue;
            if (await destinationExists(destination, controller.signal) && !await dialogRequest('confirm', `The file ${destination} already exists. Overwrite it?`)) continue;
            const stderr = [];
            try {
              await execute('dd', [`of=${destination}`], {stdin: file, signal: controller.signal, onOutput: event => { if (event?.stream === 'stderr') stderr.push(event.data); }});
            } catch (error) {
              throw new Error(stderr.join('').trim() || error.message);
            }
            await refreshCurrent();
          } catch (error) {
            setMessage(error?.name === 'AbortError' ? 'Transfer canceled.' : error.message, 'warning');
            if (controller.signal?.aborted || error?.name === 'AbortError') break;
          }
        }
      } finally {
        clearTransfer();
      }
    }

    function renderRows(entries) {
      const body = doc?.getElementById?.('filebrowser-rows');
      if (!body) return;
      clear(body);
      const parentRow = make('tr');
      const parentCell = make('td'); const parentButton = make('button', 'filebrowser-row-name', '..');
      parentButton.type = 'button'; parentButton.onclick = () => navigate(helpers.parentPath(currentPath));
      append(parentCell, parentButton); append(parentRow, parentCell, make('td', '', 'directory'), make('td', '', '--')); append(body, parentRow);
      const sorted = helpers.sortEntries(entries || [], sortField, sortDirection);
      for (const entry of sorted) {
        const row = make('tr');
        const name = make('td'); const button = make('button', 'filebrowser-row-name', entry.name);
        button.type = 'button'; button.disabled = entry.type !== 'directory'; button.onclick = () => navigate(helpers.joinPath(currentPath, entry.name));
        append(name, button);
        if (entry.type !== 'directory') {
          const download = registerAction(make('button', 'filebrowser-download', 'Download'), 'download');
          download.type = 'button'; download.onclick = () => downloadFile(helpers.joinPath(currentPath, entry.name)).catch(error => setMessage(error.message, 'warning'));
          append(name, download);
        }
        append(row, name, make('td', '', entry.type), make('td', '', entry.size == null ? '--' : formatBytes(entry.size)));
        append(body, row);
      }
    }

    function formatBytes(bytes) {
      return Number.isFinite(Number(bytes)) ? `${Number(bytes)} B` : '--';
    }

    async function refreshCurrent() { await listDirectory(); }

    async function navigate(path) {
      const normalized = helpers.normalizePath(path);
      if (!normalized) { setMessage('Enter an absolute path.', 'warning'); return; }
      currentPath = normalized;
      const input = doc?.getElementById?.('filebrowser-path'); if (input) input.value = currentPath;
      const select = doc?.getElementById?.('filebrowser-root'); if (select) select.value = currentPath;
      renderBreadcrumbs();
      await listDirectory();
    }

    async function navigateFromInput() {
      const input = doc?.getElementById?.('filebrowser-path');
      const normalized = helpers.normalizePath(input?.value || '');
      if (!normalized) { setMessage('Enter an absolute path.', 'warning'); return; }
      await navigate(normalized);
    }

    async function openBrowser(isPicker) {
      if (!supported) return;
      injectUI();
      pickerMode = Boolean(isPicker);
      const overlay = doc?.getElementById?.('filebrowser-overlay');
      if (overlay) {
        overlay.hidden = false;
        if (typeof overlay.showModal === 'function' && !overlay.open) overlay.showModal();
      }
      await resolveHome();
      try { await fetchState(); } catch (error) { setMessage(error.message, 'warning'); }
      roots = helpers.buildRoots(state, homePath);
      const inputPath = isPicker ? helpers.normalizePath(projectInput()?.value || '') : '';
      currentPath = inputPath || (currentPath === '/' ? homePath : currentPath) || homePath;
      if (!helpers.normalizePath(currentPath)) currentPath = homePath;
      renderRoots();
      const title = overlay?.querySelector?.('h2'); if (title) text(title, isPicker ? 'Select a directory' : 'Files');
      const actions = overlay?.querySelector?.('.filebrowser-picker-actions'); if (actions) actions.hidden = !isPicker;
      await navigate(currentPath);
    }

    function closeBrowser() {
      const restorePickerFocus = pickerMode;
      const overlay = doc?.getElementById?.('filebrowser-overlay');
      if (overlay?.open && typeof overlay.close === 'function') overlay.close();
      if (overlay) overlay.hidden = true;
      const input = projectInput(); if (restorePickerFocus) input?.focus?.();
      pickerMode = false;
    }

    function selectPicker() { selectPickerPath(currentPath); closeBrowser(); }

    function selectPickerPath(path) {
      const input = projectInput();
      const normalized = helpers.normalizePath(path);
      if (!input || !normalized) return;
      input.value = normalized;
      const EventConstructor = win?.Event || root?.Event;
      for (const type of ['input', 'change']) input.dispatchEvent?.(EventConstructor ? new EventConstructor(type, {bubbles: true}) : {type, bubbles: true});
      input.focus?.();
    }

    function dialogRequest(kind, message, initialValue = '') {
      return new Promise(resolve => {
        const dialog = make('dialog', 'filebrowser-dialog');
        const heading = make('h2', '', kind === 'prompt' ? 'New folder' : kind === 'confirm' ? 'Confirm action' : 'Notice');
        const body = make('p', '', safeMessage(message));
        const input = make('input'); input.hidden = kind !== 'prompt'; input.value = initialValue;
        const actions = make('div', 'actions'); const cancel = make('button', '', 'Cancel'); cancel.type = 'button'; const confirm = make('button', '', kind === 'alert' ? 'OK' : 'Continue'); confirm.type = 'button';
        if (kind === 'alert') cancel.hidden = true;
        append(actions, cancel, confirm); append(dialog, heading, body, input, actions); append(doc?.body, dialog);
        const finish = value => { dialog.close?.(); dialog.remove?.(); resolve(value); };
        cancel.onclick = () => finish(kind === 'confirm' ? false : null); confirm.onclick = () => finish(kind === 'prompt' ? input.value : true);
        dialog.oncancel = event => { event.preventDefault?.(); finish(kind === 'confirm' ? false : null); };
        dialog.showModal?.(); if (kind === 'prompt') input.focus?.(); else confirm.focus?.();
      });
    }

    async function createFolder() {
      const value = await dialogRequest('prompt', 'Folder name', '');
      if (value == null) return;
      const error = helpers.validateFolderComponent(value);
      if (error) { setMessage(error, 'warning'); await dialogRequest('alert', error); return; }
      const events = [];
      try { await execute('mkdir', [helpers.joinPath(currentPath, value)], {onOutput: event => events.push(event)}); await listDirectory(); }
      catch (failure) {
        const stderr = helpers.accumulateOutput(events, 'stderr');
        setMessage(stderr.trim() || safeMessage(failure.message), 'warning');
      }
    }

    function init(event) {
      if (started) return ready;
      started = true; initCount++;
      ready = (async () => {
        config = normalizeConfig(event?.detail?.config);
        injectUI();
        try { helpers = await loadHelpers(); }
        catch { setUnsupported(); return; }
        await probe();
      })();
      return ready;
    }

    return Object.freeze({
      init,
      selectPickerPath,
      initializationCount: () => initCount,
      probeCount: () => probeCount,
      config: () => ({...config}),
      openBrowser,
    });
  }

  if (root?.omo?.onLoad) {
    const app = createFilebrowser(root, document);
    root.omo.onLoad('filebrowser', event => app.init(event));
  }
  return {createFilebrowser};
});
