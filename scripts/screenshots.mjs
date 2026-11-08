// Takes the README screenshots from a running web app: node scripts/screenshots.mjs http://localhost:5173 docs/img
import { createRequire } from "node:module";
const require = createRequire(import.meta.url);
const { chromium } = require(process.env.PLAYWRIGHT_MODULE ?? "playwright");

const base = process.argv[2] ?? "http://localhost:5173";
const out = process.argv[3] ?? "docs/img";
const shots = [
  ["insights", "#/?src=dev-drop_index"],
  ["insight-drop-index", "#/insights/1?src=dev-drop_index"],
  ["insight-lock", "#/insights/1?src=dev-lock_contention"],
  ["insight-spill", "#/insights/1?src=dev-work_mem_spill"],
  ["queries", "#/queries?src=dev-planner_setting"],
  ["evaluation", "#/evaluation"],
];
const browser = await chromium.launch();
const errors = [];
for (const [name, hash] of shots) {
  for (const [suffix, viewport] of [["", { width: 1360, height: 900 }], ["-mobile", { width: 390, height: 844 }]]) {
    if (suffix && !["insight-drop-index", "evaluation"].includes(name)) continue;
    const page = await browser.newPage({ viewport, deviceScaleFactor: suffix ? 2 : 1 });
    page.on("pageerror", (e) => errors.push(`${name}: ${e.message}`));
    page.on("console", (m) => m.type() === "error" && errors.push(`${name}: ${m.text()}`));
    await page.goto(base + "/" + hash);
    await page.waitForTimeout(1600);
    await page.screenshot({ path: `${out}/${name}${suffix}.png`, fullPage: true });
    await page.close();
  }
}
await browser.close();
if (errors.length) {
  console.error(errors.join("\n"));
  process.exit(1);
}
