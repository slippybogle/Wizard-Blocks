// Screenshots the simple page states (see screens_test.go).
const { chromium } = require('playwright');
const cfg = JSON.parse(process.argv[2]);
(async () => {
  const browser = await chromium.launch({ executablePath: process.env.WB_CHROMIUM || undefined });
  const views = { desktop: { width: 1280, height: 900 }, phone: { width: 390, height: 844, isMobile: true, deviceScaleFactor: 2 } };
  for (const [name, url] of Object.entries(cfg.urls)) {
    for (const [vname, vp] of Object.entries(views)) {
      const ctx = await browser.newContext({ viewport: { width: vp.width, height: vp.height }, isMobile: !!vp.isMobile, deviceScaleFactor: vp.deviceScaleFactor || 1, reducedMotion: 'reduce' });
      const page = await ctx.newPage();
      const errors = [];
      page.on('pageerror', (e) => errors.push(e.message));
      page.on('console', (m) => { if (m.type() === 'error') errors.push(m.text()); });
      await page.goto(url);
      await page.waitForFunction(() => document.getElementById('status').textContent.startsWith('live'), null, { timeout: 10000 });
      await page.waitForTimeout(400);
      const overflow = await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth + 1);
      const file = `${cfg.out}/${name}-${vname}.png`;
      await page.screenshot({ path: file, fullPage: true });
      const banner = await page.evaluate(() => { const b = document.getElementById('banner'); return b.offsetParent === null ? '' : b.textContent; });
      const shown = await page.evaluate(() => Object.fromEntries(['chains', 'banner', 's-doge-wrap', 'diff'].map((id) => [id, document.getElementById(id).offsetParent !== null])));
      const wantAux = name.startsWith('ltc'), wantBanner = name.startsWith('ltc') && name !== 'ltc-doge-merged';
      if (shown.chains !== wantAux || shown['s-doge-wrap'] !== wantAux || shown.banner !== wantBanner || !shown.diff) {
        errors.push('visibility wrong: ' + JSON.stringify(shown));
      }
      console.log(`${name} ${vname}: ${file}${banner ? ' | banner: ' + banner : ''}${overflow ? ' | HORIZONTAL OVERFLOW' : ''}${errors.length ? ' | JS ERRORS: ' + errors.join('; ') : ''}`);
      if (errors.length || overflow) process.exitCode = 1;
      await ctx.close();
    }
  }
  await browser.close();
})().catch((e) => { console.error(e); process.exit(1); });
