const events = [];
let selectedIdx = -1;
let filterText = '';
let autoScroll = true;
let viewMode = 'events';
let statsSortKey = 'total';
let statsSortAsc = false;
let selectedStatsQuery = null;
let paused = false;
const collapsedTx = new Set();

// SQL syntax highlighting
const SQL_KW = new Set([
  'SELECT','FROM','WHERE','AND','OR','NOT','IN','IS','NULL','AS','ON',
  'JOIN','LEFT','RIGHT','INNER','OUTER','CROSS','FULL','NATURAL','USING',
  'INSERT','INTO','VALUES','UPDATE','SET','DELETE',
  'CREATE','ALTER','DROP','TABLE','INDEX','IF','EXISTS',
  'ORDER','BY','GROUP','HAVING','LIMIT','OFFSET',
  'UNION','ALL','DISTINCT','BETWEEN','LIKE','ILIKE',
  'CASE','WHEN','THEN','ELSE','END','ASC','DESC',
  'WITH','RECURSIVE','RETURNING',
  'BEGIN','COMMIT','ROLLBACK',
  'EXPLAIN','ANALYZE',
  'PRIMARY','KEY','FOREIGN','REFERENCES','CONSTRAINT',
  'DEFAULT','CHECK','UNIQUE','CASCADE',
  'ADD','COLUMN','RENAME','TO',
  'TRUE','FALSE',
  'EXCEPT','INTERSECT',
  'FETCH','FIRST','NEXT','ROWS','ONLY',
  'FOR','SHARE','NO','WAIT','SKIP','LOCKED',
  'OVER','PARTITION','WINDOW',
]);
const SQL_FN = new Set([
  'COUNT','SUM','AVG','MIN','MAX',
  'COALESCE','NULLIF','CAST','NOW',
  'CURRENT_TIMESTAMP','CURRENT_DATE','CURRENT_TIME',
  'EXTRACT','DATE_TRUNC',
  'LENGTH','LOWER','UPPER','TRIM','SUBSTRING','CONCAT','REPLACE',
  'ARRAY_AGG','STRING_AGG','JSON_AGG',
  'ROW_NUMBER','RANK','DENSE_RANK','LEAD','LAG',
  'FIRST_VALUE','LAST_VALUE',
  'GREATEST','LEAST','ABS','CEIL','FLOOR','ROUND','RANDOM',
]);

