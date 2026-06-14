let _sseEventSource = null;

const NetworkManager = (function() {
    return {
        closeSSE: function() {
            if (_sseEventSource) {
                _sseEventSource.close();
                _sseEventSource = null;
            }
        },

        loadConfig: async function() {
            try {
                const response = await fetch('/api/config');
                if (response.ok) {
                    const config = await safeJson(response);
                    XrayManager.balancerTag = config.balancer_tag;
                    UIManager.getElements().balancerTag.textContent = XrayManager.balancerTag;
                    if (config.log_type && config.log_type !== 'none' && typeof LogManager !== 'undefined') {
                        LogManager.setEnabled(config.log_type);
                    }
                }
            } catch (error) {
                console.error('加载配置失败:', error);
            }
            await this.initializePage();
        },

        fetchOutboundList: async function() {
            try {
                const response = await fetch('/api/outbounds');
                if (!response.ok) throw new Error('获取出站列表失败');
                const outbounds = await safeJson(response);
                if (outbounds.length === 0) return [];
                return outbounds;
            } catch (error) {
                console.error('Error loading outbounds:', error);
                const container = UIManager.getElements().outboundsList;
                showRetryUI(container, '加载出站列表失败: ' + error.message,
                    function() { NetworkManager.initializePage(); });
                return null;
            }
        },

        loadCurrentOutbound: async function() {
            try {
                const response = await fetch('/api/current-outbound');
                if (!response.ok) throw new Error('获取当前出站失败');
                const data = await safeJson(response);
                XrayManager.prevCurrentOutbound = XrayManager.currentOutbound;
                XrayManager.isAutoMode = data.auto || !data.current;
                if (XrayManager.isAutoMode) {
                    // 自动模式下，使用 active_node 作为当前节点
                    XrayManager.currentOutbound = data.active_node || '';
                } else {
                    XrayManager.currentOutbound = data.current;
                }
                UIManager.updateCurrentDisplay(data.auto, data.current, data.active_node);
                return data;
            } catch (error) {
                console.error('Error loading current outbound:', error);
                UIManager.updateErrorDisplay();
                XrayManager.isAutoMode = true;
                XrayManager.currentOutbound = '';
                return { auto: true, current: '' };
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
                    let errData;
                    try { errData = await safeJson(response); } catch(e) { errData = null; }
                    throw new Error((errData && errData.error) || '切换出站失败');
                }
                NotificationManager.showNotification('✓ 已切换到: ' + (displayName || '自动均衡'), 'success');
                if (opts.reload) {
                    setTimeout(function() { NetworkManager.initializePage(); }, 500);
                }
            } catch (error) {
                console.error('Error applying change:', error);
                NotificationManager.showNotification('✗ 切换失败: ' + error.message, 'error');
            }
        },

        initializePage: async function() {
            XrayManager.pageReady = false;

            const results = await Promise.all([
                this.loadCurrentOutbound(),
                this.fetchOutboundList()
            ]);
            const currentStatus = results[0];
            const newNodeData = results[1];

            if (newNodeData === null) {
                return;
            }

            let orderedNodeData;
            if (XrayManager.fixedNodeOrder.length === 0) {
                newNodeData.sort(function(a, b) { return a.tag.localeCompare(b.tag); });
                XrayManager.fixedNodeOrder = newNodeData.map(function(n) { return n.tag; });
                orderedNodeData = newNodeData;
            } else {
                const nodeMap = new Map(newNodeData.map(function(n) { return [n.tag, n]; }));
                orderedNodeData = [];
                const order = XrayManager.fixedNodeOrder;
                for (let i = 0; i < order.length; i++) {
                    const node = nodeMap.get(order[i]);
                    if (node) {
                        orderedNodeData.push(node);
                        nodeMap.delete(order[i]);
                    }
                }
                const newNodes = Array.from(nodeMap.values());
                if (newNodes.length > 0) {
                    newNodes.sort(function(a, b) { return a.tag.localeCompare(b.tag); });
                    orderedNodeData.push.apply(orderedNodeData, newNodes);
                    XrayManager.fixedNodeOrder =
                        XrayManager.fixedNodeOrder.concat(newNodes.map(function(n) { return n.tag; }));
                }
            }

            NodeManager.renderOutboundCards(orderedNodeData);

            const allStatuses = await NodeManager.progressiveLoadStatuses(orderedNodeData);

            const bestNode = NodeManager.findBestNode(allStatuses);
            if (bestNode && currentStatus.auto && !XrayManager.didInitialAutoSelect) {
                const confirmed = await showModal('检测到更优节点: ' + bestNode.tag + ' (延迟: ' + bestNode.status.delay + 'ms)\n是否切换？');
                if (confirmed) {
                    await this.applyOutboundChange(bestNode.tag, bestNode.tag, { reload: false });
                    await this.loadCurrentOutbound();
                    NodeManager.renderOutboundCards(orderedNodeData);
                    await NodeManager.progressiveLoadStatuses(orderedNodeData);
                }
                XrayManager.didInitialAutoSelect = true;
            }

            XrayManager.pageReady = true;
        }
    };
})();

