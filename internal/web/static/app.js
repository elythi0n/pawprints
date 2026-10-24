// The Pawprints page: Overview, Projects, Sessions, and Files over the data
// `pawprints ui` serves. Filters live in the browser; changes Claude makes
// arrive over server-sent events and refresh whatever is on screen.

import { esc, short, tokenChart, editsChart, limitChart, spark } from './charts.js';

const params = new URLSearchParams(location.search);
const TOKEN = params.get('t') || '';
const EMBED = params.get('embed') || '';

const RANGES = {
  '1h': { label: 'the last hour' },
  '24h': { label: 'the last 24 hours' },
  '7d': { label: 'the last 7 days' },
  '30d': { label: 'the last 30 days' },
};
const VIEWS = ['overview', 'projects', 'sessions', 'files'];
const SERIES = ['var(--paw)', 'var(--teal)', 'var(--cache)', 'var(--warn)', 'var(--add)', 'var(--del)'];

const app = document.getElementById('app');
const tipEl = document.getElementById('tip');
// The drawer sits outside the page, so it stays put while the page redraws.
const drawerEl = document.createElement('div');
drawerEl.id = 'drawer-root';
document.body.appendChild(drawerEl);
const toastEl = document.getElementById('toast');

// ---------- Small helpers ----------

const remembered = key => { try { return localStorage.getItem(`pawprints.${key}`) || ''; } catch { return ''; } };
const remember = (key, value) => { try { localStorage.setItem(`pawprints.${key}`, value || ''); } catch { /* kept for this visit only */ } };

function toast(text) {
  toastEl.textContent = text;
  toastEl.classList.add('show');
  clearTimeout(toast.t);
  toast.t = setTimeout(() => toastEl.classList.remove('show'), 2800);
}

const pad = n => String(n).padStart(2, '0');
const MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];
const DAYS = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'];
const hm = d => `${pad(d.getHours())}:${pad(d.getMinutes())}`;
const md = d => `${MONTHS[d.getMonth()]} ${d.getDate()}`;

function axisLabel(d) {
  if (state.range === '7d') return `${DAYS[d.getDay()]} ${pad(d.getHours())}h`;
  if (state.range === '30d') return md(d);
  return hm(d);
}

function when(iso) {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return '';
  const today = new Date();
  if (d.toDateString() === today.toDateString()) return hm(d);
  const y = new Date(today); y.setDate(today.getDate() - 1);
  if (d.toDateString() === y.toDateString()) return `Yesterday ${hm(d)}`;
  return `${md(d)} ${hm(d)}`;
}

function ago(iso) {
  const t = new Date(iso).getTime();
  if (!t) return '';
  const s = Math.max(0, (Date.now() - t) / 1000);
  if (s < 60) return 'just now';
  if (s < 3600) return `${Math.floor(s / 60)} min ago`;
  if (s < 86400) return `${Math.floor(s / 3600)} h ago`;
  return when(iso);
}

// "claude-opus-5-5" -> "Opus 5.5"; ids it does not know pass through.
function prettyModel(id) {
  const m = /^claude-([a-z]+)-(\d+)(?:-(\d+))?/.exec(id || '');
  if (!m) return id || '';
  return `${m[1][0].toUpperCase()}${m[1].slice(1)} ${m[2]}${m[3] ? `.${m[3]}` : ''}`;
}

const money = v => (v >= 100 ? `$${Math.round(v).toLocaleString('en-US')}` : `$${(v || 0).toFixed(2)}`);
const plural = (n, one, many) => `${n} ${n === 1 ? one : many}`;

function splitPath(p) {
  const i = p.lastIndexOf('/');
  return i < 0 ? esc(p) : `<span class="dir">${esc(p.slice(0, i + 1))}</span>${esc(p.slice(i + 1))}`;
}

function planBadge(plan) {
  if (!plan) return '';
  return `<span class="plan ${esc(plan.toLowerCase().split(' ')[0])}">${esc(plan)}</span>`;
}

const ICONS = {
  check: '<svg class="icon check" viewBox="0 0 24 24" aria-hidden="true"><path d="m5 12.5 4.5 4.5L19 7.5"/></svg>',
  chev: '<svg class="icon chev" viewBox="0 0 24 24" aria-hidden="true"><path d="m6 9 6 6 6-6"/></svg>',
  right: '<svg class="icon chev" viewBox="0 0 24 24" aria-hidden="true"><path d="m9 6 6 6-6 6"/></svg>',
  person: '<svg class="icon" viewBox="0 0 24 24" aria-hidden="true" style="color:var(--muted)"><circle cx="12" cy="8" r="3.5"/><path d="M5 20a7 7 0 0 1 14 0"/></svg>',
  file: '<svg class="icon" viewBox="0 0 24 24" aria-hidden="true" style="color:var(--muted)"><path d="M7 3h7l5 5v13H7Z"/><path d="M14 3v5h5"/></svg>',
  robot: '<svg class="icon" viewBox="0 0 24 24" aria-hidden="true" style="color:var(--muted)"><rect x="5" y="8" width="14" height="11" rx="3"/><path d="M12 4v4M9 13h.01M15 13h.01"/></svg>',
  sort: '<svg class="icon" viewBox="0 0 24 24" aria-hidden="true" style="color:var(--muted)"><path d="M4 7h16M7 12h10M10 17h4"/></svg>',
  close: '<svg class="icon" viewBox="0 0 24 24" aria-hidden="true"><path d="M6 6l12 12M18 6 6 18"/></svg>',
  history: '<svg class="icon" viewBox="0 0 24 24" aria-hidden="true"><path d="M4 12a8 8 0 1 0 2.5-5.8"/><path d="M4 4v4h4M12 8v4l3 2"/></svg>',
};

// ---------- API ----------

async function api(path, query = {}) {
  const url = new URL(path, location.origin);
  for (const [k, v] of Object.entries(query)) if (v) url.searchParams.set(k, v);
  const res = await fetch(url, { headers: { 'X-Pawprints-Token': TOKEN } });
  const body = await res.json().catch(() => ({}));
  if (!res.ok) throw Object.assign(new Error(body.error || `The server answered ${res.status}`), { status: res.status, body });
  return body;
}

