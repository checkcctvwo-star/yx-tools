'use strict';

const $ = s => document.querySelector(s);
const state = {
  colos: [],       // 全部机场码
  picked: [],      // 已选机场码
  results: [],     // 测速结果
  sortKey: '',
  sortDir: 'desc',
  running: false,
  hasToken: false,
  system: {},      // 运行环境信息（是否支持 crontab 等）
  pool: 1000,      // 候选 IP 数量，0 表示不限
  httping: false,  // 用真实 HTTP 请求测延迟
  noDL: false,     // 跳过下载测速
  schedules: [],   // 定时任务
  proxyUrls: [],   // 优选反代的 URL 来源
  editingSchedId: null,
};

// ── 提示 ──────────────────────────────────────────
function toast(msg, kind) {
  const el = document.createElement('div');
  el.className = 'toast' + (kind ? ' ' + kind : '');
  el.textContent = msg;
  $('#toasts').appendChild(el);
  setTimeout(() => {
    el.style.opacity = '0';
    el.style.transition = 'opacity .3s';
    setTimeout(() => el.remove(), 300);
  }, 3600);
}

async function api(path, opts) {
  const r = await fetch(path, opts);
  const text = await r.text();
  let data = null;
  try { data = text ? JSON.parse(text) : null; } catch (_) {}
  if (!r.ok) throw new Error((data && data.error) || text || ('HTTP ' + r.status));
  return data;
}

// ── 机场码选择 ────────────────────────────────────
function renderChips() {
  const box = $('#coloChips');
  box.innerHTML = '';
  state.picked.forEach(code => {
    const c = state.colos.find(x => x.code === code);
    const el = document.createElement('div');
    el.className = 'chip';
    el.innerHTML = `<b>${code}</b>${c ? c.name : ''}<span>&times;</span>`;
    el.querySelector('span').onclick = () => {
      state.picked = state.picked.filter(x => x !== code);
      renderChips();
    };
    box.appendChild(el);
  });
  // 选了地区必须走真实连接，测法要跟着锁上
  if (typeof setPing === 'function') setPing(state.picked.length > 0 || state.httping);
}

function renderColoList(q) {
  const list = $('#coloList');
  q = (q || '').trim().toLowerCase();
  if (!q) { list.classList.remove('show'); return; }
  const hit = state.colos.filter(c =>
    c.code.toLowerCase().includes(q) || c.name.includes(q) ||
    c.country.includes(q) || c.region.includes(q)
  ).slice(0, 40);
  list.innerHTML = '';
  if (!hit.length) { list.classList.remove('show'); return; }
  hit.forEach(c => {
    const el = document.createElement('div');
    el.className = 'colo-item';
    el.innerHTML = `<span>${c.name} <code>${c.code}</code></span><code>${c.country}</code>`;
    el.onclick = () => {
      if (!state.picked.includes(c.code)) state.picked.push(c.code);
      $('#coloSearch').value = '';
      list.classList.remove('show');
      renderChips();
    };
    list.appendChild(el);
  });
  list.classList.add('show');
}

// ── 结果表 ────────────────────────────────────────
function fmtSpeed(v) {
  const cls = v >= 5 ? 'g' : v >= 1 ? 'y' : 'r';
  return `<span class="${cls}">${v.toFixed(2)}</span>`;
}
function fmtDelay(v) {
  const cls = v <= 100 ? 'g' : v <= 250 ? 'y' : 'r';
  // 亚毫秒延迟取整会变成 0，看着像没测到
  const txt = v > 0 && v < 10 ? v.toFixed(2) : v.toFixed(0);
  return `<span class="${cls}">${txt}</span>`;
}

function visibleRows() {
  const q = $('#filterText').value.trim().toLowerCase();
  let rows = state.results;
  if (q) {
    rows = rows.filter(r =>
      r.ip.toLowerCase().includes(q) ||
      (r.colo || '').toLowerCase().includes(q) ||
      (r.colo_name || '').includes(q)
    );
  }
  if (state.sortKey) {
    const k = state.sortKey, dir = state.sortDir === 'asc' ? 1 : -1;
    rows = rows.slice().sort((a, b) => {
      let x = k === 'loss' ? a.loss_rate : a[k];
      let y = k === 'loss' ? b.loss_rate : b[k];
      if (typeof x === 'string') return x.localeCompare(y) * dir;
      return (x - y) * dir;
    });
  }
  return rows;
}

