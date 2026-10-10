// Screenshots the LTC + DOGE stats page (see stats_screens_test.go) and
// checks it: hot pink on black, every section and both chains present, no
// horizontal page scroll, no script errors.
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
      try {
        await page.waitForFunction(() => document.getElementById('main').textContent.includes('BLOCKS'), null, { timeout: 10000 });
      } catch (e) {
        throw new Error(`${name} ${vname}: page did not render: ${errors.join('; ') || e.message}`);
      }
      await page.waitForTimeout(6000); // a refresh after the node panel's first poll
      const text = await page.evaluate(() => document.body.innerText);
      const bg = await page.evaluate(() => getComputedStyle(document.body).backgroundColor);
      const fg = await page.evaluate(() => getComputedStyle(document.body).color);
      const overflow = await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth + 1);
      for (const want of ['ENGINE', 'LTC NODE', 'DOGE NODE', 'CHAINS', 'JOB', 'POOL', 'MINERS', 'BLOCKS', 'LTC + DOGE', 'live (3.5m)']) if (!text.includes(want)) errors.push('missing ' + want);
      if (name === 'stats-mining' && !(text.includes('dg1') && text.includes('5,412,345') && text.includes('stale 3'))) errors.push('mining data missing');
      if (name === 'stats-no-miners' && !(text.includes('no miners yet') && text.includes('no address'))) errors.push('empty-state text missing');
      if (bg !== 'rgb(0, 0, 0)') errors.push('background ' + bg);
      if (fg !== 'rgb(255, 105, 180)') errors.push('text colour ' + fg);
      const file = `${cfg.out}/${name}-${vname}.png`;
      await page.screenshot({ path: file, fullPage: true });
      console.log(`${name} ${vname}: ${file}${overflow ? ' | HORIZONTAL OVERFLOW' : ''}${errors.length ? ' | ERRORS: ' + errors.join('; ') : ''}`);
      if (errors.length || overflow) process.exitCode = 1;
      await ctx.close();
    }
  }
  await browser.close();
})().catch((e) => { console.error(e); process.exit(1); });
