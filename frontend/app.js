let currentOutbound = '';
let balancerTag = 'balancer';
let didInitialAutoSelect = false;
let fixedNodeOrder = [];
let pageReady = false;

const $ = (id) => document.getElementById(id);

const els = {
    uplink: $('stats-uplink'),
    downlink: $('stats-downlink'),
    uptime: $('stats-uptime'),
    sysMem: $('stats-sys-mem'),
    goroutines: $('stats-goroutines'),
    currentTag: $('current-tag'),
    balancerTag: $('balancer-tag'),
    outboundsList: $('outbounds-list'),
    refreshBtn: $('refresh-btn'),
    resetBtn: $('reset-btn'),
};

let notificationEl = null;
let notificationTimer = null;

function showNotification(message, type) {
    if (!notificationEl) {
        notificationEl = document.createElement('div');
        notificationEl.className = 'fixed top-4 right-4 text-white px-6 py-3 rounded-lg shadow-2xl z-50 animate-bounce';
        notificationEl.style.display = 'none';
        document.body.appendChild(notificationEl);
    }
    if (notificationTimer) clearTimeout(notificationTimer);
    notificationEl.className = 'fixed top-4 right-4 text-white px-6 py-3 rounded-lg shadow-2xl z-50 animate-bounce ' +
        (type === 'success' ? 'bg-green-500' : 'bg-red-500');
    notificationEl.innerHTML = message;
    notificationEl.style.display = '';
    notificationTimer = setTimeout(() => { notificationEl.style.display = 'none'; }, 3000);
}

window.addEventListener('error', (e) => {
    console.error('Global error:', e.error);
    showNotification('发生错误: ' + e.message, 'error');
});

window.addEventListener('load', () => {
    loadConfig();
    connectStatsSSE();

    els.resetBtn.addEventListener('click', () => {
        applyOutboundChange('', '自动均衡', { reload: true });
    });

    let refreshing = false;
    els.refreshBtn.addEventListener('click', async () => {
        if (refreshing) return;
        refreshing = true;
        try {
            didInitialAutoSelect = true;
            await initializePage();
        } finally {
            setTimeout(() => refreshing = false, 1000);
        }
    });
});

function connectStatsSSE() {
    console.log('正在连接到 /api/stats-sse...');
    const evtSource = new EventSource('/api/stats-sse');

    evtSource.addEventListener('update', (event) => {
        try {
            const data = JSON.parse(event.data);
            updateStatsUI(data);
            if (pageReady && data.outbounds && Array.isArray(data.outbounds)) {
                updateAllCardStatuses(data.outbounds);
            }
        } catch (error) {
            console.error('解析 SSE 数据失败:', error);
        }
    });

    evtSource.onopen = () => {
        console.log('SSE 连接已建立');
    };

    evtSource.onerror = () => {
        console.warn('SSE 连接错误');
    };
}

function updateStatsUI(data) {
    if (!data) return;
    els.uplink.textContent = formatBytes(data.uplink);
    els.downlink.textContent = formatBytes(data.downlink);
    els.uptime.textContent = formatDuration(data.uptime);
    els.sysMem.textContent = formatBytes(data.sys_mem);
    els.goroutines.textContent = data.goroutines;
}

function updateAllCardStatuses(outbounds) {
    for (const s of outbounds) {
        if (s && s.tag) {
            updateCardStatus(s.tag, { alive: s.alive, delay: s.delay });
        }
    }
}

async function loadConfig() {
    try {
        const response = await fetch('/api/config');
        if (response.ok) {
            const config = await response.json();
            balancerTag = config.balancer_tag;
            els.balancerTag.textContent = balancerTag;
        }
    } catch (error) {
        console.error('Error loading config:', error);
    }
    await initializePage();
}

async function initializePage() {
    pageReady = false;
    const currentStatus = await loadCurrentOutbound();
    const newNodeData = await fetchOutboundList();

    let orderedNodeData;
    if (fixedNodeOrder.length === 0) {
        console.log('First load. Sorting nodes alphabetically.');
        newNodeData.sort((a, b) => a.tag.localeCompare(b.tag));
        fixedNodeOrder = newNodeData.map(node => node.tag);
        orderedNodeData = newNodeData;
    } else {
        console.log('Refresh load. Using fixed order:', fixedNodeOrder);
        const nodeMap = new Map(newNodeData.map(node => [node.tag, node]));
        orderedNodeData = [];
        for (const tag of fixedNodeOrder) {
            const node = nodeMap.get(tag);
            if (node) {
                orderedNodeData.push(node);
                nodeMap.delete(tag);
            }
        }
        const newNodes = Array.from(nodeMap.values());
        if (newNodes.length > 0) {
            newNodes.sort((a, b) => a.tag.localeCompare(b.tag));
            orderedNodeData.push(...newNodes);
            fixedNodeOrder.push(...newNodes.map(n => n.tag));
        }
    }

    renderOutboundCards(orderedNodeData);

    const allStatuses = await progressiveLoadStatuses(orderedNodeData);

    const bestNode = findBestNode(allStatuses);
    if (bestNode && currentStatus.auto && !didInitialAutoSelect) {
        console.log('自动切换到最佳节点: ' + bestNode.tag + ' (延迟: ' + bestNode.status.delay + 'ms)');
        didInitialAutoSelect = true;
        await applyOutboundChange(bestNode.tag, bestNode.tag, { reload: false });
        await loadCurrentOutbound();
        renderOutboundCards(orderedNodeData);
        await progressiveLoadStatuses(orderedNodeData);
    }

    pageReady = true;
}

