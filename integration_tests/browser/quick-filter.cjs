// Run the real quick-filter handler against server-rendered form markup. The
// caller sends the resulting GET query through the authenticated HTTP router.
const fs = require('node:fs');
const { JSDOM } = require('jsdom');
const { html, script, handler } = JSON.parse(fs.readFileSync(0, 'utf8'));
const dom = new JSDOM(html, { runScripts: 'outside-only', url: 'http://localhost/web/submissions' });
const { window } = dom;
window.eval(script);
let query;
window.HTMLFormElement.prototype.submit = function () {
    if (query !== undefined) throw new Error('Quick filter submitted twice');
    if (this.id !== 'filter-form-advanced') throw new Error('Wrong form submitted');
    query = new URLSearchParams(new window.FormData(this)).toString();
};
if (handler) {
    const button = [...window.document.querySelectorAll('button')]
        .find(button => button.getAttribute('onclick') === `${handler}()`);
    if (!button) throw new Error(`Missing button for ${handler}`);
    button.addEventListener('click', () => window.eval(button.getAttribute('onclick')));
    button.click();
} else {
    window.submitAdvancedFilterForm();
}
if (query === undefined) throw new Error('Quick filter did not submit');
process.stdout.write(query);
window.close();