function highlightSQL(sql) {
  if (!sql || sql === '-') return escapeHTML(sql || '-');
  let out = '';
  let i = 0;
  while (i < sql.length) {
    // String literal
    if (sql[i] === "'") {
      let j = i + 1;
      while (j < sql.length) {
        if (sql[j] === "'" && sql[j + 1] === "'") { j += 2; continue; }
        if (sql[j] === "'") { j++; break; }
        j++;
      }
      out += '<span class="sql-str">' + escapeHTML(sql.slice(i, j)) + '</span>';
      i = j;
      continue;
    }
    // Parameter $1, $2, ...
    if (sql[i] === '$' && i + 1 < sql.length && sql[i + 1] >= '0' && sql[i + 1] <= '9') {
      let j = i + 1;
      while (j < sql.length && sql[j] >= '0' && sql[j] <= '9') j++;
      out += '<span class="sql-param">' + escapeHTML(sql.slice(i, j)) + '</span>';
      i = j;
      continue;
    }
    // Parameter ?
    if (sql[i] === '?') {
      out += '<span class="sql-param">?</span>';
      i++;
      continue;
    }
    // Number (after whitespace, comma, paren, operator, or start)
    if (sql[i] >= '0' && sql[i] <= '9' && (i === 0 || /[\s,=(><+\-]/.test(sql[i - 1]))) {
      let j = i;
      while (j < sql.length && ((sql[j] >= '0' && sql[j] <= '9') || sql[j] === '.')) j++;
      out += '<span class="sql-num">' + escapeHTML(sql.slice(i, j)) + '</span>';
      i = j;
      continue;
    }
    // Word
    if ((sql[i] >= 'a' && sql[i] <= 'z') || (sql[i] >= 'A' && sql[i] <= 'Z') || sql[i] === '_') {
      let j = i;
      while (j < sql.length && ((sql[j] >= 'a' && sql[j] <= 'z') || (sql[j] >= 'A' && sql[j] <= 'Z') || (sql[j] >= '0' && sql[j] <= '9') || sql[j] === '_')) j++;
      const word = sql.slice(i, j);
      const upper = word.toUpperCase();
      if (SQL_KW.has(upper)) {
        out += '<span class="sql-kw">' + escapeHTML(word) + '</span>';
      } else if (SQL_FN.has(upper)) {
        out += '<span class="sql-fn">' + escapeHTML(word) + '</span>';
      } else {
        out += escapeHTML(word);
      }
      i = j;
      continue;
    }
    // Everything else
    out += escapeHTML(sql[i]);
    i++;
  }
  return out;
}

const tbody = document.getElementById('tbody');
const tableWrap = document.getElementById('table-wrap');
const statsWrap = document.getElementById('stats-wrap');
const statsTbody = document.getElementById('stats-tbody');
const statsEl = document.getElementById('stats');
const statusEl = document.getElementById('status');
const filterEl = document.getElementById('filter');
const detailEl = document.getElementById('detail');
const statsDetailEl = document.getElementById('stats-detail');
const explainOutput = document.getElementById('explain-output');

filterEl.addEventListener('input', () => {
  filterText = filterEl.value;
  render();
});

tableWrap.addEventListener('scroll', () => {
  const el = tableWrap;
  autoScroll = el.scrollTop + el.clientHeight >= el.scrollHeight - 20;
});

// Stats sort header clicks
document.querySelectorAll('#stats-wrap th.sortable').forEach(th => {
  th.addEventListener('click', () => {
    const key = th.dataset.sort;
    if (statsSortKey === key) {
      statsSortAsc = !statsSortAsc;
    } else {
      statsSortKey = key;
      statsSortAsc = false;
    }
    document.querySelectorAll('#stats-wrap th.sortable').forEach(h => h.classList.remove('active'));
    th.classList.add('active');
    renderStats();
  });
});

function switchView(mode) {
  viewMode = mode;
  document.getElementById('tab-events').classList.toggle('active', mode === 'events');
  document.getElementById('tab-stats').classList.toggle('active', mode === 'stats');
  tableWrap.style.display = mode === 'events' ? '' : 'none';
  statsWrap.style.display = mode === 'stats' ? '' : 'none';
  if (mode === 'events') {
    detailEl.className = selectedIdx >= 0 ? 'open' : '';
    statsDetailEl.className = '';
  } else {
    detailEl.className = '';
    statsDetailEl.className = selectedStatsQuery ? 'open' : '';
  }
  render();
}

let renderPending = false;
function render() {
  if (renderPending) return;
  renderPending = true;
  requestAnimationFrame(() => {
    renderPending = false;
    if (viewMode === 'events') {
      renderTable();
    } else {
      renderStats();
    }
  });
}

// Filter parsing (matches TUI filter.go syntax)
const RE_DURATION = /^d([><])(\d+(?:\.\d+)?)(us|µs|ms|s|m)$/;
const OP_KEYWORDS = new Set(['select', 'insert', 'update', 'delete']);
const PROTOCOL_OPS = new Set(['query', 'exec', 'prepare', 'bind', 'execute', 'begin', 'commit', 'rollback']);

function parseFilterTokens(input) {
  if (!input.trim()) return [];
  const tokens = input.trim().split(/\s+/);
  return tokens.map(tok => {
    const dm = RE_DURATION.exec(tok);
    if (dm) {
      const op = dm[1];
      const val = parseFloat(dm[2]);
      const unit = dm[3];
      let ms;
      switch (unit) {
        case 'us': case 'µs': ms = val / 1000; break;
        case 'ms': ms = val; break;
        case 's': ms = val * 1000; break;
        case 'm': ms = val * 60000; break;
        default: ms = val;
      }
      return {kind: 'duration', op, ms};
    }
    if (tok.toLowerCase() === 'error') return {kind: 'error'};
    const lower = tok.toLowerCase();
    if (lower.startsWith('op:') && lower.length > 3) return {kind: 'op', pattern: lower.slice(3)};
    return {kind: 'text', text: lower};
  });
}

function matchesFilter(ev, cond) {
  switch (cond.kind) {
    case 'duration':
      return cond.op === '>' ? ev.duration_ms > cond.ms : ev.duration_ms < cond.ms;
    case 'error':
      return !!ev.error;
    case 'op':
      if (PROTOCOL_OPS.has(cond.pattern)) return ev.op.toLowerCase() === cond.pattern;
      if (OP_KEYWORDS.has(cond.pattern)) return (ev.query || '').trim().toLowerCase().startsWith(cond.pattern);
      return false;
    case 'text':
      return (ev.query || '').toLowerCase().includes(cond.text) ||
             ev.op.toLowerCase().includes(cond.text) ||
             (ev.error && ev.error.toLowerCase().includes(cond.text));
  }
  return false;
}

function getFiltered() {
  const conds = parseFilterTokens(filterText);
  if (conds.length === 0) return events.map((ev, i) => ({ev, idx: i}));
  return events.reduce((acc, ev, i) => {
    if (conds.every(c => matchesFilter(ev, c))) acc.push({ev, idx: i});
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

const TX_SKIP_OPS = new Set(['Begin', 'Commit', 'Rollback', 'Bind', 'Prepare']);

function buildDisplayRows() {
  const conds = parseFilterTokens(filterText);
  const hasFilter = conds.length > 0;

  // With filter active → flat list (current behavior)
  if (hasFilter) {
    const filtered = getFiltered();
    return filtered.map(({ev, idx}) => ({kind: 'event', eventIdx: idx}));
  }

  // No filter → group by tx
  const rows = [];
  const seenTx = new Set();

  // Pre-index events by tx_id to avoid O(n^2) scans
  const txIndex = new Map();
  for (let i = 0; i < events.length; i++) {
    const ev = events[i];
    const txId = ev.tx_id;
    if (!txId) continue;
    let entry = txIndex.get(txId);
    if (!entry) {
      entry = { indices: [] };
      txIndex.set(txId, entry);
    }
    entry.indices.push(i);
  }

  for (let i = 0; i < events.length; i++) {
    const ev = events[i];
    const txId = ev.tx_id;

    if (txId && ev.op === 'Begin' && !seenTx.has(txId)) {
      seenTx.add(txId);
      const entry = txIndex.get(txId);
      const indices = entry ? entry.indices : [i];
      rows.push({kind: 'tx', txId, eventIndices: indices});
      if (!collapsedTx.has(txId)) {
        for (const j of indices) {
          rows.push({kind: 'event', eventIdx: j});
        }
      }
    } else if (txId && seenTx.has(txId)) {
      // Already handled by summary — skip
    } else {
      rows.push({kind: 'event', eventIdx: i});
    }
  }
  return rows;
}

function txSummaryInfo(indices) {
  const queryCount = indices.filter(i => !TX_SKIP_OPS.has(events[i].op)).length;
  const first = events[indices[0]];
  const last = events[indices[indices.length - 1]];
  const startMs = new Date(first.start_time).getTime();
  const endMs = new Date(last.start_time).getTime() + last.duration_ms;
  const durationMs = endMs - startMs;
  return {queryCount, durationMs, time: first.start_time};
}

let txColorMap = new Map();
let txColorCounter = 0;

function getTxColor(txId) {
  if (!txColorMap.has(txId)) {
    txColorMap.set(txId, txColorCounter % 6);
    txColorCounter++;
  }
  return txColorMap.get(txId);
}

function renderTable() {
  const displayRows = buildDisplayRows();
  const hasFilter = filterText.trim().length > 0;
  const pauseLabel = paused ? ' (paused)' : '';
  const eventCount = hasFilter
    ? displayRows.length + '/' + events.length
    : String(events.length);
  statsEl.textContent = `${eventCount} queries${pauseLabel}`;

  const fragment = document.createDocumentFragment();
  for (const row of displayRows) {
    if (row.kind === 'tx') {
      const info = txSummaryInfo(row.eventIndices);
      const collapsed = collapsedTx.has(row.txId);
      const chevron = collapsed ? '\u25b8' : '\u25be';
      const colorIdx = getTxColor(row.txId);
      const tr = document.createElement('tr');
      tr.className = 'row tx-summary';
      tr.dataset.txColor = colorIdx;
      tr.onclick = () => toggleTx(row.txId);
      tr.innerHTML =
        `<td class="col-time"><span class="tx-chevron">${chevron}</span>${escapeHTML(fmtTime(info.time))}</td>` +
        `<td class="col-op">Tx</td>` +
        `<td class="col-query">${info.queryCount} queries</td>` +
        `<td class="col-dur">${escapeHTML(fmtDur(info.durationMs))}</td>` +
        `<td class="col-err"></td>`;
      fragment.appendChild(tr);
    } else {
      const idx = row.eventIdx;
      const ev = events[idx];
      const colorIdx = ev.tx_id ? getTxColor(ev.tx_id) : undefined;
      const isTxChild = !hasFilter && ev.tx_id;
      const tr = document.createElement('tr');
      tr.className = 'row' +
        (isTxChild ? ' tx-child' : '') +
        (idx === selectedIdx ? ' selected' : '') +
        (ev.error ? ' has-error' : '') +
        (ev.n_plus_1 ? ' n-plus-1' : '') +
        (ev.slow_query ? ' slow-query' : '');
      if (colorIdx !== undefined) tr.dataset.txColor = colorIdx;
      tr.dataset.idx = idx;
      tr.onclick = () => selectRow(idx);
      const status = ev.error ? 'E' : ev.n_plus_1 ? 'N+1' : ev.slow_query ? 'SLOW' : '';
      tr.innerHTML =
        `<td class="col-time">${escapeHTML(fmtTime(ev.start_time))}</td>` +
        `<td class="col-op">${escapeHTML(ev.op)}</td>` +
        `<td class="col-query">${highlightSQL(ev.query)}</td>` +
        `<td class="col-dur">${escapeHTML(fmtDur(ev.duration_ms))}</td>` +
        `<td class="col-err">${status}</td>`;
      fragment.appendChild(tr);
    }
  }
  tbody.replaceChildren(fragment);

  if (autoScroll && selectedIdx < 0) {
    tableWrap.scrollTop = tableWrap.scrollHeight;
  }
}

// --- Stats view ---

function buildStats() {
  const groups = new Map();
  const skipOps = new Set(['Begin', 'Commit', 'Rollback', 'Bind', 'Prepare']);
  const textConds = parseFilterTokens(filterText).filter(c => c.kind === 'text');
  for (const ev of events) {
    if (skipOps.has(ev.op)) continue;
    const nq = ev.normalized_query;
    if (!nq) continue;
    if (textConds.length > 0 && !textConds.every(c => nq.toLowerCase().includes(c.text))) continue;
    let group = groups.get(nq);
    if (!group) {
      group = {query: nq, durations: []};
      groups.set(nq, group);
    }
    group.durations.push(ev.duration_ms);
  }
  const rows = [];
  for (const g of groups.values()) {
    const durs = g.durations.sort((a, b) => a - b);
    const count = durs.length;
    const total = durs.reduce((s, d) => s + d, 0);
    const avg = total / count;
    const p95 = durs[Math.floor((count - 1) * 0.95)];
    const mx = durs[count - 1];
    rows.push({query: g.query, count, avg, p95, max: mx, total});
  }
  return rows;
}

function sortStats(rows) {
  const dir = statsSortAsc ? 1 : -1;
  rows.sort((a, b) => {
    const va = a[statsSortKey];
    const vb = b[statsSortKey];
    if (va < vb) return -1 * dir;
    if (va > vb) return 1 * dir;
    return 0;
  });
}

function renderStats() {
  const rows = buildStats();
  sortStats(rows);
  statsEl.textContent = `${rows.length} templates`;

  const fragment = document.createDocumentFragment();
  for (const r of rows) {
    const tr = document.createElement('tr');
    tr.className = 'row' + (selectedStatsQuery === r.query ? ' selected' : '');
    tr.onclick = () => selectStatsRow(r);
    tr.innerHTML =
      `<td class="stats-col-count">${r.count}</td>` +
      `<td class="stats-col-dur">${fmtDur(r.avg)}</td>` +
      `<td class="stats-col-dur">${fmtDur(r.p95)}</td>` +
      `<td class="stats-col-dur">${fmtDur(r.max)}</td>` +
      `<td class="stats-col-dur">${fmtDur(r.total)}</td>` +
      `<td class="stats-col-query" title="${escapeHTML(r.query)}">${highlightSQL(r.query)}</td>`;
    fragment.appendChild(tr);
  }
  statsTbody.replaceChildren(fragment);
}

function selectStatsRow(r) {
  if (selectedStatsQuery === r.query) {
    selectedStatsQuery = null;
    statsDetailEl.className = '';
    renderStats();
    return;
  }
  selectedStatsQuery = r.query;

  document.getElementById('sd-metrics').innerHTML =
    `<span class="detail-label">Count:</span><span class="detail-value">${r.count}</span>` +
    `<span class="detail-label" style="margin-left:12px">Avg:</span><span class="detail-value">${fmtDur(r.avg)}</span>` +
    `<span class="detail-label" style="margin-left:12px">P95:</span><span class="detail-value">${fmtDur(r.p95)}</span>` +
    `<span class="detail-label" style="margin-left:12px">Max:</span><span class="detail-value">${fmtDur(r.max)}</span>` +
    `<span class="detail-label" style="margin-left:12px">Total:</span><span class="detail-value">${fmtDur(r.total)}</span>`;
  document.getElementById('sd-query').innerHTML = highlightSQL(r.query);
  statsDetailEl.className = 'open';
  renderStats();
}

function copyStatsQuery() {
  if (!selectedStatsQuery) return;
  if (navigator.clipboard && navigator.clipboard.writeText) {
    navigator.clipboard.writeText(selectedStatsQuery).then(() => showToast('Copied!')).catch(() => fallbackCopy(selectedStatsQuery));
  } else {
    fallbackCopy(selectedStatsQuery);
  }
}

function toggleTx(txId) {
  if (collapsedTx.has(txId)) {
    collapsedTx.delete(txId);
  } else {
    collapsedTx.add(txId);
    if (selectedIdx >= 0 && events[selectedIdx] && events[selectedIdx].tx_id === txId) {
      selectedIdx = -1;
      detailEl.className = '';
    }
  }
  render();
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

  document.getElementById('d-query').innerHTML = highlightSQL(ev.query);
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

function togglePause() {
  paused = !paused;
  const btn = document.getElementById('btn-pause');
  btn.textContent = paused ? 'Resume' : 'Pause';
  btn.classList.toggle('active', paused);
  render();
}

function clearEvents() {
  events.length = 0;
  selectedIdx = -1;
  selectedStatsQuery = null;
  collapsedTx.clear();
  txColorMap = new Map();
  txColorCounter = 0;
  detailEl.className = '';
  statsDetailEl.className = '';
  render();
}

// SSE
function connectSSE() {
  const es = new EventSource('/api/events');
  es.onopen = () => {
    statusEl.textContent = 'connected';
    statusEl.className = 'status connected';
  };
  es.onmessage = (e) => {
    if (paused) return;
    const ev = JSON.parse(e.data);
    events.push(ev);
    if (ev.n_plus_1) {
      showToast('N+1 detected: ' + (ev.query || '').substring(0, 80));
    } else if (ev.slow_query) {
      showToast('Slow query: ' + (ev.query || '').substring(0, 80));
    }
    render();
  };
  es.onerror = () => {
    statusEl.textContent = 'disconnected';
    statusEl.className = 'status disconnected';
    es.close();
    setTimeout(connectSSE, 2000);
  };
}

connectSSE();
