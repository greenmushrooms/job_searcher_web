/* Résumé workspace diff editor — the v2 design promoted from the /v2 diff-lab.
 *
 * Two CodeMirror 6 editors (LEFT = working copy `markdown`, RIGHT = AI
 * suggestions `tailored`) with a line-level LCS diff we compute ourselves,
 * rendered as line-background decorations + height-matched spacers, with
 * inline word-level highlights on edited lines and center ‹/› hunk-merge
 * controls. ‹ pulls an AI suggestion into the working copy; › pushes the other
 * way.
 *
 * The workspace is an htmx fragment that re-swaps on apply-ai / re-tailor, so
 * this is built as a re-mountable controller: ResumeDiff.mount(form) destroys
 * any previous editors and builds fresh ones on the new fragment. Every edit is
 * written back into the (hidden) source <textarea>, so the existing htmx form
 * posts and the native PDF submit keep serializing the current text. If the CDN
 * import fails the textareas are never hidden, so the page degrades to plain
 * editing.
 *
 * esm.sh import shapes matter: EditorView/basicSetup from `codemirror`,
 * Decoration/WidgetType from `@codemirror/view`, StateField/StateEffect from
 * `@codemirror/state` — no `?deps=` (it breaks the export shape).
 */
import { EditorView, basicSetup } from "https://esm.sh/codemirror@6.0.1";
import { Decoration, WidgetType } from "https://esm.sh/@codemirror/view@6";
import { StateField, StateEffect } from "https://esm.sh/@codemirror/state@6";

// ── word-level diff of one modified line pair ──────────────────────────────
function tokenize(s) {
  const toks = []; let i = 0;
  while (i < s.length) {
    const ws = /\s/.test(s[i]); let j = i + 1;
    while (j < s.length && /\s/.test(s[j]) === ws) j++;
    toks.push({ text: s.slice(i, j), start: i, end: j });
    i = j;
  }
  return toks;
}
function coalesce(ranges) {
  if (ranges.length < 2) return ranges;
  ranges.sort((a, b) => a[0] - b[0]);
  const out = [ranges[0].slice()];
  for (let k = 1; k < ranges.length; k++) {
    const last = out[out.length - 1];
    if (ranges[k][0] <= last[1]) last[1] = Math.max(last[1], ranges[k][1]);
    else out.push(ranges[k].slice());
  }
  return out;
}
function wordDiff(a, b) {
  if (a.length > 2000 || b.length > 2000) return { aRanges: a.length ? [[0, a.length]] : [], bRanges: b.length ? [[0, b.length]] : [] };
  const ta = tokenize(a), tb = tokenize(b), n = ta.length, m = tb.length;
  const dp = []; for (let i = 0; i <= n; i++) dp.push(new Int32Array(m + 1));
  for (let i = n - 1; i >= 0; i--) for (let j = m - 1; j >= 0; j--)
    dp[i][j] = ta[i].text === tb[j].text ? dp[i + 1][j + 1] + 1 : Math.max(dp[i + 1][j], dp[i][j + 1]);
  const aR = [], bR = []; let i = 0, j = 0;
  while (i < n && j < m) {
    if (ta[i].text === tb[j].text) { i++; j++; }
    else if (dp[i + 1][j] >= dp[i][j + 1]) { aR.push([ta[i].start, ta[i].end]); i++; }
    else { bR.push([tb[j].start, tb[j].end]); j++; }
  }
  while (i < n) { aR.push([ta[i].start, ta[i].end]); i++; }
  while (j < m) { bR.push([tb[j].start, tb[j].end]); j++; }
  return { aRanges: coalesce(aR), bRanges: coalesce(bR) };
}

