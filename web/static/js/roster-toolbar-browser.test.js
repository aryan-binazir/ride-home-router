'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { execFileSync } = require('node:child_process');
const { pathToFileURL } = require('node:url');

const browser = process.env.BROWSER_TEST_BINARY;
const root = path.resolve(__dirname, '../../..');
const near = (actual, expected, message) => assert.ok(Math.abs(actual - expected) <= 1, `${message}: ${actual} vs ${expected}`);

function fixture(kind) {
    const singular = kind === 'drivers' ? 'driver' : 'participant';
    const template = fs.readFileSync(path.join(root, 'web/templates', `${kind}.html`), 'utf8');
    const rows = [1, 2, 3].map(id => `<tr data-search="person ${id}"${id === 3 ? ' class="hidden"' : ''}><td><input type="checkbox" data-bulk-row name="${singular}_ids" value="${id}"></td><td>Person ${id}</td></tr>`).join('');
    const section = template.match(/<section\b[\s\S]*?<\/section>/)?.[0];
    assert.ok(section, `${kind} roster section exists`);
    assert.ok(section.includes(`{{template "${singular}_list" .}}`));
    const markup = section.replace(`{{template "${singular}_list" .}}`, `<table><tbody id="${kind}-tbody">${rows}</tbody></table>`).replace(/{{.*?}}/gs, '');
    return `<!doctype html><html><head><meta name="viewport" content="width=device-width, initial-scale=1"><style>${fs.readFileSync(path.join(root, 'web/static/css/style.css'), 'utf8')}</style></head>
<body class="standard-page"><div class="app-shell"><main class="app-main"><div class="main">${markup}</div></main></div>
<script>window.browserErrors=[];window.addEventListener('error',e=>browserErrors.push(e.message));</script>
<script>${fs.readFileSync(path.join(__dirname, 'ui.js'), 'utf8')}</script>
<script>${fs.readFileSync(path.join(__dirname, 'roster.js'), 'utf8')}</script>
<script>
window.addEventListener('load', () => {
 const toolbar=document.querySelector('.roster-toolbar .bulk-toolbar');
 const count=toolbar.querySelector('.panel-count');
 const buttons=[...toolbar.querySelectorAll('button')];
 const rect=el=>{const r=el.getBoundingClientRect();return {x:r.x,y:r.y,width:r.width,height:r.height,right:r.right,bottom:r.bottom};};
 const measure=()=>({count:rect(count),toolbar:rect(toolbar),display:getComputedStyle(toolbar).display,
  buttons:buttons.map(button=>{
   const range=document.createRange();range.selectNodeContents(button);
   const textRects=[...range.getClientRects()].filter(r=>r.width>0&&r.height>0).map(r=>({x:r.x,y:r.y,right:r.right,bottom:r.bottom}));
   return {...rect(button),text:button.textContent.trim(),textRects,clientWidth:button.clientWidth,scrollWidth:button.scrollWidth};
  }),pageWidth:document.documentElement.scrollWidth,bodyWidth:document.body.scrollWidth});
 const selected=()=>[...document.querySelectorAll('input[data-bulk-row]:checked')].map(input=>input.value);
 const out={viewport:innerWidth,initial:count.textContent.trim(),initialLayout:measure()};
 buttons[0].click();out.selectedCount=count.textContent.trim();out.selected=selected();out.selectedLayout=measure();
 buttons[1].click();out.clearedCount=count.textContent.trim();out.cleared=selected();out.clearedLayout=measure();
 out.errors=browserErrors;window.evidence=out;
});
</script></body></html>`;
}

