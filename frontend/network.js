let _sseEventSource = null;
let _postInitTimer = null;

// Unified API fetch helper: checks response.ok, parses JSON, throws on error.
async function apiFetch(url, options = {}) {
    const res = await fetch(url, options);
    if (!res.ok) {
        const errData = await safeJson(res).catch(() => null);
        throw new Error(errData?.error || `请求失败 (${res.status})`);
    }
    return await safeJson(res);
}

const NetworkManager = (() => ({
    stopPostInitPolling() {
        if (_postInitTimer) {
            clearTimeout(_postInitTimer);
            _postInitTimer = null;
        }
    },

    closeSSE() {
        this.stopPostInitPolling();
        if (_sseEventSource) {
            _sseEventSource.close();
            _sseEventSource = null;
        }
    },

    async loadConfig() {
        try {
            const config = await apiFetch('/api/config');
            XrayManager.balancerTag = config.balancer_tag;
            UIManager.getElements().balancerTag.textContent = XrayManager.balancerTag;
            if (config.log_type && config.log_type !== 'none') {
                LogManager.setEnabled(config.log_type);
            }
        } catch (error) {
            console.error('加载配置失败:', error);
        }
        await this.initializePage();
    },

    async fetchOutboundList() {
        try {
            const outbounds = await apiFetch('/api/outbounds');
            return outbounds.length === 0 ? [] : outbounds;
        } catch (error) {
            console.error('Error loading outbounds:', error);
            const container = UIManager.getElements().outboundsList;
            showRetryUI(container, `加载出站列表失败: ${error.message}`,
                () => { NetworkManager.initializePage(); });
            return null;
        }
    },

    async loadCurrentOutbound() {
        try {
            const data = await apiFetch('/api/current-outbound');
            XrayManager.prevCurrentOutbound = XrayManager.currentOutbound;
            XrayManager.isAutoMode = data.auto || !data.current;
            if (XrayManager.isAutoMode) {
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

    async applyOutboundChange(outboundTag, displayName, options = { reload: true }) {
        try {
            await apiFetch('/api/switch-outbound', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ outbound_tag: outboundTag }),
            });
            NotificationManager.showNotification(`✓ 已切换到: ${displayName || '自动均衡'}`, 'success');
            if (options.reload) {
                setTimeout(() => { NetworkManager.initializePage(); }, 500);
            }
        } catch (error) {
            console.error('Error applying change:', error);
            NotificationManager.showNotification(`✗ 切换失败: ${error.message}`, 'error');
        }
    },

    async initializePage() {
        this.stopPostInitPolling();
        XrayManager.pageReady = false;
        XrayManager.didInitialAutoSelect = false;

        const [currentStatus, newNodeData] = await Promise.all([
            this.loadCurrentOutbound(),
            this.fetchOutboundList(),
        ]);

        if (newNodeData === null) return;

        let orderedNodeData;
        if (XrayManager.fixedNodeOrder.length === 0) {
            newNodeData.sort((a, b) => a.tag.localeCompare(b.tag));
            XrayManager.fixedNodeOrder = newNodeData.map((n) => n.tag);
            orderedNodeData = newNodeData;
        } else {
            const nodeMap = new Map(newNodeData.map((n) => [n.tag, n]));
            orderedNodeData = [];
            for (const tag of XrayManager.fixedNodeOrder) {
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
                XrayManager.fixedNodeOrder.push(...newNodes.map((n) => n.tag));
            }
        }

        NodeManager.renderOutboundCards(orderedNodeData);

        const allStatuses = await NodeManager.progressiveLoadStatuses(orderedNodeData);

        const bestNode = NodeManager.findBestNode(allStatuses);
        if (bestNode && currentStatus.auto && !XrayManager.didInitialAutoSelect) {
            if (bestNode.tag !== currentStatus.active_node) {
                const confirmed = await showModal(`是否切换到负载均衡模式，使用延迟最低节点？\n${bestNode.tag} (延迟: ${bestNode.status.delay}ms)`);
                if (confirmed) {
                    await this.applyOutboundChange('', '自动均衡', { reload: false });
                }
                XrayManager.isAutoMode = currentStatus.auto;
                XrayManager.currentOutbound = currentStatus.auto ? (currentStatus.active_node || '') : (currentStatus.current || '');
                NodeManager.renderOutboundCards(orderedNodeData);
                await NodeManager.progressiveLoadStatuses(orderedNodeData);
            }
            XrayManager.didInitialAutoSelect = true;
        }

        XrayManager.pageReady = true;
        this.startPostInitPolling(orderedNodeData);
    },

    startPostInitPolling(nodes) {
        this.stopPostInitPolling();
        if (!nodes || nodes.length === 0) return;

        const maxRounds = 10;
        let round = 0;

        const tick = async () => {
            if (!XrayManager.pageReady || round >= maxRounds) return;
            round++;

            try {
                const [, statuses] = await Promise.all([
                    this.loadCurrentOutbound(),
                    NodeManager.progressiveLoadStatuses(nodes),
                ]);
                if (NodeManager.findBestNode(statuses)) return;
            } catch (e) {
                console.error('后初始化轮询出错:', e);
            }

            _postInitTimer = setTimeout(tick, 5000);
        };

        _postInitTimer = setTimeout(tick, 5000);
    }
}))();

