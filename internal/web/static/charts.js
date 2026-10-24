// Charts drawn as SVG from the theme's CSS variables. Every chart takes a
// list of points that share one time axis and redraws whole on each call,
// sized to its container.

const NS = 'http://www.w3.org/2000/svg';
const tip = () => document.getElementById('tip');

export const esc = s => String(s ?? '').replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));

export function short(v) {
  v = Number(v) || 0;
  if (v >= 1e9) return `${(v / 1e9).toFixed(1)}B`;
  if (v >= 1e6) return `${(v / 1e6).toFixed(v >= 1e7 ? 0 : 1)}M`;
  if (v >= 1e3) return `${Math.round(v / 1e3)}k`;
  return String(Math.round(v));
}

function niceMax(v) {
  if (!(v > 0)) return 1;
  const p = Math.pow(10, Math.floor(Math.log10(v)));
  for (const m of [1, 1.5, 2, 2.5, 3, 4, 5, 6, 8, 10]) if (v <= m * p) return m * p;
  return 10 * p;
}

const pathOf = (xs, ys) => xs.map((x, i) => `${i ? 'L' : 'M'}${x.toFixed(1)},${ys[i].toFixed(1)}`).join('');
const areaOf = (xs, ys, base) => `${pathOf(xs, ys)}L${xs[xs.length - 1].toFixed(1)},${base}L${xs[0].toFixed(1)},${base}Z`;

function frame(el, height) {
  const w = Math.max(280, el.clientWidth || 600);
  const m = { l: 46, r: 12, t: 14, b: 24 };
  return { w, h: height, m, iw: w - m.l - m.r, ih: height - m.t - m.b };
}

const xAt = (f, i, n) => (n <= 1 ? f.m.l + f.iw / 2 : f.m.l + (f.iw * i) / (n - 1));

function timeLabels(f, pts, label) {
  let s = '';
  const n = pts.length;
  const every = Math.max(1, Math.round(n / Math.max(3, Math.floor(f.iw / 90))));
  pts.forEach((p, i) => {
    if (i % every !== 0 && i !== n - 1) return;
    if (i !== n - 1 && n - 1 - i < every * 0.6) return;
    const anchor = i === 0 ? 'start' : i === n - 1 ? 'end' : 'middle';
    s += `<text class="axis" x="${xAt(f, i, n)}" y="${f.h - 6}" text-anchor="${anchor}">${esc(label(p.at))}</text>`;
  });
  return s;
}

function yGrid(f, max, fmt) {
  let s = '';
  for (let k = 0; k <= 4; k++) {
    const y = f.m.t + f.ih - (f.ih * k) / 4;
    s += `<line class="gridline" x1="${f.m.l}" x2="${f.w - f.m.r}" y1="${y}" y2="${y}"/>`;
    s += `<text class="axis" x="${f.m.l - 8}" y="${y + 3.5}" text-anchor="end">${fmt((max / 4) * k)}</text>`;
  }
  return s;
}

// hover shows a crosshair and a tooltip for the nearest point.
function hover(f, n, svg, yOf, html, onPick) {
  const cross = document.createElementNS(NS, 'line');
  cross.setAttribute('class', 'cross');
  cross.setAttribute('y1', f.m.t);
  cross.setAttribute('y2', f.m.t + f.ih);
  cross.style.display = 'none';
  const dot = document.createElementNS(NS, 'circle');
  dot.setAttribute('class', 'dot');
  dot.setAttribute('r', '4.5');
  dot.style.display = 'none';
  svg.appendChild(cross);
  svg.appendChild(dot);
  const hit = svg.querySelector('.hit');
  const indexAt = ev => {
    const box = svg.getBoundingClientRect();
    const x = (ev.clientX - box.left) * (f.w / box.width);
    return n <= 1 ? 0 : Math.max(0, Math.min(n - 1, Math.round(((x - f.m.l) / f.iw) * (n - 1))));
  };
  hit.addEventListener('pointermove', ev => {
    const i = indexAt(ev);
    const px = xAt(f, i, n);
    cross.setAttribute('x1', px);
    cross.setAttribute('x2', px);
    cross.style.display = '';
    const y = yOf(i);
    if (y === null || y === undefined) dot.style.display = 'none';
    else { dot.setAttribute('cx', px); dot.setAttribute('cy', y); dot.style.display = ''; }
    const t = tip();
    t.innerHTML = html(i);
    t.classList.add('show');
    const tw = t.offsetWidth, th = t.offsetHeight;
    let left = ev.clientX + 14, top = ev.clientY - th - 12;
    if (left + tw > window.innerWidth - 12) left = ev.clientX - tw - 14;
    if (top < 8) top = ev.clientY + 16;
    t.style.left = `${Math.max(8, left)}px`;
    t.style.top = `${top}px`;
  });
  hit.addEventListener('pointerleave', () => {
    cross.style.display = 'none';
    dot.style.display = 'none';
    tip().classList.remove('show');
  });
  if (onPick) {
    hit.style.cursor = 'pointer';
    hit.addEventListener('click', ev => onPick(indexAt(ev)));
  }
}

