const events = [];
let selectedIdx = -1;
let filterText = '';
let autoScroll = true;

const tbody = document.getElementById('tbody');
const tableWrap = document.getElementById('table-wrap');
const statsEl = document.getElementById('stats');
const statusEl = document.getElementById('status');
const filterEl = document.getElementById('filter');
const detailEl = document.getElementById('detail');
const explainOutput = document.getElementById('explain-output');

filterEl.addEventListener('input', () => {
  filterText = filterEl.value.toLowerCase();
  renderTable();
});

tableWrap.addEventListener('scroll', () => {
  const el = tableWrap;
  autoScroll = el.scrollTop + el.clientHeight >= el.scrollHeight - 20;
});

function getFiltered() {
  if (!filterText) return events.map((ev, i) => ({ev, idx: i}));
  return events.reduce((acc, ev, i) => {
    if (ev.query.toLowerCase().includes(filterText) ||
        ev.op.toLowerCase().includes(filterText) ||
        (ev.error && ev.error.toLowerCase().includes(filterText)))
      acc.push({ev, idx: i});
    return acc;
  }, []);
}

function fmtDur(ms) {
  if (ms < 1) return (ms * 1000).toFixed(0) + '\u00b5s';
  if (ms < 1000) return ms.toFixed(1) + 'ms';
  return (ms / 1000).toFixed(2) + 's';
}

function fmtTime(iso) {
  const d = new Date(iso);
  return d.toLocaleTimeString('en-GB', {hour12: false}) + '.' + String(d.getMilliseconds()).padStart(3, '0');
}

function escapeHTML(s) {
  const el = document.createElement('span');
  el.textContent = s;
  return el.innerHTML;
}

function renderTable() {
  const filtered = getFiltered();
  statsEl.textContent = filterText
    ? `${filtered.length}/${events.length} queries`
    : `${events.length} queries`;

  const fragment = document.createDocumentFragment();
  for (const {ev, idx} of filtered) {
    const tr = document.createElement('tr');
    tr.className = 'row' + (idx === selectedIdx ? ' selected' : '') + (ev.error ? ' has-error' : '') + (ev.n_plus_1 ? ' n-plus-1' : '');
    tr.dataset.idx = idx;
    tr.onclick = () => selectRow(idx);
    tr.innerHTML =
      `<td class="col-time">${escapeHTML(fmtTime(ev.start_time))}</td>` +
      `<td class="col-op">${escapeHTML(ev.op)}</td>` +
      `<td class="col-query">${escapeHTML(ev.query || '-')}</td>` +
      `<td class="col-dur">${escapeHTML(fmtDur(ev.duration_ms))}</td>` +
      `<td class="col-err">${ev.error ? 'E' : ev.n_plus_1 ? 'N+1' : ''}</td>`;
    fragment.appendChild(tr);
  }
  tbody.replaceChildren(fragment);

  if (autoScroll && selectedIdx < 0) {
    tableWrap.scrollTop = tableWrap.scrollHeight;
  }
}

function selectRow(idx) {
  if (selectedIdx === idx) {
    selectedIdx = -1;
    detailEl.className = '';
    renderTable();
    return;
  }
  selectedIdx = idx;
  const ev = events[idx];
  document.getElementById('d-op').textContent = ev.op;
  document.getElementById('d-time').textContent = fmtTime(ev.start_time);
  document.getElementById('d-dur').textContent = fmtDur(ev.duration_ms);

  const rowsRow = document.getElementById('d-rows-row');
  if (ev.rows_affected > 0) {
    document.getElementById('d-rows').textContent = ev.rows_affected;
    rowsRow.style.display = '';
  } else {
    rowsRow.style.display = 'none';
  }

  const txRow = document.getElementById('d-tx-row');
  if (ev.tx_id) {
    document.getElementById('d-tx').textContent = ev.tx_id;
    txRow.style.display = '';
  } else {
    txRow.style.display = 'none';
  }

  const errRow = document.getElementById('d-err-row');
  if (ev.error) {
    document.getElementById('d-err').textContent = ev.error;
    errRow.style.display = '';
  } else {
    errRow.style.display = 'none';
  }

  document.getElementById('d-query').textContent = ev.query || '-';
  document.getElementById('d-args').textContent = ev.args && ev.args.length > 0
    ? 'Args: [' + ev.args.map(a => "'" + a + "'").join(', ') + ']'
    : '';

  const isQueryOp = ['Query', 'Exec', 'Execute'].includes(ev.op);
  document.getElementById('btn-explain').disabled = !isQueryOp || !ev.query;
  document.getElementById('btn-analyze').disabled = !isQueryOp || !ev.query;

  explainOutput.className = '';
  detailEl.className = 'open';
  renderTable();
}