async function post(path, body) {
  const res = await fetch(path, {
    method: 'POST',
    headers: { 'X-Pawprints-Token': TOKEN, 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw Object.assign(new Error(data.error || `The server answered ${res.status}`), { status: res.status, body: data });
  return data;
}

// ---------- State ----------

const state = {
  view: VIEWS.includes(location.hash.slice(1)) ? location.hash.slice(1) : 'overview',
  range: RANGES[params.get('range')] ? params.get('range') : RANGES[remembered('range')] ? remembered('range') : '24h',
  project: params.has('project') ? params.get('project') : remembered('project'),
  account: params.has('account') ? params.get('account') : remembered('account'),
  showCache: false,
  showIdle: false,
  showQuiet: false,
  expanded: new Set(),
  m: null,
  error: '',
  accounts: [],
  session: null,
  sess: null,
  sessError: '',
  openTurn: null,
  file: null,
  diff: null,
  confirm: null,
  fileView: null,
  backfilling: false,
  sessQuery: '',
  sessSort: ['recent', 'tokens', 'edits'].includes(remembered('sessSort')) ? remembered('sessSort') : 'recent',
  fileQuery: '',
  fileSort: ['edits', 'lines', 'recent'].includes(remembered('fileSort')) ? remembered('fileSort') : 'edits',
  showOutsideFiles: false,
  prev: null,
};

// A session or file named in the link opens with the page.
if (params.get('session') && params.get('sp')) state.session = { id: params.get('session'), project_id: params.get('sp') };
if (params.get('file') && params.get('fp')) state.fileView = { path: params.get('file'), project_id: params.get('fp'), versions: null, selected: params.get('version') || null };

// ---------- The link ----------

// The URL carries what is on screen, so a bookmark, a shared link, or the
// back button returns to it. pushNext makes the next change a new history
// entry; redraws that only refresh data replace the current one.
let pushNext = false;

function pageURL() {
  const q = new URLSearchParams();
  q.set('t', TOKEN);
  if (EMBED) q.set('embed', EMBED);
  q.set('range', state.range);
  if (state.project) q.set('project', state.project);
  if (state.account) q.set('account', state.account);
  if (state.view === 'sessions' && state.session) {
    q.set('session', state.session.id);
    q.set('sp', state.session.project_id);
  }
  if (state.fileView) {
    q.set('file', state.fileView.path);
    q.set('fp', state.fileView.project_id);
    if (state.fileView.selected) q.set('version', state.fileView.selected);
  }
  return `${location.pathname}?${q.toString()}#${state.view}`;
}

function syncURL() {
  const url = pageURL();
  if (url === `${location.pathname}${location.search}${location.hash}`) { pushNext = false; return; }
  if (pushNext) history.pushState(null, '', url);
  else history.replaceState(null, '', url);
  pushNext = false;
}

window.addEventListener('popstate', () => {
  const q = new URLSearchParams(location.search);
  const view = location.hash.slice(1);
  state.view = VIEWS.includes(view) ? view : 'overview';
  if (RANGES[q.get('range')]) state.range = q.get('range');
  state.project = q.get('project') || '';
  state.account = q.get('account') || '';
  state.session = q.get('session') && q.get('sp') ? { id: q.get('session'), project_id: q.get('sp') } : null;
  state.sess = null;
  state.fileView = q.get('file') && q.get('fp') ? { path: q.get('file'), project_id: q.get('fp'), versions: null, selected: q.get('version') || null } : null;
  state.file = null; state.diff = null; state.confirm = null;
  refreshAll().then(() => { if (state.fileView && state.fileView.selected) selectVersion(state.fileView.selected); });
});

const colorOf = (() => {
  const map = new Map();
  return id => {
    if (!map.has(id)) map.set(id, SERIES[map.size % SERIES.length]);
    return map.get(id);
  };
})();

const accountInfo = id => state.accounts.find(a => a.id === id) || null;
const accountEmail = id => (accountInfo(id) || {}).email || (id ? 'Unknown account' : '');
const multiAccount = () => state.accounts.length > 1;
const projectName = id => {
  const p = [...(state.pickerProjects || []), ...(state.m?.projects || [])].find(x => x.id === id);
  return p ? p.name : '';
};

// ---------- Loading ----------

let metricsSeq = 0;
async function loadMetrics() {
  const seq = ++metricsSeq;
  try {
    // With a project picked, the picker still lists every project of the range.
    const [m, every, prev] = await Promise.all([
      api('/api/metrics', { range: state.range, project: state.project, account: state.account }),
      state.project ? api('/api/metrics', { range: state.range, account: state.account }) : null,
      // The same span just before, for the comparisons on the tiles.
      api('/api/metrics', { range: state.range, project: state.project, account: state.account, previous: '1' }).catch(() => null),
    ]);
    if (seq !== metricsSeq) return;
    state.m = m;
    state.prev = prev ? prev.totals : null;
    state.pickerProjects = (every || m).projects;
    state.pickerProjects.forEach(p => colorOf(p.id));
    state.error = '';
    m.projects.forEach(p => colorOf(p.id));
  } catch (err) {
    if (seq !== metricsSeq) return;
    state.error = err.status === 401 ? 'This page needs the link `pawprints ui` printed, with its token. Open it from there.' : err.message;
  }
}

async function loadAccounts() {
  try { state.accounts = await api('/api/accounts'); } catch { state.accounts = []; }
  if (state.account && !accountInfo(state.account)) state.account = '';
}

async function loadSession() {
  const s = state.session;
  if (!s) { state.sess = null; return; }
  try {
    const [m, turns, events] = await Promise.all([
      api('/api/metrics', { range: state.range, project: s.project_id, session: s.id }),
      api('/api/turns', { project: s.project_id, session: s.id }),
      api('/api/log', { project: s.project_id, session: s.id }),
    ]);
    if (state.session !== s) return;
    state.sess = { m, turns, events };
    state.sessError = '';
  } catch (err) {
    state.sessError = err.message;
  }
}

async function loadFileHistory() {
  const fv = state.fileView;
  if (!fv) return;
  try {
    const events = await api('/api/log', { project: fv.project_id, path: fv.path });
    if (state.fileView !== fv) return;
    fv.versions = events;
    if (!fv.selected && fv.versions.length) fv.selected = fv.versions[0].id;
    fv.error = '';
  } catch (err) {
    fv.error = err.message;
  }
}

async function loadDiff() {
  const f = state.file;
  if (!f) { state.diff = null; return; }
  try {
    const q = { project: f.project_id, event: f.last.id };
    if (f.from && f.from.id !== f.last.id) q.from = f.from.id;
    const d = await api('/api/diff', q);
    if (state.file !== f) return;
    state.diff = { text: d.diff || '', stats: d.stats || {} };
  } catch (err) {
    state.diff = { error: err.message };
  }
}

async function refreshAll() {
  await Promise.all([loadMetrics(), loadAccounts()]);
  if (state.view === 'sessions') {
    const list = sessionList();
    if (state.session && !list.find(s => s.id === state.session.id && s.project_id === state.session.project_id)) {
      state.session = null;
    }
    if (!state.session && list.length) pickSession(list[0], false);
    await loadSession();
  }
  if (state.fileView) await loadFileHistory();
  render();
}

// Live changes come in bursts while Claude works; refresh at most every 1.5 s.
let refreshTimer = null;
let lastRefresh = 0;
function scheduleRefresh() {
  if (refreshTimer) return;
  const wait = Math.max(600, 1500 - (Date.now() - lastRefresh));
  refreshTimer = setTimeout(async () => {
    refreshTimer = null;
    lastRefresh = Date.now();
    if (state.confirm || menuOpen() || reading()) { scheduleRefresh(); return; }
    await refreshAll();
  }, wait);
}

// reading reports whether a redraw now would take something away from the
// person: a tooltip they are looking at, or text they have selected.
function reading() {
  if (tipEl.classList.contains('show')) return true;
  const sel = window.getSelection();
  return !!sel && !sel.isCollapsed && app.contains(sel.anchorNode);
}

function connectLive() {
  const live = document.getElementById('live');
  const es = new EventSource(`/api/events?t=${encodeURIComponent(TOKEN)}`);
  es.addEventListener('hello', () => { live.classList.remove('off'); live.title = 'Updates as Claude works'; });
  es.addEventListener('change', scheduleRefresh);
  es.addEventListener('usage', scheduleRefresh);
  es.onerror = () => {
    live.classList.add('off');
    live.title = 'Not connected. Is `pawprints ui` still running?';
  };
}

// ---------- Shared pieces ----------

function head(title, text, { projects = true } = {}) {
  return `<div class="head"><div><h1>${esc(title)}</h1><p>${text}</p></div></div>
    <div class="pickers">${multiAccount() ? accountPicker() : ''}${projects ? projectPicker() : ''}</div>`;
}

function accountPicker() {
  const cur = accountInfo(state.account);
  const opts = [{ id: '', email: 'All accounts', note: `${state.accounts.length} accounts together` }, ...state.accounts.map(a => ({ id: a.id, email: a.email || 'Unknown account', plan: a.plan }))];
  const shares = new Map((state.m?.accounts || []).map(a => [a.id, a.share]));
  return `<div class="dd" id="acct-dd" data-dd>
    <button type="button" class="dd-btn" id="acct-btn" data-dd-btn aria-haspopup="listbox" aria-expanded="false" aria-controls="acct-menu">
      ${ICONS.person}<span class="dd-cur">${esc(cur ? cur.email : 'All accounts')}</span>${cur ? planBadge(cur.plan) : ''}${ICONS.chev}
    </button>
    <div class="dd-menu" id="acct-menu" data-dd-menu role="listbox" aria-label="Accounts" hidden>
      <div class="dd-head">Accounts</div>
      ${opts.map(o => `
      <button type="button" class="dd-opt" role="option" data-pick-account="${esc(o.id)}" aria-selected="${state.account === o.id}">
        <span class="dd-main"><b>${esc(o.email)}</b><small>${esc(o.note || (shares.has(o.id) ? `${Math.round(shares.get(o.id) * 100)}% of tokens in ${RANGES[state.range].label}` : `nothing in ${RANGES[state.range].label}`))}</small></span>
        ${o.id ? planBadge(o.plan) : '<span></span>'}${ICONS.check}
      </button>`).join('')}
    </div>
  </div>`;
}

const ICON_FOLDER = '<svg class="icon" viewBox="0 0 24 24" aria-hidden="true" style="color:var(--muted)"><path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2Z"/></svg>';
const homeTilde = root => root.replace(/^\/home\/[^/]+/, '~');

// projectPicker is a dropdown with a search box, so it stays one control
// however many projects there are. Projects come in order of what they spent.
function projectPicker() {
  const rows = [...(state.pickerProjects || state.m?.projects || [])];
  if (state.project && !rows.find(p => p.id === state.project)) rows.unshift({ id: state.project, name: projectName(state.project) || 'This project', root: '', share: 0 });
  if (!rows.length) return '';
  const cur = rows.find(p => p.id === state.project);
  const opt = (id, name, note, title) => `
      <button type="button" class="dd-opt" role="option" data-pick-project="${esc(id)}" data-find="${esc(`${name} ${title}`.toLowerCase())}" aria-selected="${state.project === id}" title="${esc(title)}">
        <span class="dd-main"><b>${esc(name)}</b><small>${esc(note)}</small></span><span></span>${ICONS.check}
      </button>`;
  return `<div class="dd" id="proj-dd" data-dd>
    <button type="button" class="dd-btn" id="proj-btn" data-dd-btn aria-haspopup="listbox" aria-expanded="false" aria-controls="proj-menu" title="${esc(cur ? cur.root : '')}">
      ${ICON_FOLDER}<span class="dd-cur">${esc(cur ? cur.name : 'All projects')}</span>${ICONS.chev}
    </button>
    <div class="dd-menu dd-wide" id="proj-menu" data-dd-menu role="listbox" aria-label="Projects" hidden>
      ${rows.length > 6 ? '<input type="search" class="dd-search" id="proj-search" placeholder="Find a project" aria-label="Find a project" autocomplete="off" spellcheck="false">' : ''}
      <div class="dd-list">
        ${opt('', 'All projects', `${plural(rows.length, 'project', 'projects')} together`, '')}
        ${rows.map(p => opt(p.id, p.name, [p.share ? `${Math.round(p.share * 100)}% of tokens` : 'no tokens in this range', homeTilde(p.root)].filter(Boolean).join(' · '), p.root)).join('')}
        <p class="dd-none" hidden>No project matches.</p>
      </div>
    </div>
  </div>`;
}

function sortPicker(kind, options, current) {
  const cur = options.find(([v]) => v === current) || options[0];
  return `<div class="dd dd-sort" id="${kind}-sort-dd" data-dd>
    <button type="button" class="dd-btn" id="${kind}-sort" data-dd-btn aria-haspopup="listbox" aria-expanded="false" aria-controls="${kind}-sort-menu" aria-label="Sort: ${esc(cur[1])}">
      ${ICONS.sort}<span class="dd-cur">${esc(cur[1])}</span>${ICONS.chev}
    </button>
    <div class="dd-menu dd-end" id="${kind}-sort-menu" data-dd-menu role="listbox" aria-label="Sort by" hidden>
      <div class="dd-head">Sort by</div>
      ${options.map(([v, l]) => `
      <button type="button" class="dd-opt" role="option" data-pick-sort="${kind}" data-value="${v}" aria-selected="${v === cur[0]}">
        <span class="dd-main"><b>${esc(l)}</b></span><span></span>${ICONS.check}
      </button>`).join('')}
    </div>
  </div>`;
}

// Dropdowns: one open at a time; the open one closes on a pick, Esc, a click
// outside, or Tab.
function openMenu() {
  return document.querySelector('[data-dd-menu]:not([hidden])');
}

function menuOpen() {
  return !!openMenu();
}

function setMenu(dd, open) {
  document.querySelectorAll('[data-dd]').forEach(other => {
    const btn = other.querySelector('[data-dd-btn]'), menu = other.querySelector('[data-dd-menu]');
    const show = open && other === dd;
    menu.hidden = !show;
    btn.setAttribute('aria-expanded', String(show));
  });
  if (!open || !dd) return;
  const menu = dd.querySelector('[data-dd-menu]');
  const search = menu.querySelector('.dd-search');
  if (search) { search.value = ''; filterMenu(menu, ''); search.focus(); return; }
  (menu.querySelector('[aria-selected="true"]') || menu.querySelector('.dd-opt')).focus();
}

function filterMenu(menu, text) {
  const q = text.trim().toLowerCase();
  let shown = 0;
  menu.querySelectorAll('.dd-opt').forEach(o => {
    const hit = !q || (o.dataset.find || '').includes(q);
    o.hidden = !hit;
    if (hit) shown++;
  });
  const none = menu.querySelector('.dd-none');
  if (none) none.hidden = shown > 0;
}

function banners() {
  let out = '';
  if (state.error) out += `<div class="banner err" role="alert"><span>${esc(state.error)}</span><button type="button" class="btn" data-act="retry">Try again</button></div>`;
  const missing = state.m?.missing_history || 0;
  if (missing > 0) {
    out += `<div class="banner"><span><b>${plural(missing, 'older session has', 'older sessions have')} no timeline yet.</b> Their totals count, but the charts can't place them in time until the history is filled in from Claude's transcripts.</span>
      <button type="button" class="btn primary" data-act="backfill" ${state.backfilling ? 'disabled' : ''}>${state.backfilling ? 'Filling in…' : 'Fill in history'}</button></div>`;
  }
  return out;
}

function points(timeline) {
  return timeline.map(p => ({
    at: new Date(p.at),
    perMin: p.per_minute,
    cachePerMin: p.cache_read_per_minute,
    add: p.lines_added,
    del: p.lines_removed,
    counted: p.counted,
    cost: p.api_cost,
    session: p.session,
  }));
}

function spikeMarks(m) {
  const index = new Map(m.timeline.map((p, i) => [p.at, i]));
  return m.spikes.filter(s => index.has(s.at)).map(s => ({ i: index.get(s.at), times: s.times_normal }));
}

const sessionName = id => (state.m?.sessions || []).find(s => s.id === id)?.name || '';

function tokenTip(p) {
  const who = p.session ? ` · ${esc(sessionName(p.session) || p.session.slice(0, 8))}` : '';
  return `<div class="t">${esc(when(p.at.toISOString()))}${who}</div>
    <div class="r"><span>Counted tokens</span><b>${short(p.perMin)}/min</b></div>
    <div class="r"><span>Cache reads</span><b>${short(p.cachePerMin)}/min</b></div>
    <div class="r"><span>API cost</span><b>${money(p.cost)}</b></div>
    <div class="r"><span>Lines</span><b><span style="color:var(--add)">+${p.add}</span> <span style="color:var(--del)">−${p.del}</span></b></div>
    ${p.session ? '<div class="p">Click to open this session</div>' : ''}`;
}

function pickSession(row, go = true) {
  state.session = { id: row.id, project_id: row.project_id };
  state.sess = null;
  state.sessError = '';
  state.openTurn = null;
  state.file = null;
  state.diff = null;
  state.confirm = null;
  if (go) navigate('sessions');
}

function openSessionById(id) {
  const row = (state.m?.sessions || []).find(s => s.id === id);
  if (row) pickSession(row);
}

// ---------- Overview ----------

function overview() {
  const m = state.m;
  if (!m) { app.innerHTML = `${banners()}<p class="loading">Loading…</p>`; return; }
  const t = m.totals;
  const pts = points(m.timeline);
  const range = RANGES[state.range].label;
  const scope = [state.account ? esc(accountEmail(state.account)) : '', state.project ? esc(projectName(state.project)) : ''].filter(Boolean).join(', ');
  const nothing = !t.tokens.counted && !t.edits && !m.sessions.length;
  const limits = limitLines(m);
  const fullest = limits.reduce((best, l) => (!best || l.latest > best.latest ? l : best), null);
  // Idle sessions that spent nothing would bury the busy ones.
  const alive = m.sessions.filter(s => s.alive);
  const running = state.showIdle ? alive : alive.filter(s => s.status !== 'idle' || s.tokens.counted > 0 || s.edits > 0);
  const quiet = alive.length - running.length;

  app.innerHTML = `
    ${head('What Claude has been doing', `Tokens, edits, and limits for ${range}${scope ? ` in ${scope}` : ', across every account and project'}. Hover a chart for details; click it to open the session behind it.`)}
    ${banners()}
    ${nothing ? `<section class="panel welcome">
      <h2>Nothing recorded in ${range}</h2>
      <p>Pawprints fills this in as Claude works: tokens from Claude Code's transcripts, edits from its hooks. Try a longer range, or start a Claude session and watch it appear here.</p>
      <div class="actions"><button type="button" class="btn" data-set-range="7d">Show 7 days</button><button type="button" class="btn" data-set-range="30d">Show 30 days</button></div>
    </section>` : ''}
    <section class="stats" aria-label="Totals">
      <div class="stat"><span class="label">Counted tokens</span><span class="v num">${short(t.tokens.counted)}</span><span class="n">input, cache writes, output</span>${delta(t.tokens.counted, p => p.tokens.counted)}</div>
      <div class="stat"><span class="label">Cache reads</span><span class="v num">${short(t.tokens.cache_read)}</span><span class="n">cheap, shown apart</span>${delta(t.tokens.cache_read, p => p.tokens.cache_read)}</div>
      <div class="stat"><span class="label">Peak</span><span class="v num">${short(t.peak_per_minute)}<small>/min</small></span><span class="n">${t.peak_at ? `${esc(when(t.peak_at))}${t.peak_session ? ` · ${esc(t.peak_session)}` : ''}` : 'no spending yet'}</span></div>
      <div class="stat"><span class="label">Lines</span><span class="v num"><span class="add">+${short(t.lines_added)}</span> <span class="del">−${short(t.lines_removed)}</span></span><span class="n">${plural(t.files, 'file', 'files')} touched</span>${delta(t.lines_added + t.lines_removed, p => p.lines_added + p.lines_removed)}</div>
      ${fullest ? `<div class="stat"><span class="label">${esc(fullest.title)}</span><span class="v num">${Math.round(fullest.latest)}%</span><span class="n">${limits.length > 1 ? `fullest: ${esc(fullest.label)}` : fullest.resets_at ? `resets ${esc(when(fullest.resets_at))}` : 'used'}</span></div>`
        : `<div class="stat"><span class="label">Sessions</span><span class="v num">${t.sessions}</span><span class="n">${t.live_sessions} running now</span>${delta(t.sessions, p => p.sessions)}</div>`}
      <div class="stat" title="What these tokens would cost at Anthropic's API prices. On a subscription you don't pay this; it shows what the plan is worth."><span class="label">API cost</span><span class="v num">${money(t.api_cost)}</span><span class="n">${t.api_cost_complete ? 'if paid at API prices' : 'some models have no price'}</span>${delta(t.api_cost, p => p.api_cost)}</div>
    </section>
    <section class="panel" aria-labelledby="h-tokens">
      <div class="panel-head">
        <div><h2 id="h-tokens">Token rate</h2><span class="sub">tokens per minute, every ${esc(m.range.bucket)} · spikes are 3× the normal pace or more</span></div>
        <div class="legend">
          <button type="button" aria-pressed="true" disabled><i style="background:var(--paw)"></i>Counted</button>
          <button type="button" data-act="cache" aria-pressed="${state.showCache}"><i style="background:var(--cache)"></i>Cache reads</button>
        </div>
      </div>
      <div class="chart" id="c-tokens"></div>
    </section>
    <div class="grid-2">
      <div style="display:grid;gap:20px;min-width:0;align-content:start">
        <section class="panel" aria-labelledby="h-edits">
          <div class="panel-head"><div><h2 id="h-edits">Lines changed</h2><span class="sub">added above the line, removed below</span></div></div>
          <div class="chart" id="c-edits"></div>
        </section>
        <section class="panel" aria-labelledby="h-limit">
          <div class="panel-head">
            <div><h2 id="h-limit">${esc(limits[0]?.title || 'Usage limits')}</h2><span class="sub">${limits.length > 1 ? 'one line per account; limits never add up' : 'for the whole account, whatever the project filter'}</span></div>
            ${limits.length > 1 ? `<div class="limits-now">${limits.map(l => `<span><i style="background:${l.color}"></i><span class="sub">${esc(l.label)}</span><b class="num">${Math.round(l.latest)}%</b></span>`).join('')}</div>` : ''}
          </div>
          <div class="chart" id="c-limit"></div>
        </section>
      </div>
      <div style="display:grid;gap:20px;align-content:start;min-width:0">
        <section class="panel" aria-labelledby="h-live">
          <div class="panel-head"><h2 id="h-live">Running now</h2><span class="sub">${plural(alive.length, 'session', 'sessions')}</span></div>
          <div class="rows">${running.map(sessionRow).join('') || (alive.length ? '' : '<p class="empty">No Claude session is running right now.</p>')}</div>
          ${quiet ? `<button type="button" class="btn" data-act="idle" style="margin-top:10px">${plural(quiet, 'more idle session', 'more idle sessions')} with nothing in ${range}</button>` : state.showIdle && alive.length ? '<button type="button" class="btn" data-act="idle" style="margin-top:10px">Hide idle sessions</button>' : ''}
        </section>
        <section class="panel" aria-labelledby="h-spikes">
          <div class="panel-head"><h2 id="h-spikes">Spikes</h2><span class="sub">the moments that spent fastest</span></div>
          <div class="rows">${m.spikes.slice(0, 5).map(sp => `
            <div class="row spike-row" data-open-session="${esc(sp.session)}" tabindex="0" role="button">
              <span class="when">${esc(axisLabel(new Date(sp.at)))}</span>
              <span class="what">${sp.prompt ? `“${esc(sp.prompt)}”` : esc(sp.session_name || 'A session')}<small>${esc(sp.session_name)} · ${short(sp.per_minute)}/min</small></span>
              <span class="times">${sp.times_normal.toFixed(1)}×</span>
            </div>`).join('') || `<p class="empty">No spikes in ${range}: spending stayed close to its normal pace.</p>`}</div>
        </section>
        ${shareCard(m)}
      </div>
    </div>`;

  tokenChart(document.getElementById('c-tokens'), pts, {
    height: 250, showCache: state.showCache, spikes: spikeMarks(m), label: axisLabel, tooltip: tokenTip,
    onPick: p => p.session && openSessionById(p.session),
    emptyText: `No tokens spent in ${range}.`,
  });
  editsChart(document.getElementById('c-edits'), pts, {
    label: axisLabel, emptyText: `Claude changed no files in ${range}.`,
    tooltip: p => `<div class="t">${esc(when(p.at.toISOString()))}</div><div class="r"><span>Added</span><b style="color:var(--add)">+${p.add}</b></div><div class="r"><span>Removed</span><b style="color:var(--del)">−${p.del}</b></div>`,
  });
  limitChart(document.getElementById('c-limit'), limits, {
    from: new Date(m.range.from), to: new Date(m.range.to), axis: pts.map(p => p.at), label: axisLabel,
    emptyText: 'No limit readings yet. Whiskers records them while you work; without it this chart stays empty.',
  });
}

const BEFORE = { '1h': 'the hour before', '24h': 'the day before', '7d': 'the week before', '30d': 'the 30 days before' };

// delta compares a total with the same span just before.
function delta(now, pick) {
  if (!state.prev) return '';
  const before = pick(state.prev) || 0;
  const label = BEFORE[state.range];
  if (!before && !now) return '';
  if (!before) return `<span class="d" title="Nothing in ${label}">new · nothing ${label}</span>`;
  const pct = Math.round(((now - before) / before) * 100);
  if (pct === 0) return `<span class="d">same as ${label}</span>`;
  const arrow = pct > 0 ? '↑' : '↓';
  return `<span class="d ${pct > 0 ? 'up' : 'down'}" title="${short(before)} in ${label}">${arrow} ${Math.abs(pct) > 999 ? '999+' : Math.abs(pct)}% vs ${label}</span>`;
}

// limitLines picks the window that suits the range: five hours for short
// ranges, the week for long ones, else whichever has readings.
function limitLines(m) {
  const long = state.range === '7d' || state.range === '30d';
  const want = long ? 'seven_day' : 'five_hour';
  let lines = m.limits.filter(l => l.kind === want);
  if (!lines.length) lines = m.limits.filter(l => l.kind === (long ? 'five_hour' : 'seven_day'));
  if (!lines.length) lines = m.limits.slice(0, 3);
  const title = k => (k === 'five_hour' ? '5-hour limit' : k === 'seven_day' ? 'Weekly limit' : k.replace(/_/g, ' '));
  return lines.map((l, i) => ({
    ...l,
    title: title(l.kind),
    label: l.email ? `${l.email}${l.plan ? ` · ${l.plan}` : ''}` : 'This account',
    color: lines.length > 1 ? SERIES[i % SERIES.length] : 'var(--teal)',
  }));
}

function sessionRow(s) {
  return `<div class="row sess-row" data-open-session="${esc(s.id)}" tabindex="0" role="button">
    <span class="status ${esc(s.status)}" title="${esc(s.status)}"></span>
    <span class="nm">${esc(s.name)}${multiAccount() && s.account ? `<span class="acct" title="${esc(accountEmail(s.account))}">${esc(accountEmail(s.account))}</span>` : ''}<small>${[projectName(s.project_id), prettyModel(s.model), s.status].filter(Boolean).map(esc).join(' · ')}</small></span>
    ${spark(s.series)}
    <span class="rate">${short(s.tokens.counted)}<small>tokens</small></span>
  </div>`;
}

function shareCard(m) {
  const byAccount = !state.account && m.accounts.length > 1;
  const byProject = !state.project && m.projects.length > 1;
  const priced = m.models.filter(x => x.api_cost >= 0.01);
  const byModel = priced.length > 1;
  if (!byAccount && !byProject && !byModel) return '';
  const bar = rows => `<div class="share">${rows.map(r => `<span style="width:${Math.max(0.5, r.share * 100)}%;background:${r.color}"></span>`).join('')}</div>`;
  const list = rows => `<ul class="share-list">${rows.map(r => `<li><i style="background:${r.color}${r.round ? ';border-radius:50%' : ''}"></i><span style="display:inline-flex;gap:8px;align-items:center;min-width:0"><span style="overflow:hidden;text-overflow:ellipsis;white-space:nowrap">${esc(r.label)}</span>${r.badge || ''}</span><span><b>${r.display || short(r.value)}</b><small>${Math.round(r.share * 100)}%</small></span></li>`).join('')}</ul>`;
  let out = '';
  if (byAccount) {
    const rows = m.accounts.map((a, i) => ({ label: a.email || 'Unknown account', badge: planBadge(a.plan), value: a.tokens.counted, share: a.share, color: SERIES[i % SERIES.length], round: true }));
    out += `<span class="label">By account</span>${bar(rows)}${list(rows)}`;
  }
  if (byProject) {
    const rows = m.projects.slice(0, 6).map(p => ({ label: p.name, value: p.tokens.counted, share: p.share, color: colorOf(p.id) }));
    out += `${byAccount ? '<div style="height:16px"></div>' : ''}<span class="label">By project</span>${bar(rows)}${list(rows)}`;
  }
  if (byModel) {
    // Models differ most in price, so this split is by API cost.
    const cost = priced.reduce((sum, x) => sum + x.api_cost, 0) || 1;
    const rows = priced.map((x, i) => ({ label: prettyModel(x.model), value: x.api_cost, display: money(x.api_cost), share: x.api_cost / cost, color: SERIES[(i + 2) % SERIES.length], round: true }));
    out += `${byAccount || byProject ? '<div style="height:16px"></div>' : ''}<span class="label">By model · share of API cost</span>${bar(rows)}${list(rows)}`;
  }
  return `<section class="panel" aria-labelledby="h-share"><div class="panel-head"><h2 id="h-share">Where it went</h2><span class="sub">share of counted tokens</span></div>${out}</section>`;
}

// ---------- Projects ----------

function projects() {
  const m = state.m;
  if (!m) { app.innerHTML = `${banners()}<p class="loading">Loading…</p>`; return; }
  const range = RANGES[state.range].label;
  app.innerHTML = `
    ${head('Projects', `How ${range} split across projects. Pick one to narrow every screen to it.`, { projects: false })}
    ${banners()}
    <section class="panel">
      ${m.projects.length ? `<div class="table-wrap"><table>
        <thead><tr><th>Project</th>${multiAccount() ? '<th>Accounts</th>' : ''}<th class="r">Counted tokens</th><th>Share</th><th class="r">API cost</th><th class="r">Edits</th><th class="r">Lines</th><th>Pace</th></tr></thead>
        <tbody>${m.projects.map(p => `
          <tr class="click" data-project-row="${esc(p.id)}" tabindex="0" aria-current="${state.project === p.id}">
            <td><span class="pname">${esc(p.name)}<small>${esc(p.root)}</small></span></td>
            ${multiAccount() ? `<td>${p.accounts.map(a => `<span class="acct" style="margin:0 4px 0 0" title="${esc(accountEmail(a))}">${esc(accountEmail(a))}</span>`).join('')}</td>` : ''}
            <td class="r num">${short(p.tokens.counted)}</td>
            <td><span class="meter"><i style="width:${Math.max(2, p.share * 100)}%;background:${colorOf(p.id)}"></i></span> <span class="num muted" style="font-size:0.78rem">${Math.round(p.share * 100)}%</span></td>
            <td class="r num">${money(p.api_cost)}</td>
            <td class="r num">${p.edits}</td>
            <td class="r num"><span class="add">+${p.lines_added}</span> <span class="del">−${p.lines_removed}</span></td>
            <td>${spark(p.series)}</td>
          </tr>`).join('')}</tbody>
      </table></div>` : `<p class="empty">No project had activity in ${range}. Try a longer range.</p>`}
    </section>`;
}

// ---------- Sessions ----------

// currentRow is the picked session's row in the report.
function currentRow() {
  const cur = state.session;
  return cur ? sessionList().find(s => s.id === cur.id && s.project_id === cur.project_id) : null;
}

function sessionList() {
  return state.m ? state.m.sessions : [];
}

function sessions() {
  const m = state.m;
  if (!m) { app.innerHTML = `${banners()}<p class="loading">Loading…</p>`; return; }
  const list = sessionList();
  const range = RANGES[state.range].label;
  const headHtml = head('Sessions', `Sessions active in ${range}, their prompts, and the files each prompt changed. Pick a file to see what changed.`);
  if (!list.length) {
    app.innerHTML = `${headHtml}${banners()}<section class="panel"><p class="empty">No sessions${state.project ? ` in ${esc(projectName(state.project) || 'this project')}` : ''} in ${range}. Try a longer range or another project.</p></section>`;
    return;
  }
  const cur = state.session;
  let row = cur && list.find(s => s.id === cur.id && s.project_id === cur.project_id);
  // Sessions that are open but did nothing in the range wait behind a button.
  const busy = s => s.tokens.counted > 0 || s.edits > 0 || s.turns > 0 || s.status === 'busy' || s.status === 'waiting';
  const q = state.sessQuery.trim().toLowerCase();
  const matches = s => !q || [s.name, projectName(s.project_id), s.last_prompt, prettyModel(s.model), accountEmail(s.account)].some(v => (v || '').toLowerCase().includes(q));
  const found = list.filter(matches);
  // A search looks through every session, the quiet ones included.
  const shown = sortSessions(q || state.showQuiet ? found : found.filter(s => busy(s) || (row && s.id === row.id)));
  const quiet = q ? 0 : found.length - shown.length;
  // A search that leaves out the picked session shows its first match instead.
  if (q && shown.length && !(row && shown.includes(row))) {
    pickSession(shown[0], false);
    row = shown[0];
    loadSession().then(render);
  }
  app.innerHTML = `
    ${headHtml}
    ${banners()}
    <div class="browser">
      <div class="slist">
        <div class="list-tools">
          <input type="search" id="sess-search" class="find" placeholder="Find a session or prompt" aria-label="Find a session or prompt" value="${esc(state.sessQuery)}" autocomplete="off" spellcheck="false">
          ${sortPicker('sess', [['recent', 'Recent first'], ['tokens', 'Most tokens'], ['edits', 'Most edits']], state.sessSort)}
        </div>
        ${q && !shown.length ? `<p class="empty">No session matches “${esc(state.sessQuery)}”.</p>` : ''}
        <div class="slist" role="list">${shown.map(s => `
        <button type="button" class="scard" role="listitem" data-pick-session="${esc(s.id)}" data-project="${esc(s.project_id)}" aria-current="${!!row && row.id === s.id && row.project_id === s.project_id}">
          <span class="status ${esc(s.status)}" title="${esc(s.status)}"></span>
          <span class="nm">${esc(s.name)}${multiAccount() && s.account ? `<span class="acct" title="${esc(accountEmail(s.account))}">${esc(accountEmail(s.account))}</span>` : ''}<small>${[projectName(s.project_id), prettyModel(s.model), plural(s.turns, 'prompt', 'prompts')].filter(Boolean).map(esc).join(' · ')}</small></span>
          <span class="rate">${short(s.tokens.counted)}<small>${esc(ago(s.last_activity))}</small></span>
        </button>`).join('')}
        ${quiet ? `<button type="button" class="btn more" style="margin-left:0" data-act="quiet">${plural(quiet, 'more open session', 'more open sessions')} with nothing in ${range}</button>` : state.showQuiet && !q ? '<button type="button" class="btn more" style="margin-left:0" data-act="quiet">Hide quiet sessions</button>' : ''}
        </div>
      </div>
      <div style="display:grid;gap:20px;min-width:0">${row ? sessionDetail(row) : '<section class="panel"><p class="empty">Pick a session on the left.</p></section>'}</div>
    </div>`;
  if (row && state.sess) {
    const pts = points(state.sess.m.timeline);
    tokenChart(document.getElementById('c-session'), pts, {
      height: 190, bands: turnBands(state.sess.turns, state.sess.m.timeline), label: axisLabel, tooltip: tokenTip,
      emptyText: `This session spent no tokens in ${range}.`,
    });
  }
}

function sortSessions(list) {
  const by = {
    recent: (a, b) => (b.alive - a.alive) || String(b.last_activity).localeCompare(String(a.last_activity)),
    tokens: (a, b) => b.tokens.counted - a.tokens.counted,
    edits: (a, b) => b.edits - a.edits || b.lines_added - a.lines_added,
  }[state.sessSort] || (() => 0);
  return [...list].sort(by);
}

function turnBands(turns, timeline) {
  if (!timeline.length) return [];
  const starts = timeline.map(p => new Date(p.at).getTime());
  const idx = t => {
    let i = 0;
    while (i + 1 < starts.length && starts[i + 1] <= t) i++;
    return t < starts[0] ? -1 : i;
  };
  const bands = [];
  turns.forEach((t, k) => {
    const from = idx(new Date(t.at).getTime());
    const next = turns[k + 1] ? idx(new Date(turns[k + 1].at).getTime()) : starts.length - 1;
    if (from < 0 && next < 0) return;
    bands.push({ from: Math.max(0, from), to: Math.max(0, next), label: `#${k + 1}` });
  });
  return bands;
}

function sessionDetail(row) {
  const acct = accountInfo(row.account);
  const sub = [
    acct ? `${esc(acct.email)} ${planBadge(acct.plan)}` : '',
    esc(projectName(row.project_id)),
    esc(prettyModel(row.model)),
    row.started_at ? `started ${esc(when(row.started_at))}` : '',
    `${money(row.api_cost)} at API prices`,
    plural(row.edits, 'edit', 'edits'),
    `<span class="add">+${row.lines_added}</span> <span class="del">−${row.lines_removed}</span>`,
  ].filter(Boolean).join(' · ');
  const s = state.sess;
  let body;
  if (state.sessError) body = `<div class="banner err"><span>${esc(state.sessError)}</span><button type="button" class="btn" data-act="retry">Try again</button></div>`;
  else if (!s) body = '<p class="loading">Loading the session…</p>';
  else body = promptsPanel(row, s);
  return `
    <section class="panel">
      <div class="panel-head"><div><h2>${esc(row.name)}</h2><span class="sub">${sub}</span></div><span class="sub mono">${esc(RANGES[state.range].label)} · bands are prompts</span></div>
      <div class="chart" id="c-session">${s ? '' : '<p class="loading">Loading…</p>'}</div>
    </section>
    ${body}`;
}

// groupFiles turns a prompt's events into files: each file once, from the
// first edit to the last, Claude's own first, then subagents', then the
// person's.
function groupFiles(events, names = {}) {
  const byKey = new Map();
  [...events].reverse().forEach(ev => {
    // Each subagent is its own group, even when two share a type.
    const group = ev.kind === 'external' ? 'outside' : ev.agent_id ? `agent:${ev.agent_id}` : 'claude';
    const key = `${group}|${ev.path}`;
    let f = byKey.get(key);
    if (!f) {
      const agentName = ev.agent_id ? names[ev.agent_id] || `${ev.agent_type || 'subagent'} agent` : '';
      f = { key, path: ev.path, group, agentName, events: [], add: 0, del: 0 };
      byKey.set(key, f);
    }
    f.events.push(ev);
    f.add += ev.lines_added || 0;
    f.del += ev.lines_removed || 0;
  });
  const order = g => (g === 'claude' ? 0 : g === 'outside' ? 2 : 1);
  return [...byKey.values()].sort((a, b) => order(a.group) - order(b.group) || a.path.localeCompare(b.path));
}

function promptsPanel(row, s) {
  const turns = [...s.turns].reverse();
  const byTurn = new Map();
  s.events.forEach(ev => {
    if (!byTurn.has(ev.turn)) byTurn.set(ev.turn, []);
    byTurn.get(ev.turn).push(ev);
  });
  if (!turns.length) return '<section class="panel"><p class="empty">This session has no recorded prompts.</p></section>';
  return `<section class="panel">
    <div class="panel-head"><h2>Prompts</h2><span class="sub">newest first · ${plural(turns.length, 'prompt', 'prompts')}</span></div>
    <div class="turns">${turns.map(t => {
      const open = state.openTurn === t.id;
      const files = groupFiles(byTurn.get(t.id) || [], row.agent_names || {});
      const claudeFiles = files.filter(f => f.group !== 'outside');
      return `<div class="turn" data-open="${open}">
        <button type="button" data-turn="${esc(t.id)}" aria-expanded="${open}">
          ${ICONS.right}
          <span class="at">${esc(hm(new Date(t.at)))}</span>
          <span class="prompt">${esc(t.text || '(prompt not recorded)')}</span>
          <span class="tmeta"><span>${short(t.tokens || 0)} tok</span><span title="What the prompt would cost at API prices">${money(t.api_cost || 0)}</span><span>${plural(claudeFiles.length, 'file', 'files')}</span></span>
        </button>
        ${open ? `<div class="files">${renderFiles(files, row, t)}</div>
          ${claudeFiles.length ? `<div class="turn-actions"><button type="button" class="btn danger" data-undo-turn="${esc(t.id)}">Undo this prompt…</button></div>` : ''}
          ${state.confirm && state.confirm.turn === t.id ? `<div class="turn-confirm">${confirmPanel()}</div>` : ''}` : ''}
      </div>`;
    }).join('')}</div>
  </section>`;
}

const FILES_SHOWN = 12;

// renderFiles lists a prompt's files in sections: Claude's in the project,
// each subagent's, files outside the project, and changes made outside
// Claude. Long sections show their first files until expanded.
function renderFiles(files, row, turn) {
  if (!files.length) return '<p class="empty" style="padding:6px 8px">No files changed in this prompt.</p>';
  const outside = f => f.path.startsWith('/');
  const sections = [{ id: 'main', name: '', items: files.filter(f => f.group === 'claude' && !outside(f)) }];
  const agents = [...new Set(files.filter(f => f.group.startsWith('agent:')).map(f => f.group))];
  agents.forEach(g => {
    const items = files.filter(f => f.group === g);
    sections.push({ id: g, name: `Subagent · ${items[0].agentName}`, items });
  });
  sections.push({ id: 'abs', name: 'Outside the project', items: files.filter(f => f.group === 'claude' && outside(f)), folded: true });
  sections.push({ id: 'person', name: 'Outside Claude', items: files.filter(f => f.group === 'outside') });
  const row1 = f => {
    const sel = state.file && state.file.key === f.key && state.file.turn === turn.id;
    return `<button type="button" class="file" data-file="${esc(f.key)}" data-turn-of="${esc(turn.id)}" aria-current="${!!sel}">
      ${f.group === 'outside' ? ICONS.person : f.group === 'claude' ? ICONS.file : ICONS.robot}
      <span class="path" title="${esc(f.path)}">${splitPath(f.path)}${f.events.length > 1 ? `<span class="who">${f.events.length} edits</span>` : ''}</span>
      <span class="num" style="font-size:0.78rem"><span class="add">+${f.add}</span> <span class="del">−${f.del}</span></span>
    </button>`;
  };
  let out = '';
  sections.forEach(sec => {
    if (!sec.items.length) return;
    const key = `${turn.id}:${sec.id}`;
    const open = state.expanded.has(key);
    if (sec.name) out += `<div class="group">${esc(sec.name)} · ${sec.items.length}</div>`;
    if (sec.folded && !open) {
      out += `<button type="button" class="btn more" data-expand="${esc(key)}">Show ${plural(sec.items.length, 'file', 'files')} outside the project</button>`;
      return;
    }
    const shown = open || sec.items.length <= FILES_SHOWN + 2 ? sec.items : sec.items.slice(0, FILES_SHOWN);
    out += shown.map(row1).join('');
    if (shown.length < sec.items.length) out += `<button type="button" class="btn more" data-expand="${esc(key)}">Show all ${sec.items.length} files</button>`;
    else if (open && sec.items.length > FILES_SHOWN) out += `<button type="button" class="btn more" data-expand="${esc(key)}">Show fewer</button>`;
  });
  return out;
}

// ---------- Diffs and actions ----------

function parseDiff(text) {
  const rows = [];
  let o = 0, n = 0;
  for (const line of text.split('\n')) {
    if (line.startsWith('---') || line.startsWith('+++') || line === '') continue;
    if (line.startsWith('@@')) {
      const m = /@@ -(\d+)(?:,\d+)? \+(\d+)/.exec(line);
      if (m) { o = Number(m[1]) - 1; n = Number(m[2]) - 1; }
      rows.push(`<tr class="h"><td class="ln"></td><td class="ln"></td><td class="sg"></td><td>${esc(line)}</td></tr>`);
    } else if (line.startsWith('+')) {
      n++;
      rows.push(`<tr class="a"><td class="ln"></td><td class="ln">${n}</td><td class="sg">+</td><td>${esc(line.slice(1))}</td></tr>`);
    } else if (line.startsWith('-')) {
      o++;
      rows.push(`<tr class="d"><td class="ln">${o}</td><td class="ln"></td><td class="sg">−</td><td>${esc(line.slice(1))}</td></tr>`);
    } else {
      o++; n++;
      rows.push(`<tr><td class="ln">${o}</td><td class="ln">${n}</td><td class="sg"></td><td>${esc(line.slice(1))}</td></tr>`);
    }
  }
  return rows.join('');
}

const projectRoot = id => [...(state.pickerProjects || []), ...(state.m?.projects || [])].find(p => p.id === id)?.root || '';

// editorLink opens the file in VS Code; a path outside the project is already
// absolute.
function editorLink(f) {
  const root = projectRoot(f.project_id);
  const abs = f.path.startsWith('/') ? f.path : root ? `${root}/${f.path}` : '';
  if (!abs) return '';
  return `<a class="btn" href="vscode://file${encodeURI(abs)}" title="Open ${esc(abs)} in VS Code">Open in editor</a>`;
}

// ---------- The file drawer ----------

// The drawer shows one file beside the page: the change a prompt made, or
// every version of the file with the picked one's change next to the list.
const drawerOpen = () => !!(state.file || state.fileView);
// drawerFrom finds the element that opened the drawer, to focus it again on close.
let drawerFrom = '';

function closeDrawer() {
  state.file = null; state.fileView = null; state.diff = null; state.confirm = null;
  render();
}

function changeLabel(f) {
  return f.label || (f.events.length > 1 ? `${f.events.length} edits, shown as one change` : f.last.tool ? `${f.last.tool} · ${when(f.last.ts)}` : when(f.last.ts));
}

function drawerHtml() {
  const f = state.file, fv = state.fileView;
  const path = fv ? fv.path : f.path;
  const sub = fv ? (fv.versions ? `${plural(fv.versions.length, 'version', 'versions')}, newest first` : '') : changeLabel(f);
  return `<div class="drawer-backdrop" data-act="close-drawer"></div>
  <aside class="drawer${fv ? ' wide' : ''}" role="dialog" aria-modal="true" aria-labelledby="drawer-title">
    <header class="drawer-head">
      <div class="drawer-title">
        <h2 id="drawer-title" class="path" title="${esc(path)}">${splitPath(path)}</h2>
        <span class="sub">${[projectName(fv ? fv.project_id : f.project_id), sub].filter(Boolean).map(esc).join(' · ')}</span>
        ${!fv && f.prompt ? `<p class="drawer-prompt">${esc(f.prompt)}</p>` : ''}
      </div>
      <button type="button" class="btn icon-only" data-act="close-drawer" aria-label="Close" title="Close (Esc)">${ICONS.close}</button>
    </header>
    ${fv ? `<div class="drawer-split">
      <div class="drawer-versions" data-scroll="versions">${versionList(fv)}</div>
      <div class="drawer-main">${f ? changeView(f) : ''}</div>
    </div>` : `<div class="drawer-main">${changeView(f)}</div>`}
  </aside>`;
}

function changeView(f) {
  const d = state.diff;
  const pinned = f.last.pinned;
  let body;
  if (!d) body = '<p class="loading" style="padding:12px">Loading the change…</p>';
  else if (d.error) body = `<p class="empty" style="padding:12px">${esc(d.error)}</p>`;
  else if (!d.text.trim()) body = '<p class="empty" style="padding:12px">No difference: the file ended the way it started.</p>';
  else body = `<div class="code"><table>${parseDiff(d.text)}</table></div>`;
  return `<div class="drawer-bar">
      ${state.fileView ? `<span class="change-label">${esc(changeLabel(f))}</span>` : ''}
      <span class="actions">
        ${state.fileView ? '' : `<button type="button" class="btn" data-act="all-versions" title="Every version of this file">${ICONS.history}All versions</button>`}
        ${editorLink(f)}
        <button type="button" class="btn" data-act="current" title="What changed since this version">Compare with current</button>
        <button type="button" class="btn" data-act="copy">Copy</button>
        <button type="button" class="btn" data-act="ref" title="Paste into a Claude prompt; Pawprints resolves it">Copy reference</button>
        <button type="button" class="btn" data-act="${pinned ? 'unpin' : 'pin'}" title="Pinned versions are never cleaned up">${pinned ? 'Unpin' : 'Pin'}</button>
        <button type="button" class="btn danger" data-act="restore">${f.restoreLabel || 'Restore before this prompt…'}</button>
      </span>
    </div>
    ${state.confirm && !state.confirm.turn ? confirmPanel() : ''}
    <div class="drawer-diff" data-scroll="diff" tabindex="0" aria-label="The change">${body}</div>`;
}

function versionList(fv) {
  if (fv.error) return `<p class="empty">${esc(fv.error)}</p>`;
  if (!fv.versions) return '<p class="loading">Loading versions…</p>';
  if (!fv.versions.length) return '<p class="empty">No recorded versions.</p>';
  return `<div class="versions">${fv.versions.map(v => `
    <button type="button" class="version" data-version="${esc(v.id)}" aria-current="${fv.selected === v.id}">
      <span class="when">${esc(when(v.ts))}</span>
      <span class="what">${esc(v.kind === 'external' ? 'Outside Claude' : v.agent_type ? `Subagent · ${v.agent_type}` : v.tool || 'edit')}<small>${esc(v.session ? v.session.slice(0, 8) : '')}</small>${v.pinned ? '<span class="pinmark">● pinned</span>' : ''}</span>
      <span class="num" style="font-size:0.78rem"><span class="add">+${v.lines_added || 0}</span> <span class="del">−${v.lines_removed || 0}</span></span>
    </button>`).join('')}</div>`;
}

// renderDrawer redraws the drawer, keeping its scroll and focus. It slides
// in only when it opens.
function renderDrawer() {
  const open = drawerOpen();
  document.body.classList.toggle('has-drawer', open);
  if (!open) {
    if (drawerEl.firstChild) {
      drawerEl.innerHTML = '';
      delete drawerEl.dataset.change;
      if (drawerFrom) document.querySelector(drawerFrom)?.focus({ preventScroll: true });
    }
    return;
  }
  const opening = !drawerEl.firstChild;
  const change = state.file ? `${state.file.key}|${changeLabel(state.file)}` : '';
  const sameChange = drawerEl.dataset.change === change;
  const scroll = {};
  drawerEl.querySelectorAll('[data-scroll]').forEach(el => { scroll[el.dataset.scroll] = [el.scrollTop, el.scrollLeft]; });
  const active = drawerEl.contains(document.activeElement) ? document.activeElement : null;
  const focusKey = active?.dataset.act ? `[data-act="${active.dataset.act}"]` : active?.dataset.version ? `[data-version="${CSS.escape(active.dataset.version)}"]` : '';
  drawerEl.innerHTML = drawerHtml();
  drawerEl.dataset.change = change;
  drawerEl.querySelectorAll('[data-scroll]').forEach(el => {
    const at = scroll[el.dataset.scroll];
    if (at && (el.dataset.scroll !== 'diff' || sameChange)) [el.scrollTop, el.scrollLeft] = at;
  });
  if (opening) {
    drawerEl.querySelector('.drawer').classList.add('in');
    drawerEl.querySelector('.drawer-backdrop').classList.add('in');
    drawerEl.querySelector('[data-act="close-drawer"].btn').focus({ preventScroll: true });
  } else if (focusKey) {
    (drawerEl.querySelector(focusKey) || drawerEl.querySelector('[data-act="close-drawer"].btn')).focus({ preventScroll: true });
  }
}

// Tab stays inside the open drawer.
function trapFocus(ev) {
  const items = [...drawerEl.querySelectorAll('button, a[href], [tabindex="0"]')].filter(el => !el.disabled && el.offsetParent !== null);
  if (!items.length) return;
  const first = items[0], last = items[items.length - 1];
  if (!drawerEl.contains(document.activeElement)) { ev.preventDefault(); first.focus(); return; }
  if (ev.shiftKey && document.activeElement === first) { ev.preventDefault(); last.focus(); }
  else if (!ev.shiftKey && document.activeElement === last) { ev.preventDefault(); first.focus(); }
}

function confirmPanel() {
  const c = state.confirm;
  if (!c) return '';
  if (c.error) {
    return `<div class="confirm err" role="alert"><span class="what"><b>${esc(c.error)}</b>${c.conflict ? '<small>Merge keeps both your change and the restored lines, marking any clash; overwrite replaces the file.</small>' : ''}</span>
      <span class="actions"><button type="button" class="btn" data-act="cancel">Cancel</button>${c.conflict ? '<button type="button" class="btn" data-act="apply-merge">Merge</button><button type="button" class="btn danger" data-act="apply-force">Overwrite</button>' : ''}</span></div>`;
  }
  if (!c.plan) return '<div class="confirm"><span class="loading" style="padding:0">Checking what would change…</span></div>';
  const changes = c.plan.changes || [];
  const files = changes.map(ch => ch.path);
  return `<div class="confirm" role="alertdialog" aria-label="${esc(c.title)}">
    <span class="what"><b>${esc(c.title)}</b><small>${changes.length ? `${plural(changes.length, 'file', 'files')} would change: ${files.slice(0, 4).map(esc).join(', ')}${files.length > 4 ? '…' : ''}` : 'Nothing to change: the files already match.'}${changes.some(ch => ch.modified) ? ' Someone edited one of them since Claude did.' : ''}</small></span>
    <span class="actions"><button type="button" class="btn" data-act="cancel">Cancel</button>${changes.length ? `<button type="button" class="btn primary" data-act="apply">${esc(c.verb)}</button>` : ''}</span>
  </div>`;
}

async function startConfirm(c) {
  state.confirm = { ...c, plan: null };
  render();
  try {
    const plan = await post(c.path, { ...c.body, apply: false });
    if (state.confirm && state.confirm.path === c.path) state.confirm.plan = plan;
  } catch (err) {
    if (state.confirm) state.confirm = { ...state.confirm, error: conflictText(err), conflict: err.status === 409 };
  }
  render();
}

function conflictText(err) {
  if (err.status === 409) return 'The file changed after Claude last edited it.';
  return err.message;
}

async function applyConfirm(mode) {
  const c = state.confirm;
  if (!c) return;
  try {
    const res = await post(c.path, { ...c.body, apply: true, mode });
    state.confirm = null;
    const conflicts = (res.changes || []).reduce((n, ch) => n + (ch.conflicts || 0), 0);
    toast(conflicts ? `${c.done}, with ${plural(conflicts, 'conflict', 'conflicts')} marked in the file` : c.done);
    await refreshAll();
    if (state.file) { await loadDiff(); render(); }
  } catch (err) {
    state.confirm = { ...c, error: conflictText(err), conflict: err.status === 409 };
    render();
  }
}

async function copyText(text, what) {
  try {
    await navigator.clipboard.writeText(text);
    toast(`${what} copied`);
  } catch {
    toast('The browser blocked copying here');
  }
}

async function fileAction(act) {
  const f = state.file;
  if (!f) return;
  const project = f.project_id;
  switch (act) {
    case 'current':
      state.diff = null;
      render();
      try {
        const d = await api('/api/diff', { project, event: f.last.id, against: 'current' });
        state.diff = { text: d.diff || '', stats: d.stats || {} };
        f.label = 'this version → the file on disk now';
      } catch (err) { state.diff = { error: err.message }; }
      render();
      return;
    case 'copy':
      try {
        const v = await api('/api/show', { project, event: f.last.id });
        await copyText(v.after || '', 'Version');
      } catch (err) { toast(err.message); }
      return;
    case 'ref':
      await copyText(`pawprints:${f.last.id} ${f.path}`, 'Reference');
      return;
    case 'pin':
    case 'unpin':
      try {
        await post(`/api/${act}`, { project, event: f.last.id, message: f.prompt ? `from "${f.prompt.slice(0, 60)}"` : 'pinned in pawprints ui' });
        f.last.pinned = act === 'pin';
        toast(act === 'pin' ? 'Pinned: this version is kept forever' : 'Unpinned');
        render();
      } catch (err) { toast(err.message); }
      return;
    case 'restore':
      startConfirm(f.restore);
      return;
  }
}

function selectFile(f, extra) {
  state.file = { ...f, ...extra };
  state.diff = null;
  state.confirm = null;
  render();
  loadDiff().then(render);
}

// ---------- Files ----------

function files() {
  const m = state.m;
  if (!m) { app.innerHTML = `${banners()}<p class="loading">Loading…</p>`; return; }
  const range = RANGES[state.range].label;
  const fv = state.fileView;
  const { rows, outside, showOutside, q } = fileRows(m);
  app.innerHTML = `
    ${head('Files', `The files Claude changed most in ${range}. Pick one to see every version.`)}
    ${banners()}
    <section class="panel">
      <div class="list-tools">
        <input type="search" id="files-search" class="find" placeholder="Find a file" aria-label="Find a file" value="${esc(state.fileQuery)}" autocomplete="off" spellcheck="false">
        ${sortPicker('files', [['edits', 'Most edits'], ['lines', 'Most lines changed'], ['recent', 'Changed last']], state.fileSort)}
      </div>
      ${rows.length ? `<div class="table-wrap"><table>
        <thead><tr><th>File</th>${state.project ? '' : '<th>Project</th>'}<th class="r">Edits</th><th class="r">Lines</th><th class="r">Sessions</th><th>When</th><th class="r">Last change</th></tr></thead>
        <tbody>${rows.map(f => {
          const max = Math.max(1, ...f.activity);
          return `<tr class="click" data-file-row="${esc(f.path)}" data-project="${esc(f.project_id)}" tabindex="0" aria-current="${!!fv && fv.path === f.path && fv.project_id === f.project_id}">
            <td class="path clip" title="${esc(f.path)}">${splitPath(f.path)}</td>
            ${state.project ? '' : `<td class="muted">${esc(projectName(f.project_id))}</td>`}
            <td class="r num">${f.edits}</td>
            <td class="r num"><span class="add">+${f.lines_added}</span> <span class="del">−${f.lines_removed}</span></td>
            <td class="r num">${f.sessions}</td>
            <td><span class="heat" aria-hidden="true">${f.activity.map(v => `<i style="background:${v ? `color-mix(in srgb, var(--paw) ${Math.round(25 + (v / max) * 75)}%, var(--grid))` : 'var(--grid)'}"></i>`).join('')}</span></td>
            <td class="r num muted">${esc(ago(f.last_at))}</td>
          </tr>`;
        }).join('')}
        ${outside.length && !showOutside ? `<tr class="click" data-act="outside-files" tabindex="0"><td colspan="${state.project ? 6 : 7}" class="muted">${plural(outside.length, 'file', 'files')} outside your projects, such as temporary files · <b>Show</b></td></tr>` : ''}
        ${outside.length && showOutside && !q ? `<tr class="click" data-act="outside-files" tabindex="0"><td colspan="${state.project ? 6 : 7}" class="muted">Hide the files outside your projects</td></tr>` : ''}
        </tbody>
      </table></div>` : `<p class="empty">${q ? `No file matches “${esc(state.fileQuery)}”.` : `Claude changed no files in ${range}.`}</p>`}
    </section>`;
}

// Files outside every project (a temporary folder, the home folder) have
// absolute paths; they wait behind one row so they don't crowd the list.
function fileRows(m) {
  const q = state.fileQuery.trim().toLowerCase();
  const by = {
    edits: (a, b) => b.edits - a.edits,
    lines: (a, b) => (b.lines_added + b.lines_removed) - (a.lines_added + a.lines_removed),
    recent: (a, b) => String(b.last_at).localeCompare(String(a.last_at)),
  }[state.fileSort] || (() => 0);
  const all = [...m.top_files].filter(f => !q || f.path.toLowerCase().includes(q) || projectName(f.project_id).toLowerCase().includes(q)).sort(by);
  const outside = all.filter(f => f.path.startsWith('/'));
  const showOutside = !!q || state.showOutsideFiles;
  return { rows: showOutside ? all : all.filter(f => !f.path.startsWith('/')), outside, showOutside, q };
}

// ---------- Rendering and events ----------

function render() {
  document.querySelectorAll('.tabs a').forEach(a => a.setAttribute('aria-current', a.dataset.view === state.view ? 'page' : 'false'));
  document.querySelectorAll('#range button').forEach(b => b.setAttribute('aria-pressed', String(b.dataset.range === state.range)));
  const live = state.m ? state.m.totals.live_sessions : 0;
  const count = document.getElementById('live-count');
  count.hidden = !live;
  count.textContent = live ? String(live) : '';
  count.title = live ? `${live} running` : '';
  tipEl.classList.remove('show');
  const scrollY = window.scrollY;
  // A search box keeps its focus and caret across redraws.
  const active = document.activeElement;
  const focusId = active && active.id && app.contains(active) ? active.id : '';
  const caret = focusId && typeof active.selectionStart === 'number' ? active.selectionStart : null;
  ({ overview, projects, sessions, files })[state.view]();
  app.insertAdjacentHTML('beforeend', `<footer class="keys">Keys: <kbd>1</kbd>–<kbd>4</kbd> screens · <kbd>/</kbd> search · <kbd>[</kbd> <kbd>]</kbd> time range · <kbd>Esc</kbd> close</footer>`);
  window.scrollTo({ top: scrollY });
  if (focusId) {
    const el = document.getElementById(focusId);
    if (el) {
      el.focus();
      if (caret !== null && typeof el.setSelectionRange === 'function') el.setSelectionRange(caret, caret);
    }
  }
  renderDrawer();
  syncURL();
}

function navigate(view) {
  if (view !== state.view) {
    pushNext = true;
    state.file = null; state.fileView = null; state.diff = null; state.confirm = null;
  }
  state.view = view;
  render();
  window.scrollTo({ top: 0 });
  if (view === 'sessions') {
    const list = sessionList();
    if (!state.session && list.length) pickSession(list[0], false);
    if (state.session && !state.sess) loadSession().then(render);
  }
}

window.addEventListener('hashchange', () => {
  const v = location.hash.slice(1);
  if (VIEWS.includes(v) && v !== state.view) { state.view = v; render(); }
});

document.getElementById('range').addEventListener('click', ev => {
  const b = ev.target.closest('button[data-range]');
  if (!b || b.dataset.range === state.range) return;
  setRange(b.dataset.range);
});

function setRange(range) {
  pushNext = true;
  state.range = range;
  remember('range', range);
  state.m = null;
  state.sess = null;
  render();
  refreshAll();
}

async function activate(ev) {
  const t = ev.target;
  // A pick by the person is a step the back button can undo.
  if (t.closest('[data-pick-account],[data-pick-project],[data-project-row],[data-pick-session],[data-open-session],[data-file-row],[data-version],[data-set-range]')) pushNext = true;
  const pickSort = t.closest('[data-pick-sort]');
  if (pickSort) {
    const kind = pickSort.dataset.pickSort;
    if (kind === 'sess') { state.sessSort = pickSort.dataset.value; remember('sessSort', state.sessSort); }
    else { state.fileSort = pickSort.dataset.value; remember('fileSort', state.fileSort); }
    setMenu(null, false);
    render();
    document.getElementById(`${kind}-sort`)?.focus();
    return;
  }
  const ddBtn = t.closest('[data-dd-btn]');
  if (ddBtn) {
    const dd = ddBtn.closest('[data-dd]');
    setMenu(dd, dd.querySelector('[data-dd-menu]').hidden);
    return;
  }
  const pickAcct = t.closest('[data-pick-account]');
  if (pickAcct) {
    state.account = pickAcct.dataset.pickAccount;
    remember('account', state.account);
    state.session = null; state.sess = null; state.file = null; state.fileView = null;
    setMenu(null, false);
    await refreshAll();
    return;
  }
  const pickProject = t.closest('[data-pick-project]');
  if (pickProject) {
    setMenu(null, false);
    state.project = pickProject.dataset.pickProject;
    remember('project', state.project);
    state.session = null; state.sess = null; state.file = null; state.fileView = null;
    await refreshAll();
    return;
  }
  const projectRow = t.closest('[data-project-row]');
  if (projectRow) {
    state.project = projectRow.dataset.projectRow;
    remember('project', state.project);
    toast(`Showing ${projectName(state.project) || 'one project'} only`);
    state.view = 'overview';
    history.replaceState(null, '', '#overview');
    await refreshAll();
    return;
  }
  const setR = t.closest('[data-set-range]');
  if (setR) { setRange(setR.dataset.setRange); return; }
  const openSess = t.closest('[data-open-session]');
  if (openSess) { openSessionById(openSess.dataset.openSession); return; }
  const pickSess = t.closest('[data-pick-session]');
  if (pickSess) {
    const row = sessionList().find(s => s.id === pickSess.dataset.pickSession && s.project_id === pickSess.dataset.project);
    if (row) { pickSession(row, false); render(); await loadSession(); render(); }
    return;
  }
  const turn = t.closest('[data-turn]');
  if (turn) {
    state.openTurn = state.openTurn === turn.dataset.turn ? null : turn.dataset.turn;
    state.file = null; state.diff = null; state.confirm = null;
    render();
    return;
  }
  const undo = t.closest('[data-undo-turn]');
  if (undo && state.session) {
    const tt = state.sess.turns.find(x => x.id === undo.dataset.undoTurn);
    // The confirmation sits under the prompt.
    startConfirm({
      turn: undo.dataset.undoTurn, path: '/api/undo', body: { project: state.session.project_id, session: state.session.id, turn: undo.dataset.undoTurn },
      title: `Undo everything Claude changed in “${(tt?.text || 'this prompt').slice(0, 70)}”`, verb: 'Undo prompt', done: 'Prompt undone',
    });
    return;
  }
  const file = t.closest('[data-file]');
  if (file && state.sess) {
    const turnId = file.dataset.turnOf;
    const tt = state.sess.turns.find(x => x.id === turnId);
    const f = groupFiles(state.sess.events.filter(e => e.turn === turnId), currentRow()?.agent_names || {}).find(x => x.key === file.dataset.file);
    if (!f) return;
    drawerFrom = `[data-file="${CSS.escape(f.key)}"][data-turn-of="${CSS.escape(turnId)}"]`;
    state.fileView = null;
    selectFile(fileFor(f, turnId, tt), {});
    return;
  }
  const fileRow = t.closest('[data-file-row]');
  if (fileRow) {
    state.fileView = { path: fileRow.dataset.fileRow, project_id: fileRow.dataset.project, versions: null, selected: null };
    drawerFrom = `[data-file-row="${CSS.escape(fileRow.dataset.fileRow)}"][data-project="${CSS.escape(fileRow.dataset.project)}"]`;
    state.file = null; state.diff = null; state.confirm = null;
    render();
    await loadFileHistory();
    selectVersion(state.fileView?.selected);
    return;
  }
  const expand = t.closest('[data-expand]');
  if (expand) {
    const k = expand.dataset.expand;
    if (state.expanded.has(k)) state.expanded.delete(k); else state.expanded.add(k);
    render();
    return;
  }
  const version = t.closest('[data-version]');
  if (version) { selectVersion(version.dataset.version); return; }
  const act = t.closest('[data-act]');
  if (!act) return;
  switch (act.dataset.act) {
    case 'retry': state.error = ''; await refreshAll(); return;
    case 'cache': state.showCache = !state.showCache; render(); return;
    case 'idle': state.showIdle = !state.showIdle; render(); return;
    case 'quiet': state.showQuiet = !state.showQuiet; render(); return;
    case 'outside-files': state.showOutsideFiles = !state.showOutsideFiles; render(); return;
    case 'backfill':
      state.backfilling = true;
      render();
      try {
        const res = await post('/api/backfill', {});
        toast(`Filled in history for ${plural(res.sessions || 0, 'session', 'sessions')}`);
      } catch (err) { toast(err.message); }
      state.backfilling = false;
      await refreshAll();
      return;
    case 'cancel': state.confirm = null; render(); return;
    case 'apply': applyConfirm(''); return;
    case 'apply-merge': applyConfirm('three-way'); return;
    case 'apply-force': applyConfirm('force'); return;
    case 'close-drawer': closeDrawer(); return;
    case 'all-versions': {
      const f = state.file;
      if (!f) return;
      pushNext = true;
      state.fileView = { path: f.path, project_id: f.project_id, versions: null, selected: f.last.id };
      state.confirm = null;
      render();
      await loadFileHistory();
      selectVersion(state.fileView?.selected);
      return;
    }
    default: fileAction(act.dataset.act);
  }
}

function fileFor(f, turnId, turnInfo) {
  const first = f.events[0], last = f.events[f.events.length - 1];
  return {
    key: f.key, path: f.path, events: f.events, first, last, from: first, turn: turnId, prompt: turnInfo?.text || '',
    project_id: state.session.project_id,
    restoreLabel: 'Restore before this prompt…',
    restore: {
      path: '/api/restore', body: { project: state.session.project_id, event: first.id, before: true },
      title: `Put ${f.path} back the way it was before this prompt`, verb: 'Restore', done: 'File restored',
    },
  };
}

function selectVersion(id) {
  const fv = state.fileView;
  if (!fv || !fv.versions) return;
  const v = fv.versions.find(x => x.id === id);
  if (!v) return;
  fv.selected = id;
  selectFile({
    key: v.id, path: fv.path, events: [v], first: v, last: v, from: null, turn: null, fromHistory: true,
    project_id: fv.project_id, prompt: '',
    restoreLabel: 'Restore this version…',
    restore: {
      path: '/api/restore', body: { project: fv.project_id, event: v.id },
      title: `Write this version of ${fv.path} back to disk`, verb: 'Restore', done: 'Version restored',
    },
  }, {});
}

app.addEventListener('click', activate);
drawerEl.addEventListener('click', activate);
app.addEventListener('input', ev => {
  if (ev.target.classList.contains('dd-search')) filterMenu(ev.target.closest('[data-dd-menu]'), ev.target.value);
  if (ev.target.id === 'sess-search') { state.sessQuery = ev.target.value; render(); }
  if (ev.target.id === 'files-search') { state.fileQuery = ev.target.value; render(); }
});
app.addEventListener('keydown', ev => {
  const menu = openMenu();
  if (menu && ev.target.closest('[data-dd]') === menu.closest('[data-dd]')) {
    const dd = menu.closest('[data-dd]');
    const opts = [...menu.querySelectorAll('.dd-opt')].filter(o => !o.hidden);
    const search = menu.querySelector('.dd-search');
    const at = opts.indexOf(document.activeElement);
    if (ev.key === 'Escape') { ev.preventDefault(); setMenu(null, false); dd.querySelector('[data-dd-btn]').focus(); return; }
    if (ev.key === 'ArrowDown' || ev.key === 'ArrowUp') {
      ev.preventDefault();
      if (!opts.length) return;
      if (ev.key === 'ArrowUp' && at <= 0 && search) { search.focus(); return; }
      opts[ev.key === 'ArrowDown' ? Math.min(opts.length - 1, at + 1) : Math.max(0, at - 1)].focus();
      return;
    }
    // Enter in the search box picks the first match.
    if (ev.key === 'Enter' && ev.target === search && opts.length) { ev.preventDefault(); opts[0].click(); return; }
    if (ev.key === 'Tab') setMenu(null, false);
  }
  if (ev.key === 'ArrowDown' && ev.target.matches('[data-dd-btn]')) { ev.preventDefault(); setMenu(ev.target.closest('[data-dd]'), true); return; }
  if ((ev.key === 'Enter' || ev.key === ' ') && ev.target.matches('[tabindex="0"]')) { ev.preventDefault(); activate(ev); }
});
document.addEventListener('click', ev => {
  const menu = openMenu();
  if (menu && !ev.target.closest('[data-dd]')) setMenu(null, false);
});

// Esc closes a confirmation, then the drawer. Keys anywhere outside a text
// field: 1–4 for the screens, / to search, [ and ] for a shorter or longer
// time range.
document.addEventListener('keydown', ev => {
  if (ev.defaultPrevented || ev.ctrlKey || ev.metaKey || ev.altKey) return;
  if (ev.key === 'Escape' && state.confirm) { state.confirm = null; render(); return; }
  if (ev.key === 'Escape' && drawerOpen()) { closeDrawer(); return; }
  if (ev.key === 'Tab' && drawerOpen()) { trapFocus(ev); return; }
  if (state.confirm || drawerOpen()) return;
  const t = ev.target;
  if (t.isContentEditable || ['INPUT', 'TEXTAREA', 'SELECT'].includes(t.tagName)) return;
  if (/^[1-4]$/.test(ev.key)) {
    ev.preventDefault();
    navigate(VIEWS[Number(ev.key) - 1]);
    return;
  }
  if (ev.key === '/') {
    ev.preventDefault();
    const box = document.getElementById(state.view === 'sessions' ? 'sess-search' : state.view === 'files' ? 'files-search' : '');
    if (box) { box.focus(); box.select(); return; }
    const dd = document.getElementById('proj-dd');
    if (dd) setMenu(dd, true);
    return;
  }
  if (ev.key === '[' || ev.key === ']') {
    const keys = Object.keys(RANGES);
    const next = keys[keys.indexOf(state.range) + (ev.key === ']' ? 1 : -1)];
    if (next) { ev.preventDefault(); setRange(next); }
  }
});

// Theme: system, light, or dark, remembered in this browser. Inside the
// editor the panel takes the editor's colors instead.
const themeBtns = document.querySelectorAll('#theme button');
function applyTheme(choice) {
  if (choice === 'system') document.documentElement.removeAttribute('data-theme');
  else document.documentElement.setAttribute('data-theme', choice);
  themeBtns.forEach(b => b.setAttribute('aria-pressed', String(b.dataset.themeChoice === choice)));
}
applyTheme(remembered('theme') || 'system');
document.getElementById('theme').addEventListener('click', ev => {
  const b = ev.target.closest('button[data-theme-choice]');
  if (!b) return;
  applyTheme(b.dataset.themeChoice);
  remember('theme', b.dataset.themeChoice);
});

if (EMBED) {
  document.body.classList.add('embed');
  document.getElementById('theme').hidden = true;
  window.addEventListener('message', ev => {
    const d = ev.data;
    if (!d || d.type !== 'pawprints-theme') return;
    applyTheme(d.kind === 'light' ? 'light' : 'dark');
    const root = document.documentElement.style;
    const allowed = ['--bg', '--surface', '--raise', '--ink', '--muted', '--line', '--grid', '--paw', '--accent', '--cache', '--teal', '--add', '--del', '--warn'];
    for (const name of allowed) {
      const value = d.colors && d.colors[name];
      // Only plain color values: no url(), no expressions.
      if (typeof value === 'string' && /^[#a-zA-Z0-9(),.%\s-]{1,64}$/.test(value.trim()) && !/url/i.test(value)) root.setProperty(name, value.trim());
    }
  });
  window.parent.postMessage({ type: 'pawprints-ready' }, '*');
}

let resizeTimer;
window.addEventListener('resize', () => { clearTimeout(resizeTimer); resizeTimer = setTimeout(render, 150); });

if (!TOKEN) {
  app.innerHTML = '<section class="panel welcome"><h2>Open this page from <code>pawprints ui</code></h2><p>The page needs the link the command printed, which carries a key that keeps other websites out. Run <code>pawprints ui</code> in a terminal; it opens the right link.</p></section>';
} else {
  render();
  refreshAll().then(() => {
    if (state.view === 'sessions') navigate('sessions');
    if (state.fileView) selectVersion(state.fileView.selected || state.fileView.versions?.[0]?.id);
  });
  connectLive();
}
