#!/usr/bin/env node
// 認証済みブラウザへ繋いで、描画後の画面を読む / 操作する。
//
//   tools/browser.mjs open  <url>            開いて、見出し・リンク・操作できる要素を出す
//   tools/browser.mjs save  <url> <out>      描画後の HTML をファイルへ落とす
//   tools/browser.mjs links <url>            リンクだけを1行1件で出す
//   tools/browser.mjs text  <url>            描画後の本文を出す（写しは作らない）
//   tools/browser.mjs save-link <url> <リンクの文言> <out>
//                                            data: URI のリンクの中身を、印字せずにファイルへ落とす
//   tools/browser.mjs save-json <url> <API の URL> <フィールド> <out>
//                                            ページの文脈で JSON の API を呼び、フィールドの値を
//                                            印字せずにファイルへ落とす
//
// どのサブコマンドも自分のタブを開いて、終わったら閉じる（複数のサブエージェントが
//    同時に叩いても、開いているタブを共有しない）。
//
// 繋ぎ先は環境変数 `ISUCON_BROWSER_CDP_URL` である（run の起動時に渡っている）。
// ブラウザは既にポータルへログインしてあり、**こちらからログインし直す必要は無い。**
//
// ⚠️ **この道具では cookie に触らないこと。** `context.cookies()` も `document.cookie` も
//    呼ばない。認証はブラウザ側の profile の中にあり、こちらは開いているページを操作する
//    だけである。読み出してここで扱うと、workspace の履歴に認証情報が残る。
//    HTTP から使える形で cookie が要るときは `tools/browser-cookies.mjs` を通すこと
//    （workspace の外の cookie jar へ、値を印字せずに落とす専用の経路である）。
//
// 画面を「読む」だけなら MCP の browser_navigate / browser_snapshot でもよい。
// **クリック・選択・入力はこちら（Playwright）で行うこと** — MCP 側の操作は
// 同じ要素で失敗することが分かっている。
import { createRequire } from 'node:module';
import { homedir } from 'node:os';
import { pathToFileURL } from 'node:url';
import { chmod, mkdir, writeFile } from 'node:fs/promises';
import path from 'node:path';

const BROWSER_HOME =
  process.env.ISUCON_BROWSER_HOME ?? path.join(homedir(), '.isucon-browser');
const CDP = process.env.ISUCON_BROWSER_CDP_URL ?? '';

// ⚠️ node_modules を workspace のツリーへ置かないこと。デプロイの差分と
//    `git grep` が vendor の JavaScript で埋まる。ブラウザ側の置き場所から読む。
export function playwright() {
  const require = createRequire(import.meta.url);
  const modules = process.env.ISUCON_BROWSER_MODULES || path.join(BROWSER_HOME, 'node_modules');
  const entry = path.join(modules, 'playwright-core');
  try {
    return require(entry);
  } catch (e) {
    // 読み込みの失敗を全部「入っていない」と言わないこと。入っているのに読めない
    //    （Node の版が合わない・install が途中で切れた）回に、入れ直せと言うことになる。
    const missing = String(e?.message ?? '').match(/^Cannot find module '([^']+)'/)?.[1];
    if (e?.code !== 'MODULE_NOT_FOUND' || missing !== entry) {
      throw e;
    }
    console.error(`playwright-core が ${BROWSER_HOME} にありません。run の外から入れる道具なので、`);
    console.error('自分で入れずに、ブラウザを使わない経路で先へ進んでください。');
    process.exit(2);
  }
}

const TAB_OPEN_TIMEOUT_MS = 3000;
// タブが1つも無い Chrome は、CDP のエンドポイントが応答していても `connectOverCDP` を
//    この文面で断る。繋げないので、タブは CDP の HTTP エンドポイントから開く。
const NO_TAB_ERROR = 'Browser context management is not supported';

async function connectOverCDP(chromium) {
  try {
    return await chromium.connectOverCDP(CDP);
  } catch (e) {
    if (!String(e?.message ?? '').includes(NO_TAB_ERROR)) throw e;
    try {
      const res = await fetch(new URL('/json/new?about:blank', CDP), {
        method: 'PUT',
        signal: AbortSignal.timeout(TAB_OPEN_TIMEOUT_MS),
      });
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
    } catch (opening) {
      // fetch は繋げない理由（ECONNREFUSED など）を message ではなく cause に持つ。
      const why = opening?.cause?.code ?? opening?.cause?.message ?? opening?.message ?? opening;
      console.error(`[browser] タブが1つも無く、空のタブも開けませんでした（${why}）。`);
      throw e;
    }
    console.error('[browser] タブが1つも無かったので、空のタブを1つ開きました。');
    return chromium.connectOverCDP(CDP);
  }
}

