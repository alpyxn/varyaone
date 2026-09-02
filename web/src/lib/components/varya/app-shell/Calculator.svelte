<script lang="ts">
  import { onMount } from 'svelte';
  import { Calculator as CalculatorIcon, X, GripHorizontal } from '@lucide/svelte';
  import { Button } from '$lib/components/ui/button';

  const PANEL_W = 264;
  const PANEL_H = 372;

  let open = $state(false);
  let isDesktop = $state(true);
  let expr = $state('');
  let result = $state('');
  let justEvaluated = $state(false);

  let pos = $state<{ x: number; y: number } | null>(null);
  let drag: { dx: number; dy: number } | null = null;

  const OPS: Record<string, { p: number; fn: (a: number, b: number) => number }> = {
    '+': { p: 1, fn: (a, b) => a + b },
    '-': { p: 1, fn: (a, b) => a - b },
    '×': { p: 2, fn: (a, b) => a * b },
    '÷': { p: 2, fn: (a, b) => a / b }
  };

  function fmt(n: number): string {
    const r = Math.round((n + Number.EPSILON) * 1e10) / 1e10;
    return String(r);
  }

  type Computed = { ok: true; value: string } | { ok: false; error: string; hard: boolean };

  function compute(input: string): Computed {
    const s = input.replace(/[+\-×÷%]+$/, '');
    const tokens = s.match(/(\d+\.?\d*|\.\d+|[+\-×÷%])/g);
    if (!tokens || !tokens.length) return { ok: false, error: '', hard: false };

    const norm: string[] = [];
    for (const t of tokens) {
      if (t === '%') {
        const prev = norm.pop();
        if (prev === undefined || prev in OPS) return { ok: false, error: 'Hata', hard: true };
        norm.push(fmt(Number(prev) / 100));
      } else {
        norm.push(t);
      }
    }

    const output: number[] = [];
    const ops: string[] = [];
    const apply = () => {
      const op = ops.pop()!;
      const b = output.pop();
      const a = output.pop();
      if (a === undefined || b === undefined) throw new Error('bad');
      output.push(OPS[op].fn(a, b));
    };

    try {
      for (let i = 0; i < norm.length; i++) {
        const t = norm[i];
        if (t in OPS) {
          if ((i === 0 || norm[i - 1] in OPS) && t === '-') {
            norm[i + 1] = fmt(-Number(norm[i + 1]));
            continue;
          }
          while (ops.length && OPS[ops[ops.length - 1]].p >= OPS[t].p) apply();
          ops.push(t);
        } else {
          output.push(Number(t));
        }
      }
      while (ops.length) apply();
    } catch {
      return { ok: false, error: '', hard: false };
    }

    const value = output[0];
    if (value === undefined || Number.isNaN(value)) return { ok: false, error: '', hard: false };
    if (!Number.isFinite(value)) return { ok: false, error: 'Hata', hard: true };
    return { ok: true, value: fmt(value) };
  }

  function preview(input: string): string {
    const r = compute(input);
    if (r.ok) return r.value;
    return r.hard ? r.error : '';
  }

  function currentNumber(s: string): string {
    const m = s.match(/(\d*\.?\d*)$/);
    return m ? m[1] : '';
  }

  function press(key: string) {
    if (key === 'C') {
      expr = '';
      result = '';
      justEvaluated = false;
      return;
    }
    if (key === '⌫') {
      justEvaluated = false;
      expr = expr.slice(0, -1);
      result = preview(expr);
      return;
    }
    if (key === '=') {
      const r = compute(expr);
      if (r.ok) {
        expr = r.value;
        result = '';
        justEvaluated = true;
      } else if (r.hard) {
        result = r.error;
      }
      return;
    }

    const isDigit = /[0-9]/.test(key);
    const isDot = key === '.';
    const isOp = key in OPS;
    const isPercent = key === '%';

    if (justEvaluated) {
      if (isDigit || isDot) expr = '';
      justEvaluated = false;
    }

    const last = expr.slice(-1);

    if (isOp) {
      if (expr === '') {
        if (key !== '-') return;
      } else if (last in OPS) {
        const allowNegative = key === '-' && (last === '×' || last === '÷');
        if (!allowNegative) expr = expr.slice(0, -1);
      }
      expr += key;
    } else if (isPercent) {
      if (expr === '' || last in OPS || last === '%') return;
      expr += '%';
    } else if (isDot) {
      if (currentNumber(expr).includes('.')) return;
      expr += currentNumber(expr) === '' ? '0.' : '.';
    } else if (isDigit) {
      expr += key;
    } else {
      return;
    }

    result = preview(expr);
  }

  function onKeydown(event: KeyboardEvent) {
    if (!open || !isDesktop) return;
    if (event.key === 'Escape') {
      open = false;
      return;
    }
    const target = event.target as HTMLElement | null;
    if (
      target &&
      (target.isContentEditable || ['INPUT', 'TEXTAREA', 'SELECT'].includes(target.tagName))
    ) {
      return;
    }
    const map: Record<string, string> = {
      '*': '×',
      x: '×',
      X: '×',
      '/': '÷',
      Enter: '=',
      '=': '=',
      Backspace: '⌫',
      Delete: 'C'
    };
    if (/^[0-9.+\-%]$/.test(event.key)) {
      event.preventDefault();
      press(event.key);
    } else if (map[event.key]) {
      event.preventDefault();
      press(map[event.key]);
    }
  }

  function clamp(x: number, y: number) {
    return {
      x: Math.min(Math.max(8, x), window.innerWidth - PANEL_W - 8),
      y: Math.min(Math.max(8, y), window.innerHeight - PANEL_H - 8)
    };
  }

  function startDrag(event: PointerEvent) {
    if (!pos) return;
    drag = { dx: event.clientX - pos.x, dy: event.clientY - pos.y };
    (event.target as HTMLElement).setPointerCapture(event.pointerId);
  }
  function onDrag(event: PointerEvent) {
    if (!drag) return;
    pos = clamp(event.clientX - drag.dx, event.clientY - drag.dy);
  }
  function endDrag(event: PointerEvent) {
    drag = null;
    try {
      (event.target as HTMLElement).releasePointerCapture(event.pointerId);
    } catch {
      /* noop */
    }
  }

  function toggle() {
    open = !open;
    if (open) {
      pos = clamp((window.innerWidth - PANEL_W) / 2, (window.innerHeight - PANEL_H) / 2 - 40);
    }
  }

  onMount(() => {
    const mq = window.matchMedia('(min-width: 981px)');
    const update = () => {
      isDesktop = mq.matches;
      if (!isDesktop) open = false;
    };
    update();
    mq.addEventListener('change', update);
    return () => mq.removeEventListener('change', update);
  });

  const keys = [
    ['C', '⌫', '%', '÷'],
    ['7', '8', '9', '×'],
    ['4', '5', '6', '-'],
    ['1', '2', '3', '+'],
    ['0', '.', '=']
  ];