// Dice similarity over word tokens — decides "same point, edited" vs unrelated.
function lineSim(a, b) {
  if (a === b) return 1;
  const ta = tokenize(a).filter(t => /\S/.test(t.text)).map(t => t.text);
  const tb = tokenize(b).filter(t => /\S/.test(t.text)).map(t => t.text);
  if (!ta.length && !tb.length) return 1;
  if (!ta.length || !tb.length) return 0;
  const n = ta.length, m = tb.length, dp = []; for (let i = 0; i <= n; i++) dp.push(new Int32Array(m + 1));
  for (let i = n - 1; i >= 0; i--) for (let j = m - 1; j >= 0; j--)
    dp[i][j] = ta[i] === tb[j] ? dp[i + 1][j + 1] + 1 : Math.max(dp[i + 1][j], dp[i][j + 1]);
  return 2 * dp[0][0] / (n + m);
}

// Order-preserving del/add/substitute alignment within a changed gap.
function alignGap(Lidx, Ridx, A, B) {
  const p = Lidx.length, q = Ridx.length, THRESH = 0.5;
  const cost = [], back = [];
  for (let i = 0; i <= p; i++) { cost.push(new Float64Array(q + 1)); back.push(new Array(q + 1).fill(0)); }
  for (let i = p; i >= 0; i--) for (let j = q; j >= 0; j--) {
    if (i === p && j === q) { cost[i][j] = 0; continue; }
    let best = Infinity, bk = 0;
    if (i < p) { const c = 1 + cost[i + 1][j]; if (c < best) { best = c; bk = 1; } }
    if (j < q) { const c = 1 + cost[i][j + 1]; if (c < best) { best = c; bk = 2; } }
    if (i < p && j < q) { const s = lineSim(A[Lidx[i]], B[Ridx[j]]); if (s >= THRESH) { const c = (1 - s) + cost[i + 1][j + 1]; if (c < best) { best = c; bk = 3; } } }
    cost[i][j] = best; back[i][j] = bk;
  }
  const ops = []; let i = 0, j = 0;
  while (i < p || j < q) {
    const bk = back[i][j];
    if (bk === 3) { ops.push({ t: 'sub', a: Lidx[i], b: Ridx[j] }); i++; j++; }
    else if (bk === 1 || (bk === 0 && i < p)) { ops.push({ t: 'del', a: Lidx[i] }); i++; }
    else { ops.push({ t: 'add', b: Ridx[j] }); j++; }
  }
  return ops;
}