function empty(el, text) {
  el.innerHTML = `<div class="chart-empty">${esc(text)}</div>`;
}

// tokenChart draws tokens per minute, optionally cache reads, prompt bands,
// and spike marks.
export function tokenChart(el, pts, opts) {
  if (!pts.length || pts.every(p => !p.perMin && !p.cachePerMin)) {
    empty(el, opts.emptyText || 'No tokens spent in this range.');
    return;
  }
  const f = frame(el, opts.height || 240);
  const n = pts.length;
  const peak = Math.max(...pts.map(p => (opts.showCache ? Math.max(p.cachePerMin, p.perMin) : p.perMin)));
  const max = niceMax(peak * 1.12);
  const xs = pts.map((_, i) => xAt(f, i, n));
  const y = v => f.m.t + f.ih - (v / max) * f.ih;
  const base = f.m.t + f.ih;
  let s = yGrid(f, max, short);
  (opts.bands || []).forEach((b, k) => {
    const x1 = xs[Math.max(0, b.from)], x2 = xs[Math.min(n - 1, b.to)];
    if (x1 === undefined || x2 === undefined) return;
    if (k % 2 === 0) s += `<rect class="band" x="${x1}" y="${f.m.t}" width="${Math.max(2, x2 - x1)}" height="${f.ih}"/>`;
    if (x2 - x1 > 18) s += `<text class="band-label" x="${x1 + 4}" y="${f.m.t + 11}">${esc(b.label)}</text>`;
  });
  if (opts.showCache) {
    const yc = pts.map(p => y(p.cachePerMin));
    s += `<path class="area-cache" d="${areaOf(xs, yc, base)}"/><path class="line-cache" d="${pathOf(xs, yc)}"/>`;
  }
  const yt = pts.map(p => y(p.perMin));
  s += `<path class="area-tok" d="${areaOf(xs, yt, base)}"/><path class="line-tok" d="${pathOf(xs, yt)}"/>`;
  // Spikes come strongest first; a label that would overlap one already
  // placed is left out, and its ring still marks the spike.
  const placed = [];
  (opts.spikes || []).slice(0, 4).forEach(sp => {
    const sx = xs[sp.i], sy = yt[sp.i];
    if (sx === undefined) return;
    s += `<circle class="spike" cx="${sx}" cy="${sy}" r="5"/>`;
    const anchor = sx > f.w - 90 ? 'end' : 'start';
    const lx = sx + (anchor === 'end' ? -9 : 9), ly = Math.max(f.m.t + 10, sy - 6);
    const left = anchor === 'end' ? lx - 40 : lx;
    if (placed.some(b => left < b.right && left + 40 > b.left && Math.abs(ly - b.y) < 14)) return;
    placed.push({ left, right: left + 40, y: ly });
    s += `<text class="spike-label" x="${lx}" y="${ly}" text-anchor="${anchor}">${sp.times.toFixed(1)}×</text>`;
  });
  s += timeLabels(f, pts, opts.label);
  s += `<rect class="hit" x="${f.m.l}" y="${f.m.t}" width="${f.iw}" height="${f.ih}"/>`;
  el.innerHTML = `<svg viewBox="0 0 ${f.w} ${f.h}" height="${f.h}" role="img" aria-label="${esc(opts.aria || 'Tokens per minute')}">${s}</svg>`;
  hover(f, n, el.querySelector('svg'), i => yt[i], i => opts.tooltip(pts[i]), opts.onPick ? i => opts.onPick(pts[i]) : null);
}

// editsChart draws lines added above the axis and removed below it.
export function editsChart(el, pts, opts) {
  if (!pts.length || pts.every(p => !p.add && !p.del)) {
    empty(el, opts.emptyText || 'No lines changed in this range.');
    return;
  }
  const f = frame(el, opts.height || 170);
  const n = pts.length;
  const max = niceMax(Math.max(...pts.map(p => Math.max(p.add, p.del))) * 1.1);
  const mid = f.m.t + f.ih / 2;
  const bw = Math.max(1.5, Math.min(14, (f.iw / n) * 0.62));
  let s = '';
  [-1, -0.5, 0, 0.5, 1].forEach(k => {
    const yy = mid - (f.ih / 2) * k;
    s += `<line class="${k === 0 ? 'zero' : 'gridline'}" x1="${f.m.l}" x2="${f.w - f.m.r}" y1="${yy}" y2="${yy}"/>`;
    if (k !== 0) s += `<text class="axis" x="${f.m.l - 8}" y="${yy + 3.5}" text-anchor="end">${k > 0 ? '+' : '−'}${short(Math.abs(k) * max)}</text>`;
  });
  pts.forEach((p, i) => {
    const x = xAt(f, i, n) - bw / 2;
    const ha = (p.add / max) * (f.ih / 2), hd = (p.del / max) * (f.ih / 2);
    if (ha > 0) s += `<rect class="bar-add" x="${x}" y="${mid - ha}" width="${bw}" height="${ha}" rx="1"/>`;
    if (hd > 0) s += `<rect class="bar-del" x="${x}" y="${mid + 1}" width="${bw}" height="${hd}" rx="1"/>`;
  });
  s += timeLabels(f, pts, opts.label);
  s += `<rect class="hit" x="${f.m.l}" y="${f.m.t}" width="${f.iw}" height="${f.ih}"/>`;
  el.innerHTML = `<svg viewBox="0 0 ${f.w} ${f.h}" height="${f.h}" role="img" aria-label="Lines added and removed">${s}</svg>`;
  hover(f, n, el.querySelector('svg'), () => null, i => opts.tooltip(pts[i]));
}

