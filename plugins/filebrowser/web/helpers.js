'use strict';

(function (root, factory) {
  if (typeof module === 'object' && module.exports) module.exports = factory();
  else root.FilebrowserHelpers = factory();
})(typeof globalThis === 'object' ? globalThis : this, function () {
  function isUNCPath(path) {
    return typeof path === 'string' && (/^\\\\/.test(path) || /^\/\/[^/\\]/.test(path));
  }

  function normalizePath(path) {
    if (typeof path !== 'string' || /[\0\r\n]/.test(path) || isUNCPath(path)) return '';
    const drive = path.match(/^([A-Za-z]):[\\/]/);
    if (drive) {
      const parts = [];
      for (const part of path.slice(3).split(/[\\/]+/)) {
        if (!part || part === '.') continue;
        if (part === '..') { if (parts.length) parts.pop(); }
        else parts.push(part);
      }
      return drive[1].toUpperCase() + ':\\' + parts.join('\\');
    }
    if (!path.startsWith('/')) return '';
    const parts = [];
    for (const part of path.split('/')) {
      if (!part || part === '.') continue;
      if (part === '..') { if (parts.length) parts.pop(); }
      else parts.push(part);
    }
    return '/' + parts.join('/');
  }

  function parentPath(path) {
    const normalized = normalizePath(path);
    if (!normalized || normalized === '/') return '/';
    if (/^[A-Za-z]:\\$/.test(normalized)) return normalized;
    if (/^[A-Za-z]:\\/.test(normalized)) {
      const separator = normalized.lastIndexOf('\\');
      return separator <= 2 ? normalized.slice(0, 3) : normalized.slice(0, separator);
    }
    return normalized.slice(0, normalized.lastIndexOf('/')) || '/';
  }

  function breadcrumbs(path) {
    const normalized = normalizePath(path) || '/';
    if (/^[A-Za-z]:\\/.test(normalized)) {
      const rootPath = normalized.slice(0, 3);
      const result = [{label: rootPath, path: rootPath}];
      let current = normalized.slice(0, 2);
      for (const segment of normalized.slice(3).split('\\')) {
        if (!segment) continue;
        current += segment === '' ? '' : '\\' + segment;
        result.push({label: segment, path: current});
      }
      return result;
    }
    const result = [{label: '/', path: '/'}];
    if (normalized === '/') return result;
    let current = '';
    for (const segment of normalized.slice(1).split('/')) {
      current += '/' + segment;
      result.push({label: segment, path: current});
    }
    return result;
  }

  function accumulateOutput(events, stream = 'stdout') {
    return events.filter(event => event && event.stream === stream).map(event => String(event.data || '')).join('');
  }

  const accumulateStdout = events => accumulateOutput(events, 'stdout');

  function displayableName(name) {
    return typeof name === 'string' && !/[\r\n]/.test(name);
  }

  function parseListing(output) {
    return parseListingWithNotice(output).entries;
  }

  function parseListingWithNotice(output) {
    const lines = Array.isArray(output) ? output : String(output || '').split(/\r?\n/);
    const entries = [];
    let skippedNewlineNames = false;
    for (const raw of lines) {
      if (!raw) continue;
      if (!displayableName(raw)) { skippedNewlineNames = true; continue; }
      const directory = /\/$/.test(raw);
      const name = directory ? raw.replace(/\/+$/, '') : raw;
      if (!name || !displayableName(name)) { skippedNewlineNames = true; continue; }
      entries.push({name, type: directory ? 'directory' : 'file'});
    }
    return {entries, skippedNewlineNames};
  }

  function parseNullListing(output, type = 'file') {
    const entries = [];
    let skippedNewlineNames = false;
    for (const raw of String(output || '').split('\0')) {
      if (!raw) continue;
      const separator = raw.lastIndexOf('/');
      const name = separator < 0 ? raw : raw.slice(separator + 1);
      if (!name || !displayableName(name)) { skippedNewlineNames = true; continue; }
      entries.push({name, type});
    }
    return {entries, skippedNewlineNames};
  }

  function sortEntries(entries, field, direction) {
    const multiplier = direction === 'desc' ? -1 : 1;
    return entries.map((entry, index) => ({entry, index})).sort((left, right) => {
      let result = 0;
      if (field === 'size') {
        const leftSize = left.entry.size == null ? null : Number(left.entry.size);
        const rightSize = right.entry.size == null ? null : Number(right.entry.size);
        if (leftSize == null && rightSize != null) result = -1;
        else if (leftSize != null && rightSize == null) result = 1;
        else if (leftSize != null && rightSize != null) result = leftSize - rightSize;
      } else {
        result = String(left.entry[field] || '').localeCompare(String(right.entry[field] || ''));
      }
      return result ? result * multiplier : left.index - right.index;
    }).map(item => item.entry);
  }

  function buildRoots(state, home) {
    const roots = [{label: 'Home', path: normalizePath(home) || '/'}];
    const seen = new Set(roots.map(root => root.path));
    for (const item of [...(state?.projects || []), ...(state?.instances || [])]) {
      const path = normalizePath(item?.path || '');
      if (path && !seen.has(path)) {
        seen.add(path);
        roots.push({label: path, path});
      }
    }
    return roots;
  }

  function validateFolderComponent(value) {
    if (typeof value !== 'string' || !value.trim() || value === '.' || value === '..' || value === '/' || /[\\\/\0]/.test(value)) {
      return 'Enter one non-empty folder name without slashes.';
    }
    return '';
  }

  function parseByteCount(output) {
    const match = String(output || '').match(/(?:^|\s)(\d+)(?:\s|$)/);
    return match ? Number(match[1]) : null;
  }

  function validateFileComponent(value) {
    if (typeof value !== 'string' || !value || value === '.' || value === '..' || /[\\/\0\r\n]/.test(value)) {
      return 'Choose a file with a simple name and no path separators.';
    }
    return '';
  }

  function transferThreshold(size, warn, max) {
    const bytes = Number(size);
    if (!Number.isFinite(bytes) || bytes < 0 || bytes > Number(max)) return 'reject';
    return bytes > Number(warn) ? 'warn' : 'ok';
  }

  function joinPath(directory, name) {
    if (validateFileComponent(name)) return '';
    const base = normalizePath(directory) || '/';
    const separator = /^[A-Za-z]:\\/.test(base) ? '\\' : '/';
    return normalizePath(base + (base === '/' || base.endsWith('\\') ? '' : separator) + name);
  }

  function basename(path) {
    const value = String(path || '').replace(/[\\/]$/, '');
    return value.slice(Math.max(value.lastIndexOf('/'), value.lastIndexOf('\\')) + 1);
  }

  function pathError(path) {
    if (isUNCPath(path)) return 'UNC paths are not supported.';
    return normalizePath(path) ? '' : 'Enter an absolute path.';
  }

  return Object.freeze({isUNCPath, normalizePath, pathError, parentPath, breadcrumbs, accumulateOutput, accumulateStdout, displayableName, parseListing, parseListingWithNotice, parseNullListing, sortEntries, buildRoots, validateFolderComponent, validateFileComponent, parseByteCount, transferThreshold, joinPath, basename});
});