function renderTable() {
  const rows = visibleRows();
  const tb = $('#tbody');
  tb.innerHTML = '';
  $('#emptyBox').classList.toggle('hidden', rows.length > 0);
  rows.forEach((r, i) => {
    const tr = document.createElement('tr');
    tr.innerHTML =
      `<td class="c-idx">${i + 1}</td>` +
      `<td class="mono">${r.ip}</td>` +
      `<td class="c-num mono">${r.port}</td>` +
      `<td class="c-num mono">${fmtDelay(r.delay)}</td>` +
      `<td class="c-num mono">${fmtSpeed(r.speed)}</td>` +
      `<td class="c-num mono">${(r.loss_rate * 100).toFixed(0)}%</td>` +
      `<td>${r.colo_name || '-'}${r.colo ? ' <code style="opacity:.6">' + r.colo + '</code>' : ''}</td>` +
      `<td class="c-act"><button class="copy" title="复制 IP:端口">⧉</button></td>`;
    tr.querySelector('.copy').onclick = () => {
      navigator.clipboard.writeText(`${r.ip}:${r.port}`).then(
        () => toast('已复制 ' + r.ip + ':' + r.port, 'ok'),
        () => toast('复制失败', 'err')
      );
    };
    tb.appendChild(tr);
  });
  $('#statResult').textContent = '结果 ' + state.results.length;
}

// ── 运行状态 ──────────────────────────────────────
function setRunning(on, keepProgress) {
  state.running = on;
  $('#btnStart').classList.toggle('hidden', on);
  $('#btnStop').classList.toggle('hidden', !on);
  $('#statusDot').className = 'dot' + (on ? ' run' : '');
  if (keepProgress) return; // 进度事件自己在画，别覆盖成滚动动画
  const fill = $('#progressFill');
  fill.style.width = '';
  fill.className = on ? 'indet' : 'idle';
}

// 有确切进度就画百分比，没有才退回滚动动画
function setProgress(cur, total) {
  const fill = $('#progressFill');
  if (total > 0) {
    const pct = Math.min(100, Math.round(cur / total * 100));
    fill.className = '';
    fill.style.width = pct + '%';
    return pct;
  }
  fill.className = 'indet';
  fill.style.width = '';
  return null;
}

function connectEvents() {
  const es = new EventSource('/api/events');
  es.onmessage = ev => {
    let e;
    try { e = JSON.parse(ev.data); } catch (_) { return; }
    if (e.message) {
      // 带上「已测 N/M」，让人一眼看出还在动
      $('#statusText').textContent = e.total > 0
        ? `${e.message}  ${e.current}/${e.total}`
        : e.message;
    }
    if (e.type === 'progress') setProgress(e.current, e.total);
    if (e.type === 'result' && e.result) {
      // 下载测速逐条回来，测一个显示一个，不用等整批跑完
      state.results.push(e.result);
      renderTable();
      setRunning(true, true);
      return;
    }
    if (e.type === 'done') {
      state.results = e.results || [];
      renderTable();
      setProgress(1, 1); // 收到 100% 再消失，别停在半截
      setRunning(false, true);
      setTimeout(() => { if (!state.running) setRunning(false); }, 600);
      toast(`测速完成，${state.results.length} 个结果`, 'ok');
    } else if (e.type === 'error') {
      setRunning(false);
      $('#statusDot').className = 'dot err';
      toast(e.message || '测速失败', 'err');
    } else if (e.type === 'progress') {
      setRunning(true, true);
    }
  };
  es.onerror = () => { /* 浏览器会自动重连 */ };
}

// ── 启动测速 ──────────────────────────────────────
// 把左侧面板当前设置翻译成一次测速的参数对象
function collectOpts() {
  return {
    colo: state.picked.join(','),
    ipv6: $('#segIPv button.on').dataset.v === '6',
    count: +$('#inCount').value || 10,
    speed_limit: +$('#inSpeed').value || 0,
    delay_limit: +$('#inDelay').value || 1000,
    threads: +$('#inThread').value || 200,
    port: +$('#inPort').value || 443,
    test_url: $('#inURL').value.trim(),
    ip_text: $('#inIPText').value.trim(),
    sample_size: state.pool,
    // 只有点了「全部」这一档才穷举网段内每个 IP
    test_all: $('#segPool button[data-pool="0"]').classList.contains('on'),
    httping: state.httping,
    disable_dl: state.noDL,
    dl_timeout: +$('#inDLTimeout').value || 10,
    max_runtime: +$('#inMaxRun').value || 0,
  };
}

