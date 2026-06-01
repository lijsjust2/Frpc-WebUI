// === State ===
let authToken = '';
let servers = [];
let selectedServerId = null;
let editingServerId = null;
let editingProxyId = null;
let proxySortColumn = '';
let proxySortDirection = 'asc';

// === API Helper ===
async function api(method, path, body = null, skipAuthRedirect = false) {
    const opts = {
        method,
        headers: { 'Content-Type': 'application/json' },
    };
    if (authToken) {
        opts.headers['X-Auth-Token'] = authToken;
    }
    if (body) {
        opts.body = JSON.stringify(body);
    }
    const res = await fetch(`/api${path}`, opts);

    if (res.status === 401) {
        authToken = '';
        localStorage.removeItem('authToken');
        selectedServerId = null;
        showPage('login-page');
        if (!skipAuthRedirect) {
            toast('登录已过期，请重新登录', 'error');
        }
        throw new Error('unauthorized');
    }

    let data;
    try {
        data = await res.json();
    } catch (e) {
        if (!res.ok) {
            throw new Error(`请求失败 (HTTP ${res.status})`);
        }
        throw new Error('服务器返回了无效的响应');
    }
    if (!res.ok) {
        throw new Error(data.error || `HTTP ${res.status}`);
    }
    return data;
}

// === Toast ===
function toast(msg, type = 'info') {
    const container = document.getElementById('toast-container');
    const el = document.createElement('div');
    el.className = `toast toast-${type}`;
    el.textContent = msg;
    container.appendChild(el);
    setTimeout(() => el.remove(), 3000);
}

// === Loading State ===
function setButtonLoading(buttonId, loading) {
    const btn = document.getElementById(buttonId);
    if (!btn) return;
    
    if (loading) {
        btn.disabled = true;
        btn.dataset.originalHtml = btn.innerHTML;
        // Keep only the text span content, replace with loading text
        const textSpan = btn.querySelector('span') || btn;
        if (textSpan === btn) {
            btn.dataset.originalHtml = btn.innerHTML;
            btn.textContent = '处理中...';
        } else {
            btn.dataset.originalText = textSpan.textContent;
            textSpan.textContent = '处理中...';
        }
    } else {
        btn.disabled = false;
        if (btn.dataset.originalHtml) {
            btn.innerHTML = btn.dataset.originalHtml;
            delete btn.dataset.originalHtml;
        }
        if (btn.dataset.originalText) {
            const textSpan = btn.querySelector('span');
            if (textSpan) textSpan.textContent = btn.dataset.originalText;
            delete btn.dataset.originalText;
        }
    }
}

// === Page Navigation ===
function showPage(id) {
    document.querySelectorAll('.page').forEach(p => p.classList.add('hidden'));
    document.getElementById(id).classList.remove('hidden');
}

// === Modal ===
function openModal(id) {
    document.getElementById(id).classList.remove('hidden');
}

function closeModal(id) {
    document.getElementById(id).classList.add('hidden');
}

// === Init ===
async function init() {
    try {
        const status = await api('GET', '/auth/status');
        if (status.needSetup) {
            showPage('setup-page');
            return;
        }

        // Try restoring saved session
        const savedToken = localStorage.getItem('authToken');
        if (savedToken) {
            authToken = savedToken;
            try {
                await api('GET', '/servers', null, true);
                enterApp();
                return;
            } catch (e) {
                // Token expired or invalid, silently clear
                authToken = '';
                localStorage.removeItem('authToken');
            }
        }

        showPage('login-page');
    } catch (e) {
        toast('无法连接到服务器', 'error');
    }
}

// === Auth: Setup ===
document.getElementById('setup-form').addEventListener('submit', async (e) => {
    e.preventDefault();
    const pw = document.getElementById('setup-password').value;
    const confirm = document.getElementById('setup-confirm').value;
    const errEl = document.getElementById('setup-error');

    if (pw !== confirm) {
        errEl.textContent = '两次输入的密码不一致';
        errEl.classList.remove('hidden');
        return;
    }
    if (pw.length < 6) {
        errEl.textContent = '密码至少需要6位';
        errEl.classList.remove('hidden');
        return;
    }

    try {
        const res = await api('POST', '/auth/setup', { password: pw });
        authToken = res.token;
        localStorage.setItem('authToken', authToken);
        toast('密码设置成功', 'success');
        enterApp();
    } catch (e) {
        errEl.textContent = e.message;
        errEl.classList.remove('hidden');
    }
});

