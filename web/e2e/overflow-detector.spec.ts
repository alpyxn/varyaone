import { test, expect } from './fixtures/test';
import { detectOverflow } from './fixtures/overflow';
import { overflowComplaints } from './fixtures/test';

/**
 * The overflow detector, against documents small enough to reason about.
 *
 * Every responsive assertion in this suite rests on this one function, and
 * nothing else here can tell a correct verdict from a lucky one. These pages
 * are the cases that decide whether a report is a defect or a false alarm.
 */
const VIEWPORT = { width: 400, height: 600 };

const FRAME = `<style>
  html, body { margin: 0; padding: 0; }
</style>`;

async function verdict(page: import('@playwright/test').Page, body: string) {
  await page.setViewportSize(VIEWPORT);
  await page.setContent(`${FRAME}<body>${body}</body>`);
  return overflowComplaints(await page.evaluate(detectOverflow));
}

test.describe('overflow detector', () => {
  test('a page that fits reports nothing @quick', async ({ page }) => {
    expect(await verdict(page, '<div style="width:100%;height:50px"></div>')).toEqual([]);
  });

  test('a wide block with nothing to contain it is a defect @quick', async ({ page }) => {
    const complaints = await verdict(page, '<div id="wide" style="width:900px;height:50px"></div>');
    expect(complaints.join('\n')).toContain('#wide');
    expect(complaints.join('\n')).toContain('the document itself scrolls');
  });

  test('a wide table inside its own scroller is allowed', async ({ page }) => {
    expect(
      await verdict(
        page,
        `<div style="overflow-x:auto;width:100%">
           <table style="width:900px"><tr><td style="width:900px">x</td></tr></table>
         </div>`
      )
    ).toEqual([]);
  });

  test('an `overflow-x: auto` box the content fits inside contains nothing', async ({ page }) => {
    // The scroller must actually have somewhere to scroll. A box that merely
    // *could* scroll is not an excuse for a child that sticks out of it.
    const complaints = await verdict(
      page,
      `<div style="overflow-x:auto;width:100%;height:60px">
         <div id="escapee" style="width:900px;height:50px;position:fixed;left:0;top:0"></div>
       </div>`
    );
    expect(complaints.join('\n')).toContain('#escapee');
  });

  test('an `overflow: hidden` ancestor clips the page back into shape', async ({ page }) => {
    expect(
      await verdict(
        page,
        `<div style="overflow:hidden;width:100%">
           <div style="width:900px;height:50px"></div>
         </div>`
      )
    ).toEqual([]);
  });

  test('`overflow: clip` counts as containment too', async ({ page }) => {
    expect(
      await verdict(
        page,
        `<div style="overflow-x:clip;width:100%">
           <div style="width:900px;height:50px"></div>
         </div>`
      )
    ).toEqual([]);
  });

  test('a closed drawer parked off-screen is deliberate, not a defect', async ({ page }) => {
    expect(
      await verdict(
        page,
        `<aside inert style="position:fixed;left:-320px;top:0;width:320px;height:100%">
           <a href="#x">a link nobody can reach yet</a>
         </aside>`
      )
    ).toEqual([]);
  });

  test('content cut off past the left edge is reported', async ({ page }) => {
    const complaints = await verdict(
      page,
      '<div id="lost" style="position:absolute;left:-200px;top:0;width:300px;height:40px"></div>'
    );
    expect(complaints.join('\n')).toContain('cut off left');
    expect(complaints.join('\n')).toContain('#lost');
  });

  test('a display:none element is not measured', async ({ page }) => {
    expect(await verdict(page, '<div style="width:900px;height:50px;display:none"></div>')).toEqual(
      []
    );
  });

  test('visibility:hidden still takes space, and still scrolls the page', async ({ page }) => {
    // Not an oversight: `visibility: hidden` keeps the box. Pointing at the
    // element would be unhelpful — there is nothing on screen to look at — but
    // the page really does scroll sideways, so the verdict says so.
    const complaints = await verdict(
      page,
      '<div style="width:900px;height:50px;visibility:hidden"></div>'
    );
    expect(complaints).toEqual(['the document itself scrolls: 900 > 400']);
  });

  test('a hidden element inside a wide row does not stretch the page', async ({ page }) => {
    // The real case this was written for: a visually-hidden label inside a
    // horizontally scrolled table. Absolutely positioned, it resolves against
    // the nearest positioned ancestor — so the scroller must be one.
    expect(
      await verdict(
        page,
        `<div style="overflow-x:auto;width:100%;position:relative">
           <table style="width:900px"><tr><th style="width:900px">
             <span style="position:absolute;width:1px;height:1px;overflow:hidden;clip:rect(0,0,0,0)">
               label
             </span>
           </th></tr></table>
         </div>`
      )
    ).toEqual([]);
  });
});
