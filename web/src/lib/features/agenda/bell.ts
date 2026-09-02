const MUTE_KEY = 'varyaone:agenda-muted';

export function isMuted(): boolean {
  try {
    return localStorage.getItem(MUTE_KEY) === '1';
  } catch {
    return false;
  }
}

export function setMuted(muted: boolean): void {
  try {
    if (muted) localStorage.setItem(MUTE_KEY, '1');
    else localStorage.removeItem(MUTE_KEY);
  } catch {
    /* storage unavailable — mute state is best-effort */
  }
}

type AudioCtor = typeof AudioContext;

/**
 * Rings a short synthesised bell — a few decaying sine partials — via the Web
 * Audio API. No audio asset needed. Silently no-ops when muted or when the
 * browser blocks audio (e.g. no prior user gesture).
 */
export async function playBell(): Promise<void> {
  if (isMuted()) return;
  const Ctor: AudioCtor | undefined =
    window.AudioContext ??
    (window as unknown as { webkitAudioContext?: AudioCtor }).webkitAudioContext;
  if (!Ctor) return;

  try {
    const ctx = new Ctor();
    if (ctx.state === 'suspended') await ctx.resume();

    const now = ctx.currentTime;
    const master = ctx.createGain();
    master.gain.value = 0.0001;
    master.connect(ctx.destination);
    master.gain.setValueAtTime(0.0001, now);
    master.gain.exponentialRampToValueAtTime(0.5, now + 0.01);
    master.gain.exponentialRampToValueAtTime(0.0001, now + 1.6);

    for (const [freq, gain] of [
      [880, 1],
      [1320, 0.55],
      [2640, 0.25]
    ] as const) {
      const osc = ctx.createOscillator();
      const g = ctx.createGain();
      osc.type = 'sine';
      osc.frequency.value = freq;
      g.gain.value = gain;
      osc.connect(g);
      g.connect(master);
      osc.start(now);
      osc.stop(now + 1.7);
    }

    window.setTimeout(() => ctx.close().catch(() => {}), 2000);
  } catch {
    /* audio blocked — reminder toast still shows */
  }
}
