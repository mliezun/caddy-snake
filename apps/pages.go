package apps

// The pages: server-rendered HTML, one inline stylesheet, one small inline
// script. No build step, nothing loaded from elsewhere.
//
// The structure is an enterprise console: a global bar, side navigation,
// panels with a title, a count and actions, filterable tables, a details grid
// and tabs. The material is "liquid glass": the bar, the navigation, controls
// and dialogs are translucent and blurred with a bright top edge, floating over
// a soft ambient background.
//
// Rules for anyone editing them:
//   - Glass is for the chrome. Panels that hold text and tables use the
//     near-opaque variant, so contrast never depends on what is behind them.
//   - Keep the fallbacks: solid surfaces when the browser cannot blur or the
//     person asked for reduced transparency.
//   - No accent bars: never mark a selected item, an alert or a notice with a
//     thick coloured stripe on one edge. Selection is a filled capsule or the
//     weight and colour of the text.
//   - Labels, not explanations: a page says what things are and offers the
//     actions. No lead paragraphs, no inline tutorials.

const styles = `
:root {
  color-scheme: light dark;
  --bg: #e9edf3; --glow-a: rgba(64, 132, 255, .30); --glow-b: rgba(120, 96, 255, .20); --glow-c: rgba(0, 190, 200, .16);
  --glass: rgba(255, 255, 255, .58); --glass-strong: rgba(255, 255, 255, .82); --glass-soft: rgba(255, 255, 255, .42);
  --edge: rgba(255, 255, 255, .75); --rim: rgba(15, 27, 42, .09); --line: rgba(15, 27, 42, .10); --line-strong: rgba(15, 27, 42, .17);
  --shine: inset 0 1px 0 rgba(255, 255, 255, .85), inset 0 -1px 0 rgba(255, 255, 255, .18);
  --lift: 0 1px 2px rgba(15, 27, 42, .05), 0 10px 30px rgba(15, 27, 42, .08);
  --float: 0 2px 6px rgba(15, 27, 42, .07), 0 24px 60px rgba(15, 27, 42, .18);
  --text: #0c1420; --muted: #4f5d70; --faint: #77859a;
  --link: #0064d2; --primary: #0071e3; --primary-2: #0a84ff; --on-primary: #ffffff;
  --ok: #12813f; --warn: #8a5d00; --danger: #c92a1e; --danger-soft: rgba(201, 42, 30, .10);
  --hover: rgba(15, 27, 42, .045); --sel: rgba(0, 113, 227, .10); --field: rgba(255, 255, 255, .7);
  --info-bg: rgba(0, 113, 227, .09); --warn-bg: rgba(214, 150, 0, .13);
}
@media (prefers-color-scheme: dark) {
  :root {
    --bg: #070a10; --glow-a: rgba(40, 110, 255, .30); --glow-b: rgba(130, 90, 255, .22); --glow-c: rgba(0, 170, 190, .14);
    --glass: rgba(30, 37, 50, .55); --glass-strong: rgba(22, 28, 39, .80); --glass-soft: rgba(40, 48, 64, .40);
    --edge: rgba(255, 255, 255, .13); --rim: rgba(0, 0, 0, .5); --line: rgba(255, 255, 255, .09); --line-strong: rgba(255, 255, 255, .16);
    --shine: inset 0 1px 0 rgba(255, 255, 255, .16), inset 0 -1px 0 rgba(255, 255, 255, .03);
    --lift: 0 1px 2px rgba(0, 0, 0, .3), 0 12px 34px rgba(0, 0, 0, .35);
    --float: 0 2px 8px rgba(0, 0, 0, .4), 0 30px 70px rgba(0, 0, 0, .6);
    --text: #eef2f7; --muted: #9eabbd; --faint: #76849a;
    --link: #5fb0ff; --primary: #0a84ff; --primary-2: #409cff; --on-primary: #ffffff;
    --ok: #46d08a; --warn: #f0bd4a; --danger: #ff7a6e; --danger-soft: rgba(255, 122, 110, .14);
    --hover: rgba(255, 255, 255, .055); --sel: rgba(10, 132, 255, .20); --field: rgba(255, 255, 255, .06);
    --info-bg: rgba(10, 132, 255, .14); --warn-bg: rgba(240, 189, 74, .13);
  }
}
* { box-sizing: border-box; }
html { -webkit-text-size-adjust: 100%; min-height: 100%; }
body {
  margin: 0; min-height: 100vh; color: var(--text);
  font: 14px/1.45 -apple-system, BlinkMacSystemFont, "SF Pro Text", "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif;
  font-variant-numeric: tabular-nums; -webkit-font-smoothing: antialiased;
  background:
    radial-gradient(60rem 40rem at 8% -10%, var(--glow-a), transparent 60%),
    radial-gradient(50rem 38rem at 105% 10%, var(--glow-b), transparent 62%),
    radial-gradient(46rem 34rem at 55% 115%, var(--glow-c), transparent 60%),
    var(--bg);
  background-attachment: fixed;
}
a { color: var(--link); text-decoration: none; }
a:hover { text-decoration: underline; }
:focus-visible { outline: 2px solid var(--primary-2); outline-offset: 2px; }
svg { display: block; flex: none; }

/* Glass: translucent, blurred, with a bright top edge. Used for the chrome
   (bar, navigation, controls, dialogs). Content panels use the near-opaque
   variant so text stays readable. */
.glass, .top, .side, .box, dialog, #toast, .tabs {
  border: 1px solid var(--edge); outline: 1px solid var(--rim); outline-offset: 0;
  -webkit-backdrop-filter: blur(26px) saturate(180%); backdrop-filter: blur(26px) saturate(180%);
}
.top, .side, .tabs, #toast { background: var(--glass); box-shadow: var(--shine), var(--lift); }
.box, dialog { background: var(--glass-strong); box-shadow: var(--shine), var(--lift); }

/* Shell */
.frame { max-width: 88rem; margin: 0 auto; padding: 12px 14px 3rem; }
.top { position: sticky; top: 12px; z-index: 10; height: 52px; border-radius: 18px; display: flex; align-items: center; padding: 0 .6rem 0 .9rem; gap: 1rem; }
.brand { display: flex; align-items: center; gap: .6rem; font-weight: 650; font-size: 15px; color: var(--text); letter-spacing: -.01em; }
.brand:hover { text-decoration: none; }
.brand small { font-weight: 500; color: var(--muted); font-size: 13px; }
.top .space { flex: 1; }
.top .user { display: flex; align-items: center; gap: .7rem; color: var(--muted); font-size: 13px; }
.top .user strong { color: var(--text); font-weight: 600; }
.flag { display: inline-flex; align-items: center; gap: .4rem; font-size: 12.5px; font-weight: 600; color: var(--warn); padding: .3rem .7rem; border-radius: 999px; background: var(--warn-bg); }
.flag:hover { text-decoration: none; filter: brightness(1.05); }
.shell { display: flex; gap: 14px; align-items: flex-start; margin-top: 14px; }
.side { width: 216px; flex: none; border-radius: 18px; padding: .6rem; position: sticky; top: 78px; }
.side .group { font-size: 11px; font-weight: 650; text-transform: uppercase; letter-spacing: .07em; color: var(--faint); margin: .8rem .7rem .3rem; }
.side .group:first-child { margin-top: .3rem; }
.side a { display: block; padding: .45rem .7rem; color: var(--text); border-radius: 10px; font-weight: 500; transition: background .15s; }
.side a:hover { background: var(--hover); text-decoration: none; }
.side a[aria-current="page"] { background: var(--sel); color: var(--link); font-weight: 650; }
.side .foot { margin: .9rem .7rem .2rem; padding-top: .7rem; border-top: 1px solid var(--line); color: var(--faint); font-size: 12px; }
main { flex: 1; min-width: 0; }

/* Page header */
.trail { font-size: 13px; color: var(--muted); margin: .1rem 0 .5rem .2rem; display: flex; gap: .45rem; align-items: center; }
.trail .sep { color: var(--faint); }
.title { display: flex; justify-content: space-between; align-items: center; gap: 1rem; flex-wrap: wrap; margin: .1rem .2rem 1rem; min-height: 36px; }
h1 { font-size: 24px; line-height: 1.15; margin: 0; font-weight: 680; letter-spacing: -.022em; display: flex; align-items: baseline; gap: .5rem; }
.count { color: var(--muted); font-weight: 400; }
.acts { display: flex; gap: .5rem; align-items: center; flex-wrap: wrap; }

/* Panels */
.box { border-radius: 18px; margin-bottom: 14px; overflow: hidden; }
.box > header { display: flex; justify-content: space-between; align-items: center; gap: 1rem; padding: .7rem .8rem .7rem 1.1rem; border-bottom: 1px solid var(--line); min-height: 54px; flex-wrap: wrap; }
.box > header h2 { font-size: 16px; margin: 0; font-weight: 650; letter-spacing: -.012em; }
.box > .pad { padding: 1.1rem; }
.box > .none { padding: 2rem 1rem; text-align: center; color: var(--muted); }
.box > .none strong { display: block; color: var(--text); margin-bottom: .15rem; font-weight: 600; }
.tools { padding: .65rem .8rem .65rem 1.1rem; border-bottom: 1px solid var(--line); display: flex; gap: .75rem; align-items: center; }
.tools input { max-width: 24rem; }
.tools .matches { color: var(--muted); font-size: 13px; }

/* Key-value grid */
dl { margin: 0; }
.kv { display: grid; grid-template-columns: repeat(4, 1fr); row-gap: 1.1rem; }
.kv > div { padding: 0 1.1rem; border-left: 1px solid var(--line); min-width: 0; }
.kv > div:nth-child(4n+1) { border-left: 0; padding-left: 0; }
.kv dt { font-weight: 600; font-size: 12.5px; color: var(--muted); margin-bottom: .2rem; }
.kv dd { margin: 0; overflow-wrap: anywhere; }

/* Tables */
table { width: 100%; border-collapse: collapse; }
th { text-align: left; font-size: 12.5px; color: var(--muted); font-weight: 600; padding: .55rem 1.1rem; border-bottom: 1px solid var(--line); white-space: nowrap; }
td { padding: .65rem 1.1rem; border-bottom: 1px solid var(--line); vertical-align: middle; }
tbody tr:last-child td { border-bottom: 0; }
tbody tr { transition: background .12s; }
tbody tr:hover td { background: var(--hover); }
td.fit, th.fit { width: 1%; white-space: nowrap; }
td.end, th.end { text-align: right; white-space: nowrap; }
td a.name { font-weight: 650; }
.desc { color: var(--muted); font-size: 13px; }
.mono, code { font-family: ui-monospace, "SF Mono", SFMono-Regular, Menlo, Consolas, monospace; font-size: 12.5px; }

/* State */
.state { display: inline-flex; align-items: center; gap: .35rem; font-weight: 600; white-space: nowrap; }
.state.ok { color: var(--ok); } .state.warn { color: var(--warn); } .state.info { color: var(--link); } .state.off { color: var(--muted); font-weight: 400; }
.badge { display: inline-block; font-size: 11.5px; font-weight: 650; padding: 0 .5rem; line-height: 19px; border-radius: 999px; background: var(--hover); color: var(--muted); vertical-align: middle; }

/* Buttons: capsules. Secondary ones are glass; the primary one is solid with a lit top edge. */
button, .btn {
  font: inherit; font-weight: 600; font-size: 13px; height: 34px; padding: 0 1rem; border-radius: 999px; cursor: pointer;
  display: inline-flex; align-items: center; justify-content: center; gap: .4rem; white-space: nowrap; color: var(--text);
  background: var(--glass-soft); border: 1px solid var(--edge); outline: 1px solid var(--rim);
  box-shadow: var(--shine), 0 1px 2px rgba(15, 27, 42, .06);
  -webkit-backdrop-filter: blur(14px) saturate(170%); backdrop-filter: blur(14px) saturate(170%);
  transition: transform .12s, filter .15s, background .15s;
}
button:hover, .btn:hover { text-decoration: none; background: var(--glass); filter: brightness(1.04); }
button:active, .btn:active { transform: scale(.975); }
button.primary, .btn.primary {
  color: var(--on-primary); border-color: rgba(255, 255, 255, .28); outline-color: rgba(0, 60, 140, .35);
  background: linear-gradient(180deg, var(--primary-2), var(--primary));
  box-shadow: inset 0 1px 0 rgba(255, 255, 255, .42), 0 1px 2px rgba(0, 60, 140, .25), 0 6px 16px rgba(0, 113, 227, .28);
}
button.primary:hover, .btn.primary:hover { filter: brightness(1.08); background: linear-gradient(180deg, var(--primary-2), var(--primary)); }
button.danger { color: var(--danger); }
button.danger:hover { background: var(--danger-soft); }
button.link { border: 0; outline: 0; background: none; box-shadow: none; -webkit-backdrop-filter: none; backdrop-filter: none; color: var(--link); padding: 0; height: auto; font-weight: 500; border-radius: 4px; }
button.link:hover { text-decoration: underline; background: none; filter: none; }
button.link.danger { color: var(--danger); }
button:disabled { opacity: .5; cursor: progress; }

/* Fields */
label { display: block; font-weight: 600; font-size: 13px; margin: 0 0 .3rem; }
input[type=text], input[type=password], input[type=search], input:not([type]) {
  font: inherit; width: 100%; height: 34px; padding: 0 .8rem; border-radius: 11px; color: var(--text);
  background: var(--field); border: 1px solid var(--line-strong); box-shadow: inset 0 1px 2px rgba(15, 27, 42, .06);
  transition: border-color .15s, box-shadow .15s;
}
input[type=search] { border-radius: 999px; }
input:focus { outline: 0; border-color: var(--primary-2); box-shadow: 0 0 0 3px var(--sel); }
.field { margin-bottom: .9rem; }
.check { display: flex; align-items: center; gap: .5rem; font-weight: 500; margin: 0; }
.check input { width: 16px; height: 16px; accent-color: var(--primary); }
.copy { display: flex; border: 1px solid var(--line-strong); border-radius: 12px; overflow: hidden; background: var(--field); max-width: 46rem; }
.copy code { padding: 0 .85rem; line-height: 34px; overflow-x: auto; white-space: nowrap; flex: 1; }
.copy button { border: 0; outline: 0; border-left: 1px solid var(--line-strong); border-radius: 0; box-shadow: none; background: transparent; -webkit-backdrop-filter: none; backdrop-filter: none; }

/* Tabs: a segmented control */
.tabs { display: inline-flex; gap: 2px; padding: 3px; border-radius: 999px; margin: 0 0 14px; max-width: 100%; overflow-x: auto; scrollbar-width: none; }
.tabs button { border: 0; outline: 0; background: transparent; box-shadow: none; -webkit-backdrop-filter: none; backdrop-filter: none; height: 30px; padding: 0 .95rem; color: var(--muted); font-size: 13px; }
.tabs button:hover { color: var(--text); background: var(--hover); filter: none; }
.tabs button[aria-selected="true"] { color: var(--text); background: var(--glass-strong); box-shadow: var(--shine), 0 1px 3px rgba(15, 27, 42, .14); }
[role=tabpanel][hidden] { display: none; }

/* Alerts and log */
.alert { display: flex; gap: .6rem; align-items: flex-start; padding: .7rem 1rem; border: 1px solid var(--line); background: var(--info-bg); border-radius: 14px; margin-bottom: 14px; }
.alert.warn { background: var(--warn-bg); color: var(--text); }
.alert.warn svg { color: var(--warn); }
.alert svg { margin-top: 2px; }
pre.log { margin: 0; padding: .9rem 1.1rem; max-height: 26rem; overflow: auto; font: 12px/1.6 ui-monospace, "SF Mono", SFMono-Regular, Menlo, Consolas, monospace; white-space: pre-wrap; word-break: break-word; }
pre.log .t { color: var(--faint); } pre.log .note { color: var(--ok); } pre.log .err { color: var(--danger); }

/* Sign-in and messages */
.center { display: flex; align-items: flex-start; justify-content: center; padding: 12vh 1rem 2rem; }
.auth { width: 100%; max-width: 23rem; }
.auth .brand { justify-content: center; margin-bottom: 1.1rem; }
.error { color: var(--danger); font-weight: 600; margin: 0 0 .9rem; }

/* Dialogs and toast */
dialog { border-radius: 22px; padding: 0; color: var(--text); width: min(28rem, calc(100vw - 2rem)); box-shadow: var(--shine), var(--float); overflow: hidden; }
dialog::backdrop { background: rgba(8, 14, 24, .32); -webkit-backdrop-filter: blur(6px); backdrop-filter: blur(6px); }
dialog header { padding: 1.1rem 1.25rem .2rem; font-weight: 680; font-size: 17px; letter-spacing: -.015em; }
dialog .body { padding: .5rem 1.25rem 1rem; }
dialog .body p { margin: 0 0 .9rem; color: var(--muted); }
dialog .body > :last-child { margin-bottom: 0; }
dialog footer { padding: 0 1.25rem 1.1rem; display: flex; justify-content: flex-end; gap: .5rem; }
#toast { position: fixed; left: 50%; top: 20px; transform: translateX(-50%); color: var(--text); padding: .55rem 1.1rem; border-radius: 999px; font-weight: 600; max-width: min(32rem, calc(100vw - 2rem)); z-index: 30; box-shadow: var(--shine), var(--float); }
#toast.bad { color: var(--danger); }

/* People who ask for less transparency, and browsers without blur, get solid surfaces. */
@media (prefers-reduced-transparency: reduce) {
  :root { --glass: var(--glass-strong); --glass-soft: var(--glass-strong); }
  .top, .side, .box, dialog, #toast, .tabs, button, .btn { -webkit-backdrop-filter: none; backdrop-filter: none; }
}
@supports not ((backdrop-filter: blur(1px)) or (-webkit-backdrop-filter: blur(1px))) {
  .top, .side, .tabs, #toast { background: var(--glass-strong); }
}
@media (prefers-reduced-motion: reduce) { * { transition: none !important; } }

@media (max-width: 760px) {
  .frame { padding: 8px 8px 2.5rem; }
  .top { top: 8px; }
  .shell { flex-direction: column; gap: 10px; margin-top: 10px; }
  .side { width: 100%; position: static; display: flex; gap: 2px; padding: 3px; border-radius: 999px; overflow-x: auto; scrollbar-width: none; }
  .side .group, .side .foot { display: none; }
  .side a { padding: .4rem .95rem; border-radius: 999px; white-space: nowrap; }
  main { width: 100%; }
  .brand small, .top .user strong, .wide { display: none; }
  h1 { font-size: 21px; }
  .kv { grid-template-columns: 1fr 1fr; }
  .kv > div { border-left: 0; padding-left: 0; }
  table, tbody, tr, td { display: block; }
  thead { display: none; }
  tr { padding: .7rem 1.1rem; border-bottom: 1px solid var(--line); }
  tbody tr:last-child { border-bottom: 0; }
  td { padding: .1rem 0; border: 0; width: auto !important; }
  td.end { text-align: left; padding-top: .35rem; }
  td.wide { display: none; }
  tbody tr:hover td { background: transparent; }
}
`

