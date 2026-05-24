const XrayManager = (function() {
    // 封装全局变量和主要逻辑
    let currentOutbound = '';
    let balancerTag = 'balancer';
    let didInitialAutoSelect = false;
    let fixedNodeOrder = [];
    let pageReady = false;
    
    // 公共方法
    return {
        initialize: function() {
            NetworkManager.loadConfig();
        },
        getCurrentOutbound: function() {
            return currentOutbound;
        },
        setCurrentOutbound: function(value) {
            currentOutbound = value;
        },
        getBalancerTag: function() {
            return balancerTag;
        },
        setBalancerTag: function(value) {
            balancerTag = value;
        },
        getDidInitialAutoSelect: function() {
            return didInitialAutoSelect;
        },
        setDidInitialAutoSelect: function(value) {
            didInitialAutoSelect = value;
        },
        getFixedNodeOrder: function() {
            return fixedNodeOrder;
        },
        setFixedNodeOrder: function(value) {
            fixedNodeOrder = value;
        },
        getPageReady: function() {
            return pageReady;
        },
        setPageReady: function(value) {
            pageReady = value;
        }
    };
})();

const $ = (id) => document.getElementById(id);

const UIManager = (function() {
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
    
    return {
        getElements: function() {
            return els;
        },
        
        updateStatsUI: function(data) {
            if (!data) return;
            els.uplink.textContent = formatBytes(data.uplink);
            els.downlink.textContent = formatBytes(data.downlink);
            els.uptime.textContent = formatDuration(data.uptime);
            els.sysMem.textContent = formatBytes(data.sys_mem);
            els.goroutines.textContent = data.goroutines;
        },
        
        updateAllCardStatuses: function(outbounds) {
            for (const s of outbounds) {
                if (s && s.tag) {
                    this.updateCardStatus(s.tag, { alive: s.alive, delay: s.delay });
                }
            }
        },
        
        updateCardStatus: function(tag, status) {
            const statusEl = document.getElementById('status-' + tag);
            if (!statusEl) {
                setTimeout(() => {
                    const retryEl = document.getElementById('status-' + tag);
                    if (retryEl) this.performUIUpdate(retryEl, status);
                }, 50);
                return;
            }
            this.performUIUpdate(statusEl, status);
        },
        
        performUIUpdate: function(statusEl, status) {
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
    };
})();

const NotificationManager = (function() {
    let notificationEl = null;
    let notificationTimer = null;
    
    return {
        showNotification: function(message, type) {
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
    };
})();

window.addEventListener('error', (e) => {
    console.error('Global error:', e.error);
    NotificationManager.showNotification('发生错误: ' + e.message, 'error');
});

window.addEventListener('load', () => {
    XrayManager.initialize();
    connectStatsSSE();

    const uiElements = UIManager.getElements();
    uiElements.resetBtn.addEventListener('click', () => {
        NetworkManager.applyOutboundChange('', '自动均衡', { reload: true });
    });

    let refreshing = false;
    uiElements.refreshBtn.addEventListener('click', async () => {
        if (refreshing) return;
        refreshing = true;
        try {
            XrayManager.setDidInitialAutoSelect(true);
            await NetworkManager.initializePage();
        } finally {
            setTimeout(() => refreshing = false, 1000);
        }
    });
});

const NetworkManager = (function() {
    return {
        loadConfig: async function() {
            try {
                const response = await fetch('/api/config');
                if (response.ok) {
                    const config = await response.json();
                    XrayManager.setBalancerTag(config.balancer_tag);
                    const uiElements = UIManager.getElements();
                    uiElements.balancerTag.textContent = XrayManager.getBalancerTag();
                }
            } catch (error) {
                console.error('Error loading config:', error);
            }
            await this.initializePage();
        },
        
        fetchOutboundList: async function() {
            try {
                const response = await fetch('/api/outbounds');
                if (!response.ok) throw new Error('Failed to fetch outbounds');
                const outbounds = await response.json();
                if (outbounds.length === 0) return [];
                return outbounds;
            } catch (error) {
                console.error('Error loading outbounds:', error);
                const uiElements = UIManager.getElements();
                uiElements.outboundsList.innerHTML = '<p class="text-red-400 text-center py-4 col-span-full">&#10060; 加载出站列表失败</p>';
                return [];
            }
        },
        
        loadCurrentOutbound: async function() {
            try {
                const response = await fetch('/api/current-outbound');
                if (!response.ok) throw new Error('Failed to fetch current outbound');
                const data = await response.json();
                if (data.auto || !data.current) {
                    XrayManager.setCurrentOutbound('');
                    const uiElements = UIManager.getElements();
                    uiElements.currentTag.innerHTML = '<span class="text-orange-300">&#128260; 自动均衡模式</span>';
                } else {
                    XrayManager.setCurrentOutbound(data.current);
                    const uiElements = UIManager.getElements();
                    uiElements.currentTag.innerHTML = '<span class="text-green-300">&#10003; ' + data.current + '</span>';
                }
                return data;
            } catch (error) {
                console.error('Error loading current outbound:', error);
                const uiElements = UIManager.getElements();
                uiElements.currentTag.innerHTML = '<span class="text-red-300">&#10007; 获取失败</span>';
                return { auto: false, current: '' };
            }
        },
        
        applyOutboundChange: async function(outboundTag, displayName, options) {
            const opts = options || { reload: true };
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
                NotificationManager.showNotification('&#10003; 已切换到: ' + (displayName || '自动均衡'), 'success');
                if (opts.reload) {
                    setTimeout(() => this.initializePage(), 500);
                }
            } catch (error) {
                console.error('Error applying change:', error);
                NotificationManager.showNotification('&#10007; 切换失败: ' + error.message, 'error');
            }
        },
        
        initializePage: async function() {
            XrayManager.setPageReady(false);
            const currentStatus = await this.loadCurrentOutbound();
            const newNodeData = await this.fetchOutboundList();

            let orderedNodeData;
            if (XrayManager.getFixedNodeOrder().length === 0) {
                console.log('First load. Sorting nodes alphabetically.');
                newNodeData.sort((a, b) => a.tag.localeCompare(b.tag));
                XrayManager.setFixedNodeOrder(newNodeData.map(node => node.tag));
                orderedNodeData = newNodeData;
            } else {
                console.log('Refresh load. Using fixed order:', XrayManager.getFixedNodeOrder());
                const nodeMap = new Map(newNodeData.map(node => [node.tag, node]));
                orderedNodeData = [];
                for (const tag of XrayManager.getFixedNodeOrder()) {
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
                    XrayManager.setFixedNodeOrder([...XrayManager.getFixedNodeOrder(), ...newNodes.map(n => n.tag)]);
                }
            }

            NodeManager.renderOutboundCards(orderedNodeData);

            const allStatuses = await NodeManager.progressiveLoadStatuses(orderedNodeData);

            const bestNode = NodeManager.findBestNode(allStatuses);
            if (bestNode && currentStatus.auto && !XrayManager.getDidInitialAutoSelect()) {
                console.log('自动切换到最佳节点: ' + bestNode.tag + ' (延迟: ' + bestNode.status.delay + 'ms)');
                XrayManager.setDidInitialAutoSelect(true);
                await this.applyOutboundChange(bestNode.tag, bestNode.tag, { reload: false });
                await this.loadCurrentOutbound();
                NodeManager.renderOutboundCards(orderedNodeData);
                await NodeManager.progressiveLoadStatuses(orderedNodeData);
            }

            XrayManager.setPageReady(true);
        }
    };
})();

function connectStatsSSE() {
    console.log('正在连接到 /api/stats-sse...');
    const evtSource = new EventSource('/api/stats-sse');

    evtSource.addEventListener('update', (event) => {
        try {
            const data = JSON.parse(event.data);
            UIManager.updateStatsUI(data);
            if (XrayManager.getPageReady() && data.outbounds && Array.isArray(data.outbounds)) {
                UIManager.updateAllCardStatuses(data.outbounds);
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

const NodeManager = (function() {
    return {
        renderOutboundCards: function(allNodeData) {
            const uiElements = UIManager.getElements();
            uiElements.outboundsList.innerHTML = '';
            if (allNodeData.length === 0) {
                uiElements.outboundsList.innerHTML = '<p class="text-white/70 text-center py-4 col-span-full">未找到可用出站</p>';
                return;
            }
            for (const node of allNodeData) {
                if (node) {
                    uiElements.outboundsList.appendChild(this.createOutboundCard(node));
                }
            }
        },
        
        progressiveLoadStatuses: async function(nodes) {
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
                UIManager.updateCardStatus(node.tag, status);
                return { tag: node.tag, status: status };
            });
            return results;
        },
        
        findBestNode: function(allStatuses) {
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
        },
        
        createOutboundCard: function(node) {
            const tag = node.tag;
            const protocol = (node.protocol || 'unknown').toUpperCase();
            const isCurrent = tag === XrayManager.getCurrentOutbound();

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
                card.addEventListener('click', () => {
                    NetworkManager.applyOutboundChange(tag, tag, { reload: true });
                });
            } else {
                card.style.cursor = 'default';
            }

            return card;
        }
    };
})();

// 格式化函数保持不变
function formatBytes(bytes, decimals = 0) {
    if (bytes === 0) return '0 Bytes';
    const k = 1024;
    const sizes = ['Bytes', 'KB', 'MB', 'GB', 'TB', 'PB'];
    const i = Math.floor(Math.log(bytes) / Math.log(k));
    return parseFloat((bytes / Math.pow(k, i)).toFixed(decimals)) + ' ' + sizes[i];
}

function formatDuration(totalSeconds) {
    if (totalSeconds <= 0) return '0s';
    let remaining = totalSeconds;
    const days = Math.floor(remaining / 86400);
    remaining %= 86400;
    const hours = Math.floor(remaining / 3600);
    remaining %= 3600;
    const minutes = Math.floor(remaining / 60);
    const seconds = remaining % 60;
    let result = '';
    if (days > 0) result += days + 'd ';
    if (hours > 0) result += hours + 'h ';
    if (minutes > 0) result += minutes + 'm ';
    if (seconds > 0) result += seconds + 's';
    return result.trim();
}