function computeDiff(aText, bText) {
  const A = aText.split('\n'), B = bText.split('\n'), n = A.length, m = B.length;
  const dp = []; for (let i = 0; i <= n; i++) dp.push(new Int32Array(m + 1));
  for (let i = n - 1; i >= 0; i--) for (let j = m - 1; j >= 0; j--)
    dp[i][j] = A[i] === B[j] ? dp[i + 1][j + 1] + 1 : Math.max(dp[i + 1][j], dp[i][j + 1]);
  const matches = []; let i = 0, j = 0;
  while (i < n && j < m) { if (A[i] === B[j]) { matches.push([i, j]); i++; j++; } else if (dp[i + 1][j] >= dp[i][j + 1]) i++; else j++; }

  const rows = [];
  function emitGap(loI, hiI, loJ, hiJ) {
    const Lidx = []; for (let x = loI; x < hiI; x++) Lidx.push(x);
    const Ridx = []; for (let y = loJ; y < hiJ; y++) Ridx.push(y);
    if (!Lidx.length && !Ridx.length) return;
    const ops = alignGap(Lidx, Ridx, A, B);
    let bd = [], ba = [];
    function flush() {
      const k = Math.min(bd.length, ba.length);
      for (let t = 0; t < k; t++) rows.push({ l: bd[t], r: ba[t], kind: 'replace' });
      for (let t = k; t < bd.length; t++) rows.push({ l: bd[t], r: -1, kind: 'del' });
      for (let t = k; t < ba.length; t++) rows.push({ l: -1, r: ba[t], kind: 'add' });
      bd = []; ba = [];
    }
    for (const op of ops) {
      if (op.t === 'sub') { flush(); rows.push({ l: op.a, r: op.b, kind: 'sub' }); }
      else if (op.t === 'del') bd.push(op.a);
      else ba.push(op.b);
    }
    flush();
  }
  let pi = -1, pj = -1;
  for (const [mi, mj] of matches) { emitGap(pi + 1, mi, pj + 1, mj); rows.push({ l: mi, r: mj, kind: 'same' }); pi = mi; pj = mj; }
  emitGap(pi + 1, n, pj + 1, m);

  const aLine = Array.from({ length: n }, () => ({ cls: 'same', marks: [] }));
  const bLine = Array.from({ length: m }, () => ({ cls: 'same', marks: [] }));
  const leftSpacers = [], rightSpacers = [], balanceRows = [];
  let pendDel = [], pendAdd = [];
  for (const row of rows) {
    if (row.l >= 0 && row.r >= 0) {
      if (pendDel.length) { rightSpacers.push({ pos: row.r, src: pendDel }); pendDel = []; }
      if (pendAdd.length) { leftSpacers.push({ pos: row.l, src: pendAdd }); pendAdd = []; }
      if (row.kind === 'sub') {
        const cd = wordDiff(A[row.l], B[row.r]);
        aLine[row.l] = { cls: 'chg', marks: cd.aRanges };
        bLine[row.r] = { cls: 'chg', marks: cd.bRanges };
        balanceRows.push({ a: row.l, b: row.r });
      } else if (row.kind === 'replace') {
        aLine[row.l] = { cls: 'del', marks: [] };
        bLine[row.r] = { cls: 'add', marks: [] };
        balanceRows.push({ a: row.l, b: row.r });
      }
    } else if (row.l >= 0) { aLine[row.l] = { cls: 'del', marks: [] }; pendDel.push(row.l); }
    else if (row.r >= 0) { bLine[row.r] = { cls: 'add', marks: [] }; pendAdd.push(row.r); }
  }
  if (pendDel.length) rightSpacers.push({ pos: m, src: pendDel });
  if (pendAdd.length) leftSpacers.push({ pos: n, src: pendAdd });
  return { aLine, bLine, leftSpacers, rightSpacers, balanceRows, rows, n, m };
}

function buildHunks(rows, n, m) {
  const hunks = []; let cur = null;
  for (const row of rows) {
    if (row.kind === 'same') {
      if (cur) { cur.leftAnchor = row.l; cur.rightAnchor = row.r; hunks.push(cur); cur = null; }
    } else {
      if (!cur) cur = { L: [], R: [], leftAnchor: n, rightAnchor: m, topL: -1, topR: -1 };
      if (row.l >= 0) { cur.L.push(row.l); if (cur.topL < 0) cur.topL = row.l; }
      if (row.r >= 0) { cur.R.push(row.r); if (cur.topR < 0) cur.topR = row.r; }
    }
  }
  if (cur) hunks.push(cur);
  return hunks;
}

function blockText(doc, idxs) {
  return doc.sliceString(doc.line(idxs[0] + 1).from, doc.line(idxs[idxs.length - 1] + 1).to);
}
function writeLines(view, idxs, text, anchorIdx) {
  const doc = view.state.doc;
  if (idxs.length) {
    if (text === null) { // delete these whole lines (with one surrounding newline)
      const last = idxs[idxs.length - 1];
      const from = (last + 1 < doc.lines) ? doc.line(idxs[0] + 1).from : (idxs[0] > 0 ? doc.line(idxs[0]).to : doc.line(idxs[0] + 1).from);
      const to = (last + 1 < doc.lines) ? doc.line(last + 2).from : doc.line(last + 1).to;
      view.dispatch({ changes: { from, to, insert: '' } });
    } else { // replace the line contents
      view.dispatch({ changes: { from: doc.line(idxs[0] + 1).from, to: doc.line(idxs[idxs.length - 1] + 1).to, insert: text } });
    }
  } else if (text !== null) { // insert new lines at the anchor
    const atEnd = anchorIdx >= doc.lines;
    const pos = atEnd ? doc.length : doc.line(anchorIdx + 1).from;
    view.dispatch({ changes: { from: pos, to: pos, insert: atEnd ? (doc.length ? '\n' + text : text) : text + '\n' } });
  }
}

