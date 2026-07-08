const NodeManager = (() => ({
    renderOutboundCards(allNodeData) {
        const container = UIManager.getElements().outboundsList;
        const currentChanged = XrayManager.prevCurrentOutbound !== XrayManager.currentOutbound;
        XrayManager.prevCurrentOutbound = XrayManager.currentOutbound;

        if (allNodeData.length === 0) {
            container.innerHTML = '';
            container.appendChild(createEmptyState('未找到可用出站', 'col-span-full text-white/70 py-4'));
            return;
        }

        if (currentChanged) {
            container.innerHTML = '';
            for (const node of allNodeData) {
                if (node) container.appendChild(this.createOutboundCard(node));
            }
            return;
        }

        const existingCards = {};
        const cardEls = container.querySelectorAll('[data-tag]');
        for (const card of cardEls) {
            existingCards[card.getAttribute('data-tag')] = card;
        }

        for (const node of allNodeData) {
            if (!node) continue;
            const card = existingCards[node.tag];
            if (card) {
                container.appendChild(card);
                delete existingCards[node.tag];
            } else {
                container.appendChild(this.createOutboundCard(node));
            }
        }

        for (const tag of Object.keys(existingCards)) {
            container.removeChild(existingCards[tag]);
        }
    },

    async progressiveLoadStatuses(nodes) {
        const statusMap = new Map();
        try {
            const res = await fetch('/api/outbounds-status');
            if (res.ok) {
                const allStatuses = await safeJson(res);
                for (const st of allStatuses) {
                    if (st && st.tag) statusMap.set(st.tag, st);
                }
            }
        } catch (e) {
            console.error('批量加载状态失败:', e);
        }

        return nodes.map((node) => {
            let status = statusMap.get(node.tag);
            if (!status) status = { alive: false, delay: 0, error: 'not_found' };
            UIManager.updateCardStatus(node.tag, status);
            return { tag: node.tag, status };
        });
    },

    findBestNode(allStatuses) {
        let bestNode = null;
        let minDelay = Infinity;
        for (const ns of allStatuses) {
            if (ns && ns.status && ns.status.alive && ns.status.delay > 0 && ns.status.delay < minDelay) {
                minDelay = ns.status.delay;
                bestNode = ns;
            }
        }
        return bestNode;
    },

    createOutboundCard(node) {
        const tag = node.tag;
        const protocol = (node.protocol || 'unknown').toUpperCase();
        const isCurrent = tag === XrayManager.currentOutbound;

        const card = document.createElement('div');
        card.setAttribute('data-tag', tag);
        card.setAttribute('tabindex', '0');
        card.setAttribute('role', 'button');
        card.setAttribute('aria-label', `切换到节点 ${tag}`);
        card.className = `card-hover bg-white/10 backdrop-blur p-3 md:p-4 rounded-xl shadow-lg border ${isCurrent ? 'border-cyan-400 current-indicator' : 'border-white/20'}`;

        const inner = document.createElement('div');

        if (isCurrent && !XrayManager.isAutoMode) {
            card.style.cursor = 'default';
        }

        const body = document.createElement('div');
        body.className = 'mb-2 md:mb-3';

        const tagDiv = document.createElement('div');
        tagDiv.className = 'font-mono text-sm md:text-lg font-bold text-white mb-1 truncate';
        tagDiv.textContent = tag;
        tagDiv.title = tag;

        const protoDiv = document.createElement('div');
        protoDiv.className = 'text-xs text-cyan-200 font-semibold mb-2';
        protoDiv.textContent = protocol;

        body.appendChild(tagDiv);
        body.appendChild(protoDiv);

        const bottomRow = document.createElement('div');
        bottomRow.className = 'flex items-center justify-between gap-2';

        const statusDiv = document.createElement('div');
        statusDiv.id = `status-${tag}`;
        statusDiv.className = 'flex items-center gap-2 text-xs text-white/70';
        statusDiv.textContent = '○ 正在 PING...';

        bottomRow.appendChild(statusDiv);

        if (isCurrent) {
            const badge = document.createElement('div');
            badge.className = 'bg-cyan-500/90 text-white px-2 py-1 rounded-md text-xs font-bold flex items-center gap-1';
            const dot = document.createElement('span');
            dot.className = 'pulse-ring';
            dot.textContent = '●';
            badge.appendChild(dot);
            badge.appendChild(document.createTextNode(XrayManager.isAutoMode ? ' 自动' : ' 当前'));
            bottomRow.appendChild(badge);
        }

        inner.appendChild(body);
        inner.appendChild(bottomRow);
        card.appendChild(inner);

        if (!isCurrent || XrayManager.isAutoMode) {
            const handleSwitch = async () => {
                try {
                    if (isCurrent && XrayManager.isAutoMode) {
                        const msg = `将退出自动均衡模式，手动锁定到节点: ${tag}\n之后需手动切换节点或点击「自动均衡」恢复。确认？`;
                        const confirmed = await showModal(msg).catch(() => false);
                        if (!confirmed) return;
                    }
                    await NetworkManager.applyOutboundChange(tag, tag, { reload: true });
                } catch (e) {
                    console.error('切换节点失败:', e);
                    NotificationManager.showNotification(`切换节点失败: ${e.message || String(e) || '未知错误'}`, 'error');
                }
            };
            card.addEventListener('click', handleSwitch);
            card.addEventListener('keydown', (e) => {
                if (e.key === 'Enter' || e.key === ' ') {
                    e.preventDefault();
                    handleSwitch();
                }
            });
        }

        return card;
    }
}))();

