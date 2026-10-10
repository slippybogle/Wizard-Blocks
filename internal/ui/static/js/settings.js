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
    this.session = s.data;
    if (!s.data.enabled) {
      this.root.innerHTML = `<p class="note">Settings are disabled. Set <code>WB_UI_ADMIN_PASSWORD</code>
        (<code>UI_ADMIN_PASSWORD</code> in the compose <code>.env</code>), or <code>WB_UI_SETTINGS_OPEN=true</code>
        behind an authenticating proxy, and restart to edit settings here.
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
        <p class="note">Settings are protected by a settings password.</p>
        <div class="form-row"><div class="field">
          <label for="admin-pw">Settings password</label>
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
    const { settings: d, limits, saved, persistable, workers, payout } = this.data;
    const ovr = Object.entries(d.worker_overrides || {});
    const opts = workers.map((w) => `<option value="${esc(w)}"></option>`).join('');
    const payoutHtml = payout && payout.settable ? `
      <form class="form" id="payout-form" novalidate>
        <div class="field"><label for="payout-addr">Payout address (CashAddr)</label>
          <input id="payout-addr" type="text" value="${esc(payout.address)}" placeholder="bitcoincash:q…" spellcheck="false" autocapitalize="off" autocomplete="off">
          <small>${payout.address ? '100% of every block reward goes here. Changing it reconnects all miners.'
            : '<b>Not set.</b> Miners are refused (no work is issued) until you set it.'} Checked by the engine and by your node.</small></div>
        <div class="btn-row"><button type="submit" class="btn primary">Verify &amp; save address</button>
        <span class="msg" id="payout-msg" role="status"></span></div>
      </form>` : '';
    this.root.innerHTML = payoutHtml + `
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
          ${this.session && this.session.password_required ? '<button type="button" class="btn" id="logout">Log out</button>' : ''}
          <span class="msg ${isError ? 'err' : 'ok'}" id="settings-msg" role="status">${esc(message || '')}</span>
        </div>
      </form>` + this.passwordHtml();
    const pform = this.root.querySelector('#payout-form');
    if (pform) pform.addEventListener('submit', (e) => { e.preventDefault(); this.savePayout(pform); });
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
    const lo = form.querySelector('#logout');
    if (lo) lo.onclick = async () => { await api('POST', 'api/admin/logout'); this.refresh(); };
    const pw = this.root.querySelector('#pw-form');
    if (pw) {
      pw.addEventListener('submit', async (e) => {
        e.preventDefault();
        const v = pw.querySelector('#new-pw').value, v2 = pw.querySelector('#new-pw2').value;
        if (v.length < 8) { this.pwMsg('Use at least 8 characters.', true); return; }
        if (v !== v2) { this.pwMsg('The two passwords differ.', true); return; }
        const r = await api('POST', 'api/admin/password', { password: v });
        if (!r.ok) { this.pwError(r); return; }
        await this.refresh();
        this.pwMsg('Password set. It is now required to change settings.');
      });
      const rm = pw.querySelector('#rm-pw');
      if (rm) rm.onclick = async () => {
        if (!confirm('Remove the settings password? Anyone who can open this page can then change settings.')) return;
        const r = await api('DELETE', 'api/admin/password');
        if (!r.ok) { this.pwError(r); return; }
        await this.refresh();
        this.pwMsg('Password removed.');
      };
    }
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

  passwordHtml() {
    const ss = this.session;
    if (!ss || !ss.can_set_password) return '';
    const state = ss.password_set
      ? 'A settings password is set and required for every change.'
      : ss.env_password
        ? 'The password from <code>WB_UI_ADMIN_PASSWORD</code> is in use. Setting one here replaces it.'
        : 'No settings password: anyone already logged into Umbrel (or who can open this page) can change settings.';
    return `
      <form class="form" id="pw-form" novalidate>
        <div class="field"><label>Settings password (optional)</label><small>${state} Stored hashed in the data directory.</small></div>
        <div class="form-row">
          <div class="field"><label for="new-pw">${ss.password_set ? 'New password' : 'Password'}</label>
            <input id="new-pw" type="password" autocomplete="new-password" minlength="8"></div>
          <div class="field"><label for="new-pw2">Repeat</label>
            <input id="new-pw2" type="password" autocomplete="new-password" minlength="8"></div>
        </div>
        <div class="btn-row">
          <button type="submit" class="btn">${ss.password_set ? 'Change password' : 'Set settings password'}</button>
          ${ss.password_set ? '<button type="button" class="btn danger" id="rm-pw">Remove password</button>' : ''}
          <span class="msg" id="pw-msg" role="status"></span>
        </div>
      </form>`;
  }

  pwMsg(text, isError) {
    const m = this.root.querySelector('#pw-msg');
    if (!m) return;
    m.className = 'msg ' + (isError ? 'err' : 'ok');
    m.textContent = text;
  }

  pwError(r) {
    if (r.status === 401) { this.renderLogin('Session expired; log in again.'); return; }
    this.pwMsg(r.data.error || `Error ${r.status}`, true);
  }

  async savePayout(form) {
    const addr = form.querySelector('#payout-addr').value.trim();
    const msg = form.querySelector('#payout-msg');
    if (!addr) { msg.className = 'msg err'; msg.textContent = 'Enter a payout address.'; return; }
    const btn = form.querySelector('button');
    btn.disabled = true;
    msg.className = 'msg'; msg.textContent = 'Checking with the node…';
    const r = await api('PUT', 'api/admin/payout', { address: addr });
    btn.disabled = false;
    if (r.status === 401) { this.renderLogin('Session expired; log in again.'); return; }
    if (!r.ok) { msg.className = 'msg err'; msg.textContent = r.data.error || `Error ${r.status}`; return; }
    this.data = r.data;
    this.renderForm();
    const m2 = this.root.querySelector('#payout-msg');
    if (m2) { m2.className = 'msg ok'; m2.textContent = 'Saved. Miners are reconnecting with the new address.'; }
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