// === Auth: Login ===
document.getElementById('login-form').addEventListener('submit', async (e) => {
    e.preventDefault();
    const pw = document.getElementById('login-password').value;
    const errEl = document.getElementById('login-error');

    try {
        const res = await api('POST', '/auth/login', { password: pw });
        authToken = res.token;
        localStorage.setItem('authToken', authToken);
        enterApp();
    } catch (e) {
        errEl.textContent = '密码错误';
        errEl.classList.remove('hidden');
    }
});

// === Enter App ===
async function enterApp() {
    showPage('main-page');
    await loadServers();
}

// === View Config (top menu) ===
document.getElementById('btn-view-config').addEventListener('click', async () => {
    if (!selectedServerId) return;
    try {
        const data = await api('GET', `/servers/${selectedServerId}/config`);
        document.getElementById('config-viewer').textContent = data.config || '暂无配置';
        openModal('modal-config');
    } catch (e) {
        toast('获取配置失败: ' + e.message, 'error');
    }
});

// === View Logs (top menu) ===
document.getElementById('btn-view-logs').addEventListener('click', () => {
    if (!selectedServerId) return;
    openModal('modal-logs');
    refreshLogs();
    startLogsRefresh();
});

// === Change Password ===
document.getElementById('btn-change-password').addEventListener('click', () => {
    document.getElementById('password-form').reset();
    document.getElementById('password-error').classList.add('hidden');
    openModal('modal-password');
});

document.getElementById('password-form').addEventListener('submit', async (e) => {
    e.preventDefault();
    const oldPassword = document.getElementById('old-password').value;
    const newPassword = document.getElementById('new-password').value;
    const confirmPassword = document.getElementById('confirm-password').value;
    const errorEl = document.getElementById('password-error');

    if (newPassword !== confirmPassword) {
        errorEl.textContent = '两次输入的新密码不一致';
        errorEl.classList.remove('hidden');
        return;
    }

    try {
        await api('POST', '/auth/change-password', { oldPassword, newPassword });
        closeModal('modal-password');
        toast('密码修改成功', 'success');
    } catch (e) {
        errorEl.textContent = e.message;
        errorEl.classList.remove('hidden');
    }
});

// === Logout ===
document.getElementById('btn-logout').addEventListener('click', () => {
    authToken = '';
    localStorage.removeItem('authToken');
    selectedServerId = null;
    showPage('login-page');
    toast('\u5df2\u9000\u51fa\u767b\u5f55', 'info');
});

// === Load Servers ===
async function loadServers() {
    try {
        servers = await api('GET', '/servers');
        renderServerList();
    } catch (e) {
        toast('加载服务器列表失败: ' + e.message, 'error');
    }
}

function renderServerList() {
    const list = document.getElementById('server-list');

    if (servers.length === 0) {
        list.innerHTML = '<div class="empty-state"><p>暂无服务器</p><p class="text-muted">点击 + 添加第一个</p></div>';
        return;
    }

    list.innerHTML = servers.map(s => `
        <div class="server-item ${s.id === selectedServerId ? 'active' : ''}" data-id="${s.id}" onclick="selectServer('${s.id}')">
            <span class="status-indicator ${s.running ? 'running' : 'stopped'}"></span>
            <div>
                <div class="server-name">${escapeHtml(s.name)}</div>
                <div class="server-addr">${escapeHtml(s.serverAddr)}:${s.serverPort}</div>
            </div>
        </div>
    `).join('');
}

// === Select Server ===
function selectServer(id) {
    selectedServerId = id;
    renderServerList();
    renderServerDetail();
}