const script = `
const $ = (sel, root) => (root || document).querySelector(sel);
const $$ = (sel, root) => Array.from((root || document).querySelectorAll(sel));
function el(tag, props, children) {
  const node = Object.assign(document.createElement(tag), props || {});
  (children || []).forEach(c => node.append(c));
  return node;
}
function toast(text, bad) {
  let node = $('#toast');
  if (!node) { node = el('div', {id: 'toast'}); node.setAttribute('role', 'status'); document.body.appendChild(node); }
  node.textContent = text; node.className = bad ? 'bad' : '';
  clearTimeout(toast.timer); toast.timer = setTimeout(() => node.remove(), bad ? 9000 : 3000);
}
function firstLine(text) { return (text || '').split('\n')[0]; }
async function post(path, body) {
  const res = await fetch(path, {method: 'POST', headers: {'Content-Type': 'application/json'}, body: JSON.stringify(body || {})});
  let data = {};
  try { data = await res.json(); } catch (e) {}
  if (!res.ok) throw new Error(data.error || 'That did not work.');
  if (data.ok === false) throw new Error(firstLine(data.error) || 'That did not work.');
  if (data.publish && data.publish.ok === false) throw new Error(firstLine(data.publish.error));
  return data;
}
const tool = (name, args) => post('/_apps/api/tools/' + name, args);
const manage = (name, args) => post('/_apps/api/manage/' + name, args);

// dialog() shows a modal with optional text and fields. It resolves to an
// object of field values when confirmed (an empty object when there are no
// fields) and to null when cancelled. options: text, fields, confirm, danger,
// type (text the person must type to enable the confirm button).
function dialog(title, options) {
  options = options || {};
  return new Promise(resolve => {
    const form = el('form', {method: 'dialog'});
    const body = el('div', {className: 'body'});
    if (options.text) body.append(el('p', {textContent: options.text}));
    const inputs = {};
    (options.fields || []).forEach(f => {
      const input = el('input', {id: 'dlg-' + f.name, autocomplete: f.autocomplete || 'off'});
      if (f.type === 'checkbox') {
        input.type = 'checkbox';
        body.append(el('label', {className: 'check field'}, [input, f.label]));
      } else {
        if (f.type) input.type = f.type;
        if (f.placeholder) input.placeholder = f.placeholder;
        if (f.value) input.value = f.value;
        if (f.minLength) input.minLength = f.minLength;
        input.required = f.required !== false;
        const label = el('label', {textContent: f.label}); label.htmlFor = input.id;
        body.append(el('div', {className: 'field'}, [label, input]));
      }
      inputs[f.name] = input;
    });
    let typed = null;
    if (options.type) {
      typed = el('input', {autocomplete: 'off'});
      body.append(el('div', {className: 'field'}, [el('label', {textContent: 'Type ' + options.type + ' to confirm'}), typed]));
    }
    const cancel = el('button', {type: 'button', textContent: 'Cancel'});
    const ok = el('button', {type: 'submit', textContent: options.confirm || 'Confirm', className: options.danger ? 'danger' : 'primary'});
    if (typed) { ok.disabled = true; typed.addEventListener('input', () => { ok.disabled = typed.value !== options.type; }); }
    form.append(el('header', {textContent: title}), body, el('footer', {}, [cancel, ok]));
    const d = el('dialog', {}, [form]);
    document.body.append(d);
    let result = null;
    cancel.addEventListener('click', () => d.close());
    form.addEventListener('submit', () => {
      result = {};
      Object.keys(inputs).forEach(k => { result[k] = inputs[k].type === 'checkbox' ? inputs[k].checked : inputs[k].value; });
    });
    d.addEventListener('close', () => { d.remove(); resolve(result); });
    d.showModal();
    (typed || Object.values(inputs)[0] || ok).focus();
  });
}
async function run(btn, action, done) {
  if (btn) btn.disabled = true;
  try { await action(); if (done) toast(done); setTimeout(() => location.reload(), done ? 400 : 0); }
  catch (err) { toast(err.message, true); if (btn) btn.disabled = false; }
}
async function copyText(btn) {
  const text = btn.closest('.copy').querySelector('code').textContent;
  try { await navigator.clipboard.writeText(text); toast('Copied'); } catch (e) { toast('Could not copy. Select the text instead.', true); }
}
// Filter a table's rows by the text typed in the box above it.
function filterRows(input) {
  const box = input.closest('.box'), q = input.value.trim().toLowerCase();
  let shown = 0;
  $$('tbody tr', box).forEach(tr => { const hit = tr.textContent.toLowerCase().includes(q); tr.hidden = !hit; if (hit) shown++; });
  const out = $('.matches', box); if (out) out.textContent = q ? shown + (shown === 1 ? ' match' : ' matches') : '';
}
// Tabs: the selected one is kept in the address so a reload stays put.
function showTab(name) {
  const tabs = $$('[role=tab]');
  if (!tabs.length) return;
  if (!tabs.some(t => t.dataset.tab === name)) name = tabs[0].dataset.tab;
  tabs.forEach(t => t.setAttribute('aria-selected', t.dataset.tab === name));
  $$('[role=tabpanel]').forEach(p => { p.hidden = p.id !== 'tab-' + name; });
  history.replaceState(null, '', '#' + name);
}
document.addEventListener('DOMContentLoaded', () => showTab(location.hash.slice(1)));
window.addEventListener('hashchange', () => showTab(location.hash.slice(1)));
`

