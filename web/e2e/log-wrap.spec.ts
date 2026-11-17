// Text a program wrote (the log of an MCP server, of a plugin, of an agent
// session, the output of a usage script, the failures an upstream reports)
// is one long line without a space: a path, a URL, a JSON blob. Every view
// that shows such text must break it inside its box: the element scrolls
// neither sideways nor past its parent, and the page gets no sideways
// scroll, at a desktop and at a phone width. With OMO_SCREENSHOTS=<folder>
// the MCP log test also writes fix9-mcp-log-<theme>-<width>.png there.
import { mkdirSync } from "node:fs";
import { join } from "node:path";
import type { Locator, Page } from "@playwright/test";
import { script } from "./support/agents";
import { openBoard } from "./support/board";
import {
  expect,
  FAKE_AGENT_BINARY,
  FAKE_MCP_BINARY,
  test,
  type Omo,
} from "./support/omo";
import { makePlugin } from "./support/plugin";

const SHOTS = process.env.OMO_SCREENSHOTS ?? "";
const WIDTHS = [1440, 390];

/** A JSON-like blob of 330 characters with no space in it. */
const LONG = `{"error":"${"unbrokenmachinetext".repeat(17)}","path":"/srv/${"segment".repeat(5)}"}`;
if (LONG.length < 300 || LONG.includes(" "))
  throw new Error("LONG must be 300+ characters without a space");

/** The element holds its long line: nothing scrolls inside it, it stays inside the window and the page has no sideways scroll. */
async function expectWrapped(
  page: Page,
  el: Locator,
  what: string,
): Promise<void> {
  await expect(el, what).toBeVisible();
  await page.evaluate(
    () =>
      new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r))),
  );
  const m = await el.evaluate((node) => {
    const box = node.getBoundingClientRect();
    const root = document.documentElement;
    return {
      inner: node.scrollWidth - node.clientWidth,
      right: box.right,
      left: box.left,
      window: root.clientWidth,
      page: root.scrollWidth - root.clientWidth,
      body: document.body.scrollWidth - document.body.clientWidth,
    };
  });
  expect(
    m.inner,
    `${what}: scrolls sideways inside its box`,
  ).toBeLessThanOrEqual(0);
  expect(m.right, `${what}: reaches past the window`).toBeLessThanOrEqual(
    m.window,
  );
  expect(m.left, `${what}: starts left of the window`).toBeGreaterThanOrEqual(
    0,
  );
  expect(m.page, `${what}: the page scrolls sideways`).toBeLessThanOrEqual(0);
  expect(m.body, `${what}: the body scrolls sideways`).toBeLessThanOrEqual(0);
}

async function atEveryWidth(
  page: Page,
  check: (width: number) => Promise<void>,
): Promise<void> {
  for (const width of WIDTHS) {
    await page.setViewportSize({ width, height: 900 });
    await check(width);
  }
}

const fakeEnv = (omo: Omo) => ({
  FAKEMCP_SERVER: "1",
  FAKEMCP_E2E_MARK: omo.home,
});

async function putFake(omo: Omo, name: string, command: string) {
  await omo.api("PUT", `/api/mcp/servers/${name}`, {
    name,
    enabled: true,
    transport: "stdio",
    command,
    args: [],
    env: fakeEnv(omo),
    roles: [],
    global: true,
    tools: { allow: [], deny: [] },
  });
}

async function callTool(omo: Omo, bearer: string, name: string, args: object) {
  const res = await fetch(`${omo.base}/mcp`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      Accept: "application/json, text/event-stream",
      Authorization: `Bearer ${bearer}`,
    },
    body: JSON.stringify({
      jsonrpc: "2.0",
      id: 1,
      method: "tools/call",
      params: { name, arguments: args },
    }),
  });
  expect(res.status).toBe(200);
  await res.text();
}

for (const colorScheme of ["dark", "light"] as const) {
  test.describe(colorScheme, () => {
    test.use({ colorScheme });

    test(`the log of a relayed MCP server breaks a long line from its stderr (${colorScheme})`, async ({
      page,
      omo,
    }) => {
      await putFake(omo, "fake", FAKE_MCP_BINARY);
      const { secret } = await omo.api<{ secret: string }>(
        "POST",
        "/api/mcp/tokens",
        { name: "wrap" },
      );
      await callTool(omo, secret, "fake__stderr", { text: LONG });
      await openBoard(page, omo);
      await page.goto(omo.url("/settings/mcp"));
      await page
        .locator('[data-mcp-server="fake"] [data-mcp-server-toggle]')
        .click();
      const ed = page.locator('[data-mcp-server-editor="fake"]');
      await ed.locator("[data-mcp-log-toggle]").click();
      const log = page.locator('[data-mcp-log="fake"] [data-mcp-log-text]');
      await expect(log).toContainText("unbrokenmachinetext");
      await atEveryWidth(page, async (width) => {
        await expectWrapped(page, log, `MCP log at ${width}`);
        if (SHOTS !== "") {
          mkdirSync(SHOTS, { recursive: true });
          await log.evaluate((el) => el.scrollIntoView({ block: "center" }));
          await page.screenshot({
            path: join(SHOTS, `fix9-mcp-log-${colorScheme}-${width}.png`),
          });
        }
      });
    });
  });
}