class Spacer extends WidgetType {
  constructor(h) { super(); this.h = h; }
  eq(o) { return o.h === this.h; }
  toDOM() { const d = document.createElement('div'); d.style.height = this.h + 'px'; d.className = 'cm-spacer'; return d; }
  get estimatedHeight() { return this.h; }
}
const setDeco = StateEffect.define();
const diffField = StateField.define({
  create() { return Decoration.none; },
  update(v, tr) { v = v.map(tr.changes); for (const e of tr.effects) if (e.is(setDeco)) v = e.value; return v; },
  provide: f => EditorView.decorations.from(f),
});

function lineHeights(srcView, indices) {
  let total = 0;
  for (const idx of indices) {
    if (idx + 1 > srcView.state.doc.lines) continue;
    total += srcView.lineBlockAt(srcView.state.doc.line(idx + 1).from).height;
  }
  return total || (indices.length * (srcView.defaultLineHeight || 18));
}
function lineH(view, idx) {
  if (idx + 1 > view.state.doc.lines) return view.defaultLineHeight || 18;
  return view.lineBlockAt(view.state.doc.line(idx + 1).from).height;
}
function buildDeco(view, info, spacers, side) {
  const doc = view.state.doc, ranges = [];
  for (let k = 0; k < info.length; k++) {
    const li = info[k]; if (li.cls === 'same') continue;
    const line = doc.line(k + 1);
    ranges.push(Decoration.line({ class: li.cls === 'del' ? 'cm-del-line' : li.cls === 'add' ? 'cm-add-line' : 'cm-chg-line' }).range(line.from));
    if (li.cls === 'chg') for (const [s, e] of li.marks) if (e > s)
      ranges.push(Decoration.mark({ class: side === 'left' ? 'cm-del-text' : 'cm-add-text' }).range(line.from + s, line.from + e));
  }
  for (const sp of spacers) {
    let pos, sd;
    if (sp.side === 'after') { const ln = doc.line(Math.min(sp.pos + 1, doc.lines)); pos = ln.to; sd = 1; }
    else if (sp.pos >= doc.lines) { pos = doc.length; sd = 1; }
    else { pos = doc.line(sp.pos + 1).from; sd = -1; }
    ranges.push(Decoration.widget({ widget: new Spacer(sp.height), block: true, side: sd }).range(pos));
  }
  return Decoration.set(ranges, true);
}
function resolveSpacers(list, srcView) {
  return list.map(sp => ({ pos: sp.pos, side: 'before', height: lineHeights(srcView, sp.src) }));
}

// ── per-workspace controller ───────────────────────────────────────────────
class WorkspaceDiff {
  constructor(root) {
    this.root = root;
    this.taLeft = root.querySelector('textarea[name="markdown"]');   // working copy
    this.taRight = root.querySelector('textarea[name="tailored"]');  // AI suggestions (may be absent)
    this.hostLeft = root.querySelector('[data-cm="left"]');
    this.hostRight = root.querySelector('[data-cm="right"]');
    this.cm = root.querySelector('.cm');
    this.gutter = root.querySelector('.cmgutter');
    this.left = null; this.right = null; this.hunks = [];
    this._onResize = () => requestAnimationFrame(() => this.positionGutter());
  }

  destroy() {
    try { this.left && this.left.destroy(); this.right && this.right.destroy(); } catch (e) { /* detached */ }
    window.removeEventListener('resize', this._onResize);
    this.left = this.right = null;
  }