// Icons, drawn inline so nothing is fetched.
const (
	logoSVG = `<svg width="24" height="24" viewBox="0 0 24 24" aria-hidden="true"><rect width="24" height="24" rx="7" fill="var(--text)"/><path d="M6 15.5c2 2.2 4 2.2 6 0s4-2.2 6 0M6 9.500c2 2.2 4 2.2 6 0s4-2.2 6 0" fill="none" stroke="var(--bg)" stroke-width="2.2" stroke-linecap="round"/></svg>`
	okSVG   = `<svg width="15" height="15" viewBox="0 0 16 16" aria-hidden="true"><circle cx="8" cy="8" r="7" fill="none" stroke="currentColor" stroke-width="1.8"/><path d="M4.8 8.300l2.2 2.2 4.3-4.6" fill="none" stroke="currentColor" stroke-width="1.8"/></svg>`
	warnSVG = `<svg width="15" height="15" viewBox="0 0 16 16" aria-hidden="true"><path d="M8 1.800l6.7 12H1.300z" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linejoin="round"/><path d="M8 6.500v3.500M8 11.700v.6" stroke="currentColor" stroke-width="1.8"/></svg>`
	infoSVG = `<svg width="15" height="15" viewBox="0 0 16 16" aria-hidden="true"><circle cx="8" cy="8" r="7" fill="none" stroke="currentColor" stroke-width="1.8"/><path d="M8 7v4.500M8 4.300v.6" stroke="currentColor" stroke-width="1.8"/></svg>`
)