async function start() {
  try {
    await api('/api/start', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(collectOpts()),
    });
    setRunning(true);
    $('#statusDot').className = 'dot run';
    $('#statusText').textContent = '正在启动…';
    // 清掉上一轮，逐条结果才不会叠在旧数据上
    state.results = [];
    renderTable();
  } catch (e) {
    toast(e.message, 'err');
  }
}

// ── 配置弹窗 ──────────────────────────────────────
// uploadOnly 为真时只刷新上报设置：保存配置后不该把左侧
// 用户刚调好的测速参数冲回上一次的值。
async function loadConfig(uploadOnly) {
  try {
    const c = await api('/api/config');
    $('#cfgDomain').value = c.worker_domain || '';
    $('#cfgUUID').value = c.uuid || '';
    $('#cfgRepo').value = c.github_repo || '';
    $('#cfgPath').value = c.github_path || 'cloudflare_ips.txt';
    state.hasToken = !!c.has_github_token;
    $('#tokenHint').textContent = state.hasToken ? '已保存' : '';
    if (uploadOnly) return;
    // 回填上次的测速参数
    if (c.count) $('#inCount').value = c.count;
    if (c.speed_limit != null) $('#inSpeed').value = c.speed_limit;
    if (c.delay_limit) $('#inDelay').value = c.delay_limit;
    if (c.threads) { $('#inThread').value = c.threads; $('#threadVal').textContent = c.threads; }
    if (c.port) $('#inPort').value = c.port;
    if (c.test_url) $('#inURL').value = c.test_url;
    if (c.dl_timeout) $('#inDLTimeout').value = c.dl_timeout;
    if (c.max_runtime != null) $('#inMaxRun').value = c.max_runtime;
    setPing(!!c.httping);
    setDL(!!c.disable_dl);
    if (c.sample_size != null) {
      const custom = !POOL_PRESETS.includes(c.sample_size);
      if (custom) $('#inPool').value = c.sample_size;
      setPool(c.sample_size, { custom });
    }
    if (c.ipv6) {
      document.querySelectorAll('#segIPv button').forEach(b =>
        b.classList.toggle('on', b.dataset.v === '6'));
    }
    if (c.colo) {
      state.picked = c.colo.split(',').map(s => s.trim()).filter(Boolean);
      renderChips();
    }
  } catch (_) {}
}

async function saveConfig() {
  const body = {
    worker_domain: $('#cfgDomain').value.trim(),
    uuid: $('#cfgUUID').value.trim(),
    github_repo: $('#cfgRepo').value.trim(),
    github_path: $('#cfgPath').value.trim(),
  };
  const tok = $('#cfgToken').value.trim();
  if (tok) body.github_token = tok;
  try {
    await api('/api/config', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    });
    $('#cfgToken').value = '';
    $('#mask').classList.add('hidden');
    toast('配置已保存', 'ok');
    loadConfig(true);
  } catch (e) { toast(e.message, 'err'); }
}

// ── 导出与上报 ────────────────────────────────────
// ── 优选反代 ──────────────────────────────────────
// 拿现成的 IP 列表当输入源重测一遍，沿用旧 Python 版的流程。
function openProxy() {
  $('#proxyMask').classList.remove('hidden');
  updateProxyCount();
  renderProxyUrls();
}

function updateProxyCount() {
  const n = $('#proxyText').value
    .split('\n')
    .map(l => l.split('#')[0].trim())
    .filter(l => l && !l.startsWith('#')).length;
  $('#proxyCount').textContent = n ? n + ' 行' : '';
}