async function loadCurrentOutbound() {
    try {
        const response = await fetch('/api/current-outbound');
        if (!response.ok) throw new Error('Failed to fetch current outbound');
        const data = await response.json();
        if (data.auto || !data.current) {
            currentOutbound = '';
            els.currentTag.innerHTML = '<span class="text-orange-300">&#128260; 自动均衡模式</span>';
        } else {
            currentOutbound = data.current;
            els.currentTag.innerHTML = '<span class="text-green-300">&#10003; ' + data.current + '</span>';
        }
        return data;
    } catch (error) {
        console.error('Error loading current outbound:', error);
        els.currentTag.innerHTML = '<span class="text-red-300">&#10007; 获取失败</span>';
        return { auto: false, current: '' };
    }
}

async function fetchOutboundList() {
    try {
        const response = await fetch('/api/outbounds');
        if (!response.ok) throw new Error('Failed to fetch outbounds');
        const outbounds = await response.json();
        if (outbounds.length === 0) return [];
        return outbounds;
    } catch (error) {
        console.error('Error loading outbounds:', error);
        els.outboundsList.innerHTML = '<p class="text-red-400 text-center py-4 col-span-full">&#10060; 加载出站列表失败</p>';
        return [];
    }
}

function renderOutboundCards(allNodeData) {
    els.outboundsList.innerHTML = '';
    if (allNodeData.length === 0) {
        els.outboundsList.innerHTML = '<p class="text-white/70 text-center py-4 col-span-full">未找到可用出站</p>';
        return;
    }
    for (const node of allNodeData) {
        if (node) {
            els.outboundsList.appendChild(createOutboundCard(node));
        }
    }
}

async function progressiveLoadStatuses(nodes) {
    const statusMap = new Map();
    try {
        const res = await fetch('/api/outbounds-status');
        if (res.ok) {
            const allStatuses = await res.json();
            for (const st of allStatuses) {
                if (st && st.tag) {
                    statusMap.set(st.tag, st);
                }
            }
        }
    } catch (e) {
        console.error('批量加载状态失败:', e);
    }

    const results = nodes.map((node) => {
        let status = statusMap.get(node.tag);
        if (!status) {
            status = { alive: false, delay: 0, error: 'not_found' };
        }
        updateCardStatus(node.tag, status);
        return { tag: node.tag, status: status };
    });
    return results;
}

function updateCardStatus(tag, status) {
    const statusEl = document.getElementById('status-' + tag);
    if (!statusEl) {
        setTimeout(() => {
            const retryEl = document.getElementById('status-' + tag);
            if (retryEl) performUIUpdate(retryEl, status);
        }, 50);
        return;
    }
    performUIUpdate(statusEl, status);
}

function performUIUpdate(statusEl, status) {
    if (status && status.error !== 'not_found' && status.error !== 'fetch_failed') {
        const alive = status.alive;
        const delay = status.delay;
        if (alive && (delay === undefined || delay === null || delay === 0)) {
            statusEl.innerHTML = '<span class="flex h-2 w-2 relative">' +
                '<span class="animate-pulse absolute inline-flex h-2 w-2 rounded-full bg-yellow-500 opacity-75"></span>' +
                '<span class="relative inline-flex rounded-full h-2 w-2 bg-yellow-500"></span>' +
                '</span>' +
                '<span class="font-mono font-semibold text-yellow-400">检测中...</span>';
            statusEl.className = 'flex items-center gap-2 text-xs text-yellow-400';
            return;
        }
        const color = alive ? 'bg-green-500' : 'bg-red-500';
        const statusColor = alive ? 'text-green-400' : 'text-red-400';
        statusEl.innerHTML = '<span class="flex h-2 w-2 relative">' +
            '<span class="animate-ping absolute inline-flex h-2 w-2 rounded-full ' + color + ' opacity-75"></span>' +
            '<span class="relative inline-flex rounded-full h-2 w-2 ' + color + '"></span>' +
            '</span>' +
            '<span class="font-mono font-semibold">' + delay + ' ms</span>';
        statusEl.className = 'flex items-center gap-2 text-xs ' + statusColor;
    } else {
        statusEl.innerHTML = '<span>&#9675; 检测失败</span>';
        statusEl.className = 'flex items-center gap-2 text-xs text-white/70';
    }
}

