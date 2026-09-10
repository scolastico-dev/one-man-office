'use strict';

(function (root, factory) {
  if (typeof module === 'object' && module.exports) module.exports = factory();
  else root.FilebrowserHelpers = factory();
})(typeof globalThis === 'object' ? globalThis : this, function () {
  function normalizePath(path) {
    if (typeof path !== 'string' || !path.startsWith('/') || /[\0\r\n]/.test(path)) return '';
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
    return normalized.slice(0, normalized.lastIndexOf('/')) || '/';
  }

  function breadcrumbs(path) {
    const normalized = normalizePath(path) || '/';
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
    if (typeof value !== 'string' || !value.trim() || value === '.' || value === '..' || value === '/' || /[\/\0]/.test(value)) {
      return 'Enter one non-empty folder name without slashes.';
    }
    return '';
  }

  function parseByteCount(output) {
    const match = String(output || '').match(/(?:^|\s)(\d+)(?:\s|$)/);
    return match ? Number(match[1]) : null;
  }

  function joinPath(directory, name) {
    if (!displayableName(name) || !name || name === '.' || name === '..' || name.includes('/')) return '';
    const base = normalizePath(directory) || '/';
    return normalizePath(base + '/' + name);
  }

  return Object.freeze({normalizePath, parentPath, breadcrumbs, accumulateOutput, accumulateStdout, displayableName, parseListing, parseListingWithNotice, parseNullListing, sortEntries, buildRoots, validateFolderComponent, parseByteCount, joinPath});
});
