// Screenshots the plain stats page (see stats_screens_test.go) and checks it:
// black background, the pool and miner lines present, no horizontal scroll.
const { chromium } = require('playwright');
const cfg = JSON.parse(process.argv[2]);
(async () => {
  const browser = await chromium.launch({ executablePath: process.env.WB_CHROMIUM || undefined });
  const views = { desktop: { width: 1280, height: 800 }, phone: { width: 390, height: 844, isMobile: true, deviceScaleFactor: 2 } };
  for (const [name, url] of Object.entries(cfg.urls)) {
    for (const [vname, vp] of Object.entries(views)) {
      const ctx = await browser.newContext({ viewport: { width: vp.width, height: vp.height }, isMobile: !!vp.isMobile, deviceScaleFactor: vp.deviceScaleFactor || 1 });
      const page = await ctx.newPage();
      const errors = [];
      page.on('pageerror', (e) => errors.push(e.message));
      page.on('console', (m) => { if (m.type() === 'error') errors.push(m.text()); });
      await page.goto(url);
      await page.waitForFunction(() => document.getElementById('out').textContent.includes('Miners'), null, { timeout: 10000 });
      const text = await page.evaluate(() => document.getElementById('out').textContent);
      const bg = await page.evaluate(() => getComputedStyle(document.body).backgroundColor);
      const overflow = await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth + 1);
      for (const want of ['hashrate 1m', 'hashrate 5m', 'hashrate 1h', 'hashrate 24h', 'best share']) if (!text.includes(want)) errors.push('missing ' + want);
      if (name === 'stats-mining' && !(text.includes('dghome1') && text.includes('rentx.mrr-7781'))) errors.push('miners missing');
      if (name === 'stats-no-miners' && !text.includes('none yet')) errors.push('no "none yet"');
      if (bg !== 'rgb(0, 0, 0)') errors.push('background ' + bg);
      const file = `${cfg.out}/${name}-${vname}.png`;
      await page.screenshot({ path: file, fullPage: true });
      console.log(`${name} ${vname}: ${file}${overflow ? ' | HORIZONTAL OVERFLOW' : ''}${errors.length ? ' | ERRORS: ' + errors.join('; ') : ''}\n${text}\n`);
      if (errors.length || overflow) process.exitCode = 1;
      await ctx.close();
    }
  }
  await browser.close();
})().catch((e) => { console.error(e); process.exit(1); });
