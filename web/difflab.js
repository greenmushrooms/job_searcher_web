/* Diff-lab shared logic for the /v1, /v2, /v3 résumé comparison pages.
 *
 * All three share save wiring and an LCS line diff. The highlighting differs:
 *   v1 — colored overlay behind each editable textarea, live, scroll-synced
 *   v2 — CodeMirror merge view (its own module script handles rendering)
 *   v3 — plain textareas, highlighted only when "Compare" is clicked
 */
(function () {
  var body = document.body;
  var variant = body.dataset.variant;
  var jobId = body.dataset.job;
  var profile = body.dataset.profile;
  var left = document.getElementById('left');
  var right = document.getElementById('right');
  var toast = document.getElementById('toast');

  // ── LCS line diff ─────────────────────────────────────────────────────────
  // Classifies each line of A and B as 'same' | 'del' (only in A) | 'add'
  // (only in B). No gap-filling — each side is classified in place, so an
  // insertion/deletion doesn't mark everything after it as changed.
  function lineDiff(aText, bText) {
    var A = aText.split('\n'), B = bText.split('\n');
    var n = A.length, m = B.length;
    var dp = [];
    for (var i = 0; i <= n; i++) dp.push(new Int32Array(m + 1));
    for (var i = n - 1; i >= 0; i--) {
      for (var j = m - 1; j >= 0; j--) {
        dp[i][j] = A[i] === B[j] ? dp[i + 1][j + 1] + 1
                                 : Math.max(dp[i + 1][j], dp[i][j + 1]);
      }
    }
    var aCls = new Array(n).fill('same');
    var bCls = new Array(m).fill('same');
    var x = 0, y = 0;
    while (x < n && y < m) {
      if (A[x] === B[y]) { x++; y++; }
      else if (dp[x + 1][y] >= dp[x][y + 1]) { aCls[x] = 'del'; x++; }
      else { bCls[y] = 'add'; y++; }
    }
    while (x < n) aCls[x++] = 'del';
    while (y < m) bCls[y++] = 'add';
    return { A: A, B: B, aCls: aCls, bCls: bCls };
  }

  function escapeHtml(s) {
    return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
  }

  function debounce(fn, ms) {
    var t;
    return function () { clearTimeout(t); t = setTimeout(fn, ms || 120); };
  }

  function showToast(msg, isErr) {
    if (!toast) return;
    toast.textContent = msg;
    toast.className = isErr ? 'err' : '';
    setTimeout(function () { if (toast.textContent === msg) toast.textContent = ''; }, 2500);
  }

  // ── saving ────────────────────────────────────────────────────────────────
  function currentText(target) {
    if (variant === 'v2' && window.__cmGet) {
      return window.__cmGet(target === 'left' ? 'a' : 'b');
    }
    return (target === 'left' ? left : right).value;
  }

  function save(target) {
    var text = currentText(target);
    var url = target === 'left' ? '/ui/resume/master' : '/ui/jobs/' + jobId + '/generate';
    if (target === 'right' && !jobId) { showToast('No job selected to save for', true); return; }
    var bodyData = new URLSearchParams({ profile: profile, markdown: text });
    fetch(url, {
      method: 'POST',
      headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      body: bodyData,
    }).then(function (res) {
      showToast(res.ok ? (target === 'left' ? 'Master saved ✓' : 'Saved for job ✓')
                       : 'Save failed (' + res.status + ')', !res.ok);
    }).catch(function () { showToast('Network error', true); });
  }

  Array.prototype.forEach.call(document.querySelectorAll('.save'), function (b) {
    b.addEventListener('click', function () { save(b.dataset.target); });
  });

  // PDF is the per-job document, so it renders the RIGHT (job) pane. POST the
  // job markdown to the existing resume.pdf route (which saves it then streams
  // the PDF) via a hidden form so the PDF opens in a new tab.
  var pdfBtn = document.getElementById('pdfBtn');
  if (pdfBtn) pdfBtn.addEventListener('click', function () {
    if (!jobId) { showToast('No job selected', true); return; }
    var f = document.createElement('form');
    f.method = 'POST'; f.action = '/ui/jobs/' + jobId + '/resume.pdf'; f.target = '_blank'; f.style.display = 'none';
    [['profile', profile], ['markdown', currentText('right')]].forEach(function (kv) {
      var inp = document.createElement('input'); inp.type = 'hidden'; inp.name = kv[0]; inp.value = kv[1]; f.appendChild(inp);
    });
    document.body.appendChild(f); f.submit(); f.remove();
  });

  // ── version history (SCD2) ───────────────────────────────────────────────
  // The right pane is the per-job résumé; every save keeps the old version, so
  // the dropdown lists them all and lets you preview/restore an earlier one.
  var versionSel = document.getElementById('versionSel');
  var restoreBtn = document.getElementById('restoreBtn');
  var versionMarkdown = {};   // version number -> markdown
  var currentVersion = null;

  function setRight(text) {
    if (variant === 'v2' && window.__cmSet) { window.__cmSet('b', text); return; }
    right.value = text;
    right.dispatchEvent(new Event('input')); // refresh v1 overlay
  }

  function updateRestoreBtn() {
    if (!restoreBtn || !versionSel) return;
    var sel = parseInt(versionSel.value, 10);
    var hide = !sel || sel === currentVersion;
    restoreBtn.style.display = hide ? 'none' : '';
    if (!hide) restoreBtn.textContent = '↩ Restore v' + sel;
  }

  function loadVersions() {
    if (!versionSel || !jobId) return;
    fetch('/ui/jobs/' + jobId + '/resume/versions?profile=' + encodeURIComponent(profile))
      .then(function (r) { return r.ok ? r.json() : []; })
      .then(function (list) {
        var picker = versionSel.closest('.ver-pick') || versionSel;
        if (!list || !list.length) { picker.style.display = 'none'; return; }
        picker.style.display = '';
        versionMarkdown = {};
        versionSel.innerHTML = '';
        list.forEach(function (v) {
          versionMarkdown[v.version] = v.markdown;
          if (v.isCurrent) currentVersion = v.version;
          var o = document.createElement('option');
          o.value = v.version;
          o.textContent = 'v' + v.version + (v.isCurrent ? ' (current)' : '') +
                          ' · ' + (v.generatedAt || '').slice(0, 16);
          versionSel.appendChild(o);
        });
        versionSel.value = currentVersion;
        updateRestoreBtn();
      }).catch(function () { /* offline / no versions — leave hidden */ });
  }

  if (versionSel) versionSel.addEventListener('change', function () {
    var sel = parseInt(versionSel.value, 10);
    if (versionMarkdown[sel] != null) setRight(versionMarkdown[sel]);
    updateRestoreBtn();
  });

  if (restoreBtn) restoreBtn.addEventListener('click', function () {
    var sel = parseInt(versionSel.value, 10);
    if (!jobId || !sel || sel === currentVersion) return;
    fetch('/ui/jobs/' + jobId + '/resume/versions/' + sel + '/restore', {
      method: 'POST',
      headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      body: new URLSearchParams({ profile: profile }),
    }).then(function (res) {
      if (!res.ok) { showToast('Restore failed (' + res.status + ')', true); return; }
      return res.json().then(function (j) {
        showToast('Restored as v' + j.version + ' ✓');
        loadVersions(); // refresh dropdown; right pane already shows this content
      });
    }).catch(function () { showToast('Network error', true); });
  });

  loadVersions();

  // ── scroll sync (v1, v3) ────────────────────────────────────────────────
  function syncScroll(a, b) {
    var lock = false;
    function bind(src, dst) {
      src.addEventListener('scroll', function () {
        if (lock) return;
        lock = true;
        var denom = Math.max(1, src.scrollHeight - src.clientHeight);
        dst.scrollTop = (src.scrollTop / denom) * Math.max(0, dst.scrollHeight - dst.clientHeight);
        requestAnimationFrame(function () { lock = false; });
      });
    }
    bind(a, b); bind(b, a);
  }

  // ── v1: highlight overlay ────────────────────────────────────────────────
  function buildBackdrop(ta) {
    var back = document.createElement('div');
    back.className = 'hl-backdrop';
    ta.parentElement.insertBefore(back, ta);
    ta.classList.add('hl-textarea');
    ta.addEventListener('scroll', function () {
      back.scrollTop = ta.scrollTop;
      back.scrollLeft = ta.scrollLeft;
    });
    return back;
  }
  function renderBackdrop(back, lines, cls) {
    var html = '';
    for (var k = 0; k < lines.length; k++) {
      var c = cls[k] === 'del' ? ' hl-del' : cls[k] === 'add' ? ' hl-add' : '';
      html += '<span class="hl-line' + c + '">' + (escapeHtml(lines[k]) || ' ') + '\n</span>';
    }
    back.innerHTML = html;
  }

  if (variant === 'v1') {
    var backL = buildBackdrop(left), backR = buildBackdrop(right);
    var refresh = function () {
      var d = lineDiff(left.value, right.value);
      renderBackdrop(backL, d.A, d.aCls);
      renderBackdrop(backR, d.B, d.bCls);
    };
    refresh();
    var deb = debounce(refresh);
    left.addEventListener('input', deb);
    right.addEventListener('input', deb);
    syncScroll(left, right);
  }

  // ── v3: compare on demand ────────────────────────────────────────────────
  if (variant === 'v3') {
    syncScroll(left, right);
    var out = document.getElementById('compareOut');
    var btn = document.getElementById('compareBtn');
    if (btn) btn.addEventListener('click', function () {
      var d = lineDiff(left.value, right.value);
      function col(lines, cls) {
        var h = '';
        for (var k = 0; k < lines.length; k++) {
          var c = cls[k] === 'del' ? ' cl-del' : cls[k] === 'add' ? ' cl-add' : '';
          h += '<span class="cl' + c + '">' + (escapeHtml(lines[k]) || ' ') + '\n</span>';
        }
        return h;
      }
      out.innerHTML = '<div class="cmp"><div class="cmp-col">' + col(d.A, d.aCls) +
                      '</div><div class="cmp-col">' + col(d.B, d.bCls) + '</div></div>';
    });
  }
})();
