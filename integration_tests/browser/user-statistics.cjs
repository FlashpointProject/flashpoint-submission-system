const fs = require('node:fs');
const assert = require('node:assert/strict');
const { JSDOM } = require('jsdom');
const { html, script, styles } = JSON.parse(fs.readFileSync(0, 'utf8'));
const dom = new JSDOM(html, { runScripts: 'outside-only', url: 'http://localhost/web/user-statistics' });
const { window } = dom;
window.console.error = () => {};
const style = window.document.createElement('style');
// Exercise the button display rule from the UI framework as well as local CSS.
style.textContent = '.pure-button { display: inline-block; }\n' + styles;
window.document.head.appendChild(style);
window.eval(script);
const fields = ['UserID', 'Username', 'Role', 'LastUserActivity', 'UserCommentedCount',
    'UserRequestedChangesCount', 'UserApprovedCount', 'UserVerifiedCount', 'UserAddedToFlashpointCount',
    'UserRejectedCount', 'SubmissionsCount', 'SubmissionsBotHappyCount', 'SubmissionsBotUnhappyCount',
    'SubmissionsRequestedChangesCount', 'SubmissionsApprovedCount', 'SubmissionsVerifiedCount',
    'SubmissionsAddedToFlashpointCount', 'SubmissionsRejectedCount'];
const first = Object.fromEntries(fields.map((field, i) => [field, i]));
Object.assign(first, { UserID: '9007199254741001', Username: '<img src=x onerror=alert(1)>', Role: 'Staff', LastUserActivity: '2024-01-02T03:04:05Z' });
const second = { ...first, UserID: '9007199254741003', Username: 'other', UserCommentedCount: 100, LastUserActivity: '0001-01-01T00:00:00Z' };
const data = { users: [second, first] };
let requests = 0;
let respond;
window.fetch = (url, options) => {
    requests++;
    assert.equal(url, '/api/user-statistics/all');
    assert.equal(options.headers.Accept, 'application/json');
    return new Promise(resolve => { respond = () => resolve({ ok: true, json: async () => data }); });
};
const tick = () => new Promise(resolve => setImmediate(resolve));
(async () => {
    for (const inline of window.document.querySelectorAll('script')) window.eval(inline.textContent);
    assert.equal(requests, 1, 'initial page must make exactly one statistics request');
    const table = window.document.getElementById('users-table');
    const status = window.document.getElementById('user-statistics-status');
    const retry = window.document.getElementById('user-statistics-retry');
    assert.equal(table.getAttribute('aria-busy'), 'true');
    await window.populateUserStatisticsTable();
    assert.equal(requests, 1, 'duplicate clicks while loading must not create requests');
    respond(); await tick();
    assert.equal(table.rows.length, 2);
    assert.equal(table.getAttribute('aria-busy'), 'false');
    assert.equal(retry.hidden, true);
    assert.equal(window.getComputedStyle(retry).display, 'none');
    const row = table.rows[1];
    assert.equal(row.cells.length, 18);
    for (let i = 0; i < fields.length; i++) assert.equal(row.cells[i].textContent, String(first[fields[i]]));
    assert.equal(table.querySelector('img'), null, 'names must render as text');
    assert.equal(table.rows[0].cells[3].textContent, '', 'no activity is blank');
    assert.equal(row.cells[5].className, 'bgr-request-changes');
    assert.equal(row.cells[16].className, 'bgr-mark-added');
    // Existing sort headers still work after inserting the complete fragment.
    const countHeader = window.document.querySelectorAll('th')[4];
    countHeader.click();
    assert.equal(table.rows[0].cells[4].textContent, '4');
    countHeader.click();
    assert.equal(table.rows[0].cells[4].textContent, '100');
    for (const failure of [
        async () => ({ ok: false, status: 500 }),
        async () => { throw new Error('network'); },
        async () => ({ ok: true, json: async () => { throw new SyntaxError('bad JSON'); } }),
        async () => ({ ok: true, json: async () => ({ users: 'invalid' }) }),
        async () => ({ ok: true, json: async () => ({ users: [first, null] }) }),
    ]) {
        window.fetch = failure;
        await window.populateUserStatisticsTable();
        assert.equal(retry.hidden, false);
        assert.notEqual(window.getComputedStyle(retry).display, 'none');
        assert.match(status.textContent, /Unable to load/);
        assert.equal(table.rows.length, 2, 'failed refresh must not partially replace the table');
        window.fetch = async () => ({ ok: true, json: async () => data });
        await window.eval(retry.getAttribute('onclick'));
        assert.equal(table.rows.length, 2, 'retry replaces rather than duplicates');
        assert.equal(retry.hidden, true);
    }
    // Trigger the timeout without waiting 35 seconds.
    const originalTimeout = window.setTimeout;
    let expire;
    window.setTimeout = (callback, delay) => { assert.equal(delay, 35000); expire = callback; return 0; };
    window.fetch = (_, { signal }) => new Promise((_, reject) => signal.addEventListener('abort', () => reject(new Error('aborted'))));
    const timed = window.populateUserStatisticsTable();
    expire(); await timed;
    assert.equal(retry.hidden, false);
    assert.equal(table.getAttribute('aria-busy'), 'false');
    window.setTimeout = originalTimeout;
    window.fetch = async () => ({ ok: true, json: async () => ({ users: [] }) });
    await window.populateUserStatisticsTable();
    assert.equal(table.rows.length, 0);
    assert.equal(status.textContent, 'No users found.');
    window.close();
})().catch(error => { console.error(error); window.close(); process.exitCode = 1; });
