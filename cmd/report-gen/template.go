package main

// htmlTemplate is a self-contained (no external assets) HTML report.
const htmlTemplate = `<!DOCTYPE html>
<html lang="ru">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}}</title>
<style>
  :root {
    --bg:#0f1419; --panel:#1a2027; --panel2:#222b34; --border:#2d3742;
    --txt:#e6edf3; --muted:#8b949e; --pass:#3fb950; --fail:#f85149;
    --skip:#d29922; --accent:#58a6ff;
  }
  * { box-sizing:border-box; }
  body { margin:0; background:var(--bg); color:var(--txt);
    font-family:-apple-system,Segoe UI,Roboto,Helvetica,Arial,sans-serif; line-height:1.5; }
  .wrap { max-width:1100px; margin:0 auto; padding:32px 20px 80px; }
  header h1 { margin:0 0 4px; font-size:24px; }
  .meta { color:var(--muted); font-size:13px; margin-bottom:24px; }
  .meta code { color:var(--accent); }
  .summary { display:flex; gap:16px; flex-wrap:wrap; margin-bottom:28px; }
  .stat { background:var(--panel); border:1px solid var(--border); border-radius:10px;
    padding:16px 22px; min-width:120px; }
  .stat .n { font-size:30px; font-weight:700; }
  .stat .l { color:var(--muted); font-size:12px; text-transform:uppercase; letter-spacing:.5px; }
  .stat.pass .n { color:var(--pass); } .stat.fail .n { color:var(--fail); }
  .stat.skip .n { color:var(--skip); }
  .bar { height:8px; border-radius:4px; background:var(--fail); overflow:hidden; margin-bottom:28px; display:flex; }
  .bar .ok { background:var(--pass); height:100%; }
  .bar .sk { background:var(--skip); height:100%; }
  .case { background:var(--panel); border:1px solid var(--border); border-radius:12px;
    margin-bottom:18px; overflow:hidden; }
  .case > summary { list-style:none; cursor:pointer; padding:16px 20px; display:flex;
    align-items:center; gap:14px; }
  .case > summary::-webkit-details-marker { display:none; }
  .badge { font-size:11px; font-weight:700; padding:3px 10px; border-radius:20px; text-transform:uppercase; }
  .badge.pass { background:rgba(63,185,80,.15); color:var(--pass); }
  .badge.fail { background:rgba(248,81,73,.15); color:var(--fail); }
  .badge.skip { background:rgba(210,153,34,.15); color:var(--skip); }
  .badge.run  { background:rgba(139,148,158,.15); color:var(--muted); }
  .case .name { font-weight:600; font-size:16px; flex:1; }
  .case .time { color:var(--muted); font-size:13px; font-variant-numeric:tabular-nums; }
  .body { padding:0 20px 20px; }
  .desc { color:var(--muted); font-style:italic; margin:0 0 16px; padding-left:12px;
    border-left:3px solid var(--border); }
  .metrics { display:flex; gap:12px; flex-wrap:wrap; margin-bottom:18px; }
  .metric { background:var(--panel2); border:1px solid var(--border); border-radius:8px;
    padding:10px 16px; }
  .metric .mv { font-size:20px; font-weight:700; }
  .metric .mv .u { font-size:12px; color:var(--muted); font-weight:400; margin-left:3px; }
  .metric .ml { font-size:11px; color:var(--muted); text-transform:uppercase; letter-spacing:.4px; }
  .section-title { font-size:12px; text-transform:uppercase; letter-spacing:.6px;
    color:var(--muted); margin:18px 0 8px; }
  ol.steps { margin:0; padding-left:22px; }
  ol.steps li { padding:3px 0; }
  ul.checks { list-style:none; padding:0; margin:0; }
  ul.checks li { padding:5px 0 5px 26px; position:relative; }
  ul.checks li.ok::before { content:"✓"; color:var(--pass); position:absolute; left:4px; font-weight:700; }
  ul.checks li.no::before { content:"✗"; color:var(--fail); position:absolute; left:4px; font-weight:700; }
  ul.infos { list-style:none; padding:0; margin:0; color:var(--muted); }
  ul.infos li { padding:2px 0; }
  ul.infos li::before { content:"•"; color:var(--accent); margin-right:8px; }
  pre.raw { background:#0b0f14; border:1px solid var(--border); border-radius:8px;
    padding:12px 14px; overflow:auto; font-size:12px; color:var(--muted); max-height:240px; }
  .raw-toggle { color:var(--accent); cursor:pointer; font-size:13px; }
  footer { color:var(--muted); font-size:12px; text-align:center; margin-top:40px; }
</style>
</head>
<body>
<div class="wrap">
  <header>
    <h1>{{.Title}}</h1>
    <div class="meta">
      Сгенерировано: {{.Generated}}{{if .Target}} · Цель: <code>{{.Target}}</code>{{end}}
      · Суммарное время: {{dur .Duration}}
    </div>
  </header>

  <div class="summary">
    <div class="stat"><div class="n">{{.Total}}</div><div class="l">Всего</div></div>
    <div class="stat pass"><div class="n">{{.Passed}}</div><div class="l">Прошло</div></div>
    <div class="stat fail"><div class="n">{{.Failed}}</div><div class="l">Упало</div></div>
    <div class="stat skip"><div class="n">{{.Skipped}}</div><div class="l">Пропущено</div></div>
  </div>

  <div class="bar">
    <div class="ok" style="width:{{pct .Passed .Total}}%"></div>
    <div class="sk" style="width:{{pct .Skipped .Total}}%"></div>
  </div>

  {{range .Cases}}
  <details class="case" {{if eq .Status "fail"}}open{{end}}>
    <summary>
      <span class="badge {{.Status}}">{{.Status}}</span>
      <span class="name">{{caseLabel .Name}}</span>
      <span class="time">{{dur .Elapsed}}</span>
    </summary>
    <div class="body">
      {{if .Description}}<p class="desc">{{.Description}}</p>{{end}}

      {{if .Metrics}}
      <div class="metrics">
        {{range .Metrics}}
        <div class="metric">
          <div class="mv">{{.Value}}{{if .Unit}}<span class="u">{{.Unit}}</span>{{end}}</div>
          <div class="ml">{{.Name}}</div>
        </div>
        {{end}}
      </div>
      {{end}}

      {{if .Checks}}
      <div class="section-title">Проверки</div>
      <ul class="checks">
        {{range .Checks}}<li class="{{if .OK}}ok{{else}}no{{end}}">{{.Label}}</li>{{end}}
      </ul>
      {{end}}

      {{if .Steps}}
      <div class="section-title">Шаги сценария</div>
      <ol class="steps">
        {{range .Steps}}<li>{{.}}</li>{{end}}
      </ol>
      {{end}}

      {{if .Infos}}
      <div class="section-title">Детали</div>
      <ul class="infos">
        {{range .Infos}}<li>{{.}}</li>{{end}}
      </ul>
      {{end}}

      {{if .RawOutput}}
      <div class="section-title">
        <span class="raw-toggle" onclick="this.parentElement.nextElementSibling.style.display=
          this.parentElement.nextElementSibling.style.display==='none'?'block':'none'">
          Полный лог ▾</span>
      </div>
      <pre class="raw" style="display:none">{{range .RawOutput}}{{.}}
{{end}}</pre>
      {{end}}
    </div>
  </details>
  {{end}}

  <footer>Отчёт сгенерирован report-gen · go test -json</footer>
</div>
</body>
</html>`