function renderServerDetail() {
    const server = servers.find(s => s.id === selectedServerId);
    if (!server) {
        document.getElementById('no-selection').classList.remove('hidden');
        document.getElementById('server-detail').classList.add('hidden');
        stopLogsRefresh();
        return;
    }

    document.getElementById('no-selection').classList.add('hidden');
    document.getElementById('server-detail').classList.remove('hidden');

    // Header
    document.getElementById('server-detail-name').textContent = server.name;

    // Running status
    const dot = document.getElementById('server-running-dot');
    const toggleText = document.getElementById('btn-toggle-text');
    const toggleBtn = document.getElementById('btn-toggle-server');

    if (dot && toggleText && toggleBtn) {
        if (server.running) {
            dot.className = 'status-indicator running';
            toggleText.textContent = '停止';
            toggleBtn.classList.add('btn-danger');
            toggleBtn.classList.remove('btn-primary');
        } else {
            dot.className = 'status-indicator stopped';
            toggleText.textContent = '启动';
            toggleBtn.classList.remove('btn-danger');
            toggleBtn.classList.add('btn-primary');
        }
    }

    // Config grid
    const grid = document.getElementById('server-config-grid');
    grid.innerHTML = `
        <div class="config-item">
            <div class="label">服务器地址</div>
            <div class="value">${escapeHtml(server.serverAddr)}</div>
        </div>
        <div class="config-item">
            <div class="label">端口</div>
            <div class="value">${server.serverPort}</div>
        </div>
        <div class="config-item">
            <div class="label">Token</div>
            <div class="value">${server.authToken ? '••••••••' : '未设置'}</div>
        </div>
        <div class="config-item">
            <div class="label">TLS</div>
            <div class="value">${server.tlsEnable ? '已启用' : '未启用'}</div>
        </div>
        ${server.user ? `<div class="config-item"><div class="label">用户名</div><div class="value">${escapeHtml(server.user)}</div></div>` : ''}
    `;

    // Proxies
    renderProxyTable(server.proxies || []);
}

// === Proxy Table ===
function sortProxies(proxies) {
    if (!proxySortColumn) return proxies;
    const sorted = [...proxies];
    sorted.sort((a, b) => {
        let valA = '', valB = '';
        switch (proxySortColumn) {
            case 'name':
                valA = (a.name || '').toLowerCase();
                valB = (b.name || '').toLowerCase();
                break;
            case 'type':
                valA = (a.type || '').toLowerCase();
                valB = (b.type || '').toLowerCase();
                break;
            case 'localAddr':
                valA = `${a.localIP || '127.0.0.1'}:${a.localPort}`;
                valB = `${b.localIP || '127.0.0.1'}:${b.localPort}`;
                break;
            case 'remote':
                if (a.type === 'tcp' || a.type === 'udp') {
                    valA = String(a.remotePort || '');
                } else {
                    valA = (a.customDomains || []).join(',') + (a.subdomain || '');
                }
                if (b.type === 'tcp' || b.type === 'udp') {
                    valB = String(b.remotePort || '');
                } else {
                    valB = (b.customDomains || []).join(',') + (b.subdomain || '');
                }
                break;
            case 'remark':
                valA = (a.remark || '').toLowerCase();
                valB = (b.remark || '').toLowerCase();
                break;
        }
        if (valA < valB) return proxySortDirection === 'asc' ? -1 : 1;
        if (valA > valB) return proxySortDirection === 'asc' ? 1 : -1;
        return 0;
    });
    return sorted;
}

function updateSortIndicators() {
    document.querySelectorAll('.proxy-table th.sortable').forEach(th => {
        th.classList.remove('sort-asc', 'sort-desc');
        if (th.dataset.sort === proxySortColumn) {
            th.classList.add(proxySortDirection === 'asc' ? 'sort-asc' : 'sort-desc');
        }
    });
}

// Sort click handlers
document.querySelectorAll('.proxy-table th.sortable').forEach(th => {
    th.addEventListener('click', () => {
        const col = th.dataset.sort;
        if (proxySortColumn === col) {
            proxySortDirection = proxySortDirection === 'asc' ? 'desc' : 'asc';
        } else {
            proxySortColumn = col;
            proxySortDirection = 'asc';
        }
        updateSortIndicators();
        const server = servers.find(s => s.id === selectedServerId);
        if (server) renderProxyTable(server.proxies || []);
    });
});