// URL 来源行：一个输入框配一个删除按钮，最多 10 个
function renderProxyUrls() {
  const box = $('#proxyUrls');
  box.innerHTML = '';
  state.proxyUrls.forEach((u, i) => {
    const row = document.createElement('div');
    row.className = 'url-row';
    const input = document.createElement('input');
    input.type = 'text';
    input.placeholder = 'https://…/list.txt（每行一个 IP 或 IP:端口）';
    input.value = u;
    input.autocomplete = 'off';
    input.addEventListener('input', () => { state.proxyUrls[i] = input.value.trim(); });
    const del = document.createElement('button');
    del.className = 'btn ghost sm';
    del.type = 'button';
    del.textContent = '×';
    del.title = '删除这个链接';
    del.onclick = () => { state.proxyUrls.splice(i, 1); renderProxyUrls(); };
    row.appendChild(input);
    row.appendChild(del);
    box.appendChild(row);
  });
  $('#btnProxyAddUrl').classList.toggle('hidden', state.proxyUrls.length >= 10);
}

// 汇总当前弹窗里的来源，交给服务端合成
function collectSources() {
  return {
    urls: state.proxyUrls.filter(u => u.trim() !== ''),
    text: $('#proxyText').value.trim(),
    random_cf: $('#proxyRandomCF').checked,
    random_cf_count: +$('#proxyCFCount').value || 100,
  };
}

function renderProxyPreview(r) {
  const box = $('#proxyPreview');
  box.classList.remove('hidden');
  box.innerHTML = '';
  const head = document.createElement('b');
  head.textContent = `合并后共 ${r.count} 条`;
  box.appendChild(head);
  (r.sources || []).forEach(s => {
    const el = document.createElement('div');
    el.className = s.warning ? 'warn' : '';
    el.textContent = `${s.name}：${s.warning || s.count + ' 条'}`;
    box.appendChild(el);
  });
}

// 预览合并：只统计，不写文件
async function previewProxy() {
  const src = collectSources();
  if (!src.urls.length && !src.text && !src.random_cf) {
    toast('先贴一份 IP 列表、填一个 URL，或勾选随机 CF', 'err');
    return;
  }
  $('#btnProxyPreview').disabled = true;
  try {
    const r = await api('/api/proxy-fetch', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ ...src, port: +$('#inPort').value || 443, save: false }),
    });
    renderProxyPreview(r);
  } catch (e) {
    const box = $('#proxyPreview');
    box.classList.remove('hidden');
    box.innerHTML = '';
    const el = document.createElement('div');
    el.className = 'warn';
    el.textContent = e.message;
    box.appendChild(el);
  } finally {
    $('#btnProxyPreview').disabled = false;
  }
}

async function runProxy() {
  const src = collectSources();
  if (!src.urls.length && !src.text && !src.random_cf) {
    toast('先贴一份 IP 列表、填一个 URL，或勾选随机 CF', 'err');
    return;
  }
  if (state.running) { toast('正在测速，先停下来', 'err'); return; }
  try {
    const r = await api('/api/proxy-fetch', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        ...src, port: +$('#inPort').value || 443,
        save: true, take: +$('#proxyTake').value || 0,
      }),
    });
    renderProxyPreview(r);
    toast(`已生成 ${r.file}，共 ${r.count} 条，开始测速`, 'ok');
    $('#proxyMask').classList.add('hidden');
    await api('/api/start', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        proxy: true,
        colo: state.picked.join(','),
        count: +$('#inCount').value || 10,
        speed_limit: +$('#inSpeed').value || 0,
        delay_limit: +$('#inDelay').value || 1000,
        threads: +$('#inThread').value || 200,
        test_url: $('#inURL').value.trim(),
        httping: state.httping,
        disable_dl: state.noDL,
      }),
    });
    setRunning(true);
    $('#statusDot').className = 'dot run';
    $('#statusText').textContent = '正在测反代列表…';
  } catch (e) { toast(e.message, 'err'); }
}

async function uploadAPI() {
  const domain = $('#cfgDomain').value.trim();
  const uuid = $('#cfgUUID').value.trim();
  if (!domain || !uuid) {
    $('#mask').classList.remove('hidden');
    toast('请先填写 Worker 域名和 UUID', 'err');
    return;
  }
  try {
    const r = await api('/api/upload/api', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        worker_domain: domain, uuid: uuid,
        limit: +$('#cfgLimit').value || 0,
        clear: $('#cfgClear').checked,
      }),
    });
    toast(`已上报 ${r.count} 个 IP`, 'ok');
  } catch (e) { toast(e.message, 'err'); }
}

