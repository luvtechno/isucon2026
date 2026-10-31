#!/usr/bin/env node
// ベンチを Enqueue して、初期検証の結果が出るか、指定秒数たつまで見る。超えたら取り消さずに抜ける。
import { connect, closePage } from '../tools/browser.mjs';
const limit = Number(process.argv[2] || 60) * 1000;
const { browser, page } = await connect({ own: true });
await page.goto('https://portal.isucon.net/contestant/benchmark_jobs', { waitUntil: 'domcontentloaded', timeout: 30000 });
await page.waitForTimeout(1500);
await page.getByRole('button', { name: 'Enqueue' }).click({ timeout: 10000 });
const t0 = Date.now();
let href = '';
for (;;) {
  await page.waitForTimeout(4000);
  await page.goto('https://portal.isucon.net/contestant/benchmark_jobs', { waitUntil: 'domcontentloaded', timeout: 30000 });
  await page.waitForSelector('table', { timeout: 20000 });
  await page.waitForTimeout(600);
  const r = await page.evaluate(() => { const tr = document.querySelector('table tbody tr'); return tr ? { c: [...tr.querySelectorAll('td')].map(t => t.innerText.trim()), h: tr.querySelector('a')?.getAttribute('href') } : null; });
  if (r) href = r.h;
  if (r && /FINISHED|ERRORED|CANCELLED/.test(r.c.join(' ')) ) { console.log(r.c.join(' | '), r.h); break; }
  if (Date.now() - t0 > limit) { console.log('still running', r && r.c.join(' | '), r && r.h); break; }
}
await closePage(browser, page);
process.exit(0);