const layoutTemplates = `
{{define "head"}}<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}} | caddy-snake apps</title>
<link rel="icon" href="data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 24 24'%3E%3Crect width='24' height='24' rx='7' fill='%230071e3'/%3E%3Cpath d='M6 15.500c2 2.2 4 2.2 6 0s4-2.2 6 0M6 9.500c2 2.2 4 2.2 6 0s4-2.2 6 0' fill='none' stroke='white' stroke-width='2.2'/%3E%3C/svg%3E">
<style>{{.CSS}}</style>
</head>
<body>{{end}}

{{define "ok"}}<span class="state ok">{{icon "ok"}}{{.}}</span>{{end}}
{{define "warn"}}<span class="state warn">{{icon "warn"}}{{.}}</span>{{end}}
{{define "info"}}<span class="state info">{{icon "info"}}{{.}}</span>{{end}}

{{define "bar"}}<div class="top">
  <a class="brand" href="/">{{icon "logo"}}<span>caddy-snake</span><small>Apps</small></a>
  <span class="space"></span>
  {{if .User}}<form class="user" method="post" action="/_apps/logout"><strong>{{.User}}</strong><button type="submit">Sign out</button></form>
  {{else if and .Nav (not .RequireLogin)}}<a class="flag" href="/_apps/people">{{icon "warn"}}Sign-in off</a>{{end}}
</div>{{end}}

{{define "top"}}{{template "head" .}}
<div class="frame">
{{template "bar" .}}
<div class="shell">
  <nav class="side" aria-label="Main">
    <div class="group">Build</div>
    <a href="/" {{if eq .Nav "apps"}}aria-current="page"{{end}}>Apps</a>
    <a href="/_apps/connect" {{if eq .Nav "connect"}}aria-current="page"{{end}}>Connect</a>
    <div class="group">Access</div>
    <a href="/_apps/people" {{if eq .Nav "people"}}aria-current="page"{{end}}>People</a>
    <div class="foot">Version {{.Version}}</div>
  </nav>
  <main>
  {{if gt (len .Crumbs) 1}}<div class="trail">{{range $i, $c := .Crumbs}}{{if $i}}<span class="sep">/</span>{{end}}{{if $c.URL}}<a href="{{$c.URL}}">{{$c.Label}}</a>{{else}}<span>{{$c.Label}}</span>{{end}}{{end}}</div>{{end}}
{{end}}

{{define "foot"}}</main></div></div><script>{{.JS}}</script>{{end}}
{{define "end"}}</body></html>{{end}}
`