function renderProxyTable(proxies) {
    const tbody = document.getElementById('proxy-table-body');
    const emptyEl = document.getElementById('proxy-empty');
    const tableEl = document.getElementById('proxy-table');

    if (!proxies || proxies.length === 0) {
        tableEl.classList.add('hidden');
        emptyEl.classList.remove('hidden');
        return;
    }

    tableEl.classList.remove('hidden');
    emptyEl.classList.add('hidden');

    const sorted = sortProxies(proxies);

    tbody.innerHTML = sorted.map(p => {
        let remote = '';
        if (p.type === 'tcp' || p.type === 'udp') {
            remote = p.remotePort ? `:${p.remotePort}` : '-';
        } else {
            const parts = [];
            if (p.customDomains && p.customDomains.length) parts.push(p.customDomains.join(', '));
            if (p.subdomain) parts.push(`sub: ${p.subdomain}`);
            remote = parts.join('; ') || '-';
        }

        const isEnabled = p.enabled === undefined || p.enabled === null || p.enabled === true;
        const statusHtml = isEnabled
            ? '<span class="status-enabled">启用</span>'
            : '<span class="status-disabled">禁用</span>';
        const toggleBtnHtml = isEnabled
            ? `<button class="btn btn-sm btn-ghost" onclick="toggleProxy('${p.id}')" title="禁用">禁用</button>`
            : `<button class="btn btn-sm btn-ghost" onclick="toggleProxy('${p.id}')" title="启用" style="color:var(--success)">启用</button>`;

        return `
            <tr>
                <td>${escapeHtml(p.name)}</td>
                <td><span class="type-badge type-${p.type}">${p.type}</span></td>
                <td>${escapeHtml(p.localIP || '127.0.0.1')}:${p.localPort}</td>
                <td>${escapeHtml(remote)}</td>
                <td>${escapeHtml(p.remark || '')}</td>
                <td>${statusHtml}</td>
                <td>
                    ${toggleBtnHtml}
                    <button class="btn btn-sm btn-ghost" onclick="editProxy('${p.id}')" title="编辑">
                        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M11 4H4a2 2 0 00-2 2v14a2 2 0 002 2h14a2 2 0 002-2v-7"/><path d="M18.5 2.5a2.12 2.12 0 013 3L12 15l-4 1 1-4 9.5-9.5z"/></svg>
                    </button>
                    <button class="btn btn-sm btn-ghost btn-danger" onclick="deleteProxy('${p.id}')" title="删除">
                        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="3 6 5 6 21 6"/><path d="M19 6v14a2 2 0 01-2 2H7a2 2 0 01-2-2V6m3 0V4a2 2 0 012-2h4a2 2 0 012 2v2"/></svg>
                    </button>
                </td>
            </tr>
        `;
    }).join('');
}

async function toggleProxy(proxyId) {
    try {
        await api('POST', `/servers/${selectedServerId}/proxies/${proxyId}/toggle`);
        await loadServers();
        renderServerDetail();
        tryAutoRestart();
    } catch (e) {
        toast(e.message, 'error');
    }
}

// === Server CRUD ===
document.getElementById('btn-add-server').addEventListener('click', () => {
    editingServerId = null;
    document.getElementById('modal-server-title').textContent = '添加服务器';
    document.getElementById('server-form').reset();
    openModal('modal-server');
});

document.getElementById('btn-edit-server').addEventListener('click', () => {
    const server = servers.find(s => s.id === selectedServerId);
    if (!server) return;

    editingServerId = server.id;
    document.getElementById('modal-server-title').textContent = '编辑服务器';
    document.getElementById('sf-name').value = server.name;
    document.getElementById('sf-addr').value = server.serverAddr;
    document.getElementById('sf-port').value = server.serverPort;
    document.getElementById('sf-token').value = server.authToken || '';
    document.getElementById('sf-user').value = server.user || '';
    document.getElementById('sf-tls').checked = server.tlsEnable || false;
    openModal('modal-server');
});

