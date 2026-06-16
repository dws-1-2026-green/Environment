// Self-contained HTML summary generator for k6 (no external/network imports,
// so it works offline and in-cluster). Produces a styled report from the
// summary `data` object passed to handleSummary().

function fmt(n, digits) {
  if (n === undefined || n === null || Number.isNaN(n)) return '—';
  return n.toFixed(digits === undefined ? 2 : digits);
}

function metricRow(label, m, unit) {
  if (!m || !m.values) return '';
  const v = m.values;
  const cells =
    v['p(99)'] !== undefined
      ? `<td>${fmt(v.avg)}</td><td>${fmt(v.min)}</td><td>${fmt(v.med)}</td>
         <td>${fmt(v['p(90)'])}</td><td>${fmt(v['p(95)'])}</td>
         <td>${fmt(v['p(99)'])}</td><td>${fmt(v.max)}</td>`
      : `<td colspan="7">${fmt(v.count, 0)} (rate ${fmt(v.rate)}/s)</td>`;
  return `<tr><th>${label}${unit ? ` <span class="u">${unit}</span>` : ''}</th>${cells}</tr>`;
}

function thresholdRows(data) {
  const rows = [];
  Object.keys(data.metrics).forEach((name) => {
    const m = data.metrics[name];
    if (!m.thresholds) return;
    Object.keys(m.thresholds).forEach((t) => {
      const ok = m.thresholds[t].ok !== false;
      rows.push(
        `<li class="${ok ? 'ok' : 'no'}"><code>${name}</code>: ${t} → ${ok ? 'PASS' : 'FAIL'}</li>`
      );
    });
  });
  return rows.length ? rows.join('') : '<li class="muted">порогов не задано</li>';
}

// textSummary renders a compact plaintext summary for stdout — a local
// replacement for the remote jslib k6-summary module (keeps the image offline).
export function textSummary(data) {
  const m = data.metrics;
  const get = (name, key) => (m[name] && m[name].values ? m[name].values[key] : undefined);
  const lines = [];
  lines.push('');
  lines.push('  ── k6 summary ─────────────────────────────');
  lines.push(`  requests ......... ${fmt(get('http_reqs', 'count'), 0)}`);
  lines.push(`  throughput ....... ${fmt(get('http_reqs', 'rate'), 1)} req/s`);
  lines.push(`  failed ........... ${fmt((get('http_req_failed', 'rate') || 0) * 100, 2)} %`);
  lines.push(`  latency avg ...... ${fmt(get('http_req_duration', 'avg'), 1)} ms`);
  lines.push(`  latency p95 ...... ${fmt(get('http_req_duration', 'p(95)'), 1)} ms`);
  lines.push(`  latency p99 ...... ${fmt(get('http_req_duration', 'p(99)'), 1)} ms`);
  lines.push(`  latency max ...... ${fmt(get('http_req_duration', 'max'), 1)} ms`);
  if (m.accepted_events) lines.push(`  accepted ......... ${fmt(get('accepted_events', 'count'), 0)}`);
  if (m.rejected_events) lines.push(`  rejected ......... ${fmt(get('rejected_events', 'count'), 0)}`);
  lines.push('  ───────────────────────────────────────────');
  lines.push('');
  return lines.join('\n');
}

