const NodeManager = (function() {
    return {
        renderOutboundCards: function(allNodeData) {
            const uiElements = UIManager.getElements();
            const container = uiElements.outboundsList;
            const currentChanged = XrayManager.prevCurrentOutbound !== XrayManager.currentOutbound;
            XrayManager.prevCurrentOutbound = XrayManager.currentOutbound;

            if (allNodeData.length === 0) {
                container.innerHTML = '<p class="text-white/70 text-center py-4 col-span-full">未找到可用出站</p>';
                return;
            }

            if (currentChanged) {
                container.innerHTML = '';
                for (let i = 0; i < allNodeData.length; i++) {
                    if (allNodeData[i]) {
                        container.appendChild(this.createOutboundCard(allNodeData[i]));
                    }
                }
                return;
            }

            const existingCards = {};
            const cardEls = container.querySelectorAll('[data-tag]');
            for (let j = 0; j < cardEls.length; j++) {
                existingCards[cardEls[j].getAttribute('data-tag')] = cardEls[j];
            }

            for (let k = 0; k < allNodeData.length; k++) {
                const node = allNodeData[k];
                if (!node) continue;
                const card = existingCards[node.tag];
                if (card) {
                    container.appendChild(card);
                    delete existingCards[node.tag];
                } else {
                    container.appendChild(this.createOutboundCard(node));
                }
            }

            const tags = Object.keys(existingCards);
            for (let m = 0; m < tags.length; m++) {
                container.removeChild(existingCards[tags[m]]);
            }
        },

        progressiveLoadStatuses: async function(nodes) {
            const statusMap = new Map();
            try {
                const res = await fetch('/api/outbounds-status');
                if (res.ok) {
                    const allStatuses = await safeJson(res);
                    for (let i = 0; i < allStatuses.length; i++) {
                        const st = allStatuses[i];
                        if (st && st.tag) {
                            statusMap.set(st.tag, st);
                        }
                    }
                }
            } catch (e) {
                console.error('批量加载状态失败:', e);
            }

            const results = nodes.map(function(node) {
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
            for (let i = 0; i < allStatuses.length; i++) {
                const ns = allStatuses[i];
                if (ns && ns.status && ns.status.alive && ns.status.delay > 0) {
                    if (ns.status.delay < minDelay) {
                        minDelay = ns.status.delay;
                        bestNode = ns;
                    }
                }
            }
            return bestNode;
        },

        createOutboundCard: function(node) {
            const tag = node.tag;
            const protocol = (node.protocol || 'unknown').toUpperCase();
            const isCurrent = tag === XrayManager.currentOutbound;

            const card = document.createElement('div');
            card.setAttribute('data-tag', tag);
            card.setAttribute('tabindex', '0');
            card.setAttribute('role', 'button');
            card.setAttribute('aria-label', '切换到节点 ' + tag);
            card.className = 'card-hover bg-white/10 backdrop-blur p-3 md:p-4 rounded-xl shadow-lg border ' +
                (isCurrent ? 'border-cyan-400 current-indicator' : 'border-white/20');

            const inner = document.createElement('div');

            if (isCurrent) {
                // 自动模式下仍然允许点击切换到手动模式
                if (!XrayManager.isAutoMode) {
                    card.style.cursor = 'default';
                }
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
            statusDiv.id = 'status-' + tag;
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
                // 自动模式和手动模式显示不同的标签
                const badgeText = XrayManager.isAutoMode ? ' 自动' : ' 当前';
                badge.appendChild(document.createTextNode(badgeText));
                bottomRow.appendChild(badge);
            }

            inner.appendChild(body);
            inner.appendChild(bottomRow);
            card.appendChild(inner);

            // 在自动模式下，点击当前节点可以切换为手动模式
            // 在手动模式下，点击当前节点不做任何操作
            if (!isCurrent || XrayManager.isAutoMode) {
                async function handleSwitch() {
                    try {
                        if (isCurrent && XrayManager.isAutoMode) {
                            const msg = '将退出自动均衡模式，手动锁定到节点: ' + tag + '\n之后需手动切换节点或点击「自动均衡」恢复。确认？';
                            const confirmed = typeof showModal === 'function'
                                ? await showModal(msg).catch(() => false)
                                : confirm(msg);
                            if (!confirmed) return;
                        }
                        await NetworkManager.applyOutboundChange(tag, tag, { reload: true });
                    } catch (e) {
                        console.error('切换节点失败:', e);
                        try {
                            if (typeof NotificationManager !== 'undefined' && NotificationManager.showNotification) {
                                NotificationManager.showNotification('切换节点失败: ' + (e.message || String(e) || '未知错误'), 'error');
                            }
                        } catch (_) { /* swallow */ }
                    }
                }
                card.addEventListener('click', handleSwitch);
                card.addEventListener('keydown', function(e) {
                    if (e.key === 'Enter' || e.key === ' ') {
                        e.preventDefault();
                        handleSwitch();
                    }
                });
            }

            return card;
        }
    };
})();

const TabManager = (function() {
    let activeTab = 'dashboard';

    function switchTab(tabName) {
        if (tabName === activeTab) return;
        activeTab = tabName;

        var pages = document.querySelectorAll('.page-content');
        for (var i = 0; i < pages.length; i++) {
            pages[i].style.display = 'none';
        }
        var target = $('page-' + tabName);
        if (target) target.style.display = '';

        var tabs = document.querySelectorAll('.sidebar-tab');
        for (var j = 0; j < tabs.length; j++) {
            tabs[j].classList.remove('active');
        }
        var activeBtn = $('tab-' + tabName);
        if (activeBtn) activeBtn.classList.add('active');

        if (tabName === 'logs') {
            LogManager.startPolling();
        } else {
            LogManager.stopPolling();
        }
    }

    return {
        init: function() {
            var dashTab = $('tab-dashboard');
            var logsTab = $('tab-logs');
            if (dashTab) {
                dashTab.addEventListener('click', function() { switchTab('dashboard'); });
            }
            if (logsTab) {
                logsTab.addEventListener('click', function() { switchTab('logs'); });
            }
        },
        getActiveTab: function() { return activeTab; }
    };
})();

const LogManager = (function() {
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

        var params = new URLSearchParams();
        params.set('lines', '500');
        if (currentSearch) params.set('search', currentSearch);

        try {
            var res = await fetch('/api/logs?' + params.toString());
            var data = await safeJson(res);

            if (!res.ok) {
                els.status.textContent = '加载失败';
                return;
            }

            renderLines(data.lines || []);
            els.lineCount.textContent = (data.lines ? data.lines.length : 0) + ' 行';

            var now = new Date();
            var timeStr = ('0' + now.getHours()).slice(-2) + ':' + ('0' + now.getMinutes()).slice(-2) + ':' + ('0' + now.getSeconds()).slice(-2);
            els.status.textContent = '最后更新: ' + timeStr;

        } catch (e) {
            console.error('Log fetch error:', e);
            els.status.textContent = '加载失败: ' + e.message;
        }
    }

    function renderLines(lines) {
        if (!els.container) return;

        if (lines.length === 0) {
            els.container.innerHTML = '<div class="text-white/50 text-center py-8">无日志数据</div>';
            return;
        }

        var wasAtBottom = !userScrolled;
        els.container.innerHTML = '';

        var frag = document.createDocumentFragment();
        for (var i = 0; i < lines.length; i++) {
            var div = document.createElement('div');
            div.className = 'log-line';
            if (currentSearch) {
                div.innerHTML = highlightText(lines[i], currentSearch);
            } else {
                div.textContent = lines[i];
            }
            frag.appendChild(div);
        }
        els.container.appendChild(frag);

        if (wasAtBottom) {
            els.container.scrollTop = els.container.scrollHeight;
        }
    }

    function highlightText(text, search) {
        var escaped = search.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
        var re = new RegExp('(' + escaped + ')', 'gi');
        var parts = text.split(re);
        var result = '';
        for (var i = 0; i < parts.length; i++) {
            if (i % 2 === 1) {
                result += '<span class="highlight">' + escapeHtml(parts[i]) + '</span>';
            } else {
                result += escapeHtml(parts[i]);
            }
        }
        return result;
    }

    function escapeHtml(str) {
        return str.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
    }

    function startPolling() {
        if (pollTimer) return;
        loadLogs();
        pollTimer = setInterval(function() {
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
        setEnabled: function(type) {
            enabled = true;
            logType = type;
            cacheEls();

            var logsTab = $('tab-logs');
            if (logsTab) logsTab.style.display = '';

            if (els.sourceBadge) {
                els.sourceBadge.textContent = type === 'file' ? '文件' : (type === 'journal' ? 'journal' : type);
            }

            if (els.search && !els.search._bound) {
                els.search._bound = true;
                els.search.addEventListener('input', function() {
                    if (searchTimer) clearTimeout(searchTimer);
                    var val = els.search.value;
                    searchTimer = setTimeout(function() {
                        currentSearch = val;
                        loadLogs();
                    }, 300);
                });
            }

            if (els.refreshBtn && !els.refreshBtn._bound) {
                els.refreshBtn._bound = true;
                els.refreshBtn.addEventListener('click', function() { loadLogs(); });
            }

            if (els.container && !els.container._scrollBound) {
                els.container._scrollBound = true;
                els.container.addEventListener('scroll', function() {
                    var el = els.container;
                    userScrolled = (el.scrollHeight - el.scrollTop - el.clientHeight) > 30;
                });
            }
        },

        startPolling: startPolling,
        stopPolling: stopPolling,
    };
})();

window.addEventListener('error', function(e) {
    console.error('全局错误:', e.error);
    if (typeof NotificationManager !== 'undefined') {
        NotificationManager.showNotification('发生错误: ' + e.message, 'error');
    }
});

window.addEventListener('load', function() {
    if (typeof TabManager !== 'undefined') TabManager.init();
    if (typeof NetworkManager !== 'undefined') {
        NetworkManager.loadConfig();
    }
    if (typeof connectStatsSSE === 'function') connectStatsSSE();

    const uiElements = UIManager.getElements();
    if (uiElements.resetBtn) {
        uiElements.resetBtn.addEventListener('click', async function() {
            if (typeof NetworkManager !== 'undefined') {
                await NetworkManager.applyOutboundChange('', '自动均衡', { reload: true });
            }
        });
    }

    let refreshing = false;
    if (uiElements.refreshBtn) {
        uiElements.refreshBtn.addEventListener('click', async function() {
            if (refreshing || typeof NetworkManager === 'undefined') return;
            refreshing = true;
            try {
                await NetworkManager.initializePage();
            } finally {
                refreshing = false;
            }
        });
    }
});

window.addEventListener('beforeunload', function() {
    if (typeof NetworkManager !== 'undefined') NetworkManager.closeSSE();
});

document.addEventListener('visibilitychange', function() {
    if (typeof LogManager === 'undefined') return;
    if (document.hidden) {
        LogManager.stopPolling();
    } else if (typeof TabManager !== 'undefined' && TabManager.getActiveTab() === 'logs') {
        LogManager.startPolling();
    }
});
