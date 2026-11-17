// Text a program wrote is shown in a <pre> that breaks long lines (the shared
// .omo-logtext class: pre-wrap and overflow-wrap: anywhere). A <pre> with a
// log-like class that keeps `white-space: pre` scrolls sideways or widens its
// box, so a new log box must take the class.
import { expect, test } from "vitest";

const sources = import.meta.glob("/src/**/*.vue", {
  query: "?raw",
  import: "default",
  eager: true,
}) as Record<string, string>;

test("the guard sees the files it scans", () => {
  expect(Object.keys(sources)).toEqual(
    expect.arrayContaining([
      "/src/features/settings/mcp/McpServerLog.vue",
      "/src/features/terminal/LogView.vue",
    ]),
  );
});

test("every pre that shows a log or a test output breaks long lines", () => {
  const missing: string[] = [];
  for (const [file, text] of Object.entries(sources)) {
    if (file.startsWith("/src/sample/")) continue;
    for (const m of text.matchAll(/<pre\b[^>]*>/g)) {
      const tag = m[0];
      if (/log|output|result/i.test(tag) && !/\bomo-logtext\b/.test(tag))
        missing.push(`${file}: ${tag.slice(0, 80)}`);
    }
    for (const m of text.matchAll(
      /\.([\w-]*(?:log|output)[\w-]*)\s*\{([^}]*)\}/gi,
    )) {
      if (/white-space:\s*pre\s*;/.test(m[2] ?? ""))
        missing.push(`${file}: .${m[1]} keeps white-space: pre`);
    }
  }
  expect(missing).toEqual([]);
});