document.getElementById('server-form').addEventListener('submit', async (e) => {
    e.preventDefault();
    const data = {
        name: document.getElementById('sf-name').value,
        serverAddr: document.getElementById('sf-addr').value,
        serverPort: parseInt(document.getElementById('sf-port').value),
        authToken: document.getElementById('sf-token').value,
        user: document.getElementById('sf-user').value,
        tlsEnable: document.getElementById('sf-tls').checked,
    };

    try {
        if (editingServerId) {
            await api('PUT', `/servers/${editingServerId}`, data);
            toast('服务器已更新，正在重启...', 'success');
        } else {
            await api('POST', '/servers', data);
            toast('服务器已添加', 'success');
        }
        closeModal('modal-server');
        await loadServers();
        if (editingServerId) {
            selectServer(editingServerId);
            tryAutoRestart();
        }
    } catch (e) {
        toast(e.message, 'error');
    }
});

document.getElementById('btn-delete-server').addEventListener('click', async () => {
    if (!selectedServerId) return;
    const server = servers.find(s => s.id === selectedServerId);
    if (!confirm(`确定删除服务器 "${server.name}" 吗？`)) return;

    try {
        await api('DELETE', `/servers/${selectedServerId}`);
        toast('服务器已删除', 'success');
        selectedServerId = null;
        await loadServers();
        document.getElementById('no-selection').classList.remove('hidden');
        document.getElementById('server-detail').classList.add('hidden');
    } catch (e) {
        toast(e.message, 'error');
    }
});

// === Start/Stop Server ===
document.getElementById('btn-toggle-server').addEventListener('click', async () => {
    const server = servers.find(s => s.id === selectedServerId);
    if (!server) return;

    const btnId = 'btn-toggle-server';
    setButtonLoading(btnId, true);

    try {
        if (server.running) {
            await api('POST', `/servers/${selectedServerId}/stop`);
            toast('已停止', 'success');
        } else {
            await api('POST', `/servers/${selectedServerId}/start`);
            toast('已启动', 'success');
        }
        await loadServers();
        renderServerDetail();
    } catch (e) {
        toast(e.message, 'error');
    } finally {
        setButtonLoading(btnId, false);
    }
});

document.getElementById('btn-restart-server').addEventListener('click', async () => {
    const server = servers.find(s => s.id === selectedServerId);
    if (!server || !server.running) {
        toast('服务未运行，无法重启', 'error');
        return;
    }

    const btnId = 'btn-restart-server';
    setButtonLoading(btnId, true);

    try {
        toast('正在重启...', 'info');
        await api('POST', `/servers/${selectedServerId}/restart`);
        toast('已重启', 'success');
        await loadServers();
        renderServerDetail();
    } catch (e) {
        toast(e.message, 'error');
    } finally {
        setButtonLoading(btnId, false);
    }
});

async function tryAutoRestart() {
    if (!selectedServerId) return;
    const server = servers.find(s => s.id === selectedServerId);
    if (!server) return;

    try {
        if (server.running) {
            await api('POST', `/servers/${selectedServerId}/restart`);
            toast('规则已更新，自动重启成功', 'success');
        } else {
            await api('POST', `/servers/${selectedServerId}/start`);
            toast('规则已更新，自动启动成功', 'success');
        }
        await loadServers();
        renderServerDetail();
    } catch (e) {
        toast('操作失败: ' + e.message, 'error');
    }
}

// === Proxy CRUD ===
document.getElementById('btn-add-proxy').addEventListener('click', () => {
    editingProxyId = null;
    document.getElementById('modal-proxy-title').textContent = '添加规则';
    document.getElementById('proxy-form').reset();
    document.getElementById('pf-local-ip').value = '127.0.0.1';
    document.getElementById('pf-type').value = 'tcp';
    document.getElementById('pf-encryption').checked = true;
    document.getElementById('pf-compression').checked = true;
    document.getElementById('pf-bandwidth-mode').value = 'client';
    toggleProxyFields();
    openModal('modal-proxy');
});

document.getElementById('pf-type').addEventListener('change', toggleProxyFields);