const messagePage = `{{template "head" .}}
<div class="frame">{{template "bar" .}}
<div class="center"><div class="auth box">
  <header><h2>{{.Heading}}</h2></header>
  <div class="pad"><p style="margin:0">{{.Text}}</p>{{if .LinkURL}}<p style="margin:1rem 0 0"><a class="btn" href="{{.LinkURL}}">{{.LinkText}}</a></p>{{end}}</div>
</div></div></div>{{template "end" .}}`

const loginPage = `{{template "head" .}}
<div class="frame">
<div class="center"><div class="auth">
  <div class="brand">{{icon "logo"}}<span>caddy-snake</span><small>Apps</small></div>
  <div class="box">
  <header><h2>Sign in</h2></header>
  <div class="pad">
    {{if .Error}}<p class="error" role="alert">{{.Error}}</p>{{end}}
    <form method="post" action="/_apps/login">
      <input type="hidden" name="next" value="{{.Next}}">
      <div class="field"><label for="username">Username</label><input id="username" name="username" autocomplete="username" autocapitalize="none" autofocus required></div>
      <div class="field"><label for="password">Password</label><input id="password" name="password" type="password" autocomplete="current-password" required></div>
      <button class="primary" type="submit" style="width:100%">Sign in</button>
    </form>
  </div>
  </div>
</div></div></div>{{template "end" .}}`