export function htmlReport(data, meta) {
  meta = meta || {};
  const m = data.metrics;
  const reqs = m.http_reqs ? m.http_reqs.values : {};
  const failed = m.http_req_failed ? m.http_req_failed.values : {};
  const dur = m.http_req_duration ? m.http_req_duration.values : {};
  const checks = m.checks ? m.checks.values : {};

  const totalReqs = reqs.count || 0;
  const throughput = reqs.rate || 0;
  const errRate = (failed.rate || 0) * 100;
  const checkRate = (checks.rate || 0) * 100;

  const accepted = m.accepted_events ? m.accepted_events.values.count : undefined;
  const rejected = m.rejected_events ? m.rejected_events.values.count : undefined;

  return `<!DOCTYPE html>
<html lang="ru"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>${meta.title || 'k6 нагрузочный тест'}</title>
<style>
  :root{--bg:#0f1419;--panel:#1a2027;--panel2:#222b34;--border:#2d3742;
    --txt:#e6edf3;--muted:#8b949e;--pass:#3fb950;--fail:#f85149;--accent:#58a6ff;}
  *{box-sizing:border-box}body{margin:0;background:var(--bg);color:var(--txt);
    font-family:-apple-system,Segoe UI,Roboto,Arial,sans-serif;line-height:1.5}
  .wrap{max-width:1000px;margin:0 auto;padding:32px 20px 70px}
  h1{margin:0 0 4px;font-size:24px}
  .meta{color:var(--muted);font-size:13px;margin-bottom:24px}
  .meta code{color:var(--accent)}
  .tiles{display:flex;gap:16px;flex-wrap:wrap;margin-bottom:28px}
  .tile{background:var(--panel);border:1px solid var(--border);border-radius:10px;padding:16px 22px;min-width:140px}
  .tile .n{font-size:28px;font-weight:700}
  .tile .n .u{font-size:13px;color:var(--muted);font-weight:400;margin-left:4px}
  .tile .l{color:var(--muted);font-size:12px;text-transform:uppercase;letter-spacing:.5px}
  .tile.bad .n{color:var(--fail)}.tile.good .n{color:var(--pass)}
  table{width:100%;border-collapse:collapse;background:var(--panel);border:1px solid var(--border);
    border-radius:10px;overflow:hidden;margin-bottom:28px}
  th,td{padding:10px 12px;text-align:right;font-variant-numeric:tabular-nums;font-size:13px}
  thead th{background:var(--panel2);color:var(--muted);text-transform:uppercase;font-size:11px;letter-spacing:.4px}
  tbody th{text-align:left;color:var(--txt);font-weight:600}
  tbody th .u{color:var(--muted);font-weight:400;font-size:11px}
  tr:nth-child(even) td,tr:nth-child(even) th{background:rgba(255,255,255,.02)}
  .section-title{font-size:12px;text-transform:uppercase;letter-spacing:.6px;color:var(--muted);margin:18px 0 8px}
  ul.thr{list-style:none;padding:0;margin:0 0 24px}
  ul.thr li{padding:5px 0 5px 26px;position:relative}
  ul.thr li.ok::before{content:"✓";color:var(--pass);position:absolute;left:4px;font-weight:700}
  ul.thr li.no::before{content:"✗";color:var(--fail);position:absolute;left:4px;font-weight:700}
  ul.thr li.muted{color:var(--muted)} code{color:var(--accent)}
  footer{color:var(--muted);font-size:12px;text-align:center;margin-top:40px}
</style></head><body><div class="wrap">
  <h1>${meta.title || 'k6 нагрузочный тест'}</h1>
  <div class="meta">Сгенерировано: ${new Date().toISOString()}${
    meta.target ? ` · Цель: <code>${meta.target}</code>` : ''
  }${meta.scenario ? ` · Профиль: <code>${meta.scenario}</code>` : ''}</div>

  <div class="tiles">
    <div class="tile"><div class="n">${totalReqs.toLocaleString()}</div><div class="l">Запросов</div></div>
    <div class="tile good"><div class="n">${fmt(throughput, 1)}<span class="u">req/s</span></div><div class="l">Throughput</div></div>
    <div class="tile"><div class="n">${fmt(dur['p(95)'], 0)}<span class="u">ms</span></div><div class="l">Latency p95</div></div>
    <div class="tile"><div class="n">${fmt(dur['p(99)'], 0)}<span class="u">ms</span></div><div class="l">Latency p99</div></div>
    <div class="tile ${errRate > 1 ? 'bad' : 'good'}"><div class="n">${fmt(errRate, 2)}<span class="u">%</span></div><div class="l">Ошибки</div></div>
    ${
      accepted !== undefined
        ? `<div class="tile"><div class="n">${accepted.toLocaleString()}</div><div class="l">Принято</div></div>`
        : ''
    }
    ${
      rejected !== undefined && rejected > 0
        ? `<div class="tile bad"><div class="n">${rejected.toLocaleString()}</div><div class="l">Отклонено</div></div>`
        : ''
    }
    <div class="tile"><div class="n">${fmt(checkRate, 1)}<span class="u">%</span></div><div class="l">Checks passed</div></div>
  </div>

  <div class="section-title">Latency / тайминги (мс)</div>
  <table>
    <thead><tr><th style="text-align:left">Метрика</th><th>avg</th><th>min</th><th>med</th>
      <th>p90</th><th>p95</th><th>p99</th><th>max</th></tr></thead>
    <tbody>
      ${metricRow('http_req_duration', m.http_req_duration, 'ms')}
      ${metricRow('http_req_waiting', m.http_req_waiting, 'ms')}
      ${metricRow('http_req_connecting', m.http_req_connecting, 'ms')}
      ${metricRow('http_reqs', m.http_reqs, '')}
      ${metricRow('iterations', m.iterations, '')}
    </tbody>
  </table>

  <div class="section-title">Пороги (thresholds)</div>
  <ul class="thr">${thresholdRows(data)}</ul>

  <footer>Отчёт сгенерирован k6 · handleSummary</footer>
</div></body></html>`;
}
