// Records a screen-recording demo of Constellate driving your own map.
//
//   node record-demo.js --snapshot constellate-snapshot.json \
//                       --topics "pandas|kubernetes|invoice" \
//                       --code-sessions claude-code-sessions.json
//
// It starts Constellate.exe on a loopback port, drives the real app in Chromium
// with a screencast running, and writes a .webm. Nothing leaves the machine.
//
// Privacy: every conversation title in the map is visible on camera — in node
// labels, in the sidebar's topic clusters, and in the reader's "similar
// conversations" list. Prune the snapshot before recording (see README), and note
// that --topics only governs which conversation is opened on camera, not what
// else is on screen. Without --topics the reader beat is skipped entirely.
//
// Requires Node 18+ and Playwright's Chromium:  npx playwright install chromium

const { chromium } = require('playwright');
const { spawn } = require('child_process');
const fs = require('fs');
const path = require('path');
const http = require('http');

function parseArgs(argv) {
  const args = {
    exe: '', snapshot: '', codeSessions: '', topics: '', out: 'demo',
    port: 47615, width: 1280, height: 800, hideSimilar: false, keepOpen: false,
  };
  for (let i = 2; i < argv.length; i++) {
    const [flag, inline] = argv[i].split(/=(.+)/);
    const next = () => (inline !== undefined ? inline : argv[++i]);
    switch (flag) {
      case '--exe': args.exe = next(); break;
      case '--snapshot': args.snapshot = next(); break;
      case '--code-sessions': args.codeSessions = next(); break;
      case '--topics': args.topics = next(); break;
      case '--out': args.out = next(); break;
      case '--port': args.port = Number(next()); break;
      case '--width': args.width = Number(next()); break;
      case '--height': args.height = Number(next()); break;
      case '--hide-similar': args.hideSimilar = true; break;
      case '--keep-open': args.keepOpen = true; break;
      case '--help': case '-h': usage(); process.exit(0);
      default: fail(`unknown flag: ${flag}`);
    }
  }
  return args;
}

function usage() {
  console.log(`
Usage: node record-demo.js --snapshot <file> [options]

  --snapshot <file>        the map to record: Export ▾ → Snapshot in Constellate
  --topics <regex>         only open a conversation whose title matches this.
                           Omit and the reader beat is skipped (nothing opened).
  --code-sessions <file>   Export-CodeSessions.exe output, imported on camera
  --exe <file>             Constellate.exe (default: ../dist, then alongside)
  --out <dir>              output directory (default: demo)
  --port <n>               loopback port (default: 47615)
  --width/--height <n>     recording size (default: 1280x800)
  --hide-similar           hide the reader's "similar conversations" list
  --keep-open              leave the browser open at the end for inspection
`);
}

const fail = (msg) => { console.error('\n' + msg + '\n'); process.exit(1); };

function findExe(given) {
  if (given) {
    if (!fs.existsSync(given)) fail(`--exe not found: ${given}`);
    return given;
  }
  const names = process.platform === 'win32' ? ['Constellate.exe'] : ['Constellate', 'constellate'];
  const dirs = [path.join(__dirname, '..', 'dist'), __dirname, process.cwd()];
  for (const dir of dirs) {
    for (const name of names) {
      const candidate = path.join(dir, name);
      if (fs.existsSync(candidate)) return candidate;
    }
  }
  fail('Could not find Constellate.exe. Pass --exe <path>, or build it with build.ps1.');
}

const get = (url) => new Promise((resolve) => {
  const req = http.get(url, (res) => { res.resume(); resolve(res.statusCode); });
  req.on('error', () => resolve(0));
  req.setTimeout(1500, () => { req.destroy(); resolve(0); });
});

async function waitForServer(url, tries = 40) {
  for (let i = 0; i < tries; i++) {
    if (await get(url) === 200) return true;
    await new Promise(r => setTimeout(r, 250));
  }
  return false;
}