function connectStatsSSE() {
    console.log('正在连接到 /api/stats-sse...');

    const statsContainer = document.getElementById('stats-container');
    const statusBanner = document.createElement('div');
    statusBanner.id = 'sse-status-banner';
    statusBanner.style.display = 'none';
    statusBanner.className = 'text-center text-xs py-1.5 mt-3 mb-2 rounded-xl transition-colors duration-300';
    statsContainer.parentNode.insertBefore(statusBanner, statsContainer);

    function hideBanner() {
        statusBanner.style.display = 'none';
        statsContainer.style.opacity = '1';
    }

    function showBanner() {
        statusBanner.textContent = '⚠ 实时数据连接断开，正在重连...';
        statusBanner.style.display = '';
        statusBanner.className = 'text-center text-xs py-1.5 mt-3 mb-2 rounded-xl bg-yellow-600/50 text-yellow-200 transition-colors duration-300';
        statsContainer.style.opacity = '0.7';
    }

    let retryDelay = 1000;
    const maxDelay = 30000;
    const maxRetries = 10;
    let retryCount = 0;
    let evtSource = null;
    let reconnectTimer = null;

    function showPermanentDisconnect() {
        statusBanner.innerHTML = '';
        var msg = document.createElement('span');
        msg.textContent = '❌ 实时数据连接已断开（重试已达上限）';
        statusBanner.appendChild(msg);
        var btn = document.createElement('button');
        btn.textContent = '重新连接';
        btn.className = 'ml-2 px-2 underline text-yellow-100 hover:text-white';
        btn.addEventListener('click', function() {
            retryCount = 0;
            retryDelay = 1000;
            connect();
        });
        statusBanner.appendChild(btn);
        statusBanner.style.display = '';
        statusBanner.className = 'text-center text-xs py-1.5 mt-3 mb-2 rounded-xl bg-red-600/50 text-red-200 transition-colors duration-300';
        statsContainer.style.opacity = '0.5';
    }

    function connect() {
        if (reconnectTimer) {
            clearTimeout(reconnectTimer);
            reconnectTimer = null;
        }
        if (evtSource) {
            evtSource.close();
            evtSource = null;
        }
        evtSource = new EventSource('/api/stats-sse');
        _sseEventSource = evtSource;

        evtSource.addEventListener('update', function(event) {
            try {
                const data = JSON.parse(event.data);
                UIManager.updateStatsUI(data);
                if (XrayManager.pageReady && data.outbounds && Array.isArray(data.outbounds)) {
                    UIManager.updateAllCardStatuses(data.outbounds);
                }
                hideBanner();
            } catch (error) {
                console.error('解析 SSE 数据失败:', error);
            }
        });

        evtSource.onopen = function() {
            console.log('SSE 连接已建立');
            retryDelay = 1000;
            retryCount = 0;
            hideBanner();
        };

        evtSource.onerror = function() {
            if (reconnectTimer) return;
            evtSource.close();
            evtSource = null;
            _sseEventSource = null;
            retryCount++;
            if (retryCount >= maxRetries) {
                showPermanentDisconnect();
                return;
            }
            showBanner();
            console.warn('SSE 连接错误，' + (retryDelay / 1000) + 's 后重连... (' + retryCount + '/' + maxRetries + ')');
            reconnectTimer = setTimeout(function() {
                reconnectTimer = null;
                connect();
            }, retryDelay);
            retryDelay = Math.min(retryDelay * 2, maxDelay);
        };
    }

    connect();
}
