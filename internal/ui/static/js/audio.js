// All sound is synthesised with Web Audio; there are no audio files.
// Audio only starts after a user gesture (required by iOS Safari).

export class Sound {
  constructor() {
    this.ctx = null;
    this.enabled = false;
    this.intensity = 0.2;
  }

  // Must be called from a click/touch handler.
  enable() {
    if (!this.ctx) {
      const AC = window.AudioContext || window.webkitAudioContext;
      if (!AC) return false;
      this.ctx = new AC();
      this.master = this.ctx.createGain();
      this.master.gain.value = 0.22;
      this.master.connect(this.ctx.destination);
      this.startAmbient();
    }
    this.ctx.resume();
    this.master.gain.setTargetAtTime(0.22, this.ctx.currentTime, 0.1);
    this.enabled = true;
    return true;
  }

  disable() {
    this.enabled = false;
    if (this.ctx) this.master.gain.setTargetAtTime(0, this.ctx.currentTime, 0.05);
  }

  setIntensity(i) {
    this.intensity = i;
    if (this.ambLfo) this.ambLfo.frequency.setTargetAtTime(0.05 + i * 0.4, this.ctx.currentTime, 2);
    if (this.ambFilter) this.ambFilter.frequency.setTargetAtTime(260 + i * 700, this.ctx.currentTime, 2);
  }

  // Cave drone: detuned low triangles + filtered wind noise, slowly breathing.
  startAmbient() {
    const c = this.ctx;
    const out = c.createGain(); out.gain.value = 0.35; out.connect(this.master);
    const filter = c.createBiquadFilter(); filter.type = 'lowpass'; filter.frequency.value = 400; filter.Q.value = 4;
    filter.connect(out);
    this.ambFilter = filter;
    for (const [f, d] of [[55, 0], [82.4, 4], [110, -6]]) {
      const o = c.createOscillator(); o.type = 'triangle'; o.frequency.value = f; o.detune.value = d;
      const g = c.createGain(); g.gain.value = 0.18;
      o.connect(g); g.connect(filter); o.start();
    }
    const noise = c.createBufferSource();
    const buf = c.createBuffer(1, c.sampleRate * 2, c.sampleRate);
    const data = buf.getChannelData(0);
    for (let i = 0; i < data.length; i++) data[i] = Math.random() * 2 - 1;
    noise.buffer = buf; noise.loop = true;
    const nf = c.createBiquadFilter(); nf.type = 'bandpass'; nf.frequency.value = 700; nf.Q.value = 0.7;
    const ng = c.createGain(); ng.gain.value = 0.05;
    noise.connect(nf); nf.connect(ng); ng.connect(out); noise.start();
    const lfo = c.createOscillator(); lfo.frequency.value = 0.1;
    const lg = c.createGain(); lg.gain.value = 250;
    lfo.connect(lg); lg.connect(filter.frequency); lfo.start();
    this.ambLfo = lfo;
  }

  tone(type, freq, start, dur, vol, slide) {
    const c = this.ctx, o = c.createOscillator(), g = c.createGain();
    o.type = type; o.frequency.setValueAtTime(freq, start);
    if (slide) o.frequency.exponentialRampToValueAtTime(slide, start + dur);
    g.gain.setValueAtTime(0.0001, start);
    g.gain.exponentialRampToValueAtTime(vol, start + 0.005);
    g.gain.exponentialRampToValueAtTime(0.0001, start + dur);
    o.connect(g); g.connect(this.master);
    o.start(start); o.stop(start + dur + 0.02);
  }

  // Pickaxe hitting crystal: metallic partials + a noise tick.
  clink() {
    if (!this.enabled) return;
    const t = this.ctx.currentTime, base = 1500 + Math.random() * 500;
    this.tone('sine', base, t, 0.18, 0.25);
    this.tone('sine', base * 1.51, t, 0.12, 0.12);
    this.tone('square', 90, t, 0.04, 0.08, 40);
  }

  // Block submitted, awaiting confirmation: a questioning two-note chime.
  pending() {
    if (!this.enabled) return;
    const t = this.ctx.currentTime;
    this.tone('triangle', 660, t, 0.25, 0.2);
    this.tone('triangle', 880, t + 0.22, 0.4, 0.2);
  }

  // Confirmed block: 16-bit fanfare.
  fanfare() {
    if (!this.enabled) return;
    const t = this.ctx.currentTime;
    const notes = [523.25, 659.25, 783.99, 1046.5, 783.99, 1046.5, 1318.5, 1567.98];
    notes.forEach((f, i) => {
      this.tone('square', f, t + i * 0.11, 0.22, 0.12);
      this.tone('triangle', f / 2, t + i * 0.11, 0.22, 0.1);
    });
    this.tone('square', 2093, t + notes.length * 0.11, 0.9, 0.12);
  }

  creature(tier) {
    if (!this.enabled) return;
    const t = this.ctx.currentTime;
    this.tone('square', 220 + tier * 60, t, 0.12, 0.1, 440 + tier * 120);
  }
}
