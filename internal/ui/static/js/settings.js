// Ledger "Settings": live difficulty settings behind a password login.
// The endpoints exist only on the UI port; the session cookie is HttpOnly
// and every change carries the X-WB-Admin header (CSRF protection).

const esc = (s) => String(s ?? '').replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[c]);

async function api(method, path, body) {
  const res = await fetch(path, {
    method,
    headers: { 'Content-Type': 'application/json', 'X-WB-Admin': '1' },
    body: body === undefined ? undefined : JSON.stringify(body),
    credentials: 'same-origin',
    cache: 'no-store',
  });
  let data = {};
  try { data = await res.json(); } catch { /* empty */ }
  return { ok: res.ok, status: res.status, data };
}

/** Client-side mirror of the server's validation (the server is authoritative). */
export function validateSettings(d, limits) {
  const errs = [];
  const inRange = (v) => Number.isFinite(v) && v >= limits.diff_min && v <= limits.diff_max;
  if (!inRange(d.vardiff_min)) errs.push(`VARDIFF_MIN must be between ${limits.diff_min} and ${limits.diff_max}`);
  if (!inRange(d.vardiff_max)) errs.push(`VARDIFF_MAX must be between ${limits.diff_min} and ${limits.diff_max}`);
  if (inRange(d.vardiff_min) && inRange(d.vardiff_max) && d.vardiff_min > d.vardiff_max) errs.push('VARDIFF_MIN must be <= VARDIFF_MAX');
  if (!(d.vardiff_target_seconds >= limits.target_min && d.vardiff_target_seconds <= limits.target_max)) {
    errs.push(`VARDIFF_TARGET_SECONDS must be between ${limits.target_min} and ${limits.target_max}`);
  }
  if (d.fixed_diff !== 0 && !(d.fixed_diff >= d.vardiff_min && d.fixed_diff <= d.vardiff_max)) {
    errs.push('FIXED_DIFF must be 0 (vardiff) or within VARDIFF_MIN..VARDIFF_MAX');
  }
  for (const [w, v] of Object.entries(d.worker_overrides)) {
    if (!w.trim()) errs.push('Worker override names cannot be empty');
    if (!inRange(v)) errs.push(`Override for ${w} must be between ${limits.diff_min} and ${limits.diff_max}`);
  }
  return errs;
}

export class SettingsPanel {
  constructor(root) {
    this.root = root;
    this.data = null;
  }

  async refresh() {
    const s = await api('GET', 'api/admin/session');
    if (!s.ok) { this.root.innerHTML = '<p class="msg err">Settings unavailable.</p>'; return; }
    if (!s.data.enabled) {
      this.root.innerHTML = `<p class="note">Settings are disabled. Set <code>WB_UI_ADMIN_PASSWORD</code>
        (<code>UI_ADMIN_PASSWORD</code> in the compose <code>.env</code>) and restart to edit difficulty here.
        The current values come from the config: see Stratum above.</p>`;
      return;
    }
    if (!s.data.authed) { this.renderLogin(); return; }
    const r = await api('GET', 'api/admin/settings');
    if (r.status === 401) { this.renderLogin(); return; }
    this.data = r.data;
    this.renderForm();
  }

  renderLogin(message) {
    this.root.innerHTML = `
      <form class="form" id="login-form" autocomplete="on">
        <p class="note">Difficulty settings are protected. Log in with the UI admin password.</p>
        <div class="form-row"><div class="field">
          <label for="admin-pw">Admin password</label>
          <input id="admin-pw" type="password" autocomplete="current-password" required>
        </div></div>
        <div class="btn-row"><button class="btn primary" type="submit">Log in</button>
        <span class="msg ${message ? 'err' : ''}" id="login-msg" role="status">${esc(message || '')}</span></div>
      </form>`;
    this.root.querySelector('#login-form').addEventListener('submit', async (e) => {
      e.preventDefault();
      const btn = e.target.querySelector('button');
      btn.disabled = true;
      const r = await api('POST', 'api/admin/login', { password: this.root.querySelector('#admin-pw').value });
      btn.disabled = false;
      if (r.ok) this.refresh(); else this.renderLogin(r.data.error || 'Login failed');
    });
  }