/**
 * 認証済みブラウザのページを返す。
 *
 * デフォルトは**いま開いているタブ**である（画面を1回読むだけなら、そのほうが人の見て
 * いる画面と同じものを読める）。`{ own: true }` を渡すと自分のタブを新しく開く。
 *
 * ⚠️ **分単位でページを占有するなら `{ own: true }` を使うこと。** 1つのタブを共有すると、
 *    誰かが同じタブで別の画面を開いた瞬間から、こちらの読み取りが失敗し続ける
 *    （ベンチの状態を待っている最中にそれが起きると、向こうで走っているジョブを
 *    置き去りにしたまま諦めることになる）。
 *
 * 返り値の `opened` は「このタブをこちらが開いたか」である。真なら、終わったときに
 * 自分で閉じること（閉じ方は `closePage()`）。
 */
export async function connect({ own = false } = {}) {
  if (!CDP) {
    console.error('ISUCON_BROWSER_CDP_URL が空です。起動時の browser.cdp_url を確認してください。');
    process.exit(2);
  }
  const { chromium } = playwright();
  const browser = await connectOverCDP(chromium);
  const ctx = browser.contexts()[0];
  const existing = ctx.pages();
  if (own) {
    return { browser, page: await ctx.newPage(), opened: true };
  }
  // ⚠️ newPage へ落ちること。ページが1つも無いブラウザに繋いだ回に undefined を掴んで落ちる。
  return { browser, page: existing[0] ?? (await ctx.newPage()), opened: existing.length === 0 };
}

/**
 * `connect({ own: true })` で開いたタブを閉じる。
 *
 * ⚠️ **最後の1つなら閉じないこと。** タブが1つも無くなるとブラウザのウィンドウごと閉じ、
 *    期限を持たない cookie が消える。ログインをそういう cookie で持つポータルでは、
 *    タブを開き直してもログインは戻らない。
 */
export async function closePage(browser, page) {
  try {
    const others = (browser.contexts()[0]?.pages() ?? []).filter((p) => p !== page);
    if (others.length > 0) await page.close();
  } catch {
    /* 閉じられなくても、繋ぎを切れば残るのはタブ1つである */
  }
}

// 描画を待つ閾値。
// ⚠️ **`domcontentloaded` の直後に落とさないこと。** 本文を API から取ってくる資料は
//    そこではまだ空で、落ちるのは殻である（＝ `curl` と同じものが残る）。
// 見るのは描画された本文の長さである。`networkidle` では待たない（状態を polling して
//    いる画面では永遠に来ない）。本文がこれだけ出てから変化が止まるまで待ち、どちらも
//    来なければ RENDER_TIMEOUT_MS で打ち切って、そのときの DOM を落とす。
const MIN_TEXT = 500;
const QUIET_MS = 800;
const RENDER_TIMEOUT_MS = Number(process.env.ISUCON_BROWSER_RENDER_TIMEOUT_MS ?? 15000);

/**
 * 本文が描画されるまで待つ。⚠️ **待ち切れなくても落ちないこと** — 操作 UI ばかりの
 * 画面（ポータル）は MIN_TEXT に永遠に届かないので、RENDER_TIMEOUT_MS まで待って、そこで返る。
 */
async function waitForRender(page) {
  const deadline = Date.now() + RENDER_TIMEOUT_MS;
  let last = -1;
  let since = Date.now();
  for (;;) {
    let size = 0;
    try {
      size = (await page.innerText('body', { timeout: 1000 })).trim().length;
    } catch {
      size = 0; // body がまだ無い / 読めない
    }
    const now = Date.now();
    if (size !== last) {
      last = size;
      since = now;
    } else if (size >= MIN_TEXT && now - since >= QUIET_MS) {
      return;
    }
    if (now >= deadline) return;
    await page.waitForTimeout(250);
  }
}