// limitChart draws each account's usage-limit readings as a step line over
// the same time axis as the other charts.
export function limitChart(el, lines, opts) {
  if (!lines.length) {
    empty(el, opts.emptyText);
    return;
  }
  const f = frame(el, opts.height || 170);
  const from = opts.from.getTime(), to = opts.to.getTime();
  const span = Math.max(1, to - from);
  const x = t => f.m.l + ((t - from) / span) * f.iw;
  const y = v => f.m.t + f.ih - (Math.min(100, v) / 100) * f.ih;
  let s = '';
  [0, 25, 50, 75, 100].forEach(v => {
    s += `<line class="${v === 100 ? 'lim-max' : 'gridline'}" x1="${f.m.l}" x2="${f.w - f.m.r}" y1="${y(v)}" y2="${y(v)}"/>`;
    s += `<text class="axis" x="${f.m.l - 8}" y="${y(v) + 3.5}" text-anchor="end">${v}%</text>`;
  });
  const steps = lines.map(line => {
    const pts = line.points.map(p => ({ t: new Date(p.at).getTime(), v: p.percent, reset: p.reset }));
    if (pts.length) pts.push({ t: to, v: pts[pts.length - 1].v });
    return { ...line, pts };
  });
  steps.forEach(line => line.pts.forEach(p => {
    if (p.reset) s += `<line class="reset" x1="${x(p.t)}" x2="${x(p.t)}" y1="${f.m.t}" y2="${f.m.t + f.ih}"/>`;
  }));
  steps.forEach(line => {
    if (!line.pts.length) return;
    let d = `M${x(line.pts[0].t).toFixed(1)},${y(line.pts[0].v).toFixed(1)}`;
    for (let i = 1; i < line.pts.length; i++) d += `H${x(line.pts[i].t).toFixed(1)}V${y(line.pts[i].v).toFixed(1)}`;
    if (steps.length === 1) s += `<path class="lim-area" d="${d}V${f.m.t + f.ih}H${x(line.pts[0].t).toFixed(1)}Z"/>`;
    s += `<path class="lim-line" style="stroke:${line.color}" d="${d}"/>`;
  });
  const axisPts = opts.axis.map(at => ({ at }));
  s += timeLabels(f, axisPts, opts.label);
  s += `<rect class="hit" x="${f.m.l}" y="${f.m.t}" width="${f.iw}" height="${f.ih}"/>`;
  el.innerHTML = `<svg viewBox="0 0 ${f.w} ${f.h}" height="${f.h}" role="img" aria-label="Usage limits">${s}</svg>`;
  const n = axisPts.length;
  hover(f, n, el.querySelector('svg'), () => null, i => {
    const t = opts.axis[i].getTime();
    const rows = steps.map(line => {
      let v = null;
      for (const p of line.pts) if (p.t <= t) v = p.v;
      return v === null ? '' : `<div class="r"><span>${esc(line.label)}</span><b>${Math.round(v)}%</b></div>`;
    }).join('');
    return `<div class="t">${esc(opts.label(opts.axis[i]))}</div>${rows || '<div class="r"><span>No reading yet</span></div>'}`;
  });
}

// spark is a small inline trend without axes.
export function spark(values, cls = '') {
  const w = 90, h = 26;
  if (!values || values.length < 2 || values.every(v => !v)) return `<svg class="spark ${cls}" viewBox="0 0 ${w} ${h}" aria-hidden="true"><path class="l" d="M0,${h - 2}H${w}" opacity="0.35"/></svg>`;
  const max = Math.max(1, ...values);
  const xs = values.map((_, i) => (w * i) / (values.length - 1));
  const ys = values.map(v => h - 2 - (v / max) * (h - 4));
  return `<svg class="spark ${cls}" viewBox="0 0 ${w} ${h}" aria-hidden="true"><path class="a" d="${areaOf(xs, ys, h)}"/><path class="l" d="${pathOf(xs, ys)}"/></svg>`;
}
