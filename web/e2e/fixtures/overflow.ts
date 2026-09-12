/**
 * Page-level horizontal overflow detection.
 *
 * The function below is serialised into the browser by `page.evaluate`, so it
 * must stay self-contained: no imports, no closure variables, no TypeScript
 * that needs the module's types at runtime. `overflow-detector.spec.ts` runs it
 * against small synthetic documents, which is the only way to prove it tells a
 * real page overflow apart from an allowed local scroller.
 */

export type OverflowOffender = {
  selector: string;
  left: number;
  right: number;
};

export type OverflowReport = {
  viewportWidth: number;
  documentScrollWidth: number;
  /** Elements that push past the right edge with nothing to contain them. */
  offenders: OverflowOffender[];
  /** Elements whose content starts left of the viewport and is unreachable. */
  clippedLeft: OverflowOffender[];
};

/**
 * Reports elements that make the *page* scroll sideways.
 *
 * An element is only an offender when nothing between it and the document
 * contains it. Three things contain it, and each one is a deliberate layout
 * decision rather than a bug:
 *
 * - an ancestor that actually scrolls horizontally (`auto`/`scroll` with real
 *   overflow) — a wide table in its own scroller;
 * - an ancestor that clips (`hidden`/`clip`) — the content never reaches the
 *   document, so the page cannot grow to fit it;
 * - an ancestor positioned out of flow off-screen (the closed mobile drawer
 *   parked at a negative left), which the browser does not count either.
 */
export function detectOverflow(): OverflowReport {
  const doc = document.documentElement;
  const vw = doc.clientWidth;
  const offenders: { selector: string; left: number; right: number }[] = [];
  const clippedLeft: { selector: string; left: number; right: number }[] = [];

  const describe = (el: Element) => {
    const id = el.id ? `#${el.id}` : '';
    const raw = typeof el.className === 'string' ? el.className.trim() : '';
    const cls = raw ? `.${raw.split(/\s+/).slice(0, 3).join('.')}` : '';
    return `${el.tagName.toLowerCase()}${id}${cls}`;
  };

  // An ancestor contains the overflow when it scrolls it, clips it away, or
  // has taken it out of the page entirely.
  const contained = (el: HTMLElement, edge: 'left' | 'right') => {
    let node: HTMLElement | null = el;
    while (node && node !== doc) {
      if (node !== el) {
        const style = getComputedStyle(node);
        const overflowX = style.overflowX;
        if (overflowX === 'hidden' || overflowX === 'clip') return true;
        if (overflowX === 'auto' || overflowX === 'scroll') {
          // Only a scroller with somewhere to scroll counts. An `overflow-x:
          // auto` box the content already fits inside contains nothing.
          if (node.scrollWidth > node.clientWidth + 1) return true;
        }
        // A fixed/absolute ancestor that itself ends inside the viewport does
        // not extend the document, whatever its children's boxes say.
        if (
          edge === 'right' &&
          (style.position === 'fixed' || style.position === 'absolute') &&
          node.getBoundingClientRect().right <= vw + 1
        ) {
          return true;
        }
      }
      // Deliberately removed from the page: the closed mobile drawer is parked
      // off-screen left and marked inert. Nobody can reach it, so neither its
      // width nor its position is a defect.
      if (node.hasAttribute('inert') || node.getAttribute('aria-hidden') === 'true') return true;
      node = node.parentElement;
    }
    return false;
  };

  for (const el of Array.from(document.querySelectorAll<HTMLElement>('body *'))) {
    const rect = el.getBoundingClientRect();
    if (rect.width === 0 && rect.height === 0) continue;
    if (getComputedStyle(el).visibility === 'hidden') continue;
    const entry = {
      selector: describe(el),
      left: Math.round(rect.left),
      right: Math.round(rect.right)
    };
    if (rect.right > vw + 1 && !contained(el, 'right')) offenders.push(entry);
    // Content that starts left of the viewport cannot be scrolled back to in
    // an LTR document: it is simply gone. That is an accessibility failure
    // even though it never makes the page scroll.
    if (rect.left < -1 && rect.right > 0 && !contained(el, 'left')) clippedLeft.push(entry);
  }

  return {
    viewportWidth: vw,
    documentScrollWidth: doc.scrollWidth,
    offenders: offenders.slice(0, 12),
    clippedLeft: clippedLeft.slice(0, 12)
  };
}
