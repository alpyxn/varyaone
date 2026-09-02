import { describe, it, expect, afterEach } from 'vitest';
import { printDocument } from './print';

function capturePrintHtml(logo: string): string {
  const opened: string[] = [];
  window.open = (() => ({
    document: { open() {}, close() {}, write: (html: string) => opened.push(html) },
    focus() {},
    setTimeout() {},
    print() {}
  })) as unknown as typeof window.open;
  printDocument({
    title: 'Test',
    company: { name: 'Firma', logo },
    bodyHtml: '<p>gövde</p>'
  });
  return opened[0] ?? '';
}

describe('printDocument logo handling', () => {
  const originalOpen = window.open;
  afterEach(() => {
    window.open = originalOpen;
  });

  it('renders a well-formed base64 image data URI', () => {
    const html = capturePrintHtml('data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAAB');
    expect(html).toContain(
      '<img src="data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAAB" alt="" />'
    );
  });

  it('drops a logo value that tries to break out of the src attribute', () => {
    const html = capturePrintHtml('data:image/png"><script>alert(1)</script>');
    expect(html).not.toContain('<script>alert(1)</script>');
    expect(html).not.toContain('<img');
  });

  it('drops a non-data-URI logo value', () => {
    const html = capturePrintHtml('https://evil.example/x.png');
    expect(html).not.toContain('<img');
  });
});