</script>

<svelte:window onkeydown={onKeydown} />

{#if isDesktop}
  <Button
    variant="ghost"
    size="icon"
    class="calc-toggle"
    aria-label="Hesap makinesi"
    aria-pressed={open}
    title="Hesap makinesi"
    onclick={toggle}><CalculatorIcon size={17} /></Button
  >
{/if}

{#if open && pos && isDesktop}
  <div
    class="calc-panel"
    style="left:{pos.x}px; top:{pos.y}px;"
    role="dialog"
    aria-label="Hesap makinesi"
  >
    <div
      class="calc-head"
      role="toolbar"
      tabindex="-1"
      aria-label="Pencereyi taşı"
      onpointerdown={startDrag}
      onpointermove={onDrag}
      onpointerup={endDrag}
    >
      <GripHorizontal size={15} aria-hidden="true" />
      <span>Hesap makinesi</span>
      <button type="button" class="calc-close" aria-label="Kapat" onclick={() => (open = false)}>
        <X size={14} />
      </button>
    </div>
    <div class="calc-display">
      <div class="calc-expr">{expr || '0'}</div>
      <div class="calc-result">{result && result !== expr ? result : ''}</div>
    </div>
    <div class="calc-keys">
      {#each keys as row}
        {#each row as k}
          <button
            type="button"
            class="calc-key"
            class:is-op={k in OPS || k === '%'}
            class:is-clear={k === 'C' || k === '⌫'}
            class:is-eq={k === '='}
            class:is-wide={k === '0'}
            onclick={() => press(k)}>{k}</button
          >
        {/each}
      {/each}
    </div>
  </div>
{/if}

<style>
  .calc-panel {
    position: fixed;
    z-index: 2147483000;
    width: 264px;
    display: flex;
    flex-direction: column;
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-panel);
    background: var(--surface);
    box-shadow: 0 24px 70px rgb(2 6 23 / 32%);
    overflow: hidden;
    user-select: none;
  }
  .calc-head {
    display: flex;
    align-items: center;
    gap: 7px;
    padding: 7px 8px 7px 10px;
    background: var(--surface-muted);
    border-bottom: 1px solid var(--border);
    color: var(--text-muted);
    font-size: 11px;
    font-weight: 650;
    cursor: grab;
    touch-action: none;
  }
  .calc-head:active {
    cursor: grabbing;
  }
  .calc-head span {
    flex: 1;
  }
  .calc-close {
    display: inline-grid;
    place-items: center;
    width: 22px;
    height: 22px;
    border: 0;
    border-radius: 5px;
    background: transparent;
    color: var(--text-muted);
    cursor: pointer;
  }
  .calc-close:hover {
    background: var(--surface);
    color: var(--text);
  }
  .calc-display {
    padding: 12px 12px 8px;
    text-align: right;
  }
  .calc-expr {
    min-height: 24px;
    color: var(--text);
    font-size: 20px;
    font-weight: 650;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .calc-result {
    min-height: 16px;
    color: var(--text-muted);
    font-size: 12px;
  }
  .calc-keys {
    display: grid;
    grid-template-columns: repeat(4, 1fr);
    gap: 6px;
    padding: 8px 10px 12px;
  }
  .calc-key {
    height: 42px;
    border: 1px solid var(--border);
    border-radius: 8px;
    background: var(--surface);
    color: var(--text);
    font-size: 15px;
    font-weight: 600;
    cursor: pointer;
  }
  .calc-key:hover {
    background: var(--surface-muted);
  }
  .calc-key:active {
    transform: translateY(1px);
  }
  .calc-key.is-op {
    color: var(--primary);
  }
  .calc-key.is-clear {
    color: var(--danger);
  }
  .calc-key.is-eq {
    background: var(--primary);
    border-color: var(--primary);
    color: var(--primary-foreground, #fff);
  }
  .calc-key.is-wide {
    grid-column: span 2;
  }
</style>