async function uploadGitHub() {
  const repo = $('#cfgRepo').value.trim();
  if (!repo || (!state.hasToken && !$('#cfgToken').value.trim())) {
    $('#mask').classList.remove('hidden');
    toast('请先填写 GitHub 仓库和 Token', 'err');
    return;
  }
  try {
    const r = await api('/api/upload/github', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        repo: repo,
        token: $('#cfgToken').value.trim(),
        path: $('#cfgPath').value.trim(),
        limit: +$('#cfgLimit').value || 0,
      }),
    });
    toast(`已上传 ${r.count} 个 IP 到 GitHub`, 'ok');
  } catch (e) { toast(e.message, 'err'); }
}

// ── 延迟测法与下载测速 ────────────────────────────
function setPing(on) {
  state.httping = on;
  document.querySelectorAll('#segPing button').forEach(b =>
    b.classList.toggle('on', (b.dataset.ping === 'http') === on));
  const forced = state.picked.length > 0;
  $('#pingNote').textContent = on
    ? (forced ? '选了地区就得走真实连接，才能读出机房代码'
              : '走完整 HTTP 请求，含 TLS 握手和服务端响应，更接近实际体验')
    : '只做 TCP 握手，快，但数字偏低';
  // 选了地区时锁死在真实连接，避免给出无效选项
  $('#segPing button[data-ping="tcp"]').disabled = forced;
}

function setDL(off) {
  state.noDL = off;
  document.querySelectorAll('#segDL button').forEach(b =>
    b.classList.toggle('on', (b.dataset.dl === 'off') === off));
  $('#dlNote').textContent = off
    ? '只排延迟，不下载，快很多但看不出带宽'
    : '测完延迟再下载大文件，测出真实速度';
}

// ── 候选 IP 数量 ──────────────────────────────────
const POOL_PRESETS = [500, 1000, 2000, 0];

// 高亮某一档。isCustom 为真时高亮「自定义」并展开输入框
function markPool(n, isCustom) {
  document.querySelectorAll('#segPool button').forEach(b => {
    const on = b.dataset.pool === 'custom' ? isCustom
      : (!isCustom && +b.dataset.pool === n);
    b.classList.toggle('on', on);
  });
  $('#inPool').classList.toggle('hidden', !isCustom);
}

function setPool(n, opt) {
  opt = opt || {};
  state.pool = n;
  markPool(n, !!opt.custom);
  const note = $('#poolNote');
  if ($('#inIPText').value.trim() !== '') {
    note.textContent = '用你填的自定义 IP 段，不走官方段';
  } else if (n === 0) {
    note.textContent = '穷举网段内每个 IP，量很大、很慢';
  } else {
    note.textContent = `从 Cloudflare 官方 IP 段里随机抽 ${n} 个来测延迟`;
  }
}

// 切到自定义档：沿用输入框已有的值，没有就拿当前值打底
function pickCustomPool() {
  const box = $('#inPool');
  if (!box.value) box.value = state.pool || 1000;
  setPool(Math.max(1, +box.value || 1), { custom: true });
  box.focus();
}

function syncPoolNote() {
  setPool(state.pool, { custom: !$('#inPool').classList.contains('hidden') });
}

// ── 下载结果文件 ──────────────────────────────────
function download(kind) {
  const a = document.createElement('a');
  a.href = '/api/download?kind=' + encodeURIComponent(kind);
  a.download = '';
  document.body.appendChild(a);
  a.click();
  a.remove();
}

// ── 自定义 IP 段文件导入 ──────────────────────────
function importIPFile(file) {
  if (!file) return;
  if (file.size > 8 * 1024 * 1024) {
    toast('文件太大，最多 8MB', 'err');
    return;
  }
  const reader = new FileReader();
  reader.onload = () => {
    const lines = String(reader.result)
      .split(/\r?\n/)
      .map(l => l.trim())
      .filter(l => l && !l.startsWith('#'));
    if (!lines.length) {
      toast('文件里没有可用的 IP', 'err');
      return;
    }
    $('#inIPText').value = lines.join('\n');
    $('#ipFileName').textContent = file.name + ' · ' + lines.length + ' 条';
    $('.more').open = true;
    syncPoolNote();
    toast('已导入 ' + lines.length + ' 条', 'ok');
  };
  reader.onerror = () => toast('读取文件失败', 'err');
  reader.readAsText(file);
}