async function runExplain(analyze) {
  if (selectedIdx < 0) return;
  const ev = events[selectedIdx];
  const pre = document.getElementById('explain-pre');
  pre.textContent = 'Running...';
  pre.className = '';
  explainOutput.className = 'open';

  try {
    const resp = await fetch('/api/explain', {
      method: 'POST',
      headers: {'Content-Type': 'application/json'},
      body: JSON.stringify({query: ev.query, args: ev.args, analyze}),
    });
    const data = await resp.json();
    if (data.error) {
      pre.textContent = data.error;
      pre.className = 'explain-error';
    } else {
      pre.textContent = data.plan;
      pre.className = '';
    }
  } catch (e) {
    pre.textContent = 'Request failed: ' + e.message;
    pre.className = 'explain-error';
  }
}

function copyQuery(withArgs) {
  if (selectedIdx < 0) return;
  const ev = events[selectedIdx];
  let text = ev.query || '';
  if (withArgs && ev.args && ev.args.length > 0) {
    text = embedArgs(text, ev.args);
  }
  if (navigator.clipboard && navigator.clipboard.writeText) {
    navigator.clipboard.writeText(text).then(() => showToast('Copied!')).catch(() => fallbackCopy(text));
  } else {
    fallbackCopy(text);
  }
}

function embedArgs(query, args) {
  // $1, $2, ... (PostgreSQL style)
  if (/\$\d+/.test(query)) {
    return query.replace(/\$(\d+)/g, (_, n) => {
      const i = parseInt(n, 10) - 1;
      return i < args.length ? quoteArg(args[i]) : '$' + n;
    });
  }
  // ? (MySQL style)
  let i = 0;
  return query.replace(/\?/g, () => i < args.length ? quoteArg(args[i++]) : '?');
}

function quoteArg(v) {
  if (v === null || v === undefined || v === '') return "''";
  if (!isNaN(v) && v.trim() !== '') return v;
  return "'" + v.replace(/'/g, "''") + "'";
}

function fallbackCopy(text) {
  const ta = document.createElement('textarea');
  ta.value = text;
  ta.style.position = 'fixed';
  ta.style.opacity = '0';
  document.body.appendChild(ta);
  ta.select();
  document.execCommand('copy');
  document.body.removeChild(ta);
  showToast('Copied!');
}

function showToast(msg) {
  const t = document.getElementById('toast');
  t.textContent = msg;
  t.classList.add('show');
  setTimeout(() => t.classList.remove('show'), 2000);
}

// SSE
function connectSSE() {
  const es = new EventSource('/api/events');
  es.onopen = () => {
    statusEl.textContent = 'connected';
    statusEl.className = 'status connected';
  };
  es.onmessage = (e) => {
    const ev = JSON.parse(e.data);
    events.push(ev);
    if (ev.n_plus_1) {
      showToast('N+1 detected: ' + (ev.query || '').substring(0, 80));
    }
    renderTable();
  };
  es.onerror = () => {
    statusEl.textContent = 'disconnected';
    statusEl.className = 'status disconnected';
    es.close();
    setTimeout(connectSSE, 2000);
  };
}

connectSSE();