/** 画面に出ているリンクを、その資料の URL からの絶対 URL で返す。 */
async function visibleLinks(page) {
  return page.locator('a[href]').evaluateAll((as) =>
    as
      .filter((a) => a.offsetParent !== null || a.getClientRects().length > 0)
      .map((a) => ({ text: (a.textContent ?? '').trim().replace(/\s+/g, ' '), href: a.href })),
  );
}

// 区切りの無い壊れた data: URI では、`,` の前が本文になる。type/subtype の形のときだけ残す。
function dataMediatype(href) {
  return /^data:([a-z0-9.+-]+\/[a-z0-9.+-]+)(?=[;,]|$)/i.exec(href)?.[1] ?? '';
}

function hrefWithoutDataBody(href) {
  if (!/^data:/i.test(href)) return href;
  return `data:${dataMediatype(href)},…（${href.length} 文字。中身は save-link で落とす）`;
}

async function waitForLinks(page, name) {
  // 文言を含むだけのリンクは、RENDER_TIMEOUT_MS まで完全に一致するリンクを待ってから採る。先に描かれた
  //    別のリンク（「テンプレート（旧版）」など）を黙って落とさないため。
  const deadline = Date.now() + RENDER_TIMEOUT_MS;
  for (;;) {
    const links = await visibleLinks(page);
    const exact = links.filter((l) => l.text === name);
    if (exact.length > 0) return exact;
    if (Date.now() >= deadline) return links.filter((l) => l.text.includes(name));
    await page.waitForTimeout(250);
  }
}

async function saveDataLink(page, name, out) {
  const hits = await waitForLinks(page, name);
  if (hits.length !== 1) {
    console.error(
      hits.length === 0
        ? `文言「${name}」のリンクがありません。open でリンクの一覧を見てください。`
        : `文言「${name}」のリンクが ${hits.length} 個あります。1つに決まる文言を渡してください:`,
    );
    for (const link of hits) console.error(`  ${hrefWithoutDataBody(link.href)}\t${link.text}`);
    return false;
  }
  const { href } = hits[0];
  if (!/^data:/i.test(href)) {
    console.error(`data: URI のリンクではありません（${href}）。この URL から curl で落としてください。`);
    return false;
  }
  let body;
  try {
    body = Buffer.from(await (await fetch(href)).arrayBuffer());
  } catch (e) {
    // エラーの文面に URL（＝本文）が入ることがあるので、そのまま出さない。
    console.error(`data: URI のリンクを解けませんでした（data:${dataMediatype(href)}、${e?.name ?? 'Error'}）。`);
    return false;
  }
  await writePrivate(out, body);
  console.error(`[browser] ${out} へ落としました（data:${dataMediatype(href)}、${body.length} バイト。中身は印字していません）`);
  return true;
}

async function writePrivate(out, body) {
  await mkdir(path.dirname(out), { recursive: true });
  // writeFile の mode は作るときにしか効かない。既に在ったファイルは、書く前に絞る。
  try {
    await chmod(out, 0o600);
  } catch (e) {
    if (e?.code !== 'ENOENT') throw e;
  }
  await writeFile(out, body, { mode: 0o600 });
}