for (const kind of ['drivers', 'participants']) {
    for (const width of [320, 390, 460, 820, 1280]) {
        test(`${kind} roster toolbar at ${width}px fits and selects only visible rows`, { skip: !browser }, () => {
            const directory = fs.mkdtempSync(path.join(os.tmpdir(), 'rhr-roster-toolbar-'));
            try {
                fs.writeFileSync(path.join(directory, 'roster.html'), fixture(kind));
                fs.writeFileSync(path.join(directory, 'fixture.html'), `<!doctype html><html><body style="margin:0"><iframe src="roster.html" style="display:block;border:0;width:${width}px;height:1000px" onload="document.getElementById('evidence').textContent=JSON.stringify(this.contentWindow.evidence)"></iframe><pre id="evidence" hidden>pending</pre></body></html>`);
                const screenshots = path.join(root, '_scratch/roster-toolbar-browser');
                fs.mkdirSync(screenshots, { recursive: true });
                const output = execFileSync(browser, ['--headless', '--no-sandbox', '--disable-gpu', '--no-first-run', '--allow-file-access-from-files', '--force-device-scale-factor=1', `--window-size=${Math.max(width, 500)},1100`, `--user-data-dir=${directory}/profile`, `--screenshot=${path.join(screenshots, `${kind}-${width}.png`)}`, '--dump-dom', '--virtual-time-budget=1000', pathToFileURL(path.join(directory, 'fixture.html')).href], { encoding: 'utf8', timeout: 60000, stdio: ['ignore', 'pipe', 'pipe'], maxBuffer: 2 * 1024 * 1024 });
                const result = JSON.parse(output.match(/<pre id="evidence" hidden="">(.*?)<\/pre>/s)?.[1] || 'null');
                assert.ok(result, 'browser produced measurement evidence');
                assert.deepEqual(result.errors, [], 'real scripts run without browser errors');
                assert.equal(result.viewport, width);
                assert.equal(result.initial, '0 selected');
                assert.equal(result.selectedCount, '2 selected');
                assert.deepEqual(result.selected, ['1', '2'], 'hidden row stays unselected');
                assert.equal(result.clearedCount, '0 selected');
                assert.deepEqual(result.cleared, []);
                for (const state of ['initialLayout', 'selectedLayout', 'clearedLayout']) {
                    const layout = result[state];
                    const buttons = layout.buttons;
                    assert.deepEqual(buttons.map(button => button.text), ['Select visible', 'Clear selection', 'Add label', 'Remove label']);
                    assert.ok(layout.pageWidth <= width && layout.bodyWidth <= width, `${state}: no horizontal page overflow`);
                    for (const button of buttons) {
                        assert.ok(button.x >= 0 && button.right <= width, `${button.text} stays in viewport`);
                        assert.ok(button.x >= layout.toolbar.x - 1 && button.right <= layout.toolbar.right + 1, `${button.text} stays in toolbar`);
                        assert.ok(button.scrollWidth <= button.clientWidth, `${button.text} does not overflow`);
                        assert.equal(button.textRects.length, 1, `${button.text} stays on one line`);
                        const text = button.textRects[0];
                        assert.ok(text.x >= button.x && text.right <= button.right && text.y >= button.y && text.bottom <= button.bottom, `${button.text} fits inside button`);
                    }
                    if (width <= 820) {
                        assert.equal(layout.display, 'grid');
                        near(layout.count.x, layout.toolbar.x, 'count starts at toolbar edge');
                        near(layout.count.width, layout.toolbar.width, 'count spans both columns');
                        assert.ok(layout.count.bottom <= Math.min(...buttons.map(button => button.y)), 'selected count is alone above all buttons');
                        near(buttons[0].y, buttons[1].y, 'first row');
                        near(buttons[2].y, buttons[3].y, 'second row');
                        near(buttons[0].x, buttons[2].x, 'first column');
                        near(buttons[1].x, buttons[3].x, 'second column');
                        assert.ok(buttons[0].right <= buttons[1].x && buttons[2].right <= buttons[3].x, 'columns do not overlap');
                        assert.ok(Math.max(buttons[0].bottom, buttons[1].bottom) <= Math.min(buttons[2].y, buttons[3].y), 'rows do not overlap');
                        for (const button of buttons) {
                            near(button.width, buttons[0].width, 'equal button widths');
                            near(button.height, buttons[0].height, 'equal button heights');
                            assert.ok(button.width >= 44 && button.height >= 44, `${button.text} has a 44px touch target`);
                        }
                    } else {
                        assert.equal(layout.display, 'flex');
                        const items = [layout.count, ...buttons];
                        for (let i = 1; i < items.length; i++) {
                            near(items[i].y + items[i].height / 2, items[0].y + items[0].height / 2, 'desktop controls share one row');
                            assert.ok(items[i].x >= items[i - 1].right, 'desktop controls remain in order without overlap');
                        }
                    }
                }
            } finally {
                fs.rmSync(directory, { recursive: true, force: true });
            }
        });
    }
}
