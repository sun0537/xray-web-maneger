let currentOutbound = '';
let balancerTag = 'balancer';
let didInitialAutoSelect = false;
let fixedNodeOrder = [];

window.addEventListener('error', (e) => {
    console.error('Global error:', e.error);
    showNotification('发生错误: ' + e.message, 'error');
});

window.addEventListener('load', () => {
    loadConfig();
    connectStatsSSE();

    document.getElementById('reset-btn').addEventListener('click', () => {
        applyOutboundChange('', '自动均衡', { reload: true });
    });
    
    // 刷新按钮添加防抖
    let refreshing = false;
    document.getElementById('refresh-btn').addEventListener('click', async () => {
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
    console.log("正在连接到 /api/stats-sse...");
    const evtSource = new EventSource("/api/stats-sse");

    evtSource.addEventListener("update", (event) => {
        try {
            const data = JSON.parse(event.data);
            updateStatsUI(data); // 正常更新 UI
        } catch (error) {
            console.error("解析 SSE 数据失败:", error);
        }
    });
    
    evtSource.onopen = () => {
        console.log("SSE 连接已建立");
        //当连接重新建立时，重置 UI 状态
        updateStatsUI(null, true); // 传递一个 "reset" 标志
    };

    evtSource.onerror = (err) => {
        console.error("SSE 连接错误:", err);
        const errorText = "统计数据连接断开";
        document.getElementById('stats-uplink').textContent = errorText;
        document.getElementById('stats-downlink').textContent = errorText;
        document.getElementById('stats-uptime').textContent = errorText;
        document.getElementById('stats-sys-mem').textContent = errorText;
        document.getElementById('stats-goroutines').textContent = errorText;
        
        // 浏览器原生的 EventSource 会自动尝试重连，不需要自己写。
        // 它会在连接失败时自动进入“重连中”状态。
    };
}

function updateStatsUI(data, reset = false) {
    if (reset) {
        // 如果是重置调用 (onopen)，恢复 "加载中..."
        // 这可以清除 "统计数据连接断开" 的提示
        const loadingText = "加载中...";
        document.getElementById('stats-uplink').textContent = loadingText;
        document.getElementById('stats-downlink').textContent = loadingText;
        document.getElementById('stats-uptime').textContent = loadingText;
        document.getElementById('stats-sys-mem').textContent = loadingText;
        document.getElementById('stats-goroutines').textContent = loadingText;
        return;
    }
    
    if (data) {
        document.getElementById('stats-uplink').textContent = formatBytes(data.uplink);
        document.getElementById('stats-downlink').textContent = formatBytes(data.downlink);
        document.getElementById('stats-uptime').textContent = formatDuration(data.uptime);
        document.getElementById('stats-sys-mem').textContent = formatBytes(data.sys_mem);
        document.getElementById('stats-goroutines').textContent = data.goroutines;
    }
}


async function loadConfig() {
    try {
        const response = await fetch('/api/config');
        if (response.ok) {
            const config = await response.json();
            balancerTag = config.balancer_tag;
            document.getElementById('balancer-tag').textContent = balancerTag;
        }
    } catch (error) {
        console.error('Error loading config:', error);
    }
    await initializePage();
}

async function initializePage() {
    // 1. 获取当前状态
    const currentStatus = await loadCurrentOutbound();
    
    // 2. 获取出站列表 (仅含 tag 和 protocol)
    const newNodeData = await fetchOutboundList();

    let orderedNodeData;
    if (fixedNodeOrder.length === 0) {
        console.log("First load. Sorting nodes alphabetically.");
        newNodeData.sort((a, b) => a.tag.localeCompare(b.tag));
        fixedNodeOrder = newNodeData.map(node => node.tag);
        orderedNodeData = newNodeData;
    } else {
        console.log("Refresh load. Using fixed order:", fixedNodeOrder);
        const nodeMap = new Map(newNodeData.map(node => [node.tag, node]));
        orderedNodeData = [];
        fixedNodeOrder.forEach(tag => {
            const node = nodeMap.get(tag);
            if (node) {
                orderedNodeData.push(node);
                nodeMap.delete(tag);
            }
        });
        const newNodes = Array.from(nodeMap.values());
        if (newNodes.length > 0) {
            newNodes.sort((a, b) => a.tag.localeCompare(b.tag));
            orderedNodeData.push(...newNodes);
            fixedNodeOrder.push(...newNodes.map(n => n.tag)); 
        }
    }

    // 3. 立即渲染骨架卡片
    renderOutboundCards(orderedNodeData);

    setTimeout(async () => {
        // 4. 异步加载所有节点的状态 (会一个一个更新 UI)
        const allStatuses = await progressiveLoadStatuses(orderedNodeData);

        // 5. 自动切换逻辑 (现在可以安全运行)
        const bestNode = findBestNode(allStatuses);
        if (bestNode && currentStatus.auto && !didInitialAutoSelect) {
            console.log(`自动切换到最佳节点: ${bestNode.tag} (延迟: ${bestNode.status.delay}ms)`);
            didInitialAutoSelect = true;
            await applyOutboundChange(bestNode.tag, bestNode.tag, { reload: false });
            
            // 手动更新 UI，而不是重新渲染
            await loadCurrentOutbound(); // 1. 重新获取 'currentOutbound' 变量
            
            // 重新渲染以正确更新 'current' 状态和点击事件
            renderOutboundCards(orderedNodeData);
        }
    }, 0);
}


async function loadCurrentOutbound() {
    try {
        const response = await fetch('/api/current-outbound');
        if (!response.ok) throw new Error('Failed to fetch current outbound');
        const data = await response.json();
        const currentTagEl = document.getElementById('current-tag');
        if (data.auto || !data.current) {
            currentOutbound = '';
            currentTagEl.innerHTML = '<span class="text-orange-300">&#128260; 自动均衡模式</span>';
        } else {
            currentOutbound = data.current;
            currentTagEl.innerHTML = '<span class="text-green-300">&#10003; ' + data.current + '</span>';
        }
        return data; 
    } catch (error) {
        console.error('Error loading current outbound:', error);
        document.getElementById('current-tag').innerHTML = '<span class="text-red-300">&#10007; 获取失败</span>';
        return { auto: false, current: '' }; 
    }
}


//  只获取出站列表
async function fetchOutboundList() {
    const listEl = document.getElementById('outbounds-list');
    listEl.innerHTML = `
        <div class="text-white/70 text-center py-8 col-span-full">
            <svg class="animate-spin h-8 w-8 mx-auto mb-2" fill="none" viewBox="0 0 24 24">
                <circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4"></circle>
                <path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"></path>
            </svg>
            正在加载节点列表...
        </div>`;
    try {
        //  /api/outbounds 现在返回 [{tag, protocol}]
        const response = await fetch('/api/outbounds');
        if (!response.ok) throw new Error('Failed to fetch outbounds');
        const outbounds = await response.json();
        if (outbounds.length === 0) return [];
        return outbounds; // 返回 [{tag, protocol}, ...]
    } catch (error) {
        console.error('Error loading outbounds:', error);
        listEl.innerHTML = '<p class="text-red-400 text-center py-4 col-span-full">&#10060; 加载出站列表失败</p>';
        return [];
    }
}

//  渲染骨架卡片
function renderOutboundCards(allNodeData) {
    const listEl = document.getElementById('outbounds-list');
    listEl.innerHTML = ''; 
    if (allNodeData.length === 0) {
        listEl.innerHTML = '<p class="text-white/70 text-center py-4 col-span-full">未找到可用出站</p>';
        return;
    }
    allNodeData.forEach(node => {
        if (node) { 
            // 传递 {tag, protocol}
            const card = createOutboundCard(node);
            listEl.appendChild(card);
        }
    });
}

// 渐进式加载状态
async function progressiveLoadStatuses(nodes) {
    // 1. 创建一个 promise 数组。
    //    使用 .map() 来立即启动 *所有* 的 fetch 请求。
    const statusPromises = nodes.map(async (node) => {
        let statusData = { tag: node.tag, status: null };
        try {
            const res = await fetch('/api/outbound-status?tag=' + encodeURIComponent(node.tag));
            if (res.ok) {
                const status = await res.json();
                statusData.status = status;
            } else {
                 statusData.status = { alive: false, delay: 0, error: 'fetch_failed' };
            }
        } catch (e) {
            statusData.status = { alive: false, delay: 0, error: e.message };
        }
        
        // 2. 只要 *这一个* fetch 完成了，就 *立即* 更新它对应的 UI 卡片。
        //    这实现了“渐进式加载”的视觉效果。
        updateCardStatus(node.tag, statusData.status);
        
        // 3. 返回数据给 Promise.all
        return statusData;
    });
    
    // 4. 等待 *所有* 的 promise (无论成功或失败) 都执行完毕。
    //    这保证了在运行 findBestNode 之前，已经拥有了所有节点的状态。
    return await Promise.all(statusPromises);
}

//  更新单个卡片的状态
function updateCardStatus(tag, status) {
    const statusEl = document.getElementById(`status-${tag}`);
    if (!statusEl) {
        // 如果没找到，可能是 DOM 还没渲染完，尝试在 50ms 后重试一次
        setTimeout(() => {
            const retryEl = document.getElementById(`status-${tag}`);
            if (retryEl) performUIUpdate(retryEl, status);
        }, 50);
        return;
    }

    performUIUpdate(statusEl, status);
}
function performUIUpdate(statusEl, status) {
    let statusHTML = '';
    if (status && status.error !== 'not_found' && status.error !== 'fetch_failed') {
        const alive = status.alive;
        const delay = status.delay || 0;
        const color = alive ? 'bg-green-500' : 'bg-red-500';
        const statusColor = alive ? 'text-green-400' : 'text-red-400';
        
        statusHTML = `
            <span class="flex h-2 w-2 relative">
                <span class="animate-ping absolute inline-flex h-2 w-2 rounded-full ${color} opacity-75"></span>
                <span class="relative inline-flex rounded-full h-2 w-2 ${color}"></span>
            </span>
            <span class="font-mono font-semibold">${delay} ms</span>`;
        statusEl.className = `flex items-center gap-2 text-xs ${statusColor}`;
    } else {
        statusHTML = '<span>&#9675; 检测失败</span>';
        statusEl.className = 'flex items-center gap-2 text-xs text-white/70';
    }
    statusEl.innerHTML = statusHTML;
}
// findBestNode 接收 {tag, status} 列表
function findBestNode(allStatuses) {
    let bestNode = null;
    let minDelay = Infinity;
    for (const nodeStatus of allStatuses) {
        if (nodeStatus && nodeStatus.status && nodeStatus.status.alive && nodeStatus.status.delay >= 0) {
            if (nodeStatus.status.delay < minDelay) {
                minDelay = nodeStatus.status.delay;
                bestNode = nodeStatus; // {tag: "...", status: {...}}
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
    card.setAttribute('data-tag', tag); // 添加 data-tag 以便 JS 查找
    card.className = 'card-hover bg-white/10 backdrop-blur p-4 rounded-xl shadow-lg border ' + 
        (isCurrent ? 'border-cyan-400 current-indicator' : 'border-white/20');
    
    let statusHTML = `
        <div id="status-${tag}" class="flex items-center gap-2 text-xs text-white/70">
            &#9675; 正在 PING...
        </div>`;

    const currentBadge = isCurrent ? 
        '<div class="absolute top-2 right-2 bg-cyan-500/90 text-white px-2 py-1 rounded-md text-xs font-bold flex items-center gap-1">' +
        '<span class="pulse-ring">&#9679;</span> 当前' +
        '</div>' : '';

    card.innerHTML = 
        '<div class="relative">' +
            currentBadge +
            '<div class="mb-3">' +
                '<div class="font-mono text-lg font-bold text-white mb-1 truncate" title="' + tag + '">' + tag + '</div>' +
                '<div class="text-xs text-cyan-200 font-semibold mb-2">' + protocol + '</div>' +
            '</div>' +
            statusHTML +
        '</div>';
    
    if (!isCurrent) {
        card.addEventListener('click', () => applyOutboundChange(tag, tag, { reload: true }));
    } else {
        card.style.cursor = 'default';
    }
    
    return card;
}


async function applyOutboundChange(outboundTag, displayName, options = { reload: true }) {
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
        if (options.reload) {
            setTimeout(() => {
                initializePage();
            }, 500);
        }
    } catch (error) {
        console.error('Error applying change:', error);
        showNotification('&#10007; 切换失败: ' + error.message, 'error');
    }
}
function showNotification(message, type) {
    const notification = document.createElement('div');
    const bgColor = type === 'success' ? 'bg-green-500' : 'bg-red-500';
    notification.className = 'fixed top-4 right-4 ' + bgColor + ' text-white px-6 py-3 rounded-lg shadow-2xl z-50 animate-bounce';
    notification.innerHTML = message;
    document.body.appendChild(notification);
    setTimeout(() => notification.remove(), 3000);
}
function formatBytes(bytes, decimals = 2) {
    if (bytes === 0) return '0 Bytes';
    const k = 1024;
    const dm = decimals < 0 ? 0 : decimals;
    const sizes = ['Bytes', 'KB', 'MB', 'GB', 'TB', 'PB'];
    const i = Math.floor(Math.log(bytes) / Math.log(k));
    return parseFloat((bytes / Math.pow(k, i)).toFixed(dm)) + ' ' + sizes[i];
}
function formatDuration(totalSeconds) {
    if (totalSeconds <= 0) return '0s';
    const days = Math.floor(totalSeconds / (3600 * 24));
    totalSeconds %= 3600 * 24;
    const hours = Math.floor(totalSeconds / 3600);
    totalSeconds %= 3600;
    const minutes = Math.floor(totalSeconds / 60);
    const seconds = totalSeconds % 60;
    let result = '';
    if (days > 0) result += days + 'd ';
    if (hours > 0) result += hours + 'h ';
    if (minutes > 0) result += minutes + 'm ';
    if (seconds > 0) result += seconds + 's';
    return result.trim();
}