  mount() {
    if (!this.taLeft || !this.hostLeft) return; // nothing to edit
    const self = this;
    const exts = (ta) => [basicSetup, diffField, EditorView.lineWrapping,
      EditorView.updateListener.of(u => {
        if (!u.docChanged) return;
        ta.value = u.state.doc.toString();        // keep the form-serialized source current
        if (self.right) queueMicrotask(() => self.recompute());
      })];

    this.left = new EditorView({ doc: this.taLeft.value, extensions: exts(this.taLeft), parent: this.hostLeft });
    this.taLeft.style.display = 'none';

    if (this.taRight && this.hostRight) {
      this.right = new EditorView({ doc: this.taRight.value, extensions: exts(this.taRight), parent: this.hostRight });
      this.taRight.style.display = 'none';
      setTimeout(() => this.recompute(), 120); // after layout so wrapped heights measure
      const a = this.left.scrollDOM, b = this.right.scrollDOM; let lock = false;
      const bind = (s, d) => s.addEventListener('scroll', () => {
        requestAnimationFrame(() => this.positionGutter());
        if (lock) return; lock = true; d.scrollTop = s.scrollTop; requestAnimationFrame(() => lock = false);
      });
      bind(a, b); bind(b, a);
      window.addEventListener('resize', this._onResize);
    }
  }

  recompute() {
    if (!this.left || !this.right) return;
    const d = computeDiff(this.left.state.doc.toString(), this.right.state.doc.toString());
    const lsp = resolveSpacers(d.leftSpacers, this.right), rsp = resolveSpacers(d.rightSpacers, this.left);
    for (const pr of d.balanceRows) {
      const hA = lineH(this.left, pr.a), hB = lineH(this.right, pr.b);
      if (hA < hB - 0.5) lsp.push({ pos: pr.a, side: 'after', height: hB - hA });
      else if (hB < hA - 0.5) rsp.push({ pos: pr.b, side: 'after', height: hA - hB });
    }
    this.left.dispatch({ effects: setDeco.of(buildDeco(this.left, d.aLine, lsp, 'left')) });
    this.right.dispatch({ effects: setDeco.of(buildDeco(this.right, d.bLine, rsp, 'right')) });
    this.hunks = buildHunks(d.rows, d.n, d.m);
    requestAnimationFrame(() => this.positionGutter());
  }

  applyHunk(h, dir) {
    if (dir === 'toRight') writeLines(this.right, h.R, h.L.length ? blockText(this.left.state.doc, h.L) : null, h.rightAnchor);
    else writeLines(this.left, h.L, h.R.length ? blockText(this.right.state.doc, h.R) : null, h.leftAnchor);
  }

  positionGutter() {
    if (!this.gutter || !this.left || !this.right || !this.cm) return;
    this.gutter.innerHTML = '';
    const cmTop = this.cm.getBoundingClientRect().top;
    const scTop = this.left.scrollDOM.getBoundingClientRect().top - cmTop;
    const viewH = this.left.scrollDOM.clientHeight, scroll = this.left.scrollDOM.scrollTop;
    for (const h of this.hunks) {
      let blockTop;
      if (h.topL >= 0) blockTop = this.left.lineBlockAt(this.left.state.doc.line(h.topL + 1).from).top;
      else if (h.topR >= 0) blockTop = this.right.lineBlockAt(this.right.state.doc.line(h.topR + 1).from).top;
      else continue;
      const y = blockTop - scroll;
      if (y < -10 || y > viewH + 10) continue;
      const ctl = document.createElement('div');
      ctl.className = 'hunk-ctl';
      ctl.style.top = (scTop + y + 1) + 'px';
      const bl = document.createElement('button'); bl.type = 'button'; bl.textContent = '‹'; bl.title = 'Use the AI suggestion here (→ working copy)';
      const br = document.createElement('button'); br.type = 'button'; br.textContent = '›'; br.title = 'Send the working copy here (→ AI suggestion)';
      bl.addEventListener('click', () => this.applyHunk(h, 'toLeft'));
      br.addEventListener('click', () => this.applyHunk(h, 'toRight'));
      ctl.appendChild(bl); ctl.appendChild(br); this.gutter.appendChild(ctl);
    }
  }