// 印字する URL から query と fragment を落とす。署名付きの URL や `?token=` に資格情報が入る。
function urlWithoutQuery(u) {
  try {
    const parsed = new URL(u);
    return `${parsed.origin}${parsed.pathname}`;
  } catch {
    return String(u).split(/[?#]/)[0];
  }
}

const MAX_FIELDS = 50;

function describeFields(value) {
  if (value === null || typeof value !== 'object') return [`（JSON の ${value === null ? 'null' : typeof value}）`];
  if (Array.isArray(value)) return [`（JSON の array、${value.length} 個）`];
  const entries = Object.entries(value);
  const lines = entries.slice(0, MAX_FIELDS).map(([k, v]) => {
    if (typeof v === 'string') return `${k}\tstring（${v.length} 文字）`;
    if (Array.isArray(v)) return `${k}\tarray（${v.length} 個）`;
    return `${k}\t${v === null ? 'null' : typeof v}`;
  });
  if (entries.length > MAX_FIELDS) lines.push(`…ほか ${entries.length - MAX_FIELDS} 個`);
  return lines;
}

// `.` を含む名前のキー（`cloudformation.yaml`）も辿れるように、各階層で長いキーから当てる。
// 配列は添字（`templates.0.body`）で辿る。見つからなければ、いちばん深く辿れた階層を返す。
function lookupField(value, parts, depth = 0) {
  if (parts.length === 0) return { found: true, value };
  let deepest = { found: false, parent: value, depth };
  if (value === null || typeof value !== 'object') return deepest;
  for (let i = parts.length; i >= 1; i--) {
    const key = parts.slice(0, i).join('.');
    if (Object.hasOwn(value, key)) {
      const hit = lookupField(value[key], parts.slice(i), depth + i);
      if (hit.found) return hit;
      if (hit.depth > deepest.depth) deepest = hit;
    }
  }
  return deepest;
}

async function saveJsonField(page, api, field, out) {
  let res;
  try {
    res = await page.evaluate(async (u) => {
      try {
        const r = await fetch(u, { credentials: 'include', headers: { Accept: 'application/json' } });
        return { url: r.url, status: r.status, type: r.headers.get('content-type') ?? '', body: await r.text() };
      } catch (e) {
        return { error: e?.name ?? 'Error' };
      }
    }, api);
  } catch (e) {
    // ページがログイン画面などへ移ると、評価の途中で文脈が消える。
    console.error(`ページの中で API を呼ぶ前にページが移りました（${e?.name ?? 'Error'}）。ログインが切れていないか、open で確かめてください。`);
    return false;
  }
  if (res.error) {
    console.error(`API を呼べませんでした（${urlWithoutQuery(api)}、${res.error}）。ページと同じオリジンの URL か確かめてください。`);
    return false;
  }
  const shown = urlWithoutQuery(res.url);
  if (res.status < 200 || res.status >= 300) {
    console.error(`API が HTTP ${res.status} を返しました（${shown}、${res.type || '種類なし'}、${res.body.length} 文字）。`);
    if ([401, 403, 405].includes(res.status)) {
      console.error(
        'save-json は cookie だけを載せた GET で呼びます。ページが POST で呼ぶ・Authorization ヘッダを付ける API には使えません。',
      );
    }
    return false;
  }
  let parsed;
  try {
    parsed = JSON.parse(res.body);
  } catch {
    console.error(`API の応答が JSON ではありません（${shown}、${res.type || '種類なし'}、${res.body.length} 文字）。`);
    return false;
  }
  const hit = lookupField(parsed, field.split('.'));
  if (!hit.found) {
    console.error(`フィールド「${field}」がありません。辿れたところまでの階層のフィールド（値は出していません）:`);
    for (const line of describeFields(hit.parent)) console.error(`  ${line}`);
    return false;
  }
  const { value } = hit;
  let body;
  if (typeof value === 'string') {
    body = value;
  } else if (value !== null && typeof value === 'object') {
    body = `${JSON.stringify(value, null, 2)}\n`;
  } else {
    console.error(`フィールド「${field}」は ${value === null ? 'null' : typeof value} です。文字列かオブジェクトのフィールドを渡してください。`);
    return false;
  }
  await writePrivate(out, body);
  console.error(`[browser] ${out} へ落としました（${shown} の ${field}、${Buffer.byteLength(body)} バイト。中身は印字していません）`);
  return true;
}

async function main(argv) {
  // ⚠️ `| head` で読まれたときに traceback を吐かないこと。読む側は長い一覧を切って
  //    使うので、閉じたパイプへ書いた瞬間の EPIPE が「道具が壊れている」ように見える。
  process.stdout.on('error', (e) => {
    if (e?.code === 'EPIPE') process.exit(0);
    throw e;
  });

  const [sub, url, ...rest] = argv;
  const names = { 'save-link': ['name', 'out'], 'save-json': ['api', 'field', 'out'] }[sub] ?? ['out'];
  const { name, api, field, out } = Object.fromEntries(names.map((k, i) => [k, rest[i]]));
  // ⚠️ 引数の検査は繋ぐより先に済ませること。あとに置くと、打ち間違えた1回が
  //    共有のブラウザを別のページへ動かしてから断ることになる（人が開いていた画面が消える）。
  if (!sub || !url) {
    console.error('usage: tools/browser.mjs <open|save|links|text> <url> [out]');
    console.error('       tools/browser.mjs save-link <url> <リンクの文言> <out>');
    console.error('       tools/browser.mjs save-json <url> <API の URL> <フィールド> <out>');
    process.exit(2);
  }
  if (!['open', 'save', 'links', 'text', 'save-link', 'save-json'].includes(sub)) {
    console.error(`知らないサブコマンドです: ${sub}`);
    process.exit(2);
  }
  if (sub === 'save' && !out) {
    console.error('save には出力先が要ります: tools/browser.mjs save <url> <out>');
    process.exit(2);
  }
  if (sub === 'save-link' && (!name || !out)) {
    console.error('save-link にはリンクの文言と出力先が要ります: tools/browser.mjs save-link <url> <リンクの文言> <out>');
    process.exit(2);
  }
  if (sub === 'save-json' && (!api || !field || !out)) {
    console.error(
      'save-json には API の URL・フィールド・出力先が要ります: tools/browser.mjs save-json <url> <API の URL> <フィールド> <out>',
    );
    process.exit(2);
  }

  // ⚠️ **自分のタブで開くこと（`own: true`）。** 複数のサブエージェントが同時に
  //    この道具を叩くことがある。開いているタブを共有すると、片方が開いた画面をもう片方が読む。
  //    URL を開き直すので、人の見ている画面と同じものを読む用は無い。
  const { browser, page } = await connect({ own: true });
  // ⚠️ `networkidle` で待たないこと。ポータルが状態を polling していると永遠に来ない。
  await page.goto(url, { waitUntil: 'domcontentloaded' });

  try {
    if (sub === 'save') {
      // 描画後の DOM を落とす。`curl` の返す初期 HTML とは別物である
      //    （JavaScript が描くポータルでは、初期 HTML に本文が1文字も無い）。
      //
      // 落とすのは、本文が描画されるのを待ってからである（→ waitForRender）。**落とし直しでは
      //    直せない** — 同じ URL をもう一度開いても、ページの同じ時点をもう一度撮るだけである。
      await waitForRender(page);
      // 写しは読まれる（fetch-doc.sh は変換に失敗した回に raw を読めと言う）ので、data: URI の
      //    リンクの本文も落とす。Chromium の直列化は属性を必ず `"` で囲む。
      const html = (await page.content()).replace(
        /\bhref="(data:[^"]*)"/gi,
        (_, href) => `href="${hrefWithoutDataBody(href)}"`,
      );
      await writeFile(out, html);
      console.error(`[browser] ${out} へ落としました（${page.url()}）`);
    } else if (sub === 'save-link') {
      if (!(await saveDataLink(page, name, out))) process.exitCode = 1;
    } else if (sub === 'save-json') {
      if (!(await saveJsonField(page, api, field, out))) process.exitCode = 1;
    } else if (sub === 'links') {
      for (const link of await visibleLinks(page)) {
        console.log(`${hrefWithoutDataBody(link.href)}\t${link.text}`);
      }
    } else if (sub === 'text') {
      // 描画後の本文を出す。資料を写しにせず、その場で読むときに使う。
      await waitForRender(page);
      console.log(await page.innerText('body'));
    } else if (sub === 'open') {
      console.log(`# ${await page.title()}\t${page.url()}`);
      console.log('## リンク');
      for (const link of await visibleLinks(page)) {
        console.log(`${hrefWithoutDataBody(link.href)}\t${link.text}`);
      }
      console.log('## 操作できる要素');
      for (const role of ['button', 'link', 'combobox', 'textbox', 'checkbox', 'radio']) {
        // ⚠️ `??` で繋がないこと。文字を持たない要素の textContent は null ではなく空文字
        //    なので、そこで止まって aria-label まで落ちてこない。アイコンだけのボタン
        //    （`<button aria-label="ベンチマークを実行"><svg/></button>`）が一覧から消える。
        const names = await page.getByRole(role).evaluateAll((els) =>
          els.map((el) => (el.textContent || el.getAttribute('aria-label') || el.name || '').trim()),
        );
        for (const name of names.filter(Boolean)) {
          console.log(`${role}\t${name}`);
        }
      }
    }
  } finally {
    // 自分で開いたタブを閉じる（最後の1つなら残す → closePage）。
    await closePage(browser, page);
    await browser.close(); // CDP 接続を切るだけ。ブラウザは終了しない
  }
}

// import されたときは何もしない（connect() だけ使うアダプタがある）。
// ⚠️ `file://` + パスの文字列連結で比べないこと。workspace のパスに空白や `#` が
//    入った回に一致しなくなり、直に叩いても何も起きない道具になる。
if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  await main(process.argv.slice(2));
}