const appsPage = `{{template "top" .}}
<div class="box">
  <header>
    <h1>Apps <span class="count">({{len .Apps}})</span></h1>
    <div class="acts"><a class="btn" href="/_apps/connect">Connect assistant</a><button class="primary" onclick="createApp(this)">Create app</button></div>
  </header>
  {{if .Apps}}
  <div class="tools"><input type="search" placeholder="Find apps" aria-label="Find apps" oninput="filterRows(this)"><span class="matches"></span></div>
  <table>
    <thead><tr><th>Name</th><th>Address</th><th class="fit">Status</th><th class="fit wide">Drafts</th><th class="fit">Last published</th><th class="fit wide">Activity</th><th class="end">Actions</th></tr></thead>
    <tbody>
    {{range .Apps}}
      <tr>
        <td><a class="name" href="/_apps/app/{{.Slug}}">{{.Name}}</a>{{if .Description}}<div class="desc">{{.Description}}</div>{{end}}</td>
        <td class="mono"><a href="{{.URL}}">{{.URL}}</a></td>
        <td class="fit">{{if .Published}}{{template "ok" "Live"}}{{else}}{{template "warn" "Not ready"}}{{end}}</td>
        <td class="fit wide">{{len .Drafts}}</td>
        <td class="fit desc">{{if .PublishedAt}}Published {{.PublishedAt}}{{else}}Not published{{end}}</td>
        <td class="fit wide">{{if .Awake}}Awake{{if .Memory}}, {{.Memory}}{{end}}{{else}}<span class="state off">Asleep</span>{{end}}</td>
        <td class="end">{{if .Published}}<a href="{{.URL}}">Open</a>{{else}}-{{end}}</td>
      </tr>
    {{end}}
    </tbody>
  </table>
  {{else}}
  <div class="none"><strong>No apps</strong>Create one here, or connect your assistant and ask it to build one.</div>
  {{end}}
</div>
{{template "foot" .}}
<script>
async function createApp(btn) {
  const v = await dialog('Create app', {confirm: 'Create app', fields: [{name: 'name', label: 'Name', placeholder: 'Team lunch orders'}, {name: 'description', label: 'Description', required: false}]});
  if (!v) return;
  btn.disabled = true;
  try { const data = await tool('create_app', v); location.href = '/_apps/app/' + data.app.slug; }
  catch (err) { toast(err.message, true); btn.disabled = false; }
}
</script>{{template "end" .}}`

