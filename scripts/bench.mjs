#!/usr/bin/env node
// ポータルのベンチマークを Enqueue して（--no-enqueue で省略）、終わるまで待って結果を出す。
import { connect, closePage } from '../tools/browser.mjs';
const args = process.argv.slice(2);
const enqueue = !args.includes('--no-enqueue');
const URL_JOBS = 'https://portal.isucon.net/contestant/benchmark_jobs';
const { browser, page } = await connect({ own: true });
const rows = async () => {
  await page.goto(URL_JOBS, { waitUntil: 'domcontentloaded', timeout: 30000 });
  await page.waitForSelector('table', { timeout: 20000 });
  await page.waitForTimeout(800);
  return page.evaluate(() => [...document.querySelectorAll('table tbody tr')].slice(0, 5).map(tr => ({
    cells: [...tr.querySelectorAll('td')].map(td => td.innerText.trim()),
    href: tr.querySelector('a')?.getAttribute('href') || '',
  })));
};
if (enqueue) {
  await page.goto(URL_JOBS, { waitUntil: 'domcontentloaded', timeout: 30000 });
  await page.waitForTimeout(1500);
  const btn = page.getByRole('button', { name: 'Enqueue' });
  await btn.click({ timeout: 10000 });
  await page.waitForTimeout(1500);
  console.log('enqueued', new Date().toISOString());
}
let last = '';
for (;;) {
  const r = await rows();
  const top = r[0];
  const s = top ? top.cells.join(' | ') : '(none)';
  if (s !== last) { console.log(new Date().toISOString(), s); last = s; }
  if (top && /FINISHED|ERRORED|CANCELLED/.test(s)) {
    console.log('href', top.href);
    break;
  }
  await page.waitForTimeout(5000);
}
await closePage(browser, page);
process.exit(0);