function toggleProxyFields() {
    const type = document.getElementById('pf-type').value;
    const tcpFields = document.getElementById('proxy-tcp-fields');
    const httpFields = document.getElementById('proxy-http-fields');

    // Reset all
    tcpFields.classList.add('hidden');
    httpFields.classList.add('hidden');

    if (type === 'http' || type === 'https') {
        httpFields.classList.remove('hidden');
    } else {
        tcpFields.classList.remove('hidden');
    }
}

function editProxy(proxyId) {
    const server = servers.find(s => s.id === selectedServerId);
    if (!server) return;
    const proxy = server.proxies.find(p => p.id === proxyId);
    if (!proxy) return;

    editingProxyId = proxyId;
    document.getElementById('modal-proxy-title').textContent = '编辑规则';
    document.getElementById('pf-name').value = proxy.name;
    document.getElementById('pf-type').value = proxy.type;
    document.getElementById('pf-local-ip').value = proxy.localIP || '127.0.0.1';
    document.getElementById('pf-local-port').value = proxy.localPort;
    document.getElementById('pf-remote-port').value = proxy.remotePort || '';
    document.getElementById('pf-domains').value = (proxy.customDomains || []).join(', ');
    document.getElementById('pf-subdomain').value = proxy.subdomain || '';
    document.getElementById('pf-http-user').value = proxy.httpUser || '';
    document.getElementById('pf-http-password').value = proxy.httpPassword || '';
    document.getElementById('pf-encryption').checked = proxy.useEncryption || false;
    document.getElementById('pf-compression').checked = proxy.useCompression || false;
    document.getElementById('pf-bandwidth').value = proxy.bandwidthLimit || '';
    document.getElementById('pf-bandwidth-mode').value = proxy.bandwidthLimitMode || 'client';
    document.getElementById('pf-remark').value = proxy.remark || '';
    const isEnabled = proxy.enabled === undefined || proxy.enabled === null || proxy.enabled === true;
    document.getElementById('pf-enabled').checked = isEnabled;
    toggleProxyFields();
    openModal('modal-proxy');
}

async function deleteProxy(proxyId) {
    if (!confirm('确定删除此规则吗？')) return;
    try {
        await api('DELETE', `/servers/${selectedServerId}/proxies/${proxyId}`);
        toast('规则已删除，正在重启...', 'success');
        await loadServers();
        renderServerDetail();
        tryAutoRestart();
    } catch (e) {
        toast(e.message, 'error');
    }
}

document.getElementById('proxy-form').addEventListener('submit', async (e) => {
    e.preventDefault();

    if (!selectedServerId) {
        toast('请先选择一个服务器', 'error');
        return;
    }

    const type = document.getElementById('pf-type').value;
    const name = document.getElementById('pf-name').value.trim();
    const localPort = document.getElementById('pf-local-port').value;

    if (!name) {
        toast('请输入规则名称', 'error');
        return;
    }

    if (!localPort || isNaN(parseInt(localPort))) {
        toast('请输入有效的本地端口', 'error');
        return;
    }

    const domainsRaw = document.getElementById('pf-domains').value.trim();

    const data = {
        name: name,
        type: type,
        localIP: document.getElementById('pf-local-ip').value || '127.0.0.1',
        localPort: parseInt(localPort),
        useEncryption: document.getElementById('pf-encryption').checked,
        useCompression: document.getElementById('pf-compression').checked,
        bandwidthLimit: document.getElementById('pf-bandwidth').value.trim(),
        bandwidthLimitMode: document.getElementById('pf-bandwidth-mode').value,
        remark: document.getElementById('pf-remark').value.trim(),
        enabled: document.getElementById('pf-enabled').checked,
    };

    if (type === 'tcp' || type === 'udp') {
        const rp = document.getElementById('pf-remote-port').value;
        if (rp) data.remotePort = parseInt(rp);
    } else if (type === 'http' || type === 'https') {
        if (domainsRaw) data.customDomains = domainsRaw.split(',').map(d => d.trim()).filter(Boolean);
        const sub = document.getElementById('pf-subdomain').value.trim();
        if (sub) data.subdomain = sub;
        const httpUser = document.getElementById('pf-http-user').value.trim();
        if (httpUser) data.httpUser = httpUser;
        const httpPassword = document.getElementById('pf-http-password').value.trim();
        if (httpPassword) data.httpPassword = httpPassword;
    }

    const submitBtn = e.target.querySelector('button[type="submit"]');
    const btnId = submitBtn ? submitBtn.id : 'btn-save-proxy';
    setButtonLoading(btnId, true);

    try {
        if (editingProxyId) {
            await api('PUT', `/servers/${selectedServerId}/proxies/${editingProxyId}`, data);
            toast('规则已更新，正在重启...', 'success');
        } else {
            await api('POST', `/servers/${selectedServerId}/proxies`, data);
            toast('规则已添加，正在重启...', 'success');
        }
        closeModal('modal-proxy');
        await loadServers();
        renderServerDetail();
        tryAutoRestart();
    } catch (e) {
        toast('保存失败: ' + e.message, 'error');
    } finally {
        setButtonLoading(btnId, false);
    }
});