const appPage = `{{template "top" .}}
<div class="title">
  <h1>{{.App.Name}}</h1>
  <div class="acts">
    {{if .Published}}<a class="btn" href="{{.URL}}">Open</a>{{end}}
    {{if .IsDraft}}<button class="primary" onclick="publishDraft(this)">Publish draft</button>{{else}}<button onclick="newDraft(this)">Create draft</button>{{end}}
    <button class="danger" onclick="removeThis(this)">Delete</button>
  </div>
</div>

<div class="box">
  <header><h2>Details</h2></header>
  <div class="pad"><dl class="kv">
    <div><dt>Status</dt><dd>{{if .IsDraft}}{{template "info" "Draft"}}{{else if .Published}}{{template "ok" "Live"}}{{else}}{{template "warn" "Not ready"}}{{end}}</dd></div>
    <div><dt>Address</dt><dd class="mono">{{if .Published}}<a href="{{.URL}}">{{.FullURL}}</a>{{else}}{{.FullURL}}{{end}}</dd></div>
    <div><dt>Live version</dt><dd class="mono">{{if .App.LiveVersion}}{{.App.LiveVersion}}{{else}}-{{end}}</dd></div>
    <div><dt>Last published</dt><dd>{{if .PublishedAt}}{{.PublishedAt}}{{else}}-{{end}}</dd></div>
    <div><dt>Activity</dt><dd>{{if .Awake}}Awake{{if .Memory}}, {{.Memory}}{{end}}{{else}}Asleep{{end}}</dd></div>
    <div><dt>Created</dt><dd>{{.CreatedAt}}</dd></div>
    <div><dt>Created by</dt><dd>{{if .App.CreatedBy}}{{.App.CreatedBy}}{{else}}-{{end}}</dd></div>
    <div><dt>{{if .IsDraft}}Draft of{{else}}Description{{end}}</dt><dd>{{if .IsDraft}}<a href="/_apps/app/{{.ParentSlug}}">{{.ParentSlug}}</a>{{else if .App.Description}}{{.App.Description}}{{else}}-{{end}}</dd></div>
  </dl></div>
</div>

<div class="tabs" role="tablist">
  <button role="tab" data-tab="versions" onclick="showTab('versions')">Versions ({{len .Versions}})</button>
  {{if not .IsDraft}}<button role="tab" data-tab="drafts" onclick="showTab('drafts')">Drafts ({{len .Drafts}})</button>{{end}}
  <button role="tab" data-tab="settings" onclick="showTab('settings')">Settings ({{len .Settings}})</button>
  <button role="tab" data-tab="activity" onclick="showTab('activity')">Activity</button>
</div>

<div class="box" role="tabpanel" id="tab-versions">
  <header><h2>Versions <span class="count">({{len .Versions}})</span></h2></header>
  {{if .Versions}}
  <table>
    <thead><tr><th class="fit">Version</th><th>Change</th><th class="fit">Saved</th><th class="fit wide">By</th><th class="fit">State</th><th class="end">Actions</th></tr></thead>
    <tbody>
    {{range .Versions}}
      <tr>
        <td class="fit mono">{{.ID}}</td>
        <td>{{.Message}}</td>
        <td class="fit">{{.When}}</td>
        <td class="fit wide">{{.Author}}</td>
        <td class="fit">{{if .Live}}{{template "ok" "Live"}}{{else}}-{{end}}</td>
        <td class="end">{{if .Live}}-{{else}}<button class="link" data-version="{{.ID}}" onclick="goBack(this)">Go back to this</button>{{end}}</td>
      </tr>
    {{end}}
    </tbody>
  </table>
  {{else}}<div class="none"><strong>No versions</strong></div>{{end}}
</div>

{{if not .IsDraft}}
<div class="box" role="tabpanel" id="tab-drafts" hidden>
  <header><h2>Drafts <span class="count">({{len .Drafts}})</span></h2><button onclick="newDraft(this)">Create draft</button></header>
  {{if .Drafts}}
  <table>
    <thead><tr><th>Name</th><th>Address</th><th class="end">Actions</th></tr></thead>
    <tbody>
    {{range .Drafts}}<tr><td><a class="name" href="/_apps/app/{{.Slug}}">{{.Name}}</a></td><td class="mono"><a href="{{.URL}}">{{.URL}}</a></td><td class="end"><a href="{{.URL}}">Open</a></td></tr>{{end}}
    </tbody>
  </table>
  {{else}}<div class="none"><strong>No drafts</strong>A draft is a private copy of the app and its data.</div>{{end}}
</div>
{{end}}

<div class="box" role="tabpanel" id="tab-settings" hidden>
  <header><h2>Settings <span class="count">({{len .Settings}})</span></h2><button onclick="addSetting(this)">Add setting</button></header>
  {{if .Settings}}
  <table>
    <thead><tr><th>Name</th><th>Value</th><th class="end">Actions</th></tr></thead>
    <tbody>
    {{range .Settings}}<tr><td class="mono">{{.}}</td><td class="desc">Hidden</td><td class="end"><button class="link danger" data-name="{{.}}" onclick="removeSetting(this)">Remove</button></td></tr>{{end}}
    </tbody>
  </table>
  {{else}}<div class="none"><strong>No settings</strong>Passwords and tokens the app reads.</div>{{end}}
</div>

<div class="box" role="tabpanel" id="tab-activity" hidden>
  <header><h2>Activity</h2><button onclick="location.reload()">Refresh</button></header>
  {{if .Activity}}<pre class="log">{{range .Activity}}<span class="t">{{.When}}</span>  <span class="{{.Class}}">{{.Line}}</span>
{{end}}</pre>
  {{else}}<div class="none"><strong>No activity</strong></div>{{end}}
</div>
{{template "foot" .}}
<script>
const APP = {{.AppSlug}}, DRAFT = {{.DraftName}};
const target = extra => Object.assign(DRAFT ? {app: APP, draft: DRAFT} : {app: APP}, extra || {});
async function goBack(btn) {
  if (!await dialog('Go back to version ' + btn.dataset.version, {text: 'Data is not changed.', confirm: 'Go back'})) return;
  run(btn, () => tool('roll_back', target({version: btn.dataset.version})), 'Went back');
}
async function newDraft(btn) {
  const v = await dialog('Create draft', {confirm: 'Create draft', fields: [{name: 'name', label: 'Name', placeholder: 'new form'}]});
  if (!v) return;
  btn.disabled = true;
  try { const data = await tool('create_draft', {app: APP, name: v.name}); location.href = '/_apps/app/' + data.draft.slug; }
  catch (err) { toast(err.message, true); btn.disabled = false; }
}
async function publishDraft(btn) {
  if (!await dialog('Publish draft', {text: 'It replaces the app. The draft and its data are removed.', confirm: 'Publish'})) return;
  btn.disabled = true;
  try { await tool('publish_draft', {app: APP, draft: DRAFT}); location.href = '/_apps/app/' + APP; }
  catch (err) { toast(err.message, true); btn.disabled = false; }
}
async function addSetting(btn) {
  const v = await dialog('Add setting', {confirm: 'Save', fields: [{name: 'name', label: 'Name', placeholder: 'API_TOKEN'}, {name: 'value', label: 'Value', type: 'password'}]});
  if (!v) return;
  run(btn, () => tool('set_setting', target({name: v.name.trim().toUpperCase(), value: v.value})), 'Setting saved');
}
async function removeSetting(btn) {
  if (!await dialog('Remove ' + btn.dataset.name, {confirm: 'Remove', danger: true})) return;
  run(btn, () => tool('set_setting', target({name: btn.dataset.name, value: null})), 'Setting removed');
}
async function removeThis(btn) {
  try {
    if (DRAFT) {
      if (!await dialog('Delete draft', {text: 'The draft and its copy of the data are removed.', confirm: 'Delete', danger: true})) return;
      btn.disabled = true;
      await tool('delete_draft', {app: APP, draft: DRAFT}); location.href = '/_apps/app/' + APP;
    } else {
      if (!await dialog('Delete ' + APP, {text: 'All versions, drafts and data are removed. This cannot be undone.', confirm: 'Delete', danger: true, type: APP})) return;
      btn.disabled = true;
      await tool('delete_app', {app: APP, confirm: APP}); location.href = '/';
    }
  } catch (err) { toast(err.message, true); btn.disabled = false; }
}
</script>{{template "end" .}}`