const TabManager = (() => {
    let activeTab = 'dashboard';

    function switchTab(tabName) {
        if (tabName === activeTab) return;
        activeTab = tabName;

        for (const page of document.querySelectorAll('.page-content')) {
            page.style.display = 'none';
        }
        const target = $(`page-${tabName}`);
        if (target) target.style.display = '';

        for (const tab of document.querySelectorAll('.sidebar-tab')) {
            tab.classList.remove('active');
        }
        const activeBtn = $(`tab-${tabName}`);
        if (activeBtn) activeBtn.classList.add('active');

        if (tabName === 'logs') {
            LogManager.startPolling();
        } else {
            LogManager.stopPolling();
        }
    }

    return {
        init() {
            const dashTab = $('tab-dashboard');
            const logsTab = $('tab-logs');
            if (dashTab) dashTab.addEventListener('click', () => switchTab('dashboard'));
            if (logsTab) logsTab.addEventListener('click', () => switchTab('logs'));
        },
        getActiveTab: () => activeTab,
    };
})();

const LogManager = (() => {
    let enabled = false;
    let logType = 'none';
    let pollTimer = null;
    let searchTimer = null;
    let currentSearch = '';
    let userScrolled = false;
    const POLL_INTERVAL = 3000;

    const els = {
        container: null,
        search: null,
        status: null,
        lineCount: null,
        sourceBadge: null,
        autoRefresh: null,
        refreshBtn: null,
    };

    function cacheEls() {
        els.container = $('log-container');
        els.search = $('log-search');
        els.status = $('log-status');
        els.lineCount = $('log-line-count');
        els.sourceBadge = $('log-source-badge');
        els.autoRefresh = $('log-auto-refresh');
        els.refreshBtn = $('log-refresh-btn');
    }

    async function loadLogs() {
        if (!els.container) cacheEls();
        if (!els.container) return;

        const params = new URLSearchParams();
        params.set('lines', '500');
        if (currentSearch) params.set('search', currentSearch);

        try {
            const res = await fetch(`/api/logs?${params.toString()}`);
            const data = await safeJson(res);

            if (!res.ok) {
                els.status.textContent = '加载失败';
                return;
            }

            renderLines(data.lines || []);
            els.lineCount.textContent = `${data.lines ? data.lines.length : 0} 行`;

            const now = new Date();
            const timeStr = now.toLocaleTimeString('zh-CN', { hour12: false });
            els.status.textContent = `最后更新: ${timeStr}`;
        } catch (e) {
            console.error('Log fetch error:', e);
            els.status.textContent = `加载失败: ${e.message}`;
        }
    }

    function renderLines(lines) {
        if (!els.container) return;

        if (lines.length === 0) {
            els.container.innerHTML = '';
            els.container.appendChild(createEmptyState('无日志数据', 'text-white/50 py-8'));
            return;
        }

        const wasAtBottom = !userScrolled;
        els.container.innerHTML = '';

        const frag = document.createDocumentFragment();
        for (const line of lines) {
            const div = document.createElement('div');
            div.className = 'log-line';
            if (currentSearch) {
                div.innerHTML = highlightText(line, currentSearch);
            } else {
                div.textContent = line;
            }
            frag.appendChild(div);
        }
        els.container.appendChild(frag);

        if (wasAtBottom) {
            els.container.scrollTop = els.container.scrollHeight;
        }
    }

    function highlightText(text, search) {
        const escaped = search.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
        const re = new RegExp(`(${escaped})`, 'gi');
        const parts = text.split(re);
        return parts.map((part, i) =>
            i % 2 === 1
                ? `<span class="highlight">${escapeHtml(part)}</span>`
                : escapeHtml(part)
        ).join('');
    }

    function escapeHtml(str) {
        return str.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
    }

    function startPolling() {
        if (pollTimer) return;
        loadLogs();
        pollTimer = setInterval(() => {
            if (els.autoRefresh && els.autoRefresh.checked) {
                loadLogs();
            }
        }, POLL_INTERVAL);
    }

    function stopPolling() {
        if (pollTimer) {
            clearInterval(pollTimer);
            pollTimer = null;
        }
    }

    return {
        setEnabled(type) {
            enabled = true;
            logType = type;
            cacheEls();

            const logsTab = $('tab-logs');
            if (logsTab) logsTab.style.display = '';

            if (els.sourceBadge) {
                els.sourceBadge.textContent = type === 'file' ? '文件' : (type === 'journal' ? 'journal' : type);
            }

            if (els.search && !els.search._bound) {
                els.search._bound = true;
                els.search.addEventListener('input', () => {
                    if (searchTimer) clearTimeout(searchTimer);
                    const val = els.search.value;
                    searchTimer = setTimeout(() => {
                        currentSearch = val;
                        loadLogs();
                    }, 300);
                });
            }

            if (els.refreshBtn && !els.refreshBtn._bound) {
                els.refreshBtn._bound = true;
                els.refreshBtn.addEventListener('click', () => loadLogs());
            }

            if (els.container && !els.container._scrollBound) {
                els.container._scrollBound = true;
                els.container.addEventListener('scroll', () => {
                    const el = els.container;
                    userScrolled = (el.scrollHeight - el.scrollTop - el.clientHeight) > 30;
                });
            }
        },

        startPolling,
        stopPolling,
    };
})();

window.addEventListener('error', (e) => {
    console.error('全局错误:', e.error);
    if (typeof NotificationManager !== 'undefined') {
        NotificationManager.showNotification(`发生错误: ${e.message}`, 'error');
    }
});

window.addEventListener('load', () => {
    TabManager.init();
    NetworkManager.loadConfig();
    connectStatsSSE();

    const uiElements = UIManager.getElements();
    if (uiElements.resetBtn) {
        uiElements.resetBtn.addEventListener('click', async () => {
            await NetworkManager.applyOutboundChange('', '自动均衡', { reload: true });
        });
    }

    let refreshing = false;
    if (uiElements.refreshBtn) {
        uiElements.refreshBtn.addEventListener('click', async () => {
            if (refreshing) return;
            refreshing = true;
            try {
                await NetworkManager.initializePage();
            } finally {
                refreshing = false;
            }
        });
    }
});

window.addEventListener('beforeunload', () => {
    NetworkManager.closeSSE();
});

document.addEventListener('visibilitychange', () => {
    if (document.hidden) {
        LogManager.stopPolling();
    } else if (TabManager.getActiveTab() === 'logs') {
        LogManager.startPolling();
    }
});