  renderForm(message, isError) {
    const { settings: d, limits, saved, persistable, workers } = this.data;
    const ovr = Object.entries(d.worker_overrides || {});
    const opts = workers.map((w) => `<option value="${esc(w)}"></option>`).join('');
    this.root.innerHTML = `
      <form class="form" id="settings-form" novalidate>
        <p class="note">Changes apply to connected miners immediately (new difficulty + fresh job) and are
        ${persistable ? 'saved to disk; saved values override the config/env on restart' : '<b>not</b> saved: no data directory is configured'}.
        Source: ${saved ? 'saved from this page' : 'config/env'}.</p>
        <div class="form-row">
          ${field('vardiff_min', 'VARDIFF_MIN', d.vardiff_min, 'Lowest share difficulty')}
          ${field('vardiff_max', 'VARDIFF_MAX', d.vardiff_max, 'Highest share difficulty')}
          ${field('vardiff_target_seconds', 'VARDIFF_TARGET_SECONDS', d.vardiff_target_seconds, `Seconds per share (${limits.target_min}-${limits.target_max})`)}
          ${field('fixed_diff', 'FIXED_DIFF', d.fixed_diff, '0 = vardiff; otherwise every miner uses this')}
        </div>
        <div class="field"><label>Per-worker overrides (beat FIXED_DIFF and the miner's d= password; clamped to min/max)</label></div>
        <datalist id="worker-names">${opts}</datalist>
        <div id="ovr-list" class="form">${ovr.map(([w, v]) => ovrRow(w, v)).join('')}</div>
        <div class="btn-row"><button type="button" class="btn" id="ovr-add">+ Add override</button></div>
        <div class="btn-row">
          <button type="submit" class="btn primary">Apply &amp; save</button>
          <button type="button" class="btn danger" id="reset">Reset to config</button>
          <button type="button" class="btn" id="logout">Log out</button>
          <span class="msg ${isError ? 'err' : 'ok'}" id="settings-msg" role="status">${esc(message || '')}</span>
        </div>
      </form>`;
    const form = this.root.querySelector('#settings-form');
    const list = form.querySelector('#ovr-list');
    form.querySelector('#ovr-add').onclick = () => list.insertAdjacentHTML('beforeend', ovrRow('', ''));
    list.addEventListener('click', (e) => { if (e.target.matches('.ovr-del')) e.target.closest('.ovr-row').remove(); });
    form.addEventListener('submit', (e) => { e.preventDefault(); this.save(form); });
    form.querySelector('#reset').onclick = async () => {
      if (!confirm('Restore the difficulty settings from the config/env and delete the saved settings?')) return;
      const r = await api('POST', 'api/admin/settings/reset');
      if (r.ok) { this.data = r.data; this.renderForm('Reset to config values.'); } else this.handleError(r);
    };
    form.querySelector('#logout').onclick = async () => { await api('POST', 'api/admin/logout'); this.refresh(); };
  }

  collect(form) {
    const num = (id) => Number(form.querySelector('#' + id).value);
    const d = {
      vardiff_min: num('vardiff_min'), vardiff_max: num('vardiff_max'),
      vardiff_target_seconds: num('vardiff_target_seconds'), fixed_diff: num('fixed_diff') || 0,
      worker_overrides: {},
    };
    for (const row of form.querySelectorAll('.ovr-row')) {
      const w = row.querySelector('.ovr-name').value.trim();
      const v = row.querySelector('.ovr-diff').value;
      if (!w && !v) continue;
      d.worker_overrides[w] = Number(v);
    }
    return d;
  }

  async save(form) {
    const d = this.collect(form);
    const errs = validateSettings(d, this.data.limits);
    form.querySelectorAll('input').forEach((i) => i.removeAttribute('aria-invalid'));
    if (errs.length) { this.setMsg(errs.join('\n'), true); return; }
    const btn = form.querySelector('button[type=submit]');
    btn.disabled = true;
    const r = await api('PUT', 'api/admin/settings', d);
    btn.disabled = false;
    if (r.ok) { this.data = r.data; this.renderForm('Applied to all connected miners' + (r.data.persistable ? ' and saved.' : ' (not saved: no data dir).')); } else this.handleError(r);
  }

  handleError(r) {
    if (r.status === 401) { this.renderLogin('Session expired; log in again.'); return; }
    this.setMsg(r.data.error || `Error ${r.status}`, true);
  }

  setMsg(text, isError) {
    const m = this.root.querySelector('#settings-msg');
    if (!m) return;
    m.className = 'msg ' + (isError ? 'err' : 'ok');
    m.textContent = text;
  }
}

function field(id, label, value, help) {
  return `<div class="field"><label for="${id}">${label}</label>
    <input id="${id}" type="text" inputmode="decimal" value="${esc(value)}" spellcheck="false">
    <small>${esc(help)}</small></div>`;
}

function ovrRow(worker, diff) {
  return `<div class="ovr-row">
    <input class="ovr-name" type="text" list="worker-names" placeholder="worker (exact username)" value="${esc(worker)}" aria-label="Worker name" spellcheck="false" autocapitalize="off">
    <input class="ovr-diff" type="text" inputmode="decimal" placeholder="difficulty" value="${esc(diff)}" aria-label="Difficulty">
    <button type="button" class="btn ovr-del" aria-label="Remove override">×</button></div>`;
}