const connectPage = `{{template "top" .}}
<div class="title"><h1>Connect</h1></div>

<div class="box">
  <header><h2>Assistant endpoint</h2></header>
  <div class="pad">
    <div class="field"><label>MCP server address</label><div class="copy"><code>{{.MCPURL}}</code><button type="button" onclick="copyText(this)">Copy</button></div></div>
    <div class="field" style="margin-bottom:0"><label>Claude Code</label><div class="copy"><code>claude mcp add --transport http apps {{.MCPURL}}</code><button type="button" onclick="copyText(this)">Copy</button></div></div>
  </div>
</div>

{{if .RequireLogin}}
<div class="box">
  <header><h2>Access keys <span class="count">({{len .Keys}})</span></h2><button class="primary" onclick="newKey(this)">Create key</button></header>
  <div class="pad" id="new-key" hidden style="border-bottom:1px solid var(--line)">
    <label>New key. Copy it now, it is not shown again.</label>
    <div class="copy"><code></code><button type="button" onclick="copyText(this)">Copy</button></div>
  </div>
  {{if .Keys}}
  <table>
    <thead><tr><th>Name</th><th class="fit">Created</th><th class="fit wide">Last used</th><th class="end">Actions</th></tr></thead>
    <tbody>{{range .Keys}}<tr><td>{{.Name}}</td><td class="fit">{{.Created}}</td><td class="fit wide">{{.LastUsed}}</td><td class="end"><button class="link danger" data-name="{{.Name}}" onclick="revokeKey(this)">Remove</button></td></tr>{{end}}</tbody>
  </table>
  {{else}}<div class="none"><strong>No access keys</strong>Your assistant needs one to sign in as you.</div>{{end}}
</div>
{{end}}
{{template "foot" .}}
<script>
async function newKey(btn) {
  const v = await dialog('Create access key', {confirm: 'Create key', fields: [{name: 'name', label: 'Name', value: 'assistant'}]});
  if (!v) return;
  btn.disabled = true;
  try {
    const data = await manage('keys/create', {name: v.name});
    const box = $('#new-key'); box.hidden = false; $('code', box).textContent = data.key;
  } catch (err) { toast(err.message, true); }
  btn.disabled = false;
}
async function revokeKey(btn) {
  if (!await dialog('Remove key ' + btn.dataset.name, {text: 'Anything using it stops working.', confirm: 'Remove', danger: true})) return;
  run(btn, () => manage('keys/revoke', {name: btn.dataset.name}), 'Key removed');
}
</script>{{template "end" .}}`

const peoplePage = `{{template "top" .}}
{{if not .RequireLogin}}
<div class="alert warn">{{icon "warn"}}<span><strong>Sign-in is off.</strong> Anyone who can reach this server can use it.</span></div>
<div class="box">
  <header><h1>People</h1></header>
  <div class="pad">
    <div class="field"><label>1. Add an administrator</label><div class="copy"><code>caddysnake apps user add NAME --admin</code><button type="button" onclick="copyText(this)">Copy</button></div></div>
    <div class="field" style="margin-bottom:0"><label>2. Restart with sign-in required</label><div class="copy"><code>caddysnake apps --require-login</code><button type="button" onclick="copyText(this)">Copy</button></div></div>
  </div>
</div>
{{else if not .Admin}}
<div class="box"><header><h1>People</h1></header><div class="none"><strong>Administrators only</strong></div></div>
{{else}}
<div class="box">
  <header><h1>People <span class="count">({{len .People}})</span></h1><button class="primary" onclick="addPerson(this)">Add person</button></header>
  <div class="tools"><input type="search" placeholder="Find people" aria-label="Find people" oninput="filterRows(this)"><span class="matches"></span></div>
  <table>
    <thead><tr><th>Username</th><th class="fit">Role</th><th class="end">Actions</th></tr></thead>
    <tbody>
    {{range .People}}
    <tr>
      <td><strong>{{.Username}}</strong>{{if .You}} <span class="badge">You</span>{{end}}</td>
      <td class="fit">{{if .Admin}}Administrator{{else}}Member{{end}}</td>
      <td class="end"><button class="link" data-user="{{.Username}}" onclick="setPassword(this)">Set password</button>{{if not .You}}&nbsp;&nbsp;|&nbsp;&nbsp;<button class="link danger" data-user="{{.Username}}" onclick="removePerson(this)">Remove</button>{{end}}</td>
    </tr>
    {{end}}
    </tbody>
  </table>
</div>
{{end}}
{{template "foot" .}}
<script>
async function addPerson(btn) {
  const v = await dialog('Add person', {confirm: 'Add person', fields: [
    {name: 'username', label: 'Username'},
    {name: 'password', label: 'Password (8 characters or more)', type: 'password', minLength: 8, autocomplete: 'new-password'},
    {name: 'admin', label: 'Administrator', type: 'checkbox'}]});
  if (!v) return;
  run(btn, () => manage('people/add', {username: v.username.trim().toLowerCase(), password: v.password, admin: v.admin}), 'Person added');
}
async function setPassword(btn) {
  const v = await dialog('Set password for ' + btn.dataset.user, {text: 'They are signed out everywhere.', confirm: 'Set password', fields: [{name: 'password', label: 'New password (8 characters or more)', type: 'password', minLength: 8, autocomplete: 'new-password'}]});
  if (!v) return;
  run(btn, () => manage('people/password', {username: btn.dataset.user, password: v.password}), 'Password set');
}
async function removePerson(btn) {
  if (!await dialog('Remove ' + btn.dataset.user, {text: 'Their access keys stop working.', confirm: 'Remove', danger: true})) return;
  run(btn, () => manage('people/remove', {username: btn.dataset.user}), 'Person removed');
}
</script>{{template "end" .}}`