// === Logs ===
let logsRefreshInterval = null;

document.getElementById('btn-refresh-logs').addEventListener('click', refreshLogs);

function startLogsRefresh() {
    // Stop existing interval if any
    stopLogsRefresh();
    // Refresh every 3 seconds
    logsRefreshInterval = setInterval(refreshLogs, 3000);
}

function stopLogsRefresh() {
    if (logsRefreshInterval) {
        clearInterval(logsRefreshInterval);
        logsRefreshInterval = null;
    }
}

// Stop logs refresh when logs modal is closed
const logsModalObserver = new MutationObserver(() => {
    const modal = document.getElementById('modal-logs');
    if (modal && modal.classList.contains('hidden')) {
        stopLogsRefresh();
    }
});
document.addEventListener('DOMContentLoaded', () => {
    const modal = document.getElementById('modal-logs');
    if (modal) {
        logsModalObserver.observe(modal, { attributes: true, attributeFilter: ['class'] });
    }
});

async function refreshLogs() {
    if (!selectedServerId) return;
    try {
        const data = await api('GET', `/servers/${selectedServerId}/logs`);
        const viewer = document.getElementById('log-viewer');
        if (!viewer) return;
        // Preserve scroll position if user is scrolling
        const wasAtBottom = viewer.scrollHeight - viewer.scrollTop - viewer.clientHeight < 50;
        viewer.textContent = data.logs || '暂无日志';
        if (wasAtBottom) {
            viewer.scrollTop = viewer.scrollHeight;
        }
    } catch (e) {
        // ignore
    }
}

document.getElementById('btn-clear-logs').addEventListener('click', () => {
    showConfirm('清空日志', '确定要清空所有日志吗？此操作不可恢复。', async () => {
        try {
            await api('DELETE', `/servers/${selectedServerId}/logs`);
            document.getElementById('log-viewer').textContent = '暂无日志';
            toast('日志已清空', 'success');
        } catch (e) {
            toast('清空失败: ' + e.message, 'error');
        }
    });
});

let confirmCallback = null;

function showConfirm(title, message, callback) {
    document.getElementById('confirm-title').textContent = title;
    document.getElementById('confirm-message').textContent = message;
    confirmCallback = callback;
    openModal('modal-confirm');
}

document.getElementById('btn-confirm-ok').addEventListener('click', () => {
    closeModal('modal-confirm');
    if (confirmCallback) {
        confirmCallback();
        confirmCallback = null;
    }
});

// === FRPC Version ===

// === Utilities ===
function escapeHtml(str) {
    if (!str) return '';
    const div = document.createElement('div');
    div.textContent = str;
    return div.innerHTML;
}

// === Auto refresh ===
setInterval(async () => {
    if (authToken && document.getElementById('main-page').classList.contains('hidden') === false) {
        try {
            await loadServers();
            if (selectedServerId) {
                renderServerDetail();
            }
        } catch (e) {
            // Token expired - api() already handles redirect, stop retrying silently
        }
    }
}, 10000);