function connectStatsSSE() {
    console.log('正在连接到 /api/stats-sse...');

    const statsContainer = document.getElementById('stats-container');
    const statusBanner = document.createElement('div');
    statusBanner.id = 'sse-status-banner';
    statusBanner.style.display = 'none';
    statusBanner.className = 'text-center text-xs py-1.5 mt-3 mb-2 rounded-xl transition-colors duration-300';
    statsContainer.parentNode.insertBefore(statusBanner, statsContainer);

    const hideBanner = () => {
        statusBanner.style.display = 'none';
        statsContainer.style.opacity = '1';
    };

    const showBanner = () => {
        statusBanner.textContent = '⚠ 实时数据连接断开，正在重连...';
        statusBanner.style.display = '';
        statusBanner.className = 'text-center text-xs py-1.5 mt-3 mb-2 rounded-xl bg-yellow-600/50 text-yellow-200 transition-colors duration-300';
        statsContainer.style.opacity = '0.7';
    };

    let retryDelay = 1000;
    const maxDelay = 30000;
    const maxRetries = 10;
    let retryCount = 0;
    let evtSource = null;
    let reconnectTimer = null;

    const showPermanentDisconnect = () => {
        statusBanner.innerHTML = '';
        const msg = document.createElement('span');
        msg.textContent = '❌ 实时数据连接已断开（重试已达上限）';
        statusBanner.appendChild(msg);
        const btn = document.createElement('button');
        btn.textContent = '重新连接';
        btn.className = 'ml-2 px-2 underline text-yellow-100 hover:text-white';
        btn.addEventListener('click', () => {
            retryCount = 0;
            retryDelay = 1000;
            connect();
        });
        statusBanner.appendChild(btn);
        statusBanner.style.display = '';
        statusBanner.className = 'text-center text-xs py-1.5 mt-3 mb-2 rounded-xl bg-red-600/50 text-red-200 transition-colors duration-300';
        statsContainer.style.opacity = '0.5';
    };

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

        evtSource.addEventListener('update', (event) => {
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

        evtSource.onopen = () => {
            console.log('SSE 连接已建立');
            retryDelay = 1000;
            retryCount = 0;
            hideBanner();
        };

        evtSource.onerror = () => {
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
            console.warn(`SSE 连接错误，${retryDelay / 1000}s 后重连... (${retryCount}/${maxRetries})`);
            reconnectTimer = setTimeout(() => {
                reconnectTimer = null;
                connect();
            }, retryDelay);
            retryDelay = Math.min(retryDelay * 2, maxDelay);
        };
    }

    connect();
}