// ── 定时任务 ──────────────────────────────────────
function fmtTime(ts) {
  if (!ts) return '—';
  const d = new Date(ts * 1000);
  const p = n => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`;
}

function fmtInterval(min) {
  if (min % 1440 === 0) return `每 ${min / 1440} 天`;
  if (min % 60 === 0) return `每 ${min / 60} 小时`;
  return `每 ${min} 分钟`;
}

function schedTargets(t) {
  const parts = [];
  if (t.upload && t.upload.github) parts.push('GitHub');
  if (t.upload && t.upload.worker) parts.push('Worker');
  return parts.length ? parts.join(' + ') : '不上传';
}

async function loadSchedules() {
  const box = $('#schedList');
  box.innerHTML = '';
  try {
    state.schedules = await api('/api/schedules') || [];
    if (!state.schedules.length) {
      box.innerHTML = '<div class="empty" style="padding:14px">还没有定时任务</div>';
      $('#schedHint').textContent = '';
      return;
    }
    $('#schedHint').textContent = state.schedules.length + ' 条';
    renderSchedules();
  } catch (e) {
    box.innerHTML = '<div class="empty" style="padding:14px">' + e.message + '</div>';
  }
}

function renderSchedules() {
  const box = $('#schedList');
  box.innerHTML = '';
  state.schedules.forEach(t => {
    const item = document.createElement('div');
    item.className = 'sched-item' + (t.enabled ? '' : ' off');

    const row = document.createElement('div');
    row.className = 'row';
    const name = document.createElement('b');
    name.textContent = t.name || '未命名';
    const onoff = document.createElement('label');
    onoff.className = 'check';
    onoff.style.marginTop = '0';
    const cb = document.createElement('input');
    cb.type = 'checkbox';
    cb.checked = !!t.enabled;
    cb.title = '启用 / 停用';
    cb.onchange = () => toggleSched(t, cb.checked);
    onoff.appendChild(cb);
    onoff.appendChild(document.createTextNode('启用'));
    row.appendChild(name);
    row.appendChild(onoff);

    const meta = document.createElement('div');
    meta.className = 'meta';
    meta.innerHTML = `<span>${fmtInterval(t.interval_minutes)}</span>` +
      `<span>上次 ${fmtTime(t.last_run)}</span>` +
      `<span>下次 ${fmtTime(t.next_run)}</span>` +
      `<span>${schedTargets(t)}${t.upload && t.upload.top_n ? ' · 前 ' + t.upload.top_n : ''}</span>`;

    const log = document.createElement('div');
    log.className = 'log';
    log.textContent = t.last_log || '还没有执行记录';

    const act = document.createElement('div');
    act.className = 'act';
    const runBtn = document.createElement('button');
    runBtn.className = 'btn ghost sm';
    runBtn.textContent = '立即执行';
    runBtn.onclick = () => runSchedNow(t);
    const editBtn = document.createElement('button');
    editBtn.className = 'btn ghost sm';
    editBtn.textContent = '编辑';
    editBtn.onclick = () => editSched(t);
    const delBtn = document.createElement('button');
    delBtn.className = 'btn ghost sm';
    delBtn.textContent = '删除';
    delBtn.onclick = () => delSched(t);
    act.appendChild(runBtn);
    act.appendChild(editBtn);
    act.appendChild(delBtn);

    item.appendChild(row);
    item.appendChild(meta);
    item.appendChild(log);
    item.appendChild(act);
    box.appendChild(item);
  });
}

function openSched() {
  $('#schedMask').classList.remove('hidden');
  closeSchedForm();
  loadSchedules();
}

function closeSchedForm() {
  state.editingSchedId = null;
  $('#schedForm').classList.add('hidden');
}

// 来源与参数摘要：保存任务时按这个内容快照
function srcSummary() {
  const src = collectSources();
  const parts = [];
  if (src.urls.length) parts.push(`URL ${src.urls.length} 个`);
  if (src.text) parts.push(`粘贴 ${src.text.split('\n').filter(l => l.trim()).length} 行`);
  if (src.random_cf) parts.push(`随机 CF ${src.random_cf_count} 个`);
  if (!parts.length) parts.push('空');
  return parts.join(' + ');
}

function optsSummary() {
  const o = collectOpts();
  return [
    o.colo ? '地区 ' + o.colo : '不限地区',
    '数量 ' + o.count,
    '端口 ' + o.port,
    o.speed_limit ? '速度≥' + o.speed_limit + 'MB/s' : '不限速度',
    o.httping ? '真实连接' : 'TCP 握手',
    o.disable_dl ? '不测下载' : '测下载',
  ].join(' · ');
}

function fillSchedForm(t) {
  $('#schedName').value = (t && t.name) || '';
  $('#schedMin').value = (t && t.interval_minutes) || 360;
  markSchedPreset((t && t.interval_minutes) || 360);
  $('#schedUploadGH').checked = t ? !!(t.upload && t.upload.github) : !!$('#cfgRepo').value.trim();
  $('#schedUploadWorker').checked = t ? !!(t.upload && t.upload.worker) : !!($('#cfgDomain').value.trim() && $('#cfgUUID').value.trim());
  $('#schedWorkerClear').checked = t ? !!(t.upload && t.upload.worker_clear) : true;
  $('#schedTopN').value = (t && t.upload && t.upload.top_n) || 20;
  $('#schedSrcHint').textContent = srcSummary();
  $('#schedOptsHint').textContent = optsSummary();
}

function markSchedPreset(min) {
  document.querySelectorAll('#schedPresets button').forEach(b =>
    b.classList.toggle('on', +b.dataset.min === +min));
}

function newSched() {
  state.editingSchedId = null;
  fillSchedForm(null);
  $('#schedForm').classList.remove('hidden');
  $('#schedForm').scrollIntoView({ behavior: 'smooth', block: 'nearest' });
}

function editSched(t) {
  state.editingSchedId = t.id;
  fillSchedForm(t);
  $('#schedForm').classList.remove('hidden');
  $('#schedForm').scrollIntoView({ behavior: 'smooth', block: 'nearest' });
}

async function saveSched() {
  const name = $('#schedName').value.trim();
  const min = +$('#schedMin').value;
  if (!name) { toast('给任务起个名字', 'err'); return; }
  if (!min || min < 1) { toast('执行频率至少 1 分钟', 'err'); return; }
  const body = {
    name,
    enabled: true,
    interval_minutes: min,
    opts: collectOpts(),
    sources: collectSources(),
    upload: {
      github: $('#schedUploadGH').checked,
      worker: $('#schedUploadWorker').checked,
      worker_clear: $('#schedWorkerClear').checked,
      top_n: +$('#schedTopN').value || 0,
    },
  };
  try {
    if (state.editingSchedId) {
      await api('/api/schedules/' + encodeURIComponent(state.editingSchedId), {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      });
      toast('任务已更新', 'ok');
    } else {
      await api('/api/schedules', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      });
      toast('任务已创建', 'ok');
    }
    closeSchedForm();
    loadSchedules();
  } catch (e) { toast(e.message, 'err'); }
}

async function toggleSched(t, on) {
  try {
    await api('/api/schedules/' + encodeURIComponent(t.id), {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ ...t, enabled: on }),
    });
    t.enabled = on;
    renderSchedules();
    toast(on ? '任务已启用' : '任务已停用', 'ok');
  } catch (e) { toast(e.message, 'err'); }
}

async function delSched(t) {
  if (!confirm(`删除定时任务「${t.name}」？`)) return;
  try {
    await api('/api/schedules/' + encodeURIComponent(t.id), { method: 'DELETE' });
    toast('已删除', 'ok');
    loadSchedules();
  } catch (e) { toast(e.message, 'err'); }
}

async function runSchedNow(t) {
  try {
    await api('/api/schedules/' + encodeURIComponent(t.id) + '/run', { method: 'POST' });
    toast(`任务「${t.name}」已触发，稍等片刻可刷新查看日志`, 'ok');
    setTimeout(loadSchedules, 2500);
  } catch (e) { toast(e.message, 'err'); }
}

// ── 初始化 ────────────────────────────────────────
(async function init() {
  try { state.colos = await api('/api/colos'); } catch (_) {}

  $('#coloSearch').addEventListener('input', e => renderColoList(e.target.value));
  $('#coloSearch').addEventListener('focus', e => renderColoList(e.target.value));
  document.addEventListener('click', e => {
    if (!e.target.closest('.colo-box')) $('#coloList').classList.remove('show');
  });

  document.querySelectorAll('#segIPv button').forEach(b => {
    b.onclick = () => {
      document.querySelectorAll('#segIPv button').forEach(x => x.classList.remove('on'));
      b.classList.add('on');
    };
  });

  $('#inThread').addEventListener('input', e => { $('#threadVal').textContent = e.target.value; });
  document.querySelectorAll('#segPool button').forEach(b => {
    b.onclick = () => {
      if (b.dataset.pool === 'custom') pickCustomPool();
      else setPool(+b.dataset.pool);
    };
  });
  document.querySelectorAll('#segPing button').forEach(b => {
    b.onclick = () => { if (!b.disabled) setPing(b.dataset.ping === 'http'); };
  });
  document.querySelectorAll('#segDL button').forEach(b => {
    b.onclick = () => setDL(b.dataset.dl === 'off');
  });
  $('#inPool').addEventListener('input', e => {
    const v = +e.target.value;
    setPool(v > 0 ? v : 0, { custom: true });
  });
  $('#inIPText').addEventListener('input', syncPoolNote);
  $('#btnStart').onclick = start;
  $('#btnStop').onclick = () => api('/api/cancel', { method: 'POST' }).catch(() => {});
  $('#filterText').addEventListener('input', renderTable);

  document.querySelectorAll('thead th[data-sort]').forEach(th => {
    th.onclick = () => {
      const k = th.dataset.sort;
      if (state.sortKey === k) {
        state.sortDir = state.sortDir === 'asc' ? 'desc' : 'asc';
      } else {
        state.sortKey = k;
        state.sortDir = (k === 'delay' || k === 'loss') ? 'asc' : 'desc';
      }
      document.querySelectorAll('thead th').forEach(x => x.removeAttribute('data-dir'));
      th.setAttribute('data-dir', state.sortDir);
      renderTable();
    };
  });

  $('#btnConfig').onclick = () => $('#mask').classList.remove('hidden');
  $('#btnCfgClose').onclick = () => $('#mask').classList.add('hidden');
  $('#btnCfgSave').onclick = saveConfig;
  $('#mask').onclick = e => { if (e.target === $('#mask')) $('#mask').classList.add('hidden'); };
  $('#btnProxy').onclick = openProxy;
  $('#btnProxyClose').onclick = () => $('#proxyMask').classList.add('hidden');
  $('#proxyMask').onclick = e => { if (e.target === $('#proxyMask')) $('#proxyMask').classList.add('hidden'); };
  $('#btnProxyRun').onclick = runProxy;
  $('#btnProxyPreview').onclick = previewProxy;
  $('#btnProxyAddUrl').onclick = () => { state.proxyUrls.push(''); renderProxyUrls(); };
  $('#proxyText').addEventListener('input', updateProxyCount);
  $('#proxyFile').addEventListener('change', e => {
    const f = e.target.files && e.target.files[0];
    e.target.value = '';
    if (!f) return;
    const reader = new FileReader();
    reader.onload = () => {
      $('#proxyText').value = String(reader.result);
      $('#proxyFileName').textContent = f.name;
      updateProxyCount();
    };
    reader.onerror = () => toast('读取文件失败', 'err');
    reader.readAsText(f);
  });
  $('#btnUploadAPI').onclick = uploadAPI;
  $('#btnUploadGH').onclick = uploadGitHub;
  $('#btnDownload').onclick = () => download('result');

  $('#inIPFile').addEventListener('change', e => {
    importIPFile(e.target.files && e.target.files[0]);
    e.target.value = '';
  });

  $('#btnCron').onclick = openSched;
  $('#schedMask').onclick = e => { if (e.target === $('#schedMask')) $('#schedMask').classList.add('hidden'); };
  $('#btnSchedNew').onclick = newSched;
  $('#btnSchedCancel').onclick = closeSchedForm;
  $('#btnSchedSave').onclick = saveSched;
  $('#schedMin').addEventListener('input', e => markSchedPreset(e.target.value));
  document.querySelectorAll('#schedPresets button').forEach(b => {
    b.onclick = () => {
      document.querySelectorAll('#schedPresets button').forEach(x => x.classList.remove('on'));
      b.classList.add('on');
      $('#schedMin').value = b.dataset.min;
    };
  });

  try { state.system = await api('/api/system') || {}; } catch (_) {}

  await loadConfig();
  try {
    const st = await api('/api/status');
    if (st.running) { setRunning(true); }
    if (st.count) { state.results = await api('/api/results'); renderTable(); }
  } catch (_) {}
  connectEvents();
})();