// === Theme Management ===
function getAutoTheme() {
    // 1. Check system preference
    if (window.matchMedia && window.matchMedia('(prefers-color-scheme: light)').matches) {
        return 'light';
    }
    if (window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)').matches) {
        return 'dark';
    }
    // 2. Fallback: time-based (6:00-18:00 = light)
    const hour = new Date().getHours();
    return (hour >= 6 && hour < 18) ? 'light' : 'dark';
}

function applyTheme(theme) {
    document.documentElement.setAttribute('data-theme', theme);
    updateThemeIcon(theme);
}

function updateThemeIcon(theme) {
    const icon = document.getElementById('theme-icon');
    if (!icon) return;
    if (theme === 'light') {
        // Sun icon
        icon.innerHTML = '<circle cx="12" cy="12" r="5"/><line x1="12" y1="1" x2="12" y2="3"/><line x1="12" y1="21" x2="12" y2="23"/><line x1="4.22" y1="4.22" x2="5.64" y2="5.64"/><line x1="18.36" y1="18.36" x2="19.78" y2="19.78"/><line x1="1" y1="12" x2="3" y2="12"/><line x1="21" y1="12" x2="23" y2="12"/><line x1="4.22" y1="19.78" x2="5.64" y2="18.36"/><line x1="18.36" y1="5.64" x2="19.78" y2="4.22"/>';
    } else {
        // Moon icon
        icon.innerHTML = '<path d="M21 12.79A9 9 0 1111.21 3 7 7 0 0021 12.79z"/>';
    }
}

// Initialize theme immediately
(function () {
    const saved = localStorage.getItem('theme');
    const theme = saved || getAutoTheme();
    applyTheme(theme);
})();

// Theme toggle button
document.getElementById('btn-theme-toggle').addEventListener('click', () => {
    const current = document.documentElement.getAttribute('data-theme') || 'dark';
    const next = current === 'dark' ? 'light' : 'dark';
    localStorage.setItem('theme', next);
    applyTheme(next);
});

// Listen for system theme changes
window.matchMedia('(prefers-color-scheme: dark)').addEventListener('change', (e) => {
    if (!localStorage.getItem('theme')) {
        applyTheme(e.matches ? 'dark' : 'light');
    }
});

// === Export Config ===
document.getElementById('btn-export-config').addEventListener('click', async () => {
    try {
        const res = await fetch('/api/export', {
            headers: { 'X-Auth-Token': authToken }
        });

        if (res.status === 401) {
            authToken = '';
            localStorage.removeItem('authToken');
            selectedServerId = null;
            showPage('login-page');
            toast('登录已过期，请重新登录', 'error');
            return;
        }

        if (!res.ok) {
            try { const d = await res.json(); toast('导出失败: ' + d.error, 'error'); }
            catch { toast('导出失败', 'error'); }
            return;
        }

        const blob = await res.blob();
        const url = URL.createObjectURL(blob);
        const a = document.createElement('a');
        a.href = url;
        a.download = `frpc-webui-backup-${new Date().toISOString().slice(0,10)}.json`;
        a.click();
        URL.revokeObjectURL(url);
        toast('配置已导出', 'success');
    } catch (e) {
        toast('导出失败: ' + e.message, 'error');
    }
});

// === Import Config ===
document.getElementById('btn-import-config').addEventListener('click', () => {
    document.getElementById('import-file-input').click();
});

document.getElementById('import-file-input').addEventListener('change', async (e) => {
    const file = e.target.files[0];
    if (!file) return;

    if (!confirm('导入配置将覆盖当前所有服务器配置，确定继续吗？')) {
        e.target.value = '';
        return;
    }

    try {
        const text = await file.text();
        const data = JSON.parse(text);
        if (!Array.isArray(data)) {
            toast('无效的配置文件格式', 'error');
            return;
        }
        await api('POST', '/import', data);
        toast('配置已导入，正在刷新...', 'success');
        selectedServerId = null;
        await loadServers();
        document.getElementById('no-selection').classList.remove('hidden');
        document.getElementById('server-detail').classList.add('hidden');
    } catch (err) {
        toast('导入失败: ' + (err.message || '文件格式错误'), 'error');
    } finally {
        e.target.value = '';
    }
});

// === Start ===
init();