function findBestNode(allStatuses) {
    let bestNode = null;
    let minDelay = Infinity;
    for (const nodeStatus of allStatuses) {
        if (nodeStatus && nodeStatus.status && nodeStatus.status.alive && nodeStatus.status.delay > 0) {
            if (nodeStatus.status.delay < minDelay) {
                minDelay = nodeStatus.status.delay;
                bestNode = nodeStatus;
            }
        }
    }
    return bestNode;
}

function createOutboundCard(node) {
    const tag = node.tag;
    const protocol = (node.protocol || 'unknown').toUpperCase();
    const isCurrent = tag === currentOutbound;

    const card = document.createElement('div');
    card.setAttribute('data-tag', tag);
    card.className = 'card-hover bg-white/10 backdrop-blur p-4 rounded-xl shadow-lg border ' +
        (isCurrent ? 'border-cyan-400 current-indicator' : 'border-white/20');

    const inner = document.createElement('div');
    inner.className = 'relative';

    if (isCurrent) {
        const badge = document.createElement('div');
        badge.className = 'absolute top-2 right-2 bg-cyan-500/90 text-white px-2 py-1 rounded-md text-xs font-bold flex items-center gap-1';
        badge.innerHTML = '<span class="pulse-ring">&#9679;</span> 当前';
        inner.appendChild(badge);
    }

    const body = document.createElement('div');
    body.className = 'mb-3';

    const tagDiv = document.createElement('div');
    tagDiv.className = 'font-mono text-lg font-bold text-white mb-1 truncate';
    tagDiv.textContent = tag;
    tagDiv.title = tag;

    const protoDiv = document.createElement('div');
    protoDiv.className = 'text-xs text-cyan-200 font-semibold mb-2';
    protoDiv.textContent = protocol;

    body.appendChild(tagDiv);
    body.appendChild(protoDiv);

    const statusDiv = document.createElement('div');
    statusDiv.id = 'status-' + tag;
    statusDiv.className = 'flex items-center gap-2 text-xs text-white/70';
    statusDiv.innerHTML = '&#9675; 正在 PING...';

    inner.appendChild(body);
    inner.appendChild(statusDiv);
    card.appendChild(inner);

    if (!isCurrent) {
        card.addEventListener('click', () => applyOutboundChange(tag, tag, { reload: true }));
    } else {
        card.style.cursor = 'default';
    }

    return card;
}

async function applyOutboundChange(outboundTag, displayName, options) {
    var opts = options || { reload: true };
    try {
        const response = await fetch('/api/switch-outbound', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ outbound_tag: outboundTag })
        });
        if (!response.ok) {
            const err = await response.json();
            throw new Error(err.error || 'Failed to apply change');
        }
        showNotification('&#10003; 已切换到: ' + (displayName || '自动均衡'), 'success');
        if (opts.reload) {
            setTimeout(() => initializePage(), 500);
        }
    } catch (error) {
        console.error('Error applying change:', error);
        showNotification('&#10007; 切换失败: ' + error.message, 'error');
    }
}

function formatBytes(bytes, decimals) {
    if (bytes === 0) return '0 Bytes';
    var dm = (decimals === undefined || decimals < 0) ? 0 : decimals;
    var k = 1024;
    var sizes = ['Bytes', 'KB', 'MB', 'GB', 'TB', 'PB'];
    var i = Math.floor(Math.log(bytes) / Math.log(k));
    return parseFloat((bytes / Math.pow(k, i)).toFixed(dm)) + ' ' + sizes[i];
}

function formatDuration(totalSeconds) {
    if (totalSeconds <= 0) return '0s';
    var days = Math.floor(totalSeconds / 86400);
    totalSeconds %= 86400;
    var hours = Math.floor(totalSeconds / 3600);
    totalSeconds %= 3600;
    var minutes = Math.floor(totalSeconds / 60);
    var seconds = totalSeconds % 60;
    var result = '';
    if (days > 0) result += days + 'd ';
    if (hours > 0) result += hours + 'h ';
    if (minutes > 0) result += minutes + 'm ';
    if (seconds > 0) result += seconds + 's';
    return result.trim();
}