(async () => {
  const args = parseArgs(process.argv);
  if (!args.snapshot) { usage(); fail('--snapshot is required.'); }
  for (const [flag, file] of [['--snapshot', args.snapshot], ['--code-sessions', args.codeSessions]]) {
    if (file && !fs.existsSync(file)) fail(`${flag} not found: ${file}`);
  }
  const exe = findExe(args.exe);
  const url = `http://127.0.0.1:${args.port}/`;
  fs.mkdirSync(args.out, { recursive: true });

  const topics = args.topics ? new RegExp(args.topics, 'i') : null;
  if (!topics) {
    console.log('No --topics given: no conversation will be opened on camera.');
  }

  // Serve the app ourselves so the recording is of a known, empty-cache state.
  console.log(`Starting ${path.basename(exe)} on port ${args.port}...`);
  const server = spawn(exe, ['-no-browser', '-port', String(args.port)], { stdio: 'ignore' });
  server.on('error', (e) => fail(`Could not start ${exe}: ${e.message}`));
  const stop = () => { try { server.kill(); } catch {} };
  process.on('exit', stop);
  process.on('SIGINT', () => { stop(); process.exit(130); });

  if (!await waitForServer(url)) {
    stop();
    fail(`${url} never came up. Is another copy already running, or the port taken?`);
  }

  const results = [];
  const check = (name, ok, detail = '') => {
    results.push({ name, ok, detail });
    console.log(`  ${ok ? '✅' : '❌'} ${name}${detail ? ' — ' + detail : ''}`);
  };

  const browser = await chromium.launch();
  const ctx = await browser.newContext({
    viewport: { width: args.width, height: args.height },
    recordVideo: { dir: args.out, size: { width: args.width, height: args.height } },
  });
  const page = await ctx.newPage();
  const errors = [];
  page.on('pageerror', e => errors.push(e.message));
  page.on('console', m => {
    if (m.type() !== 'error') return;
    // The app probes for a snapshot served alongside it; nothing is served here,
    // so its 404 is expected and not a fault worth reporting.
    if ((m.location().url || '').includes('constellate-snapshot.json')) return;
    errors.push(m.text());
  });

  // Captions live in the page, so the screencast picks them up.
  const installCaptions = () => page.evaluate(() => {
    const el = document.createElement('div');
    el.id = 'demo-caption';
    Object.assign(el.style, {
      position: 'fixed', left: '50%', bottom: '190px', transform: 'translateX(-50%)',
      maxWidth: '760px', padding: '11px 20px', borderRadius: '10px',
      background: 'rgba(12,12,14,.88)', border: '1px solid rgba(255,255,255,.14)',
      color: '#f2f2f4', font: '500 15px/1.45 ui-sans-serif,system-ui,Segoe UI,sans-serif',
      textAlign: 'center', zIndex: 9999, pointerEvents: 'none', opacity: '0',
      transition: 'opacity .35s ease', boxShadow: '0 10px 30px rgba(0,0,0,.5)',
    });
    document.body.appendChild(el);
    window.__caption = (text) => {
      const c = document.getElementById('demo-caption');
      if (!text) { c.style.opacity = '0'; return; }
      c.textContent = text;
      c.style.opacity = '1';
    };
  });
  const say = async (text, hold = 1900) => {
    await page.evaluate(t => window.__caption(t), text);
    await page.waitForTimeout(hold);
  };
  const clearCaption = async (pause = 300) => {
    await page.evaluate(() => window.__caption(null));
    await page.waitForTimeout(pause);
  };
  // The canvas, not the sidebar — gestures must land on the map.
  const canvasBox = () => page.evaluate(() => {
    const r = document.querySelector('canvas').getBoundingClientRect();
    return { cx: r.left + r.width / 2, cy: r.top + r.height / 2 };
  });

  console.log('\nRecording:');
  await page.goto(url, { waitUntil: 'load' });
  await page.waitForTimeout(900);
  await installCaptions();
  if (args.hideSimilar) {
    await page.addStyleTag({ content: '#rsimilar{display:none !important}' });
  }

  await say('Constellate — one executable, nothing to install', 2400);
  await say('Import an export: ChatGPT, Claude, Gemini or markdown', 1900);
  await page.locator('#filepick').setInputFiles(args.snapshot);
  await page.waitForTimeout(4000);
  const loaded = await page.evaluate(() => S.convs.length);
  check('map imported', loaded > 0, `${loaded} conversations`);
  if (!loaded) fail('The snapshot imported nothing — is it a Constellate snapshot?');

  await say('Conversations about the same things pull together', 2400);
  await clearCaption(400);
  await page.waitForTimeout(2600);   // let the force layout settle

  // Zoom.
  let box = await canvasBox();
  await page.mouse.move(box.cx, box.cy);
  await say('Scroll to zoom', 900);
  const rBefore = await page.evaluate(() => R.cam.r);
  for (let i = 0; i < 9; i++) { await page.mouse.wheel(0, -120); await page.waitForTimeout(90); }
  await page.waitForTimeout(500);
  check('zoom', await page.evaluate(() => R.cam.r) < rBefore);
  await clearCaption(200);

  // Orbit.
  await say('Drag to orbit', 900);
  const thetaBefore = await page.evaluate(() => R.cam.theta);
  await page.mouse.move(box.cx, box.cy);
  await page.mouse.down();
  await page.mouse.move(box.cx + 190, box.cy + 60, { steps: 35 });
  await page.mouse.move(box.cx - 90, box.cy - 40, { steps: 35 });
  await page.mouse.up();
  await page.waitForTimeout(400);
  check('orbit', await page.evaluate(() => R.cam.theta) !== thetaBefore);
  await clearCaption(200);

  // Search, using a term from the map itself so it always matches something.
  const term = await page.evaluate((src) => {
    const re = src ? new RegExp(src, 'i') : null;
    const pool = re ? S.convs.filter(c => re.test(c.title)) : S.convs;
    const title = (pool[0] || S.convs[0]).title;
    const word = title.split(/[\s—:,.]+/).filter(w => w.length > 4)[0];
    return (word || title).toLowerCase();
  }, args.topics);
  const search = page.locator('input[placeholder*="search" i]').first();
  await search.click();
  await say('Search fades everything that does not match', 1100);
  await search.type(term, { delay: 110 });
  await page.waitForTimeout(1900);
  check('search', await page.evaluate(() => !!(S.searchSet && S.searchSet.size)), `"${term}"`);
  await clearCaption(200);
  await search.fill('');
  await page.waitForTimeout(1000);

  // Open one conversation — only ever one whose title matches --topics.
  if (topics) {
    await say('Click a node to read the conversation', 1200);
    const hit = await page.evaluate((src) => {
      const re = new RegExp(src, 'i');
      const rect = document.querySelector('canvas').getBoundingClientRect();
      let best = null;
      for (let i = 0; i < G.N; i++) {
        if (!G.vis[i] || !re.test(S.convs[i].title)) continue;
        const v = new THREE.Vector3(G.pos[i * 3], G.pos[i * 3 + 1], G.pos[i * 3 + 2]).project(R.camera);
        if (Math.abs(v.x) > 0.45 || Math.abs(v.y) > 0.45) continue;
        const score = S.convs[i].msgCount || 0;
        if (!best || score > best.score) {
          best = { score, title: S.convs[i].title,
                   x: rect.left + (v.x * .5 + .5) * rect.width, y: rect.top + (-v.y * .5 + .5) * rect.height };
        }
      }
      return best;
    }, args.topics);
    if (hit) {
      await page.mouse.move(hit.x, hit.y, { steps: 18 });
      await page.waitForTimeout(400);
      await page.mouse.click(hit.x, hit.y);
      await page.waitForTimeout(2400);
      const open = await page.evaluate(() => document.getElementById('reader').classList.contains('open'));
      check('conversation opened', open, hit.title.slice(0, 60));
      await clearCaption(200);
      await page.locator('#rclose').click().catch(() => {});
      await page.waitForTimeout(600);
    } else {
      check('conversation opened', false, 'no --topics match was on screen; beat skipped');
      await clearCaption(200);
    }
  }

  // Axes layout.
  await say('Second layout: time across, topic contrasts up and back', 1600);
  await page.getByText('Axes', { exact: true }).first().click();
  await page.waitForTimeout(2600);
  box = await canvasBox();
  await page.mouse.move(box.cx, box.cy);
  await page.mouse.down();
  await page.mouse.move(box.cx + 140, box.cy + 30, { steps: 30 });
  await page.mouse.up();
  await page.waitForTimeout(700);
  check('axes layout', await page.evaluate(() => S.layoutMode === 'axes'));
  await clearCaption(200);
  await page.getByText('Galaxy', { exact: true }).first().click();
  await page.waitForTimeout(2000);

  // Claude Code sessions, imported on camera.
  if (args.codeSessions) {
    await say('Export-CodeSessions turns Claude Code transcripts into a file like this', 2600);
    const before = await page.evaluate(() => S.convs.length);
    await page.locator('#filepick').setInputFiles(args.codeSessions);
    await page.waitForTimeout(4000);
    const after = await page.evaluate(() => S.convs.length);
    const codeCount = await page.evaluate(() => S.convs.filter(c => c.provider === 'claude-code').length);
    check('code sessions imported', codeCount > 0, `${codeCount} sessions, map ${before} → ${after}`);
    await say('Coding sessions arrive as their own provider, beside the chats', 2600);
    await clearCaption(200);
  }

  // Colour by cluster, then a closing orbit.
  await page.selectOption('#colormode', 'cluster').catch(() => {});
  await say('Colour by topic cluster instead of provider', 2200);
  await clearCaption(300);
  box = await canvasBox();
  await page.mouse.move(box.cx, box.cy);
  await page.mouse.down();
  await page.mouse.move(box.cx + 150, box.cy - 20, { steps: 45 });
  await page.mouse.up();
  await say('All local — no server, no account, nothing uploaded', 2800);
  await clearCaption(700);

  check('no console errors', errors.length === 0, errors.slice(0, 2).join(' | '));

  if (args.keepOpen) {
    console.log('\n--keep-open: press Ctrl+C to finish and save the recording.');
    await new Promise(() => {});
  }
  const video = page.video();
  await ctx.close();          // the video is only finalised on context close
  await browser.close();
  stop();

  const raw = video ? await video.path() : null;
  let final = raw;
  if (raw) {
    final = path.join(args.out, 'constellate-demo.webm');
    fs.renameSync(raw, final);
  }

  const failed = results.filter(r => !r.ok);
  console.log(`\n${failed.length ? '⚠️  ' + failed.length + ' step(s) did not verify' : 'All steps verified'}`);
  if (final) {
    console.log(`Recording: ${path.resolve(final)} (${(fs.statSync(final).size / (1 << 20)).toFixed(1)} MB)`);
    console.log('Watch it before sharing — every title in your map is on screen.');
  }
  process.exit(failed.length ? 1 : 0);
})().catch(e => { console.error('\nFAILED:', e.message); process.exit(1); });
