package web

const IndexHTML = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>LLM Agent Chat</title>
<style>
  :root {
    --bg: #0b0b0b; --surface: #141414; --sidebar: #111111;
    --border: #222; --border-light: #2a2a2a;
    --text: #e8e8e8; --text-dim: #777; --text-muted: #555;
    --accent: #4f9cf7; --accent-hover: #3a8ae5; --accent-dim: rgba(79,156,247,0.12);
    --claude: #d97706; --claude-dim: rgba(217,119,6,0.12);
    --user-bg: #1e293b; --bot-bg: #161616;
    --danger: #ef4444; --green: #22c55e; --green-dim: rgba(34,197,94,0.12);
    --purple: #a855f7; --purple-dim: rgba(168,85,247,0.12);
    --team-orange: #f59e0b; --team-orange-dim: rgba(245,158,11,0.12);
  }
  * { margin:0; padding:0; box-sizing:border-box; }
  body { font-family:-apple-system,"Segoe UI","Noto Sans SC",sans-serif; background:var(--bg); color:var(--text); height:100vh; display:flex; overflow:hidden; }

  .sidebar { width:280px; min-width:280px; background:var(--sidebar); border-right:1px solid var(--border); display:flex; flex-direction:column; height:100vh; }
  .sidebar-header { padding:16px; border-bottom:1px solid var(--border); }
  .sidebar-header h2 { font-size:14px; font-weight:600; color:var(--text-dim); letter-spacing:0.5px; text-transform:uppercase; margin-bottom:12px; }
  .btn-row { display:flex; gap:8px; }
  .new-chat-btn { flex:1; padding:10px; border:1px dashed var(--border-light); border-radius:8px; background:transparent; color:var(--text-dim); font-size:13px; cursor:pointer; transition:all .2s; }
  .new-chat-btn:hover { border-color:var(--accent); color:var(--accent); background:var(--accent-dim); }
  .new-team-btn { flex:1; padding:10px; border:1px dashed var(--border-light); border-radius:8px; background:transparent; color:var(--team-orange); font-size:13px; cursor:pointer; transition:all .2s; border-color:rgba(245,158,11,0.3); }
  .new-team-btn:hover { border-color:var(--team-orange); background:var(--team-orange-dim); }

  .conv-list { flex:1; overflow-y:auto; padding:8px; }
  .conv-list::-webkit-scrollbar { width:4px; }
  .conv-list::-webkit-scrollbar-thumb { background:var(--border); border-radius:2px; }
  .section-label { font-size:10px; font-weight:600; color:var(--text-muted); text-transform:uppercase; letter-spacing:0.8px; padding:8px 12px 4px; }
  .conv-item { padding:10px 12px; border-radius:8px; cursor:pointer; margin-bottom:2px; transition:background .15s; display:flex; align-items:center; justify-content:space-between; gap:8px; }
  .conv-item:hover { background:var(--accent-dim); }
  .conv-item.active { background:var(--accent-dim); border:1px solid rgba(79,156,247,0.2); }
  .conv-item.team-item:hover { background:var(--team-orange-dim); }
  .conv-item.team-item.active { background:var(--team-orange-dim); border-color:rgba(245,158,11,0.3); }
  .conv-item .conv-info { flex:1; min-width:0; }
  .conv-item .conv-title { font-size:13px; white-space:nowrap; overflow:hidden; text-overflow:ellipsis; }
  .conv-item .conv-meta { font-size:11px; color:var(--text-muted); margin-top:2px; display:flex; align-items:center; gap:6px; }
  .conv-item .conv-model-dot { width:6px; height:6px; border-radius:50%; display:inline-block; }
  .conv-item .agent-id { font-size:10px; color:var(--text-muted); background:var(--border); padding:0 5px; border-radius:4px; font-family:monospace; }
  .conv-item .team-badge { font-size:10px; color:var(--team-orange); background:var(--team-orange-dim); padding:0 5px; border-radius:4px; font-weight:600; }
  .streaming-dot { display:inline-block; width:8px; height:8px; border-radius:50%; background:#22c55e; margin:0 4px; vertical-align:middle; animation:pulse-dot 1.2s ease-in-out infinite; }
  @keyframes pulse-dot { 0%,100%{opacity:1;transform:scale(1)} 50%{opacity:.4;transform:scale(.7)} }
  .conv-item .delete-btn { opacity:0; background:none; border:none; color:var(--text-muted); cursor:pointer; font-size:16px; padding:2px 4px; border-radius:4px; transition:all .15s; }
  .conv-item:hover .delete-btn { opacity:1; }
  .conv-item .delete-btn:hover { color:var(--danger); background:rgba(239,68,68,0.1); }
  .conv-item .members-btn { opacity:0; background:none; border:none; color:var(--team-orange); cursor:pointer; font-size:12px; padding:2px 6px; border-radius:4px; transition:all .15s; font-family:inherit; }
  .conv-item:hover .members-btn { opacity:1; }
  .conv-item .members-btn:hover { background:var(--team-orange-dim); }

  .rename-input { background:var(--bg); border:1px solid var(--accent); border-radius:4px; color:var(--text); font-size:13px; padding:1px 6px; outline:none; width:100%; font-family:inherit; }
  .header-rename-btn { background:none; border:none; color:var(--text-muted); cursor:pointer; font-size:13px; padding:2px 6px; border-radius:4px; transition:all .15s; }
  .header-rename-btn:hover { color:var(--text); background:rgba(255,255,255,0.05); }
  .header-title-editable { cursor:pointer; }
  .header-title-editable:hover { text-decoration:underline; text-decoration-color:var(--text-muted); text-underline-offset:3px; }

  .modal-overlay { position:fixed; inset:0; background:rgba(0,0,0,0.6); backdrop-filter:blur(4px); display:flex; align-items:center; justify-content:center; z-index:1000; animation:fadeIn .2s ease; }
  .modal { background:var(--surface); border:1px solid var(--border); border-radius:16px; width:480px; max-width:90vw; max-height:80vh; overflow:hidden; display:flex; flex-direction:column; box-shadow:0 20px 60px rgba(0,0,0,0.4); animation:modalIn .25s ease; }
  @keyframes modalIn { from{opacity:0;transform:scale(0.95) translateY(10px)} to{opacity:1;transform:scale(1) translateY(0)} }
  .modal-header { padding:16px 20px; border-bottom:1px solid var(--border); display:flex; align-items:center; gap:10px; }
  .modal-header h3 { flex:1; font-size:16px; font-weight:600; color:var(--team-orange); }
  .modal-header .modal-close { background:none; border:none; color:var(--text-muted); font-size:20px; cursor:pointer; padding:2px 6px; border-radius:6px; transition:all .15s; }
  .modal-header .modal-close:hover { color:var(--text); background:rgba(255,255,255,0.05); }
  .modal-body { padding:16px 20px; overflow-y:auto; flex:1; }
  .modal-body::-webkit-scrollbar { width:4px; }
  .modal-body::-webkit-scrollbar-thumb { background:var(--border); border-radius:2px; }

  .member-card { border:1px solid var(--border); border-radius:10px; margin-bottom:10px; overflow:hidden; transition:border-color .2s; }
  .member-card:hover { border-color:var(--border-light); }
  .member-card-header { display:flex; align-items:center; gap:10px; padding:12px 14px; }
  .member-card-dot { width:10px; height:10px; border-radius:50%; flex-shrink:0; }
  .member-card-info { flex:1; }
  .member-card-name { font-size:14px; font-weight:600; }
  .member-card-role { font-size:11px; color:var(--text-muted); margin-top:1px; }
  .member-card-duty-btn { padding:4px 10px; border:1px solid var(--border-light); border-radius:6px; background:transparent; color:var(--text-dim); font-size:12px; cursor:pointer; transition:all .2s; font-family:inherit; white-space:nowrap; }
  .member-card-duty-btn:hover { border-color:var(--accent); color:var(--accent); background:var(--accent-dim); }
  .member-card-duty-btn.active { border-color:var(--accent); color:var(--accent); background:var(--accent-dim); }
  .member-card-duty { display:none; padding:0 14px 14px; }
  .member-card-duty.open { display:block; }
  .member-card-duty-content { font-size:13px; color:var(--text-dim); line-height:1.8; white-space:pre-wrap; padding:10px 14px; background:rgba(0,0,0,0.2); border-radius:8px; border-left:3px solid var(--border); }
  .member-card-remove { background:none; border:none; color:var(--text-muted); font-size:14px; cursor:pointer; padding:2px 6px; border-radius:4px; transition:all .15s; opacity:0.5; }
  .member-card-remove:hover { color:var(--danger); opacity:1; background:rgba(239,68,68,0.1); }

  .add-member-btn { width:100%; padding:10px; border:1px dashed var(--border-light); border-radius:10px; background:transparent; color:var(--team-orange); font-size:13px; cursor:pointer; transition:all .2s; font-family:inherit; margin-top:4px; }
  .add-member-btn:hover { border-color:var(--team-orange); background:var(--team-orange-dim); }

  .form-group { margin-bottom:14px; }
  .form-group label { display:block; font-size:12px; font-weight:600; color:var(--text-dim); margin-bottom:5px; letter-spacing:0.3px; }
  .form-group input, .form-group textarea { width:100%; padding:8px 12px; border:1px solid var(--border); border-radius:8px; background:var(--bg); color:var(--text); font-size:13px; font-family:inherit; outline:none; transition:border-color .2s; }
  .form-group input:focus, .form-group textarea:focus { border-color:var(--accent); }
  .form-group input::placeholder, .form-group textarea::placeholder { color:var(--text-muted); }
  .form-group textarea { resize:vertical; min-height:80px; line-height:1.6; }
  .form-group .form-hint { font-size:11px; color:var(--text-muted); margin-top:3px; }
  .form-row { display:flex; gap:10px; }
  .form-row .form-group { flex:1; }
  .color-options { display:flex; gap:6px; margin-top:4px; }
  .color-option { width:24px; height:24px; border-radius:50%; border:2px solid transparent; cursor:pointer; transition:all .15s; }
  .color-option:hover { transform:scale(1.15); }
  .color-option.selected { border-color:var(--text); box-shadow:0 0 0 2px var(--bg), 0 0 0 4px currentColor; }
  .form-actions { display:flex; gap:8px; justify-content:flex-end; margin-top:18px; }
  .form-actions button { padding:8px 20px; border-radius:8px; font-size:13px; font-weight:500; cursor:pointer; transition:all .2s; font-family:inherit; }
  .btn-cancel { background:transparent; border:1px solid var(--border); color:var(--text-dim); }
  .btn-cancel:hover { border-color:var(--text-muted); color:var(--text); }
  .btn-submit { background:var(--team-orange); border:none; color:#000; font-weight:600; }
  .btn-submit:hover { filter:brightness(1.1); }
  .btn-submit:disabled { opacity:0.4; cursor:not-allowed; }

  .memory-panel { border-top:1px solid var(--border); padding:12px 16px; max-height:280px; overflow-y:auto; }
  .memory-panel::-webkit-scrollbar { width:4px; }
  .memory-panel::-webkit-scrollbar-thumb { background:var(--border); border-radius:2px; }
  .memory-header { display:flex; align-items:center; justify-content:space-between; margin-bottom:8px; cursor:pointer; }
  .memory-header h3 { font-size:12px; font-weight:600; color:var(--green); letter-spacing:0.5px; text-transform:uppercase; }
  .memory-header .mem-count { font-size:11px; color:var(--text-muted); background:var(--green-dim); padding:1px 6px; border-radius:8px; }
  .memory-entries { display:none; }
  .memory-entries.open { display:block; }
  .mem-type-group { margin-bottom:8px; }
  .mem-type-label { font-size:11px; font-weight:600; padding:2px 8px; border-radius:4px; display:inline-block; margin-bottom:4px; letter-spacing:0.3px; }
  .mem-type-label.type-user { background:rgba(79,156,247,0.15); color:#4f9cf7; }
  .mem-type-label.type-feedback { background:rgba(217,119,6,0.15); color:#d97706; }
  .mem-type-label.type-project { background:rgba(34,197,94,0.15); color:#22c55e; }
  .mem-type-label.type-reference { background:rgba(168,85,247,0.15); color:#a855f7; }
  .memory-entry { font-size:12px; color:var(--text-dim); padding:3px 0 3px 10px; line-height:1.5; border-left:2px solid var(--border); margin-left:4px; }
  .memory-entry.type-user { border-left-color:#4f9cf7; }
  .memory-entry.type-feedback { border-left-color:#d97706; }
  .memory-entry.type-project { border-left-color:#22c55e; }
  .memory-entry.type-reference { border-left-color:#a855f7; }
  .memory-entry .mem-name { font-weight:500; color:var(--text); }
  .memory-entry .mem-age { font-size:10px; color:var(--text-muted); margin-left:6px; }
  .memory-entry .mem-content { font-size:11px; color:var(--text-muted); margin-top:1px; }
  .clear-mem-btn { margin-top:6px; padding:4px 10px; border:1px solid var(--border-light); border-radius:6px; background:transparent; color:var(--text-muted); font-size:11px; cursor:pointer; transition:all .2s; }
  .clear-mem-btn:hover { border-color:var(--danger); color:var(--danger); }

  .team-panel { border-top:1px solid var(--border); padding:12px 16px; max-height:340px; overflow-y:auto; display:none; }
  .team-panel::-webkit-scrollbar { width:4px; }
  .team-panel::-webkit-scrollbar-thumb { background:var(--border); border-radius:2px; }
  .team-panel-header { display:flex; align-items:center; justify-content:space-between; margin-bottom:10px; }
  .team-panel-header h3 { font-size:12px; font-weight:600; color:var(--team-orange); letter-spacing:0.5px; text-transform:uppercase; }
  .team-panel-header .worker-count { font-size:11px; color:var(--text-muted); background:var(--team-orange-dim); padding:1px 6px; border-radius:8px; }
  .worker-card { margin-bottom:6px; border:1px solid var(--border); border-radius:8px; overflow:hidden; transition:border-color .2s; }
  .worker-card:hover { border-color:var(--border-light); }
  .worker-card-header { display:flex; align-items:center; gap:8px; padding:8px 10px; cursor:pointer; transition:background .15s; }
  .worker-card-header:hover { background:rgba(255,255,255,0.02); }
  .worker-card-dot { width:10px; height:10px; border-radius:50%; flex-shrink:0; }
  .worker-card-name { font-size:13px; font-weight:600; flex:1; }
  .worker-card-label { font-size:11px; color:var(--text-muted); }
  .worker-card-arrow { font-size:10px; color:var(--text-muted); transition:transform .2s; }
  .worker-card-arrow.open { transform:rotate(90deg); }
  .worker-card-body { display:none; padding:0 10px 10px 28px; font-size:12px; color:var(--text-dim); line-height:1.7; white-space:pre-wrap; }
  .worker-card-body.open { display:block; }
  .worker-card-role { font-size:10px; font-weight:600; padding:1px 6px; border-radius:4px; letter-spacing:0.3px; }

  .main { flex:1; display:flex; flex-direction:column; height:100vh; min-width:0; }
  header { padding:12px 24px; border-bottom:1px solid var(--border); display:flex; align-items:center; gap:10px; background:var(--surface); flex-wrap:wrap; }
  header h1 { font-size:16px; font-weight:600; }
  .header-id { font-size:11px; color:var(--text-muted); background:var(--border); padding:1px 7px; border-radius:4px; font-family:monospace; }
  .header-team-badge { font-size:11px; color:var(--team-orange); background:var(--team-orange-dim); padding:2px 8px; border-radius:10px; font-weight:600; }
  .model-select { margin-left:auto; position:relative; }
  .model-select select { appearance:none; padding:6px 32px 6px 12px; border:1px solid var(--border-light); border-radius:8px; background:var(--bg); color:var(--text); font-size:13px; font-weight:500; cursor:pointer; outline:none; transition:border-color .2s; }
  .model-select select:hover, .model-select select:focus { border-color:var(--accent); }
  .model-select::after { content:"▾"; position:absolute; right:10px; top:50%; transform:translateY(-50%); color:var(--text-dim); font-size:12px; pointer-events:none; }
  .model-badge { font-size:11px; padding:2px 8px; border-radius:10px; font-weight:500; }
  .model-badge.gpt { background:var(--accent-dim); color:var(--accent); }
  .model-badge.claude { background:var(--claude-dim); color:var(--claude); }
  .context-badge { font-size:11px; padding:2px 8px; border-radius:10px; font-weight:500; background:var(--green-dim); color:var(--green); }
  .worker-badges { display:flex; gap:4px; }
  .worker-badge { font-size:10px; padding:2px 6px; border-radius:6px; font-weight:500; }

  #chat-box { flex:1; overflow-y:auto; padding:24px; display:flex; flex-direction:column; gap:16px; }
  #chat-box::-webkit-scrollbar { width:6px; }
  #chat-box::-webkit-scrollbar-thumb { background:var(--border); border-radius:3px; }
  .empty-state { flex:1; display:flex; flex-direction:column; align-items:center; justify-content:center; color:var(--text-muted); gap:12px; }
  .empty-state .icon { font-size:48px; opacity:0.3; }
  .empty-state p { font-size:15px; }
  .empty-state .sub { font-size:12px; }

  .msg { max-width:80%; padding:12px 16px; border-radius:12px; line-height:1.7; font-size:14px; white-space:pre-wrap; word-break:break-word; animation:fadeIn .25s ease; }
  @keyframes fadeIn { from{opacity:0;transform:translateY(6px)} to{opacity:1;transform:translateY(0)} }
  .msg.user { align-self:flex-end; background:var(--user-bg); border:1px solid #2d3a4e; }
  .msg.bot  { align-self:flex-start; background:var(--bot-bg); border:1px solid var(--border); }
  .msg .role { font-size:11px; margin-bottom:4px; font-weight:500; }
  .msg.user .role { color:var(--text-dim); }
  .msg.bot .role.gpt-role { color:var(--accent); }
  .msg.bot .role.claude-role { color:var(--claude); }

  /* Team-specific message styles */
  .msg.team-msg { max-width:90%; }
  .msg.team-leader { align-self:flex-start; background:#1a1600; border:1px solid rgba(245,158,11,0.2); }
  .msg.team-leader .role { color:var(--team-orange); }
  .msg.team-worker { align-self:flex-start; background:var(--surface); border:1px solid var(--border); }
  .msg.team-system { align-self:center; background:transparent; border:1px dashed var(--border); color:var(--text-muted); font-size:12px; max-width:60%; text-align:center; padding:8px 16px; }

  .worker-result { margin-top:4px; }
  .worker-result-header { font-size:12px; font-weight:600; cursor:pointer; padding:4px 0; display:flex; align-items:center; gap:6px; }
  .worker-result-header .arrow { font-size:10px; transition:transform .2s; }
  .worker-result-header .arrow.open { transform:rotate(90deg); }
  .worker-result-body { display:none; font-size:13px; padding:8px 12px; margin-top:4px; background:rgba(0,0,0,0.2); border-radius:8px; border-left:3px solid var(--border); }
  .worker-result-body.open { display:block; }

  /* Handoff — WeChat-style chat bubble */
  .handoff { align-self:stretch; display:flex; gap:10px; padding:4px 0; animation:fadeIn .3s ease; }
  .handoff-avatar { width:36px; height:36px; border-radius:50%; display:flex; align-items:center; justify-content:center; font-size:14px; font-weight:700; color:#fff; flex-shrink:0; line-height:1; margin-top:2px; }
  .handoff-body { flex:1; min-width:0; }
  .handoff-name { font-size:11px; font-weight:600; margin-bottom:3px; display:flex; align-items:center; gap:6px; }
  .handoff-bubble { position:relative; background:var(--surface); border:1px solid var(--border); border-radius:2px 12px 12px 12px; padding:10px 14px; font-size:13px; line-height:1.7; color:var(--text-dim); max-width:75%; word-break:break-word; }
  .handoff-bubble::before { content:''; position:absolute; left:-7px; top:8px; width:0; height:0; border-top:6px solid transparent; border-bottom:6px solid transparent; border-right:7px solid var(--border); }
  .handoff-bubble::after { content:''; position:absolute; left:-5px; top:9px; width:0; height:0; border-top:5px solid transparent; border-bottom:5px solid transparent; border-right:6px solid var(--surface); }
  .handoff-summary { color:var(--text); }
  .handoff-at { display:inline-block; margin-top:4px; font-size:12px; font-weight:600; padding:1px 6px; border-radius:4px; }
  .handoff-arrow { color:var(--text-muted); font-size:11px; margin:0 2px; }

  /* Task dispatch — Leader chat bubble */
  .task-dispatch { align-self:stretch; display:flex; gap:10px; padding:4px 0; animation:fadeIn .3s ease; }
  .task-dispatch-avatar { width:36px; height:36px; border-radius:50%; background:var(--team-orange); display:flex; align-items:center; justify-content:center; font-size:15px; font-weight:700; color:#fff; flex-shrink:0; line-height:1; margin-top:2px; }
  .task-dispatch-body { flex:1; min-width:0; }
  .task-dispatch-name { font-size:11px; font-weight:600; color:var(--team-orange); margin-bottom:3px; display:flex; align-items:center; gap:6px; }
  .task-dispatch-target { font-size:11px; font-weight:500; }
  .task-dispatch-bubble { position:relative; background:#1a1600; border:1px solid rgba(245,158,11,0.2); border-radius:2px 12px 12px 12px; padding:10px 14px; font-size:13px; line-height:1.7; color:var(--text-dim); max-width:80%; word-break:break-word; }
  .task-dispatch-bubble::before { content:''; position:absolute; left:-7px; top:8px; width:0; height:0; border-top:6px solid transparent; border-bottom:6px solid transparent; border-right:7px solid rgba(245,158,11,0.2); }
  .task-dispatch-bubble::after { content:''; position:absolute; left:-5px; top:9px; width:0; height:0; border-top:5px solid transparent; border-bottom:5px solid transparent; border-right:6px solid #1a1600; }
  .task-dispatch-content { color:var(--text); }
  .task-dispatch-badge { display:inline-block; margin-top:6px; font-size:11px; font-weight:600; padding:2px 8px; border-radius:4px; }

  .typing { align-self:flex-start; color:var(--text-dim); font-size:14px; padding:8px 0; }
  .typing span { animation:blink 1.4s infinite; }
  .typing span:nth-child(2) { animation-delay:.2s; }
  .typing span:nth-child(3) { animation-delay:.4s; }
  @keyframes blink { 0%,60%,100%{opacity:.2} 30%{opacity:1} }

  .phase-separator { align-self:stretch; display:flex; align-items:center; gap:12px; padding:6px 0; animation:fadeIn .25s ease; }
  .phase-separator::before, .phase-separator::after { content:''; flex:1; height:1px; background:var(--border); }
  .phase-separator .phase-label { font-size:11px; font-weight:600; color:var(--team-orange); white-space:nowrap; letter-spacing:0.3px; }
  .revision-separator { align-self:stretch; display:flex; align-items:center; gap:12px; padding:6px 0; animation:fadeIn .25s ease; }
  .revision-separator::before, .revision-separator::after { content:''; flex:1; height:1px; background:var(--danger); opacity:0.3; }
  .revision-separator .revision-label { font-size:11px; font-weight:600; color:var(--danger); white-space:nowrap; }
  .review-complete-badge { align-self:center; font-size:11px; color:var(--green); background:var(--green-dim); padding:4px 14px; border-radius:12px; font-weight:500; animation:fadeIn .25s ease; }

  .turn-indicator { align-self:center; font-size:11px; color:var(--team-orange); background:var(--team-orange-dim); padding:4px 14px; border-radius:12px; font-weight:600; animation:fadeIn .25s ease; letter-spacing:0.3px; }
  .verify-separator { align-self:stretch; display:flex; align-items:center; gap:12px; padding:6px 0; animation:fadeIn .25s ease; }
  .verify-separator::before, .verify-separator::after { content:''; flex:1; height:1px; background:var(--purple); opacity:0.3; }
  .verify-separator .verify-label { font-size:11px; font-weight:600; color:var(--purple); white-space:nowrap; }
  .max-turns-badge { align-self:center; font-size:11px; color:var(--claude); background:var(--claude-dim); padding:4px 14px; border-radius:12px; font-weight:500; animation:fadeIn .25s ease; }
  .continue-badge { align-self:center; font-size:11px; color:var(--accent); background:var(--accent-dim); padding:4px 14px; border-radius:12px; font-weight:500; animation:fadeIn .25s ease; }

  .team-progress { align-self:stretch; padding:8px 16px; margin:4px 0; border-radius:8px; background:var(--surface); border:1px solid var(--border); font-size:12px; }
  .team-progress .progress-item { padding:4px 0; display:flex; align-items:center; gap:8px; }
  .team-progress .progress-dot { width:8px; height:8px; border-radius:50%; }
  .team-progress .progress-dot.pending { background:var(--text-muted); }
  .team-progress .progress-dot.working { background:var(--team-orange); animation:pulse 1s infinite; }
  .team-progress .progress-dot.done { background:var(--green); }
  .team-progress .progress-dot.error { background:var(--danger); }
  @keyframes pulse { 0%,100%{opacity:1} 50%{opacity:0.4} }

  #input-area { padding:14px 24px; border-top:1px solid var(--border); display:flex; gap:10px; background:var(--surface); align-items:flex-end; }
  #msg-input { flex:1; padding:12px 16px; border-radius:10px; border:1px solid var(--border); background:var(--bg); color:var(--text); font-size:14px; outline:none; transition:border-color .2s; resize:none; min-height:44px; max-height:120px; font-family:inherit; line-height:1.5; }
  #msg-input:focus { border-color:var(--accent); }
  #msg-input::placeholder { color:var(--text-muted); }
  #send-btn { padding:10px 24px; border:none; border-radius:10px; background:var(--accent); color:#fff; font-size:14px; font-weight:500; cursor:pointer; transition:background .2s; white-space:nowrap; height:44px; }
  #send-btn:hover { background:var(--accent-hover); }
  #send-btn:disabled { opacity:.4; cursor:not-allowed; }

  /* ── Docs Panel ── */
  .docs-toggle-btn { position:fixed; top:12px; right:16px; z-index:900; padding:6px 14px; border:1px solid var(--border-light); border-radius:8px; background:var(--surface); color:var(--text-dim); font-size:13px; cursor:pointer; transition:all .2s; font-family:inherit; }
  .docs-toggle-btn:hover { border-color:var(--accent); color:var(--accent); background:var(--accent-dim); }
  .docs-toggle-btn.active { border-color:var(--accent); color:var(--accent); background:var(--accent-dim); }

  .docs-overlay { position:fixed; inset:0; z-index:950; display:flex; justify-content:flex-end; animation:fadeIn .2s ease; }
  .docs-overlay-bg { position:absolute; inset:0; background:rgba(0,0,0,0.4); backdrop-filter:blur(2px); }
  .docs-panel { position:relative; display:flex; height:100vh; animation:docsPanelIn .3s ease; max-width:90vw; }
  @keyframes docsPanelIn { from{transform:translateX(100%)} to{transform:translateX(0)} }

  .docs-content-pane { width:0; background:var(--bg); display:flex; flex-direction:column; overflow:hidden; transition:width .3s ease; position:relative; }
  .docs-content-pane.open { width:55vw; min-width:400px; transition:none; }
  .docs-resize-handle { position:absolute; left:0; top:0; bottom:0; width:5px; cursor:col-resize; background:transparent; z-index:10; transition:background .15s; }
  .docs-resize-handle:hover, .docs-resize-handle.dragging { background:var(--accent); }
  .docs-content-header { padding:14px 20px; border-bottom:1px solid var(--border); display:flex; align-items:center; gap:10px; flex-shrink:0; }
  .docs-content-header h3 { flex:1; font-size:15px; font-weight:600; color:var(--text); white-space:nowrap; overflow:hidden; text-overflow:ellipsis; }
  .docs-content-header .docs-close-content { background:none; border:none; color:var(--text-muted); font-size:18px; cursor:pointer; padding:2px 6px; border-radius:4px; }
  .docs-content-header .docs-close-content:hover { color:var(--text); background:rgba(255,255,255,0.05); }
  .docs-content-body { flex:1; overflow-y:auto; padding:24px; }
  .docs-content-body::-webkit-scrollbar { width:6px; }
  .docs-content-body::-webkit-scrollbar-thumb { background:var(--border); border-radius:3px; }

  .docs-list-pane { width:280px; min-width:280px; background:var(--surface); display:flex; flex-direction:column; }
  .docs-list-header { padding:14px 16px; border-bottom:1px solid var(--border); display:flex; align-items:center; justify-content:space-between; }
  .docs-list-header h3 { font-size:14px; font-weight:600; color:var(--text); }
  .docs-list-header .docs-close-btn { background:none; border:none; color:var(--text-muted); font-size:18px; cursor:pointer; padding:2px 6px; border-radius:4px; }
  .docs-list-header .docs-close-btn:hover { color:var(--text); background:rgba(255,255,255,0.05); }
  .docs-list { flex:1; overflow-y:auto; padding:8px; }
  .docs-list::-webkit-scrollbar { width:4px; }
  .docs-list::-webkit-scrollbar-thumb { background:var(--border); border-radius:2px; }
  .doc-item { padding:10px 12px; border-radius:8px; cursor:pointer; margin-bottom:2px; transition:all .15s; display:flex; align-items:center; gap:10px; }
  .doc-item:hover { background:var(--accent-dim); }
  .doc-item.active { background:var(--accent-dim); border-left:3px solid var(--accent); }
  .doc-item-icon { font-size:16px; flex-shrink:0; opacity:0.6; }
  .doc-item-info { flex:1; min-width:0; }
  .doc-item-title { font-size:13px; font-weight:500; white-space:nowrap; overflow:hidden; text-overflow:ellipsis; }
  .doc-item-name { font-size:11px; color:var(--text-muted); margin-top:1px; font-family:monospace; white-space:nowrap; overflow:hidden; text-overflow:ellipsis; }

  /* ── Markdown rendered styles ── */
  .md-body { font-size:14px; line-height:1.8; color:var(--text); }
  .md-body h1 { font-size:24px; font-weight:700; margin:0 0 16px; padding-bottom:8px; border-bottom:1px solid var(--border); }
  .md-body h2 { font-size:20px; font-weight:600; margin:28px 0 12px; padding-bottom:6px; border-bottom:1px solid var(--border); color:var(--accent); }
  .md-body h3 { font-size:16px; font-weight:600; margin:22px 0 8px; color:var(--text); }
  .md-body h4 { font-size:14px; font-weight:600; margin:18px 0 6px; color:var(--text-dim); }
  .md-body p { margin:0 0 12px; }
  .md-body ul, .md-body ol { margin:0 0 12px; padding-left:24px; }
  .md-body li { margin:4px 0; }
  .md-body li > ul, .md-body li > ol { margin:4px 0 4px; }
  .md-body code { background:rgba(79,156,247,0.1); color:var(--accent); padding:1px 5px; border-radius:4px; font-size:13px; font-family:"SF Mono",Consolas,monospace; }
  .md-body pre { background:#0d0d0d; border:1px solid var(--border); border-radius:8px; padding:14px 16px; overflow-x:auto; margin:0 0 16px; }
  .md-body pre code { background:none; color:var(--text-dim); padding:0; font-size:13px; line-height:1.6; }
  .md-body blockquote { border-left:3px solid var(--accent); margin:0 0 12px; padding:8px 16px; background:var(--accent-dim); border-radius:0 8px 8px 0; color:var(--text-dim); }
  .md-body table { width:100%; border-collapse:collapse; margin:0 0 16px; font-size:13px; }
  .md-body th, .md-body td { border:1px solid var(--border); padding:8px 12px; text-align:left; }
  .md-body th { background:rgba(255,255,255,0.03); font-weight:600; }
  .md-body hr { border:none; border-top:1px solid var(--border); margin:20px 0; }
  .md-body a { color:var(--accent); text-decoration:none; }
  .md-body a:hover { text-decoration:underline; }
  .md-body strong { color:var(--text); font-weight:600; }
  .md-body img { max-width:100%; border-radius:8px; }
</style>
</head>
<body>

<div class="sidebar">
  <div class="sidebar-header">
    <h2>Agent & Team</h2>
    <div class="btn-row">
      <button class="new-chat-btn" onclick="createAgent()">+ Agent</button>
      <button class="new-team-btn" onclick="createTeam()">+ Team</button>
    </div>
  </div>
  <div class="conv-list" id="conv-list"></div>
  <div class="memory-panel" id="memory-panel">
    <div class="memory-header" onclick="toggleMemory()">
      <h3 id="mem-title">记忆</h3>
      <span class="mem-count" id="mem-count">0</span>
    </div>
    <div class="memory-entries" id="memory-entries"></div>
  </div>
  <div class="team-panel" id="team-panel">
    <div class="team-panel-header">
      <h3 id="team-panel-title">团队成员</h3>
      <span class="worker-count" id="worker-count">0</span>
    </div>
    <div id="worker-cards"></div>
  </div>
</div>

<div class="main">
  <header>
    <h1 id="header-title">未选择</h1>
    <span class="header-id" id="header-id" style="display:none"></span>
    <span class="header-team-badge" id="header-team-badge" style="display:none">TEAM</span>
    <span class="model-badge gpt" id="header-badge" style="display:none"></span>
    <span class="context-badge" id="context-badge" style="display:none"></span>
    <div class="worker-badges" id="worker-badges"></div>
    <div class="model-select">
      <select id="model-select" onchange="onModelChange()">
        <option value="gpt-5.4">GPT-5.4</option>
        <option value="claude-4-6-opus">Claude 4.6 Opus</option>
      </select>
    </div>
  </header>
  <div id="chat-box">
    <div class="empty-state">
      <div class="icon">🤖</div>
      <p>新建 Agent 或 Team 开始对话</p>
      <div class="sub">Agent = 独立对话 | Team = 多 Agent 协作（Leader + Workers）</div>
    </div>
  </div>
  <div id="input-area">
    <textarea id="msg-input" rows="1" placeholder="输入消息，按 Enter 发送..."></textarea>
    <button id="send-btn">发送</button>
  </div>
</div>

<button class="docs-toggle-btn" id="docs-toggle-btn" onclick="toggleDocsPanel()">📄 文档</button>

<script src="https://cdn.jsdelivr.net/npm/marked/marked.min.js"></script>
<script>
const $ = id => document.getElementById(id);
const chatBox      = $('chat-box');
const input        = $('msg-input');
const sendBtn      = $('send-btn');
const modelSel     = $('model-select');
const convListEl   = $('conv-list');
const headerTitle  = $('header-title');
const headerIdEl   = $('header-id');
const headerBadge  = $('header-badge');
const headerTeamBadge = $('header-team-badge');
const contextBadge = $('context-badge');
const workerBadgesEl = $('worker-badges');
const memPanel     = $('memory-panel');
const memCountEl   = $('mem-count');
const memEntriesEl = $('memory-entries');
const memTitleEl   = $('mem-title');
const teamPanel    = $('team-panel');
const teamPanelTitle = $('team-panel-title');
const workerCountEl  = $('worker-count');
const workerCardsEl  = $('worker-cards');

const MODEL_LABELS = { 'gpt-5.4':'GPT-5.4', 'claude-4-6-opus':'Claude 4.6 Opus' };
const MODEL_COLORS = { 'gpt-5.4':'#4f9cf7', 'claude-4-6-opus':'#d97706' };
const WORKER_COLORS = { 'researcher':'#4f9cf7', 'coder':'#22c55e', 'reviewer':'#a855f7', 'leader':'#f59e0b' };
const WORKER_LABELS = { 'researcher':'研究员', 'coder':'编码者', 'reviewer':'审核者', 'leader':'Leader' };

let agents = [];
let teams = [];
let activeType = null; // 'agent' | 'team'
let activeId = null;
let activeHistory = [];
let teamMessages = [];
let memoryOpen = false;
let streamingTeamId = null;
let streamingAbort = null;
let streamingTypingText = {};

function escapeHtml(s) { const d=document.createElement('div'); d.textContent=s; return d.innerHTML; }

// ── Data loading ──

async function loadAll() {
  const [aResp, tResp] = await Promise.all([fetch('/api/agents'), fetch('/api/teams')]);
  const aData = await aResp.json();
  const tData = await tResp.json();
  agents = aData.agents || [];
  teams = tData.teams || [];
  renderSidebar();
}

// ── Agent operations ──

async function createAgent() {
  const model = modelSel.value;
  const resp = await fetch('/api/agents', {
    method:'POST', headers:{'Content-Type':'application/json'},
    body: JSON.stringify({model}),
  });
  const agent = await resp.json();
  agents.push(agent);
  activeType = 'agent';
  activeId = agent.id;
  activeHistory = [];
  modelSel.value = agent.model;
  renderAll();
  loadAgentMemory();
  input.focus();
}

async function switchAgent(id) {
  activeType = 'agent';
  activeId = id;
  unlockInput();
  const agent = agents.find(a => a.id === id);
  if (agent) modelSel.value = agent.model;
  const resp = await fetch('/api/agent/' + id + '/history');
  const data = await resp.json();
  activeHistory = data.history || [];
  renderAll();
  loadAgentMemory();
  input.focus();
}

async function deleteAgent(id, evt) {
  evt.stopPropagation();
  if (!confirm('确定删除 Agent #' + id + '？')) return;
  await fetch('/api/agent/' + id, {method:'DELETE'});
  agents = agents.filter(a => a.id !== id);
  if (activeType === 'agent' && activeId === id) {
    activeId = null; activeType = null; activeHistory = [];
  }
  renderAll();
}

// ── Team operations ──

async function createTeam() {
  const model = modelSel.value;
  const resp = await fetch('/api/teams', {
    method:'POST', headers:{'Content-Type':'application/json'},
    body: JSON.stringify({model}),
  });
  const team = await resp.json();
  teams.push(team);
  activeType = 'team';
  activeId = team.id;
  teamMessages = [];
  unlockInput();
  renderAll();
  input.focus();
}

async function switchTeam(id) {
  activeType = 'team';
  activeId = id;
  teamMessages = [];
  unlockInput();
  renderAll();
  const team = teams.find(t => t.id === id);
  if (team) modelSel.value = team.model;
  const resp = await fetch('/api/team/' + id + '/messages');
  const data = await resp.json();
  if (activeType === 'team' && activeId === id) {
    teamMessages = data.messages || [];
    renderAll();
    if (streamingTeamId === id) {
      showTyping(streamingTypingText[id] || '处理中...');
      lockInput();
    }
  }
  input.focus();
}

async function deleteTeam(id, evt) {
  evt.stopPropagation();
  if (!confirm('确定删除 Team #' + id + '？所有协作记录将被永久删除。')) return;
  await fetch('/api/team/' + id, {method:'DELETE'});
  teams = teams.filter(t => t.id !== id);
  if (activeType === 'team' && activeId === id) {
    activeId = null; activeType = null; teamMessages = [];
  }
  renderAll();
}

// ── Model change ──

async function onModelChange() {
  if (!activeId) return;
  if (activeType === 'agent') {
    const model = modelSel.value;
    await fetch('/api/agent/' + activeId + '/model', {
      method:'PUT', headers:{'Content-Type':'application/json'},
      body: JSON.stringify({model}),
    });
    const agent = agents.find(a => a.id === activeId);
    if (agent) agent.model = model;
  }
  renderSidebar();
  updateHeader();
}

// ── Header ──

function updateHeader() {
  workerBadgesEl.innerHTML = '';
  headerTeamBadge.style.display = 'none';
  headerTitle.className = '';
  headerTitle.title = '';
  headerTitle.onclick = null;

  if (activeType === 'agent') {
    const agent = agents.find(a => a.id === activeId);
    if (!agent) { headerTitle.textContent = '未选择'; headerIdEl.style.display='none'; headerBadge.style.display='none'; contextBadge.style.display='none'; return; }
    headerTitle.textContent = agent.title;
    headerIdEl.textContent = '#' + agent.id; headerIdEl.style.display = '';
    const label = MODEL_LABELS[agent.model] || agent.model;
    headerBadge.textContent = label;
    headerBadge.className = 'model-badge ' + (agent.model.includes('claude') ? 'claude' : 'gpt');
    headerBadge.style.display = '';
    const rounds = Math.floor(activeHistory.length / 2);
    contextBadge.textContent = '上下文: ' + rounds + ' 轮';
    contextBadge.style.display = '';
    memPanel.style.display = '';
    teamPanel.style.display = 'none';
  } else if (activeType === 'team') {
    const team = teams.find(t => t.id === activeId);
    if (!team) { headerTitle.textContent = '未选择'; headerIdEl.style.display='none'; headerBadge.style.display='none'; contextBadge.style.display='none'; return; }
    headerTitle.textContent = team.name;
    headerTitle.className = 'header-title-editable';
    headerTitle.title = '点击重命名';
    headerTitle.onclick = () => startRenameTeamHeader(team);
    headerIdEl.textContent = '#' + team.id; headerIdEl.style.display = '';
    headerTeamBadge.style.display = '';
    const label = MODEL_LABELS[team.model] || team.model;
    headerBadge.textContent = label;
    headerBadge.className = 'model-badge ' + (team.model.includes('claude') ? 'claude' : 'gpt');
    headerBadge.style.display = '';
    contextBadge.style.display = 'none';
    memPanel.style.display = 'none';
    teamPanel.style.display = '';
    renderTeamPanel(team);
    if (team.workers) {
      team.workers.forEach(w => {
        const badge = document.createElement('span');
        badge.className = 'worker-badge';
        badge.style.background = (WORKER_COLORS[w.name]||'#777') + '20';
        badge.style.color = WORKER_COLORS[w.name]||'#777';
        badge.textContent = w.label;
        workerBadgesEl.appendChild(badge);
      });
    }
  } else {
    headerTitle.textContent = '未选择';
    headerIdEl.style.display = 'none';
    headerBadge.style.display = 'none';
    contextBadge.style.display = 'none';
    memPanel.style.display = '';
    teamPanel.style.display = 'none';
  }
}

// ── Sidebar ──

function renderSidebar() {
  convListEl.innerHTML = '';
  if (teams.length > 0) {
    const lbl = document.createElement('div');
    lbl.className = 'section-label';
    lbl.textContent = 'TEAMS';
    convListEl.appendChild(lbl);
    teams.slice().reverse().forEach(team => {
      const div = document.createElement('div');
      div.className = 'conv-item team-item' + (activeType==='team'&&team.id===activeId ? ' active' : '');
      div.onclick = () => switchTeam(team.id);

      const infoDiv = document.createElement('div');
      infoDiv.className = 'conv-info';

      const titleDiv = document.createElement('div');
      titleDiv.className = 'conv-title';
      const isStreaming = (streamingTeamId === team.id);
      titleDiv.innerHTML = '<span class="team-badge">T' + team.id + '</span> ';
      if (isStreaming) {
        const dot = document.createElement('span');
        dot.className = 'streaming-dot';
        dot.title = streamingTypingText[team.id] || '处理中';
        titleDiv.appendChild(dot);
      }
      const nameSpan = document.createElement('span');
      nameSpan.textContent = team.name;
      nameSpan.title = '双击重命名';
      nameSpan.ondblclick = (e) => { e.stopPropagation(); startRenameTeam(team, nameSpan); };
      titleDiv.appendChild(nameSpan);
      infoDiv.appendChild(titleDiv);

      const metaDiv = document.createElement('div');
      metaDiv.className = 'conv-meta';
      metaDiv.innerHTML = '<span>' + (MODEL_LABELS[team.model]||team.model) + '</span><span>' + team.created_at + '</span>';
      infoDiv.appendChild(metaDiv);
      div.appendChild(infoDiv);

      const membersBtn = document.createElement('button');
      membersBtn.className = 'members-btn';
      membersBtn.textContent = '👥 成员';
      membersBtn.onclick = (e) => { e.stopPropagation(); showTeamMembers(team); };
      div.appendChild(membersBtn);
      const delBtn = document.createElement('button');
      delBtn.className = 'delete-btn'; delBtn.innerHTML = '✕';
      delBtn.onclick = (e) => deleteTeam(team.id, e);
      div.appendChild(delBtn);
      convListEl.appendChild(div);
    });
  }
  if (agents.length > 0) {
    const lbl = document.createElement('div');
    lbl.className = 'section-label';
    lbl.textContent = 'AGENTS';
    convListEl.appendChild(lbl);
    agents.slice().reverse().forEach(agent => {
      const div = document.createElement('div');
      div.className = 'conv-item' + (activeType==='agent'&&agent.id===activeId ? ' active' : '');
      div.onclick = () => switchAgent(agent.id);
      const color = MODEL_COLORS[agent.model] || '#4f9cf7';
      div.innerHTML =
        '<div class="conv-info">' +
          '<div class="conv-title"><span class="agent-id">#' + agent.id + '</span> ' + escapeHtml(agent.title) + '</div>' +
          '<div class="conv-meta"><span class="conv-model-dot" style="background:' + color + '"></span><span>' + (MODEL_LABELS[agent.model]||agent.model) + '</span><span>' + agent.created_at + '</span></div>' +
        '</div>';
      const delBtn = document.createElement('button');
      delBtn.className = 'delete-btn'; delBtn.innerHTML = '✕';
      delBtn.onclick = (e) => deleteAgent(agent.id, e);
      div.appendChild(delBtn);
      convListEl.appendChild(div);
    });
  }
}

// ── Chat rendering ──

function renderChat() {
  chatBox.innerHTML = '';
  if (activeType === 'agent') {
    renderAgentChat();
  } else if (activeType === 'team') {
    renderTeamChat();
  } else {
    chatBox.innerHTML = '<div class="empty-state"><div class="icon">🤖</div><p>新建 Agent 或 Team 开始对话</p><div class="sub">Agent = 独立对话 | Team = 多 Agent 协作</div></div>';
  }
}

function renderAgentChat() {
  if (activeHistory.length === 0) {
    chatBox.innerHTML = '<div class="empty-state"><div class="icon">🤖</div><p>开始与 Agent #' + activeId + ' 对话</p><div class="sub">每个 Agent 拥有独立的记忆和对话历史</div></div>';
    return;
  }
  const agent = agents.find(a => a.id === activeId);
  activeHistory.forEach(m => {
    const div = document.createElement('div');
    div.className = 'msg ' + (m.role==='user' ? 'user' : 'bot');
    const label = document.createElement('div');
    label.className = 'role';
    if (m.role==='user') { label.textContent = '你'; }
    else {
      const ml = agent ? agent.model : 'gpt-5.4';
      label.textContent = MODEL_LABELS[ml] || ml;
      label.className += (ml||'').includes('claude') ? ' claude-role' : ' gpt-role';
    }
    div.appendChild(label);
    const content = document.createElement('div');
    content.textContent = m.content;
    div.appendChild(content);
    chatBox.appendChild(div);
  });
  chatBox.scrollTop = chatBox.scrollHeight;
}

function renderTeamChat() {
  if (teamMessages.length === 0) {
    const team = teams.find(t => t.id === activeId);
    chatBox.innerHTML = '<div class="empty-state"><div class="icon">👥</div><p>' + escapeHtml(team ? team.name : 'Team') + '</p><div class="sub">Leader 会分析你的请求，分派给 Worker 并综合结果</div></div>';
    return;
  }
  teamMessages.forEach(m => renderTeamMessage(m));
  chatBox.scrollTop = chatBox.scrollHeight;
}

function renderTeamMessage(m) {
  const div = document.createElement('div');
  if (m.from === 'user') {
    div.className = 'msg user team-msg';
    div.innerHTML = '<div class="role">你</div><div>' + escapeHtml(m.content) + '</div>';
  } else if (m.type === 'phase') {
    div.className = 'phase-separator';
    div.innerHTML = '<span class="phase-label">' + escapeHtml(m.content) + '</span>';
    chatBox.appendChild(div);
    return;
  } else if (m.type === 'revision') {
    div.className = 'revision-separator';
    div.innerHTML = '<span class="revision-label">🔄 ' + escapeHtml(m.content) + '</span>';
    chatBox.appendChild(div);
    return;
  } else if (m.type === 'review_complete') {
    div.className = 'review-complete-badge';
    div.textContent = '✅ ' + m.content;
    chatBox.appendChild(div);
    return;
  } else if (m.type === 'leader_turn') {
    div.className = 'turn-indicator';
    div.textContent = m.content;
    chatBox.appendChild(div);
    return;
  } else if (m.type === 'verify_phase') {
    div.className = 'verify-separator';
    div.innerHTML = '<span class="verify-label">🔎 ' + escapeHtml(m.content) + '</span>';
    chatBox.appendChild(div);
    return;
  } else if (m.type === 'max_turns') {
    div.className = 'max-turns-badge';
    div.textContent = '⚠️ ' + m.content;
    chatBox.appendChild(div);
    return;
  } else if (m.type === 'continue_task') {
    div.className = 'continue-badge';
    div.textContent = '🔄 ' + m.content;
    chatBox.appendChild(div);
    return;
  } else if (m.type === 'pipeline') {
    div.className = 'msg team-system team-msg';
    div.style.borderColor = 'var(--team-orange)';
    div.innerHTML = '⚡ ' + escapeHtml(m.content);
  } else if (m.type === 'plan') {
    div.className = 'task-dispatch';
    div.innerHTML =
      '<div class="task-dispatch-avatar">L</div>' +
      '<div class="task-dispatch-body">' +
        '<div class="task-dispatch-name">Leader <span style="color:var(--text-muted);font-weight:400;font-size:10px;">分析计划</span></div>' +
        '<div class="task-dispatch-bubble">' +
          '<div class="task-dispatch-content">' + escapeHtml(m.content) + '</div>' +
        '</div>' +
      '</div>';
    chatBox.appendChild(div);
    return;
  } else if (m.type === 'status') {
    div.className = 'msg team-system team-msg';
    div.textContent = (m.from === 'leader' ? '📋 ' : '') + m.content;
  } else if (m.type === 'task') {
    const toColor = WORKER_COLORS[m.to] || '#777';
    const toLabel = WORKER_LABELS[m.to] || m.to;
    div.className = 'task-dispatch';
    div.innerHTML =
      '<div class="task-dispatch-avatar">L</div>' +
      '<div class="task-dispatch-body">' +
        '<div class="task-dispatch-name">Leader' +
          '<span class="handoff-arrow">→</span>' +
          '<span class="task-dispatch-target" style="color:' + toColor + '">' + escapeHtml(toLabel) + '</span>' +
        '</div>' +
        '<div class="task-dispatch-bubble">' +
          '<div class="task-dispatch-content">' + escapeHtml(m.content) + '</div>' +
          '<span class="task-dispatch-badge" style="background:' + toColor + '20;color:' + toColor + '">📤 分派给 ' + escapeHtml(toLabel) + '</span>' +
        '</div>' +
      '</div>';
    chatBox.appendChild(div);
    return;
  } else if (m.type === 'result') {
    const color = WORKER_COLORS[m.from] || '#777';
    const label = WORKER_LABELS[m.from] || m.from;
    div.className = 'msg team-worker team-msg';
    div.style.borderLeftColor = color;
    const rid = 'wr_' + m.id;
    div.innerHTML =
      '<div class="worker-result">' +
        '<div class="worker-result-header" onclick="toggleWorkerResult(\'' + rid + '\')">' +
          '<span class="arrow" id="arrow_' + rid + '">▶</span>' +
          '<span style="color:' + color + '">' + label + '</span> 完成' +
        '</div>' +
        '<div class="worker-result-body" id="' + rid + '">' + escapeHtml(m.content) + '</div>' +
      '</div>';
  } else if (m.type === 'handoff') {
    const fromColor = WORKER_COLORS[m.from] || '#777';
    const fromLabel = WORKER_LABELS[m.from] || m.from;
    const toName = m.to || '';
    const toColor = WORKER_COLORS[toName] || '#777';
    const toLabel = WORKER_LABELS[toName] || toName;
    const initial = (fromLabel||'?').charAt(0);
    const content = m.content || '';
    const atMatch = content.match(/@(\S+)/);
    const summaryText = content.replace(/@\S+/g, '').replace(/^已完成工作[：:]\s*/, '').trim();

    div.className = 'handoff';
    div.innerHTML =
      '<div class="handoff-avatar" style="background:' + fromColor + '">' + initial + '</div>' +
      '<div class="handoff-body">' +
        '<div class="handoff-name" style="color:' + fromColor + '">' + escapeHtml(fromLabel) +
          '<span class="handoff-arrow">→</span>' +
          '<span style="color:' + toColor + '">' + escapeHtml(toLabel) + '</span>' +
        '</div>' +
        '<div class="handoff-bubble" style="border-color:' + fromColor + '30">' +
          '<div class="handoff-summary">' + escapeHtml(summaryText) + '</div>' +
          (toName ? '<span class="handoff-at" style="background:' + toColor + '20;color:' + toColor + '">@' + escapeHtml(toLabel) + ' 接手</span>' : '') +
        '</div>' +
      '</div>';
    chatBox.appendChild(div);
    return;
  } else if (m.type === 'error') {
    div.className = 'msg team-system team-msg';
    div.style.borderColor = 'var(--danger)';
    div.innerHTML = '❌ <span style="color:var(--danger)">' + escapeHtml(m.from) + '</span>: ' + escapeHtml(m.content);
  } else if (m.type === 'synthesis' || m.type === 'reply') {
    div.className = 'msg team-leader team-msg';
    div.innerHTML = '<div class="role" style="color:var(--team-orange)">Leader 综合回答</div><div>' + escapeHtml(m.content) + '</div>';
  } else {
    div.className = 'msg bot team-msg';
    div.innerHTML = '<div class="role">' + escapeHtml(m.from) + '</div><div>' + escapeHtml(m.content) + '</div>';
  }
  chatBox.appendChild(div);
}

function toggleWorkerResult(id) {
  const body = document.getElementById(id);
  const arrow = document.getElementById('arrow_' + id);
  if (body) {
    const open = body.classList.toggle('open');
    if (arrow) arrow.classList.toggle('open', open);
  }
}

function renderAll() { renderSidebar(); renderChat(); updateHeader(); }

function renderTeamPanel(team) {
  const workers = team.workers || [];
  teamPanelTitle.textContent = team.name + ' 成员';
  workerCountEl.textContent = (workers.length + 1);
  workerCardsEl.innerHTML = '';

  const leaderCard = buildWorkerCard('leader', 'Leader', '编排者', '#f59e0b',
    'Leader 负责分析用户的请求，将复杂任务拆解并分派给合适的 Worker 执行，最后综合所有 Worker 的结果给出完整回答。\n\n对于简单问题，Leader 会直接回答而不分派任务。');
  workerCardsEl.appendChild(leaderCard);

  workers.forEach(w => {
    const card = buildWorkerCard(w.name, w.label, w.name, w.color || WORKER_COLORS[w.name] || '#777', w.specialty || '暂无职责描述');
    workerCardsEl.appendChild(card);
  });
}

function buildWorkerCard(name, label, role, color, specialty) {
  const card = document.createElement('div');
  card.className = 'worker-card';
  const cid = 'wc_' + name + '_' + Date.now();

  const header = document.createElement('div');
  header.className = 'worker-card-header';
  header.onclick = () => toggleWorkerCard(cid);
  header.innerHTML =
    '<span class="worker-card-dot" style="background:' + color + '"></span>' +
    '<span class="worker-card-name" style="color:' + color + '">' + escapeHtml(label) + '</span>' +
    '<span class="worker-card-role" style="background:' + color + '20;color:' + color + '">' + escapeHtml(role) + '</span>' +
    '<span class="worker-card-arrow" id="arrow_' + cid + '">▶</span>';
  card.appendChild(header);

  const body = document.createElement('div');
  body.className = 'worker-card-body';
  body.id = cid;
  body.textContent = specialty;
  card.appendChild(body);

  return card;
}

function toggleWorkerCard(id) {
  const body = document.getElementById(id);
  const arrow = document.getElementById('arrow_' + id);
  if (body) {
    const open = body.classList.toggle('open');
    if (arrow) arrow.classList.toggle('open', open);
  }
}

// ── Team rename ──

async function renameTeam(teamId, newName) {
  newName = newName.trim();
  if (!newName) return false;
  try {
    const resp = await fetch('/api/team/' + teamId, {
      method:'PUT', headers:{'Content-Type':'application/json'},
      body: JSON.stringify({name: newName}),
    });
    if (!resp.ok) return false;
    await loadAll();
    if (activeType === 'team' && activeId === teamId) updateHeader();
    return true;
  } catch(e) { return false; }
}

function startRenameTeam(team, nameSpan) {
  const oldName = team.name;
  const input = document.createElement('input');
  input.className = 'rename-input';
  input.value = oldName;
  input.onclick = (e) => e.stopPropagation();

  const finish = async () => {
    const val = input.value.trim();
    if (val && val !== oldName) {
      await renameTeam(team.id, val);
    } else {
      nameSpan.textContent = oldName;
      nameSpan.style.display = '';
      input.remove();
    }
  };

  input.onblur = finish;
  input.onkeydown = (e) => {
    if (e.key === 'Enter') { e.preventDefault(); input.blur(); }
    if (e.key === 'Escape') { input.value = oldName; input.blur(); }
  };

  nameSpan.style.display = 'none';
  nameSpan.parentElement.appendChild(input);
  input.focus();
  input.select();
}

function startRenameTeamHeader(team) {
  const oldName = team.name;
  const input = document.createElement('input');
  input.className = 'rename-input';
  input.value = oldName;
  input.style.fontSize = '16px';
  input.style.fontWeight = '600';
  input.style.width = Math.max(200, oldName.length * 16 + 40) + 'px';

  const finish = async () => {
    const val = input.value.trim();
    if (val && val !== oldName) {
      await renameTeam(team.id, val);
    }
    headerTitle.textContent = (teams.find(t=>t.id===team.id)||team).name;
    headerTitle.className = 'header-title-editable';
    headerTitle.title = '点击重命名';
    headerTitle.onclick = () => startRenameTeamHeader(teams.find(t=>t.id===team.id)||team);
  };

  input.onblur = finish;
  input.onkeydown = (e) => {
    if (e.key === 'Enter') { e.preventDefault(); input.blur(); }
    if (e.key === 'Escape') { input.value = oldName; input.blur(); }
  };

  headerTitle.textContent = '';
  headerTitle.className = '';
  headerTitle.onclick = null;
  headerTitle.appendChild(input);
  input.focus();
  input.select();
}

// ── Team Members Modal ──

const PRESET_COLORS = ['#4f9cf7','#22c55e','#a855f7','#ef4444','#f59e0b','#ec4899','#14b8a6','#f97316','#6366f1','#64748b'];

function showTeamMembers(team) {
  const existing = document.getElementById('team-modal-overlay');
  if (existing) existing.remove();

  const overlay = document.createElement('div');
  overlay.className = 'modal-overlay';
  overlay.id = 'team-modal-overlay';
  overlay.onclick = (e) => { if (e.target === overlay) overlay.remove(); };

  const modal = document.createElement('div');
  modal.className = 'modal';

  const header = document.createElement('div');
  header.className = 'modal-header';
  header.innerHTML = '<h3>👥 ' + escapeHtml(team.name) + ' — 团队成员 (' + ((team.workers||[]).length + 1) + ')</h3>';
  const closeBtn = document.createElement('button');
  closeBtn.className = 'modal-close';
  closeBtn.innerHTML = '✕';
  closeBtn.onclick = () => overlay.remove();
  header.appendChild(closeBtn);
  modal.appendChild(header);

  const body = document.createElement('div');
  body.className = 'modal-body';
  body.id = 'modal-member-list';

  renderMemberList(body, team);

  modal.appendChild(body);
  overlay.appendChild(modal);
  document.body.appendChild(overlay);
}

function renderMemberList(container, team) {
  container.innerHTML = '';

  const leaderDuty = 'Leader 负责分析用户的请求，将复杂任务拆解并分派给合适的 Worker 执行，最后综合所有 Worker 的结果给出完整回答。\n\n对于简单问题，Leader 会直接回答而不分派任务。';
  container.appendChild(buildMemberCard('Leader', '编排者', '#f59e0b', leaderDuty, null, null));

  const workers = team.workers || [];
  workers.forEach(w => {
    const duty = w.specialty || '暂无职责描述';
    container.appendChild(buildMemberCard(w.label, w.name, w.color || WORKER_COLORS[w.name] || '#777', duty, team.id, w.name));
  });

  const addBtn = document.createElement('button');
  addBtn.className = 'add-member-btn';
  addBtn.textContent = '+ 添加新成员';
  addBtn.onclick = () => showAddMemberForm(container, team);
  container.appendChild(addBtn);
}

function buildMemberCard(label, role, color, duty, teamId, workerName) {
  const card = document.createElement('div');
  card.className = 'member-card';
  card.style.borderLeftColor = color;

  const hdr = document.createElement('div');
  hdr.className = 'member-card-header';

  const dot = document.createElement('span');
  dot.className = 'member-card-dot';
  dot.style.background = color;
  hdr.appendChild(dot);

  const info = document.createElement('div');
  info.className = 'member-card-info';
  info.innerHTML = '<div class="member-card-name" style="color:' + color + '">' + escapeHtml(label) + '</div>' +
    '<div class="member-card-role">' + escapeHtml(role) + '</div>';
  hdr.appendChild(info);

  const dutyBtn = document.createElement('button');
  dutyBtn.className = 'member-card-duty-btn';
  dutyBtn.textContent = '📋 职责';

  const dutyDiv = document.createElement('div');
  dutyDiv.className = 'member-card-duty';
  const dutyContent = document.createElement('div');
  dutyContent.className = 'member-card-duty-content';
  dutyContent.style.borderLeftColor = color;
  dutyContent.textContent = duty;
  dutyDiv.appendChild(dutyContent);

  dutyBtn.onclick = (e) => {
    e.stopPropagation();
    const open = dutyDiv.classList.toggle('open');
    dutyBtn.classList.toggle('active', open);
  };
  hdr.appendChild(dutyBtn);

  if (teamId !== null && workerName !== null) {
    const removeBtn = document.createElement('button');
    removeBtn.className = 'member-card-remove';
    removeBtn.innerHTML = '✕';
    removeBtn.title = '移除成员';
    removeBtn.onclick = async (e) => {
      e.stopPropagation();
      if (!confirm('确定移除 ' + label + '？')) return;
      const resp = await fetch('/api/team/' + teamId + '/workers', {
        method:'DELETE', headers:{'Content-Type':'application/json'},
        body: JSON.stringify({name: workerName}),
      });
      if (resp.ok) {
        await loadAll();
        const team = teams.find(t => t.id === teamId);
        if (team) {
          const container = document.getElementById('modal-member-list');
          if (container) renderMemberList(container, team);
          if (activeType === 'team' && activeId === teamId) renderAll();
        }
      }
    };
    hdr.appendChild(removeBtn);
  }

  card.appendChild(hdr);
  card.appendChild(dutyDiv);
  return card;
}

function showAddMemberForm(container, team) {
  const existing = document.getElementById('add-member-form');
  if (existing) { existing.remove(); return; }

  let selectedColor = PRESET_COLORS[Math.floor(Math.random() * PRESET_COLORS.length)];

  const form = document.createElement('div');
  form.id = 'add-member-form';
  form.style.cssText = 'border:1px solid var(--team-orange); border-radius:10px; padding:16px; margin-top:8px; background:rgba(245,158,11,0.03);';

  form.innerHTML =
    '<div style="font-size:14px;font-weight:600;color:var(--team-orange);margin-bottom:14px;">添加新成员</div>' +
    '<div class="form-row">' +
      '<div class="form-group"><label>显示名称</label><input id="add-w-label" placeholder="如：数据分析师"></div>' +
      '<div class="form-group"><label>英文标识</label><input id="add-w-name" placeholder="如：analyst"><div class="form-hint">唯一标识，仅小写字母和下划线</div></div>' +
    '</div>' +
    '<div class="form-group"><label>代表色</label><div class="color-options" id="add-w-colors"></div></div>' +
    '<div class="form-group"><label>职责描述</label><textarea id="add-w-duty" rows="4" placeholder="描述该成员的具体工作内容，如：\n- 分析数据趋势和模式\n- 生成可视化报表\n- 提供数据驱动的建议"></textarea></div>' +
    '<div class="form-actions">' +
      '<button class="btn-cancel" id="add-w-cancel">取消</button>' +
      '<button class="btn-submit" id="add-w-submit">确认添加</button>' +
    '</div>';

  container.appendChild(form);

  const colorsDiv = document.getElementById('add-w-colors');
  PRESET_COLORS.forEach(c => {
    const dot = document.createElement('span');
    dot.className = 'color-option' + (c === selectedColor ? ' selected' : '');
    dot.style.background = c;
    dot.onclick = () => {
      selectedColor = c;
      colorsDiv.querySelectorAll('.color-option').forEach(d => d.classList.remove('selected'));
      dot.classList.add('selected');
    };
    colorsDiv.appendChild(dot);
  });

  const labelInput = document.getElementById('add-w-label');
  const nameInput = document.getElementById('add-w-name');
  labelInput.addEventListener('input', () => {
    if (!nameInput.dataset.manual) {
      nameInput.value = labelInput.value.trim().toLowerCase().replace(/[^a-z0-9]/g, '_').replace(/_+/g, '_').replace(/^_|_$/g, '');
    }
  });
  nameInput.addEventListener('input', () => { nameInput.dataset.manual = '1'; });

  document.getElementById('add-w-cancel').onclick = () => form.remove();
  document.getElementById('add-w-submit').onclick = async () => {
    const label = labelInput.value.trim();
    const name = nameInput.value.trim();
    const duty = document.getElementById('add-w-duty').value.trim();

    if (!label) { labelInput.focus(); labelInput.style.borderColor='var(--danger)'; return; }
    if (!name) { nameInput.focus(); nameInput.style.borderColor='var(--danger)'; return; }

    const specialty = duty
      ? '你是团队中的' + label + '。职责：\n' + duty + '\n请认真完成 Leader 分配的任务。'
      : '你是团队中的' + label + '。请认真完成 Leader 分配的任务。';

    const submitBtn = document.getElementById('add-w-submit');
    submitBtn.disabled = true;
    submitBtn.textContent = '添加中...';

    try {
      const resp = await fetch('/api/team/' + team.id + '/workers', {
        method:'POST', headers:{'Content-Type':'application/json'},
        body: JSON.stringify({name, label, color: selectedColor, specialty}),
      });
      if (!resp.ok) {
        const err = await resp.json();
        alert(err.error || '添加失败');
        submitBtn.disabled = false; submitBtn.textContent = '确认添加';
        return;
      }
      await loadAll();
      const updatedTeam = teams.find(t => t.id === team.id);
      if (updatedTeam) {
        renderMemberList(container, updatedTeam);
        if (activeType === 'team' && activeId === team.id) renderAll();
      }
    } catch(e) {
      alert('网络错误: ' + e.message);
      submitBtn.disabled = false; submitBtn.textContent = '确认添加';
    }
  };

  form.scrollIntoView({behavior:'smooth', block:'end'});
  labelInput.focus();
}

// ── Typing indicator ──

function showTyping(text) {
  hideTyping();
  const msg = text || '思考中';
  if (streamingTeamId != null) streamingTypingText[streamingTeamId] = msg;
  const div = document.createElement('div');
  div.className = 'typing'; div.id = 'typing-indicator';
  div.innerHTML = msg + '<span>.</span><span>.</span><span>.</span>';
  chatBox.appendChild(div);
  chatBox.scrollTop = chatBox.scrollHeight;
}
function updateTyping(text) {
  if (streamingTeamId != null) streamingTypingText[streamingTeamId] = text;
  let el = document.getElementById('typing-indicator');
  if (!el) {
    el = document.createElement('div');
    el.className = 'typing'; el.id = 'typing-indicator';
    chatBox.appendChild(el);
  }
  el.innerHTML = text + '<span>.</span><span>.</span><span>.</span>';
  chatBox.scrollTop = chatBox.scrollHeight;
}
function hideTyping() {
  const el = document.getElementById('typing-indicator');
  if (el) el.remove();
}

// ── Memory ──

function toggleMemory() {
  memoryOpen = !memoryOpen;
  memEntriesEl.className = 'memory-entries' + (memoryOpen ? ' open' : '');
}

const MEM_TYPE_LABELS = { 'user':'👤 用户画像', 'feedback':'💬 行为反馈', 'project':'📁 项目上下文', 'reference':'📎 外部引用' };
const MEM_TYPE_ORDER = ['user','feedback','project','reference'];

function memoryAge(createdAt) {
  if (!createdAt) return '';
  const diff = Date.now() - new Date(createdAt).getTime();
  const days = Math.floor(diff / 86400000);
  if (days === 0) return '今天';
  if (days === 1) return '昨天';
  return days + '天前';
}

async function loadAgentMemory() {
  if (activeType !== 'agent' || !activeId) {
    memTitleEl.textContent = '记忆'; memCountEl.textContent = '0';
    memEntriesEl.innerHTML = '<div style="font-size:11px;color:var(--text-muted);padding:4px 0;">选择 Agent 查看记忆</div>';
    return;
  }
  memTitleEl.textContent = 'Agent #' + activeId + ' 记忆';
  try {
    const resp = await fetch('/api/agent/' + activeId + '/memory');
    const data = await resp.json();
    const entries = data.entries || [];
    memCountEl.textContent = entries.length;
    memEntriesEl.innerHTML = '';
    if (entries.length === 0) {
      memEntriesEl.innerHTML = '<div style="font-size:11px;color:var(--text-muted);padding:4px 0;">暂无记忆，对话中会自动提取</div>';
    } else {
      const grouped = {};
      entries.forEach(e => { const t = e.type||'user'; if(!grouped[t]) grouped[t]=[]; grouped[t].push(e); });
      MEM_TYPE_ORDER.forEach(t => {
        const items = grouped[t];
        if (!items || items.length === 0) return;
        const group = document.createElement('div');
        group.className = 'mem-type-group';
        const label = document.createElement('div');
        label.className = 'mem-type-label type-' + t;
        label.textContent = (MEM_TYPE_LABELS[t]||t) + ' (' + items.length + ')';
        group.appendChild(label);
        items.forEach(e => {
          const div = document.createElement('div');
          div.className = 'memory-entry type-' + t;
          const age = memoryAge(e.created_at);
          div.innerHTML = '<span class="mem-name">' + escapeHtml(e.name||'') + '</span>' +
            (age ? '<span class="mem-age">' + age + '</span>' : '') +
            '<div class="mem-content">' + escapeHtml(e.content||'') + '</div>';
          group.appendChild(div);
        });
        memEntriesEl.appendChild(group);
      });
      const btn = document.createElement('button');
      btn.className = 'clear-mem-btn';
      btn.textContent = '清除此 Agent 记忆';
      btn.onclick = clearAgentMemory;
      memEntriesEl.appendChild(btn);
    }
  } catch(e) {}
}

async function clearAgentMemory() {
  if (!activeId || activeType !== 'agent') return;
  if (!confirm('确定清除 Agent #' + activeId + ' 的所有记忆？')) return;
  await fetch('/api/agent/' + activeId + '/memory', {method:'DELETE'});
  loadAgentMemory();
}

// ── Send message ──

function lockInput() { sendBtn.disabled = true; input.disabled = true; }
function unlockInput() { sendBtn.disabled = false; input.disabled = false; input.focus(); }

async function send() {
  const text = input.value.trim();
  if (!text || !activeId || !activeType) return;
  input.value = ''; input.style.height = 'auto';
  lockInput();

  if (activeType === 'agent') {
    await sendAgent(text);
    unlockInput();
  } else if (activeType === 'team') {
    sendTeam(text);
  }
}

async function sendAgent(text) {
  activeHistory.push({role:'user', content:text});
  renderChat(); updateHeader(); showTyping();
  try {
    const resp = await fetch('/api/agent/' + activeId + '/chat', {
      method:'POST', headers:{'Content-Type':'application/json'},
      body: JSON.stringify({message:text}),
    });
    const data = await resp.json();
    hideTyping();
    activeHistory.push({role:'assistant', content: data.error ? '错误: '+data.error : data.reply});
    await loadAll();
  } catch(e) {
    hideTyping();
    activeHistory.push({role:'assistant', content:'网络错误: '+e.message});
  }
  renderAll();
  setTimeout(loadAgentMemory, 3000);
}

async function sendTeam(text) {
  const targetTeamId = activeId;

  const userMsg = {id:'local_u',from:'user',to:'leader',type:'chat',content:text,timestamp:new Date().toISOString()};
  teamMessages.push(userMsg);
  renderChat();
  showTyping('Leader 正在分析');

  if (streamingAbort) { streamingAbort.abort(); }
  const abortCtrl = new AbortController();
  streamingAbort = abortCtrl;
  streamingTeamId = targetTeamId;

  try {
    const resp = await fetch('/api/team/' + targetTeamId + '/chat', {
      method:'POST',
      headers:{'Content-Type':'application/json'},
      body: JSON.stringify({message:text}),
      signal: abortCtrl.signal,
    });

    const reader = resp.body.getReader();
    const decoder = new TextDecoder();
    let buffer = '';

    while (true) {
      const {done, value} = await reader.read();
      if (done) break;
      buffer += decoder.decode(value, {stream:true});

      const lines = buffer.split('\n');
      buffer = lines.pop();

      for (const line of lines) {
        if (line.startsWith('event: ')) {
          var currentEvent = line.slice(7).trim();
        }
        if (line.startsWith('data: ') && currentEvent) {
          try {
            const data = JSON.parse(line.slice(6));
            handleTeamSSE(currentEvent, data, targetTeamId);
          } catch(e) {}
          currentEvent = null;
        }
      }
    }

    if (activeType === 'team' && activeId === targetTeamId) {
      hideTyping();
      const msgResp = await fetch('/api/team/' + targetTeamId + '/messages');
      const msgData = await msgResp.json();
      teamMessages = msgData.messages || [];
      await loadAll();
      renderAll();
    }
  } catch(e) {
    if (e.name === 'AbortError') return;
    if (activeType === 'team' && activeId === targetTeamId) {
      hideTyping();
      teamMessages.push({id:'err',from:'system',to:'user',type:'error',content:'网络错误: '+e.message,timestamp:new Date().toISOString()});
      renderAll();
    }
  } finally {
    if (streamingTeamId === targetTeamId) {
      streamingTeamId = null;
      streamingAbort = null;
      delete streamingTypingText[targetTeamId];
    }
    if (activeType === 'team' && activeId === targetTeamId) {
      unlockInput();
    }
  }
}

const PHASE_ICONS = {'1':'🔍','2':'💻','3':'🔎','4':'🔎'};

let currentLeaderTurn = 0;

function handleTeamSSE(event, data, targetTeamId) {
  const isVisible = (activeType === 'team' && activeId === targetTeamId);

  function saveTyping(text) { streamingTypingText[targetTeamId] = text; }
  function clearTyping() { delete streamingTypingText[targetTeamId]; }

  switch(event) {
    case 'leader_start': {
      const turn = parseInt(data.turn) || 1;
      currentLeaderTurn = turn;
      saveTyping('Leader 第 '+turn+' 轮分析');
      if (!isVisible) break;
      if (turn > 1) {
        hideTyping();
        teamMessages.push({id:'turn_'+Date.now(),from:'leader',to:'*',type:'leader_turn',content:'Leader 第 '+turn+' 轮分析',timestamp:new Date().toISOString()});
        renderChat();
      }
      updateTyping('Leader 第 '+turn+' 轮分析');
      break;
    }
    case 'leader_plan':
      clearTyping();
      if (!isVisible) break;
      hideTyping();
      teamMessages.push({id:'plan_'+Date.now(),from:'leader',to:'*',type:'plan',content:data.plan,timestamp:new Date().toISOString()});
      renderChat();
      break;
    case 'pipeline_mode':
      if (!isVisible) break;
      hideTyping();
      teamMessages.push({id:'pipeline_'+Date.now(),from:'leader',to:'*',type:'pipeline',content:data.status,timestamp:new Date().toISOString()});
      renderChat();
      break;
    case 'phase_start':
      if (!isVisible) break;
      hideTyping();
      teamMessages.push({id:'phase_'+Date.now(),from:'leader',to:'*',type:'phase',content:(PHASE_ICONS[data.phase]||'📋')+' Phase '+data.phase+': '+data.name,timestamp:new Date().toISOString()});
      renderChat();
      break;
    case 'task_dispatch':
      saveTyping(data.label + ' 执行中');
      if (!isVisible) break;
      teamMessages.push({id:'task_'+Date.now()+'_'+data.worker,from:'leader',to:data.worker,type:'task',content:data.task,timestamp:new Date().toISOString()});
      renderChat();
      showTyping(data.label + ' 执行中');
      break;
    case 'worker_start':
      saveTyping(data.label + ' 执行中');
      if (!isVisible) break;
      updateTyping(data.label + ' 执行中');
      break;
    case 'worker_done':
      clearTyping();
      if (!isVisible) break;
      hideTyping();
      teamMessages.push({id:'res_'+Date.now()+'_'+data.worker,from:data.worker,to:'leader',type:'result',content:data.result,timestamp:new Date().toISOString()});
      renderChat();
      break;
    case 'worker_error':
      clearTyping();
      if (!isVisible) break;
      hideTyping();
      teamMessages.push({id:'err_'+Date.now()+'_'+data.worker,from:data.worker,to:'leader',type:'error',content:data.error,timestamp:new Date().toISOString()});
      renderChat();
      break;
    case 'worker_handoff':
      if (!isVisible) break;
      teamMessages.push({
        id:'handoff_'+Date.now()+'_'+data.from,
        from:data.from, to:data.to, type:'handoff',
        content:'已完成工作：' + data.summary + ' @' + data.to_label,
        timestamp:new Date().toISOString()
      });
      renderChat();
      break;
    case 'worker_heartbeat':
      saveTyping(data.label + ' 执行中 (' + data.elapsed + ')');
      if (!isVisible) { renderSidebar(); break; }
      updateTyping(data.label + ' 执行中 (' + data.elapsed + ')');
      break;
    case 'worker_continue': {
      const label = data.label || data.worker;
      saveTyping(label + ' 继续执行');
      if (!isVisible) break;
      hideTyping();
      teamMessages.push({id:'cont_'+Date.now()+'_'+data.worker,from:'leader',to:'*',type:'continue_task',content:label+' 接收后续指令（第 '+data.turn+' 轮）',timestamp:new Date().toISOString()});
      renderChat();
      showTyping(label + ' 继续执行');
      break;
    }
    case 'verify_start':
      saveTyping('验证阶段');
      if (!isVisible) break;
      hideTyping();
      teamMessages.push({id:'verify_'+Date.now(),from:'leader',to:'*',type:'verify_phase',content:'验证阶段',timestamp:new Date().toISOString()});
      renderChat();
      break;
    case 'max_turns_reached':
      saveTyping('正在强制综合');
      if (!isVisible) break;
      hideTyping();
      teamMessages.push({id:'maxturns_'+Date.now(),from:'system',to:'*',type:'max_turns',content:'Leader 达到最大 '+data.turns+' 轮，正在强制综合',timestamp:new Date().toISOString()});
      renderChat();
      break;
    case 'revision_start':
      saveTyping('编码者正在修改');
      if (!isVisible) break;
      hideTyping();
      teamMessages.push({id:'rev_'+Date.now(),from:'leader',to:'*',type:'revision',content:'第'+data.round+'轮修改：'+data.reason,timestamp:new Date().toISOString()});
      renderChat();
      showTyping('编码者正在修改');
      break;
    case 'review_complete':
      clearTyping();
      if (!isVisible) break;
      hideTyping();
      teamMessages.push({id:'revc_'+Date.now(),from:'leader',to:'*',type:'review_complete',content:data.status,timestamp:new Date().toISOString()});
      renderChat();
      break;
    case 'leader_synthesize':
      saveTyping('Leader 正在综合结果');
      if (!isVisible) break;
      showTyping('Leader 正在综合结果');
      break;
    case 'final_reply':
      clearTyping();
      if (!isVisible) break;
      hideTyping();
      currentLeaderTurn = 0;
      teamMessages.push({id:'final_'+Date.now(),from:'leader',to:'user',type:'synthesis',content:data.content,timestamp:new Date().toISOString()});
      renderChat();
      break;
    case 'error':
      clearTyping();
      if (!isVisible) break;
      hideTyping();
      teamMessages.push({id:'err_'+Date.now(),from:'system',to:'user',type:'error',content:data.error,timestamp:new Date().toISOString()});
      renderChat();
      break;
  }
}

// ── Input handling ──

input.addEventListener('input', function() { this.style.height='auto'; this.style.height=Math.min(this.scrollHeight,120)+'px'; });
sendBtn.addEventListener('click', send);
input.addEventListener('keydown', e => { if(e.key==='Enter'&&!e.shiftKey){e.preventDefault();send();} });

// ── Docs Panel ──

let docsPanelOpen = false;
let docsCache = null;
let activeDocName = null;

async function loadDocsList() {
  if (docsCache) return docsCache;
  const resp = await fetch('/api/docs');
  const data = await resp.json();
  docsCache = data.docs || [];
  return docsCache;
}

function toggleDocsPanel() {
  if (docsPanelOpen) {
    closeDocsPanel();
  } else {
    openDocsPanel();
  }
}

async function openDocsPanel() {
  docsPanelOpen = true;
  $('docs-toggle-btn').classList.add('active');

  const docs = await loadDocsList();

  const overlay = document.createElement('div');
  overlay.className = 'docs-overlay';
  overlay.id = 'docs-overlay';

  const bg = document.createElement('div');
  bg.className = 'docs-overlay-bg';
  bg.onclick = closeDocsPanel;
  overlay.appendChild(bg);

  const panel = document.createElement('div');
  panel.className = 'docs-panel';

  const contentPane = document.createElement('div');
  contentPane.className = 'docs-content-pane';
  contentPane.id = 'docs-content-pane';
  panel.appendChild(contentPane);

  const listPane = document.createElement('div');
  listPane.className = 'docs-list-pane';

  const listHeader = document.createElement('div');
  listHeader.className = 'docs-list-header';
  listHeader.innerHTML = '<h3>📚 文档列表</h3>';
  const closeBtn = document.createElement('button');
  closeBtn.className = 'docs-close-btn';
  closeBtn.innerHTML = '✕';
  closeBtn.onclick = closeDocsPanel;
  listHeader.appendChild(closeBtn);
  listPane.appendChild(listHeader);

  const list = document.createElement('div');
  list.className = 'docs-list';
  list.id = 'docs-list';

  if (docs.length === 0) {
    list.innerHTML = '<div style="padding:20px;text-align:center;color:var(--text-muted);font-size:13px;">docs/ 目录下暂无文档</div>';
  } else {
    docs.forEach(doc => {
      const item = document.createElement('div');
      item.className = 'doc-item' + (doc.name === activeDocName ? ' active' : '');
      item.id = 'doc-item-' + doc.name;
      item.onclick = () => openDoc(doc);
      item.innerHTML =
        '<span class="doc-item-icon">📄</span>' +
        '<div class="doc-item-info">' +
          '<div class="doc-item-title">' + escapeHtml(doc.title) + '</div>' +
          '<div class="doc-item-name">' + escapeHtml(doc.name) + '</div>' +
        '</div>';
      list.appendChild(item);
    });
  }
  listPane.appendChild(list);
  panel.appendChild(listPane);
  overlay.appendChild(panel);
  document.body.appendChild(overlay);

  if (activeDocName) {
    const doc = docs.find(d => d.name === activeDocName);
    if (doc) openDoc(doc);
  }
}

function closeDocsPanel() {
  docsPanelOpen = false;
  $('docs-toggle-btn').classList.remove('active');
  const el = document.getElementById('docs-overlay');
  if (el) el.remove();
}

async function openDoc(doc) {
  activeDocName = doc.name;

  const listEl = document.getElementById('docs-list');
  if (listEl) {
    listEl.querySelectorAll('.doc-item').forEach(el => el.classList.remove('active'));
    const activeEl = document.getElementById('doc-item-' + doc.name);
    if (activeEl) activeEl.classList.add('active');
  }

  const pane = document.getElementById('docs-content-pane');
  if (!pane) return;

  pane.innerHTML = '<div style="padding:40px;text-align:center;color:var(--text-muted);">加载中...</div>';
  pane.classList.add('open');

  try {
    const resp = await fetch('/api/doc/' + encodeURIComponent(doc.name));
    if (!resp.ok) throw new Error('加载失败');
    const raw = await resp.text();

    pane.innerHTML = '';

    const resizeHandle = document.createElement('div');
    resizeHandle.className = 'docs-resize-handle';
    initDocsResize(resizeHandle, pane);
    pane.appendChild(resizeHandle);

    const header = document.createElement('div');
    header.className = 'docs-content-header';
    header.innerHTML = '<h3>' + escapeHtml(doc.title) + '</h3>';
    const closeContentBtn = document.createElement('button');
    closeContentBtn.className = 'docs-close-content';
    closeContentBtn.innerHTML = '✕';
    closeContentBtn.onclick = () => {
      pane.classList.remove('open');
      pane.style.width = '';
      pane.innerHTML = '';
      activeDocName = null;
      const listEl2 = document.getElementById('docs-list');
      if (listEl2) listEl2.querySelectorAll('.doc-item').forEach(el => el.classList.remove('active'));
    };
    header.appendChild(closeContentBtn);
    pane.appendChild(header);

    const body = document.createElement('div');
    body.className = 'docs-content-body';

    const mdDiv = document.createElement('div');
    mdDiv.className = 'md-body';
    if (typeof marked !== 'undefined') {
      marked.setOptions({ breaks: true, gfm: true });
      mdDiv.innerHTML = marked.parse(raw);
    } else {
      mdDiv.innerHTML = '<pre style="white-space:pre-wrap;">' + escapeHtml(raw) + '</pre>';
    }
    body.appendChild(mdDiv);
    pane.appendChild(body);
  } catch(e) {
    pane.innerHTML = '<div style="padding:40px;text-align:center;color:var(--danger);">加载失败: ' + escapeHtml(e.message) + '</div>';
  }
}

function initDocsResize(handle, pane) {
  let startX, startW;
  const onMouseMove = (e) => {
    const delta = startX - e.clientX;
    const newW = Math.max(300, Math.min(window.innerWidth - 320, startW + delta));
    pane.style.width = newW + 'px';
  };
  const onMouseUp = () => {
    handle.classList.remove('dragging');
    document.body.style.cursor = '';
    document.body.style.userSelect = '';
    document.removeEventListener('mousemove', onMouseMove);
    document.removeEventListener('mouseup', onMouseUp);
  };
  handle.addEventListener('mousedown', (e) => {
    e.preventDefault();
    startX = e.clientX;
    startW = pane.offsetWidth;
    handle.classList.add('dragging');
    document.body.style.cursor = 'col-resize';
    document.body.style.userSelect = 'none';
    document.addEventListener('mousemove', onMouseMove);
    document.addEventListener('mouseup', onMouseUp);
  });
}

document.addEventListener('keydown', (e) => {
  if (e.key === 'Escape' && docsPanelOpen) closeDocsPanel();
});

// ── Init ──

loadAll().then(() => {
  if (teams.length > 0) {
    switchTeam(teams[teams.length-1].id);
  } else if (agents.length > 0) {
    switchAgent(agents[agents.length-1].id);
  }
});
</script>
</body>
</html>`