test("the failure texts of an MCP server break a long path: list, state, test result", async ({
  page,
  omo,
}) => {
  const missing = `/nowhere/${"p".repeat(240)}`;
  await putFake(omo, "broken", missing);
  await openBoard(page, omo);
  await page.goto(omo.url("/settings/mcp"));
  await expect(page.locator('[data-mcp-server="broken"]')).toBeVisible();
  await page
    .locator('[data-mcp-server="broken"] [data-mcp-server-toggle]')
    .click();
  const ed = page.locator('[data-mcp-server-editor="broken"]');
  await ed.locator("[data-mcp-test]").click();
  const result = ed.locator("[data-mcp-test-result]");
  await expect(result).toContainText("The test failed");
  // The failure reaches the state and the list once the server was used.
  const { secret } = await omo.api<{ secret: string }>(
    "POST",
    "/api/mcp/tokens",
    { name: "wrap" },
  );
  await fetch(`${omo.base}/mcp`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      Accept: "application/json, text/event-stream",
      Authorization: `Bearer ${secret}`,
    },
    body: JSON.stringify({ jsonrpc: "2.0", id: 1, method: "tools/list" }),
  }).then((r) => r.text());
  await atEveryWidth(page, async (width) => {
    await expectWrapped(page, result, `MCP test failure at ${width}`);
    const listError = page.locator(
      '[data-mcp-server="broken"] .mcp-list__error',
    );
    await expectWrapped(page, listError, `MCP list failure at ${width}`);
    const last = ed.locator("[data-mcp-last-error]");
    await expectWrapped(page, last, `MCP state failure at ${width}`);
  });
});

test("the log of a plugin breaks a long line", async ({ page, omo }) => {
  const name = "wrapper";
  const repo = makePlugin(
    join(omo.home, "plugin-src", name),
    {
      name,
      version: "1.0.0",
      description: "Logs a long line",
      hooks: [{ event: "ticket_moved", lua: "moved.lua" }],
    },
    { "moved.lua": `omo.log(${JSON.stringify(LONG)})\n` },
  );
  await omo.api("POST", "/api/plugins", {
    name,
    source: repo.source,
    subpath: "",
    ref: "",
    acknowledge: "community",
  });
  const n = await omo.createTicket({ title: "Move me" });
  await omo.moveTicket(n, "ready");
  await openBoard(page, omo);
  await page.goto(omo.url("/settings/plugins"));
  await page.locator(`[data-plugin="${name}"] [data-plugin-toggle]`).click();
  const log = page.locator(
    `[data-plugin-log="${name}"] [data-plugin-log-text]`,
  );
  await expect(async () => {
    await page
      .locator(`[data-plugin-log="${name}"] [data-plugin-log-refresh]`)
      .click();
    await expect(log).toContainText("unbrokenmachinetext", { timeout: 1_000 });
  }).toPass({ timeout: 20_000 });
  await atEveryWidth(page, (width) =>
    expectWrapped(page, log, `plugin log at ${width}`),
  );
});

test.describe("an agent that prints a long line and exits", () => {
  test.use({
    omoOptions: {
      agents: { developer: script("ready", `print|${LONG}`, "exit|0") },
    },
  });

  test("the log of an agent session breaks it", async ({ page, omo }) => {
    const n = await omo.createTicket({ title: "Long output" });
    await omo.moveTicket(n, "ready");
    await openBoard(page, omo);
    await page.goto(omo.url(`/${n}?tab=agents`));
    const item = page
      .locator(`dialog[data-ticket-panel] [data-session]`)
      .first();
    await expect(item.locator("[data-ended]")).toBeVisible({ timeout: 30_000 });
    const show = item.getByRole("button", { name: "Show log" });
    if (await show.count()) await show.click();
    const log = item.locator("[data-log] pre");
    await expect(log).toContainText("unbrokenmachinetext");
    await atEveryWidth(page, (width) =>
      expectWrapped(page, log, `session log at ${width}`),
    );
  });
});

test("the output of a usage script breaks a long log line and a long failure", async ({
  page,
  omo,
}) => {
  await omo.api("PUT", "/api/profiles/fake", {
    cli: "custom",
    command: FAKE_AGENT_BINARY,
    args: [],
    env: {},
    account_dir: "",
    usage_script: "",
    prompt_delivery: {},
    models: {
      default: {
        args: [],
        env: {},
        usage_windows: [],
        description: "scripted fake agent",
      },
    },
  });
  await openBoard(page, omo);
  await page.goto(omo.url("/settings/usage"));
  await page.getByRole("button", { name: "New script…" }).click();
  const dialog = page.getByRole("dialog", { name: "New script" });
  await dialog.getByLabel("Name of the script").fill("long-output");
  await dialog.getByRole("button", { name: "Create" }).click();
  const ed = page.locator('[data-script-editor="long-output"]');
  await ed
    .locator("[data-script-text]")
    .fill(
      `function usage(account)\n  omo.log(${JSON.stringify(LONG)})\n  error(${JSON.stringify(LONG)})\nend\n`,
    );
  await ed.getByLabel("Run as").selectOption("fake");
  await ed.locator("[data-test-run]").click();
  await expect(ed.locator("[data-test-result]")).toBeVisible();
  await atEveryWidth(page, async (width) => {
    await expectWrapped(
      page,
      ed.locator("[data-test-log]"),
      `script log at ${width}`,
    );
    await expectWrapped(
      page,
      ed.locator("[data-test-error]"),
      `script failure at ${width}`,
    );
  });
});
