'use strict';

(function (root, factory) {
  if (typeof module === 'object' && module.exports) module.exports = factory(root, root.document, require('./helpers.js'), require('./commands.js'));
  else factory(root, root.document, root.FilebrowserHelpers, root.FilebrowserCommands);
})(typeof globalThis === 'object' ? globalThis : this, function (root, document, initialHelpers, initialCommandFactory) {
  const DEFAULT_CONFIG = Object.freeze({
    download_warn_bytes: 52428800,
    upload_warn_bytes: 52428800,
    upload_max_bytes: 1073741824,
  });
  function createFilebrowser(win = root, doc = document) {
    const omo = win?.omo || {};
    let helpers = initialHelpers || win?.FilebrowserHelpers;
    let commandFactory = initialCommandFactory || win?.FilebrowserCommands;
    let commands = null;
    let started = false;
    let initCount = 0;
    let probeCount = 0;
    let available = false;
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
    let fileElements = [];
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
        isUNCPath: path => typeof path === 'string' && (/^\\\\/.test(path) || /^\/\/[^/\\]/.test(path)),
        normalizePath: path => {
          const value = String(path || '');
          if (/^\\\\/.test(value) || /^\/\/[^/\\]/.test(value) || /[\0\r\n]/.test(value)) return '';
          const drive = value.match(/^([A-Za-z]):[\\/]/);
          if (drive) return drive[1].toUpperCase() + ':\\' + value.slice(3).split(/[\\/]+/).filter(part => part && part !== '.').reduce((parts, part) => { if (part === '..') parts.pop(); else parts.push(part); return parts; }, []).join('\\');
          if (!value.startsWith('/')) return '';
          return ('/' + value.split('/').filter(part => part && part !== '.').reduce((parts, part) => { if (part === '..') parts.pop(); else parts.push(part); return parts; }, []).join('/')) || '/';
        },
        pathError: path => (/^\\\\/.test(String(path || '')) || /^\/\/[^/\\]/.test(String(path || ''))) ? 'UNC paths are not supported.' : 'Enter an absolute path.',
        parentPath: path => { const normalized = path || '/'; return normalized === '/' ? '/' : normalized.slice(0, normalized.lastIndexOf('/')) || '/'; },
        breadcrumbs: path => [{label: '/', path}],
        accumulateOutput: (events, stream = 'stdout') => events.filter(event => event?.stream === stream).map(event => String(event.data || '')).join(''),
        accumulateStdout: events => events.filter(event => event?.stream === 'stdout').map(event => String(event.data || '')).join(''),
        parseNullListing: (output, type = 'file') => ({entries: String(output || '').split('\0').filter(Boolean).map(path => ({name: path.slice(path.lastIndexOf('/') + 1), type})), skippedNewlineNames: false}),
        parseListingWithNotice: output => ({entries: String(output || '').split(/\r?\n/).filter(Boolean).map(name => ({name: name.replace(/\/$/, ''), type: name.endsWith('/') ? 'directory' : 'file'})), skippedNewlineNames: false}),
        sortEntries: entries => entries,
        buildRoots: (value, home) => [{label: 'Home', path: home}],
        validateFolderComponent: value => value && !/[\\\/\0]/.test(value) ? '' : 'Enter one non-empty folder name without slashes.',
        parseByteCount: output => Number(String(output).match(/\d+/)?.[0] || '') || null,
        joinPath: (dir, name) => !name || /[\\\/\0\r\n]/.test(name) || name === '.' || name === '..' ? '' : (/^[A-Za-z]:\\/.test(dir) ? `${dir}${dir.endsWith('\\') ? '' : '\\'}${name}` : (dir === '/' ? '' : dir) + '/' + name),
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

    async function loadCommands() {
      if (commandFactory?.create) return commandFactory;
      if (!doc?.createElement || !doc?.head?.append) throw new Error('Failed to load filebrowser commands.');
      await new Promise((resolve, reject) => {
        const script = doc.createElement('script');
        const source = doc.currentScript?.src || '/plugins/filebrowser/web/main.js';
        script.src = source.replace(/main\.js(?:\?.*)?$/, 'commands.js');
        script.async = false;
        script.onload = resolve;
        script.onerror = () => reject(new Error('Failed to load filebrowser commands.'));
        doc.head.append(script);
      });
      commandFactory = win.FilebrowserCommands;
      if (!commandFactory?.create) throw new Error('Failed to load filebrowser commands.');
      return commandFactory;
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

    function registerControl(element, action) {
      if (element) { if (action) element.dataset.filebrowserAction = action; fileElements.push(element); element.disabled = !available; }
      return element;
    }

    const registerAction = registerControl;

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
      const button = registerControl(make('button', 'filebrowser-browse', 'Browse...'), 'browse');
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
      const toolbar = byID('toolbar');
      const main = byID('main');
      if (!doc?.getElementById?.('filebrowser-button') && toolbar) {
        const button = registerControl(make('button', '', 'Files'), 'files');
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
        const rootSelect = registerControl(make('select'), 'root'); rootSelect.id = 'filebrowser-root'; rootSelect.onchange = () => navigate(rootSelect.value);
        const path = registerControl(make('input'), 'path'); path.id = 'filebrowser-path'; path.type = 'text'; path.placeholder = '/absolute/path';
        const go = registerControl(make('button', '', 'Go'), 'navigate'); go.type = 'button'; go.onclick = () => navigateFromInput();
        path.onkeydown = event => { if (event.key === 'Enter') { event.preventDefault(); navigateFromInput(); } };
        const hidden = make('label', 'filebrowser-hidden');
        const checkbox = registerControl(make('input'), 'show-hidden'); checkbox.type = 'checkbox'; checkbox.id = 'filebrowser-show-hidden'; checkbox.onchange = () => listDirectory();
        append(hidden, checkbox, ' Show hidden files');
        const refresh = registerAction(make('button', '', 'Refresh'), 'refresh'); refresh.type = 'button'; refresh.id = 'filebrowser-refresh'; refresh.onclick = () => refreshCurrent();
        const uploadLabel = make('label', 'filebrowser-upload-label', 'Upload'); uploadLabel.id = 'filebrowser-upload-label';
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
          registerControl(button, 'sort');
          button.id = `filebrowser-sort-${field}`;
          button.onclick = () => { if (sortField === field) sortDirection = sortDirection === 'asc' ? 'desc' : 'asc'; else { sortField = field; sortDirection = 'asc'; } renderRows(currentEntries); };
          append(th, button); append(headerRow, th);
        }
        append(thead, headerRow); const body = make('tbody'); body.id = 'filebrowser-rows'; append(table, thead, body);
        const fileActions = make('div', 'filebrowser-actions');
        const newFolder = registerAction(make('button', '', 'New folder'), 'mkdir'); newFolder.type = 'button'; newFolder.id = 'filebrowser-new-folder'; newFolder.onclick = () => createFolder();
        const cancel = make('button', '', 'Cancel'); cancel.type = 'button'; cancel.onclick = closeBrowser;
        const select = registerControl(make('button', '', 'Select'), 'select'); select.type = 'button'; select.id = 'filebrowser-select'; select.onclick = selectPicker;
        const pickerActions = make('div', 'filebrowser-picker-actions');
        append(fileActions, newFolder);
        append(pickerActions, cancel, select);
        append(overlay, heading, controls, crumbs, message, note, progressStatus, progress, cancelTransfer, table, fileActions, pickerActions);
        append(main, overlay);
      }
    }

    function setUnavailable() {
      available = false;
      for (const element of fileElements) element.disabled = true;
      updatePickerControls();
    }

    function setAvailable() {
      available = true;
      for (const element of fileElements) element.disabled = false;
      updatePickerControls();
    }

    function updatePickerControls() {
      const uploadLabel = doc?.getElementById?.('filebrowser-upload-label');
      const upload = doc?.getElementById?.('filebrowser-upload');
      if (uploadLabel) uploadLabel.hidden = pickerMode;
      if (upload) upload.disabled = !available || pickerMode;
      const actions = doc?.getElementById?.('filebrowser-overlay')?.querySelector?.('.filebrowser-picker-actions');
      if (actions) actions.hidden = !pickerMode;
    }

    async function execute(command, args, options = {}) {
      if (typeof omo.execute !== 'function') throw new Error('The file manager is unavailable.');
      return omo.execute(command, args, {...options, cwd: 'home'});
    }

    async function probe() {
      probeCount++;
      try {
        if (!commandFactory?.create) throw new Error('The file manager commands are unavailable.');
        commands = commandFactory.create(execute);
        await commands.select();
        setAvailable();
      } catch (error) {
        setUnavailable();
        setMessage(error?.message || 'File manager unavailable: unable to probe for a supported command adapter; install pwsh or powershell.exe and try again.', 'warning');
      }
    }

    async function resolveHome() {
      if (homeResolved) return homePath;
      homeResolved = true;
      try { const value = await commands.home(); if (helpers.normalizePath(value)) homePath = helpers.normalizePath(value); }
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
        const button = registerControl(make('button', '', crumb.label), 'navigate'); button.type = 'button'; button.onclick = () => navigate(crumb.path); append(container, button);
      }
    }

    async function listDirectory() {
      if (!available) return;
      const generation = ++listGeneration;
      const events = [];
      const hidden = doc?.getElementById?.('filebrowser-show-hidden')?.checked;
      try {
        const result = await commands.list(currentPath, {includeHidden: hidden, directoriesOnly: pickerMode, onOutput: event => events.push(event)});
        const visibleEntries = result.entries;
        const skippedNewlineNames = result.skippedNewlineNames;
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

    async function downloadFile(filePath) {
      if (!available) return;
      if (!filePath) throw new Error('The download destination is not a safe file path.');
      const name = helpers.basename?.(filePath) || filePath.slice(filePath.lastIndexOf('/') + 1);
      const controller = makeTransferController();
      activeTransfer = controller;
      try {
        try { if (!await commands.isFile(filePath, {signal: controller.signal})) throw new Error('not a regular file'); }
        catch { throw new Error('The selected download source is not a regular file.'); }
        const metadata = await commands.stat(filePath, {signal: controller.signal});
        const size = metadata?.size;
        if (size == null) throw new Error('Unable to determine the download size.');
        const decision = helpers.transferThreshold(size, config.download_warn_bytes, Number.POSITIVE_INFINITY);
        if (decision === 'warn' && !await dialogRequest('confirm', transferAdvice('download', name, size))) return;
        const response = await omo.trigger(null, 'download', [filePath]);
        const rawURL = response?.result?.url;
        if (typeof rawURL !== 'string' || !rawURL.startsWith('/') || rawURL.startsWith('//') || /[\\\0\r\n]/.test(rawURL)) {
          throw new Error('The download hook returned an invalid same-origin URL.');
        }
        const link = make('a');
        link.href = rawURL; link.hidden = true;
        append(doc?.body, link);
        try { link.click?.(); }
        finally { link.remove?.(); }
      } finally {
        if (activeTransfer === controller) activeTransfer = null;
      }
    }

    async function destinationExists(path, signal) {
      return commands.exists(path, {signal});
    }

    async function uploadFiles(files) {
      if (!available || pickerMode) return;
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
              await commands.upload(destination, {stdin: file, signal: controller.signal, onOutput: event => { if (event?.stream === 'stderr') stderr.push(event.data); }});
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
      const parentCell = make('td'); const parentButton = registerControl(make('button', 'filebrowser-row-name', '..'), 'navigate');
      parentButton.type = 'button'; parentButton.onclick = () => navigate(helpers.parentPath(currentPath));
      append(parentCell, parentButton); append(parentRow, parentCell, make('td', '', 'directory'), make('td', '', '--')); append(body, parentRow);
      const sorted = helpers.sortEntries(entries || [], sortField, sortDirection);
      for (const entry of sorted) {
        const row = make('tr');
        const name = make('td'); const button = registerControl(make('button', 'filebrowser-row-name', entry.name), 'navigate');
        button.type = 'button'; button.disabled = !available || entry.type !== 'directory'; button.onclick = () => navigate(helpers.joinPath(currentPath, entry.name));
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
      if (!available) return;
      const normalized = helpers.normalizePath(path);
      if (!normalized) { setMessage(helpers.pathError?.(path) || 'Enter an absolute path.', 'warning'); return; }
      currentPath = normalized;
      const input = doc?.getElementById?.('filebrowser-path'); if (input) input.value = currentPath;
      const select = doc?.getElementById?.('filebrowser-root'); if (select) select.value = currentPath;
      renderBreadcrumbs();
      await listDirectory();
    }

    async function navigateFromInput() {
      if (!available) return;
      const input = doc?.getElementById?.('filebrowser-path');
      const normalized = helpers.normalizePath(input?.value || '');
      if (!normalized) { setMessage(helpers.pathError?.(input?.value || '') || 'Enter an absolute path.', 'warning'); return; }
      await navigate(normalized);
    }

    async function openBrowser(isPicker) {
      injectUI();
      pickerMode = Boolean(isPicker);
      updatePickerControls();
      const overlay = doc?.getElementById?.('filebrowser-overlay');
      if (overlay) {
        overlay.hidden = false;
        if (typeof overlay.showModal === 'function' && !overlay.open) overlay.showModal();
      }
      if (!available) return;
      await resolveHome();
      try { await fetchState(); } catch (error) { setMessage(error.message, 'warning'); }
      roots = helpers.buildRoots(state, homePath);
      const inputPath = isPicker ? helpers.normalizePath(projectInput()?.value || '') : '';
      currentPath = inputPath || (currentPath === '/' ? homePath : currentPath) || homePath;
      if (!helpers.normalizePath(currentPath)) currentPath = homePath;
      renderRoots();
      const title = overlay?.querySelector?.('h2'); if (title) text(title, isPicker ? 'Select a directory' : 'Files');
      await navigate(currentPath);
    }

    function closeBrowser() {
      const restorePickerFocus = pickerMode;
      const overlay = doc?.getElementById?.('filebrowser-overlay');
      if (overlay?.open && typeof overlay.close === 'function') overlay.close();
      if (overlay) overlay.hidden = true;
      const input = projectInput(); if (restorePickerFocus) input?.focus?.();
      pickerMode = false;
      updatePickerControls();
    }

    function selectPicker() { if (!available) return; selectPickerPath(currentPath); closeBrowser(); }

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
      if (!available) return;
      const value = await dialogRequest('prompt', 'Folder name', '');
      if (value == null) return;
      const error = helpers.validateFolderComponent(value);
      if (error) { setMessage(error, 'warning'); await dialogRequest('alert', error); return; }
      const events = [];
      try { await commands.mkdir(helpers.joinPath(currentPath, value), {onOutput: event => events.push(event)}); await listDirectory(); }
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
        catch { setUnavailable(); return; }
        try { await loadCommands(); }
        catch { setUnavailable(); return; }
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