  // Replace the working-copy editor content (used by the version picker).
  setWorking(text) {
    if (this.left) this.left.dispatch({ changes: { from: 0, to: this.left.state.doc.length, insert: text } });
    else if (this.taLeft) this.taLeft.value = text;
  }
  getWorking() { return this.left ? this.left.state.doc.toString() : (this.taLeft ? this.taLeft.value : ''); }

  // ── SCD2 version history dropdown (working-copy versions) ────────────────
  wireVersions() {
    const root = this.root, self = this;
    const pick = root.querySelector('.ver-pick');
    const sel = pick && pick.querySelector('select');
    const restoreBtn = root.querySelector('.restore-btn');
    if (!sel) return;
    const job = root.dataset.job, profile = root.dataset.profile || '';
    let byNum = {}, current = null;

    const updateRestore = () => {
      if (!restoreBtn) return;
      const v = parseInt(sel.value, 10);
      const hide = !v || v === current;
      restoreBtn.style.display = hide ? 'none' : '';
      if (!hide) restoreBtn.textContent = '↩ Restore v' + v;
    };
    const load = () => {
      if (!job) return;
      fetch('/ui/jobs/' + job + '/resume/versions?profile=' + encodeURIComponent(profile))
        .then(r => r.ok ? r.json() : [])
        .then(list => {
          if (!list || !list.length) { if (pick) pick.style.display = 'none'; return; }
          if (pick) pick.style.display = '';
          byNum = {}; sel.innerHTML = ''; current = null;
          list.forEach(v => {
            byNum[v.version] = v.markdown;
            if (v.isCurrent) current = v.version;
            const o = document.createElement('option');
            o.value = v.version;
            o.textContent = 'v' + v.version + (v.isCurrent ? ' (current)' : '') + ' · ' + (v.generatedAt || '').slice(0, 16);
            sel.appendChild(o);
          });
          sel.value = current;
          updateRestore();
        }).catch(() => { /* offline / no versions */ });
    };
    this.loadVersions = load;

    sel.addEventListener('change', () => {
      const v = parseInt(sel.value, 10);
      if (byNum[v] != null) self.setWorking(byNum[v]);
      updateRestore();
    });
    if (restoreBtn) restoreBtn.addEventListener('click', () => {
      const v = parseInt(sel.value, 10);
      if (!job || !v || v === current) return;
      fetch('/ui/jobs/' + job + '/resume/versions/' + v + '/restore', {
        method: 'POST',
        headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
        body: new URLSearchParams({ profile: profile }),
      }).then(r => r.ok ? r.json() : Promise.reject()).then(() => load()).catch(() => { /* surfaced by htmx banner elsewhere */ });
    });
    load();
  }
}

// ── public controller (one mounted workspace at a time) ────────────────────
const ResumeDiff = {
  instance: null,
  mount(root) {
    root = root || document.querySelector('.draft-form');
    if (!root) return;
    if (this.instance) this.instance.destroy();
    const w = new WorkspaceDiff(root);
    this.instance = w;
    w.mount();
    w.wireVersions();
  },
  refreshVersions() { if (this.instance && this.instance.loadVersions) this.instance.loadVersions(); },
  setWorking(t) { if (this.instance) this.instance.setWorking(t); },
  getWorking() { return this.instance ? this.instance.getWorking() : ''; },
};
window.ResumeDiff = ResumeDiff;

// Mount on first load if a workspace is already present (e.g. direct navigation).
if (document.querySelector('.draft-form')) ResumeDiff.mount();
