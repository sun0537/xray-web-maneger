const XrayManager = (function() {
    let currentOutbound = '';
    let prevCurrentOutbound = '';
    let balancerTag = 'balancer';
    let didInitialAutoSelect = false;
    let fixedNodeOrder = [];
    let pageReady = false;

    return {
        getCurrentOutbound: function() { return currentOutbound; },
        setCurrentOutbound: function(v) { currentOutbound = v; },
        getPrevCurrentOutbound: function() { return prevCurrentOutbound; },
        setPrevCurrentOutbound: function(v) { prevCurrentOutbound = v; },
        getBalancerTag: function() { return balancerTag; },
        setBalancerTag: function(v) { balancerTag = v; },
        getDidInitialAutoSelect: function() { return didInitialAutoSelect; },
        setDidInitialAutoSelect: function(v) { didInitialAutoSelect = v; },
        getFixedNodeOrder: function() { return fixedNodeOrder; },
        setFixedNodeOrder: function(v) { fixedNodeOrder = v; },
        getPageReady: function() { return pageReady; },
        setPageReady: function(v) { pageReady = v; }
    };
})();

const $ = (id) => document.getElementById(id);

function escapeHtml(str) {
    const div = document.createElement('div');
    div.textContent = str;
    return div.innerHTML;
}

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
        getElements: function() { return els; },

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
                var alive = status.alive;
                var delay = status.delay;
                statusEl.innerHTML = '';
                if (alive && (delay === undefined || delay === null || delay === 0)) {
                    statusEl.className = 'flex items-center gap-2 text-xs text-yellow-400';
                    var pulseWrap = document.createElement('span');
                    pulseWrap.className = 'flex h-2 w-2 relative';
                    var pulseAnim = document.createElement('span');
                    pulseAnim.className = 'animate-pulse absolute inline-flex h-2 w-2 rounded-full bg-yellow-500 opacity-75';
                    var pulseDot = document.createElement('span');
                    pulseDot.className = 'relative inline-flex rounded-full h-2 w-2 bg-yellow-500';
                    pulseWrap.appendChild(pulseAnim);
                    pulseWrap.appendChild(pulseDot);
                    var label = document.createElement('span');
                    label.className = 'font-mono font-semibold text-yellow-400';
                    label.textContent = '\u68C0\u6D4B\u4E2D...';
                    statusEl.appendChild(pulseWrap);
                    statusEl.appendChild(label);
                    return;
                }
                var color = alive ? 'bg-green-500' : 'bg-red-500';
                var statusColor = alive ? 'text-green-400' : 'text-red-400';
                statusEl.className = 'flex items-center gap-2 text-xs ' + statusColor;
                var pingWrap = document.createElement('span');
                pingWrap.className = 'flex h-2 w-2 relative';
                var pingAnim = document.createElement('span');
                pingAnim.className = 'animate-ping absolute inline-flex h-2 w-2 rounded-full ' + color + ' opacity-75';
                var pingDot = document.createElement('span');
                pingDot.className = 'relative inline-flex rounded-full h-2 w-2 ' + color;
                pingWrap.appendChild(pingAnim);
                pingWrap.appendChild(pingDot);
                var delayLabel = document.createElement('span');
                delayLabel.className = 'font-mono font-semibold';
                delayLabel.textContent = (delay || 0) + ' ms';
                statusEl.appendChild(pingWrap);
                statusEl.appendChild(delayLabel);
            } else {
                statusEl.innerHTML = '';
                statusEl.className = 'flex items-center gap-2 text-xs text-white/70';
                var failLabel = document.createElement('span');
                failLabel.textContent = '\u25CB \u68C0\u6D4B\u5931\u8D25';
                statusEl.appendChild(failLabel);
            }
        },

        updateCurrentDisplay: function(auto, current) {
            const el = els.currentTag;
            while (el.firstChild) el.removeChild(el.firstChild);
            const span = document.createElement('span');
            if (auto || !current) {
                span.className = 'text-orange-300';
                span.textContent = '\uD83D\uDD04 \u81EA\u52A8\u5747\u8861\u6A21\u5F0F';
            } else {
                span.className = 'text-green-300';
                span.textContent = '\u2713 ' + current;
            }
            el.appendChild(span);
        },

        updateErrorDisplay: function() {
            const el = els.currentTag;
            while (el.firstChild) el.removeChild(el.firstChild);
            const span = document.createElement('span');
            span.className = 'text-red-300';
            span.textContent = '\u2717 \u83B7\u53D6\u5931\u8D25';
            el.appendChild(span);
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
                notificationEl.setAttribute('role', 'alert');
                notificationEl.className = 'fixed top-4 right-4 text-white px-6 py-3 rounded-lg shadow-2xl z-50 transition-all duration-300';
                notificationEl.style.display = 'none';
                document.body.appendChild(notificationEl);
            }
            if (notificationTimer) clearTimeout(notificationTimer);
            notificationEl.className = 'fixed top-4 right-4 text-white px-6 py-3 rounded-lg shadow-2xl z-50 transition-all duration-300 ' +
                (type === 'success' ? 'bg-green-500' : 'bg-red-500');
            notificationEl.textContent = message;
            notificationEl.style.display = '';
            notificationTimer = setTimeout(function() { notificationEl.style.display = 'none'; }, 3000);
        }
    };
})();

window.addEventListener('error', function(e) {
    console.error('Global error:', e.error);
    NotificationManager.showNotification('\u53D1\u751F\u9519\u8BEF: ' + e.message, 'error');
});

window.addEventListener('load', function() {
    NetworkManager.loadConfig();
    connectStatsSSE();

    var uiElements = UIManager.getElements();
    uiElements.resetBtn.addEventListener('click', function() {
        NetworkManager.applyOutboundChange('', '\u81EA\u52A8\u5747\u8861', { reload: true });
    });

    var refreshing = false;
    uiElements.refreshBtn.addEventListener('click', async function() {
        if (refreshing) return;
        refreshing = true;
        try {
            XrayManager.setDidInitialAutoSelect(true);
            await NetworkManager.initializePage();
        } finally {
            refreshing = false;
        }
    });
});

window.addEventListener('beforeunload', function() {
    if (window._sseEventSource) {
        window._sseEventSource.close();
    }
});

const NetworkManager = (function() {
    async function safeJson(response) {
        var contentType = response.headers.get('content-type') || '';
        if (contentType.indexOf('application/json') !== -1) {
            return await response.json();
        }
        throw new Error('Server returned non-JSON response (' + response.status + ')');
    }

    return {
        loadConfig: async function() {
            try {
                var response = await fetch('/api/config');
                if (response.ok) {
                    var config = await safeJson(response);
                    XrayManager.setBalancerTag(config.balancer_tag);
                    UIManager.getElements().balancerTag.textContent = XrayManager.getBalancerTag();
                }
            } catch (error) {
                console.error('Error loading config:', error);
            }
            await this.initializePage();
        },

        fetchOutboundList: async function() {
            try {
                var response = await fetch('/api/outbounds');
                if (!response.ok) throw new Error('Failed to fetch outbounds');
                var outbounds = await safeJson(response);
                if (outbounds.length === 0) return [];
                return outbounds;
            } catch (error) {
                console.error('Error loading outbounds:', error);
                UIManager.getElements().outboundsList.innerHTML =
                    '<p class="text-red-400 text-center py-4 col-span-full">\u274C \u52A0\u8F7D\u51FA\u7AD9\u5217\u8868\u5931\u8D25</p>';
                return [];
            }
        },

        loadCurrentOutbound: async function() {
            try {
                var response = await fetch('/api/current-outbound');
                if (!response.ok) throw new Error('Failed to fetch current outbound');
                var data = await safeJson(response);
                XrayManager.setPrevCurrentOutbound(XrayManager.getCurrentOutbound());
                if (data.auto || !data.current) {
                    XrayManager.setCurrentOutbound('');
                } else {
                    XrayManager.setCurrentOutbound(data.current);
                }
                UIManager.updateCurrentDisplay(data.auto, data.current);
                return data;
            } catch (error) {
                console.error('Error loading current outbound:', error);
                UIManager.updateErrorDisplay();
                return { auto: true, current: '' };
            }
        },

        applyOutboundChange: async function(outboundTag, displayName, options) {
            var opts = options || { reload: true };
            try {
                var response = await fetch('/api/switch-outbound', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({ outbound_tag: outboundTag })
                });
                if (!response.ok) {
                    var errData;
                    try { errData = await response.json(); } catch(e) { errData = null; }
                    throw new Error((errData && errData.error) || 'Failed to apply change');
                }
                NotificationManager.showNotification('\u2713 \u5DF2\u5207\u6362\u5230: ' + (displayName || '\u81EA\u52A8\u5747\u8861'), 'success');
                if (opts.reload) {
                    setTimeout(function() { NetworkManager.initializePage(); }, 500);
                }
            } catch (error) {
                console.error('Error applying change:', error);
                NotificationManager.showNotification('\u2717 \u5207\u6362\u5931\u8D25: ' + error.message, 'error');
            }
        },

        initializePage: async function() {
            XrayManager.setPageReady(false);

            var results = await Promise.all([
                this.loadCurrentOutbound(),
                this.fetchOutboundList()
            ]);
            var currentStatus = results[0];
            var newNodeData = results[1];

            var orderedNodeData;
            if (XrayManager.getFixedNodeOrder().length === 0) {
                newNodeData.sort(function(a, b) { return a.tag.localeCompare(b.tag); });
                XrayManager.setFixedNodeOrder(newNodeData.map(function(n) { return n.tag; }));
                orderedNodeData = newNodeData;
            } else {
                var nodeMap = new Map(newNodeData.map(function(n) { return [n.tag, n]; }));
                orderedNodeData = [];
                var order = XrayManager.getFixedNodeOrder();
                for (var i = 0; i < order.length; i++) {
                    var node = nodeMap.get(order[i]);
                    if (node) {
                        orderedNodeData.push(node);
                        nodeMap.delete(order[i]);
                    }
                }
                var newNodes = Array.from(nodeMap.values());
                if (newNodes.length > 0) {
                    newNodes.sort(function(a, b) { return a.tag.localeCompare(b.tag); });
                    orderedNodeData.push.apply(orderedNodeData, newNodes);
                    XrayManager.setFixedNodeOrder(
                        XrayManager.getFixedNodeOrder().concat(newNodes.map(function(n) { return n.tag; }))
                    );
                }
            }

            NodeManager.renderOutboundCards(orderedNodeData);

            var allStatuses = await NodeManager.progressiveLoadStatuses(orderedNodeData);

            var bestNode = NodeManager.findBestNode(allStatuses);
            if (bestNode && currentStatus.auto && !XrayManager.getDidInitialAutoSelect()) {
                console.log('\u81EA\u52A8\u5207\u6362\u5230\u6700\u4F73\u8282\u70B9: ' + bestNode.tag + ' (\u5EF6\u8FDF: ' + bestNode.status.delay + 'ms)');
                await this.applyOutboundChange(bestNode.tag, bestNode.tag, { reload: false });
                XrayManager.setDidInitialAutoSelect(true);
                await this.loadCurrentOutbound();
                NodeManager.renderOutboundCards(orderedNodeData);
                await NodeManager.progressiveLoadStatuses(orderedNodeData);
            }

            XrayManager.setPageReady(true);
        }
    };
})();

function connectStatsSSE() {
    console.log('\u6B63\u5728\u8FDE\u63A5\u5230 /api/stats-sse...');
    var evtSource = new EventSource('/api/stats-sse');
    window._sseEventSource = evtSource;

    var statsContainer = document.getElementById('stats-container');
    var statusBanner = document.createElement('div');
    statusBanner.id = 'sse-status-banner';
    statusBanner.style.display = 'none';
    statusBanner.className = 'text-center text-xs py-1 rounded-t-xl transition-colors duration-300';
    statsContainer.parentNode.insertBefore(statusBanner, statsContainer);

    function hideBanner() {
        statusBanner.style.display = 'none';
        statsContainer.style.opacity = '1';
    }

    function showBanner() {
        statusBanner.textContent = '\u26A0 \u5B9E\u65F6\u6570\u636E\u8FDE\u63A5\u65AD\u5F00\uFF0C\u6B63\u5728\u91CD\u8FDE...';
        statusBanner.style.display = '';
        statusBanner.className = 'text-center text-xs py-1 rounded-t-xl bg-yellow-600/50 text-yellow-200';
        statsContainer.style.opacity = '0.7';
    }

    evtSource.addEventListener('update', function(event) {
        try {
            var data = JSON.parse(event.data);
            UIManager.updateStatsUI(data);
            if (XrayManager.getPageReady() && data.outbounds && Array.isArray(data.outbounds)) {
                UIManager.updateAllCardStatuses(data.outbounds);
            }
            hideBanner();
        } catch (error) {
            console.error('\u89E3\u6790 SSE \u6570\u636E\u5931\u8D25:', error);
        }
    });

    evtSource.onopen = function() {
        console.log('SSE \u8FDE\u63A5\u5DF2\u5EFA\u7ACB');
        hideBanner();
    };

    evtSource.onerror = function() {
        if (evtSource.readyState === EventSource.CLOSED) {
            console.warn('SSE \u8FDE\u63A5\u5DF2\u5173\u95ED');
            showBanner();
        } else {
            console.warn('SSE \u8FDE\u63A5\u9519\u8BEF\uFF0C\u7B49\u5F85\u91CD\u8FDE...');
            setTimeout(function() {
                if (evtSource.readyState !== EventSource.OPEN) {
                    showBanner();
                }
            }, 1000);
        }
    };
}

const NodeManager = (function() {
    return {
        renderOutboundCards: function(allNodeData) {
            var uiElements = UIManager.getElements();
            var container = uiElements.outboundsList;
            var currentChanged = XrayManager.getPrevCurrentOutbound() !== XrayManager.getCurrentOutbound();
            XrayManager.setPrevCurrentOutbound(XrayManager.getCurrentOutbound());

            if (allNodeData.length === 0) {
                container.innerHTML = '<p class="text-white/70 text-center py-4 col-span-full">\u672A\u627E\u5230\u53EF\u7528\u51FA\u7AD9</p>';
                return;
            }

            if (currentChanged) {
                container.innerHTML = '';
                for (var i = 0; i < allNodeData.length; i++) {
                    if (allNodeData[i]) {
                        container.appendChild(this.createOutboundCard(allNodeData[i]));
                    }
                }
                return;
            }

            var existingCards = {};
            var cardEls = container.querySelectorAll('[data-tag]');
            for (var j = 0; j < cardEls.length; j++) {
                existingCards[cardEls[j].getAttribute('data-tag')] = cardEls[j];
            }

            for (var k = 0; k < allNodeData.length; k++) {
                var node = allNodeData[k];
                if (!node) continue;
                var card = existingCards[node.tag];
                if (card) {
                    container.appendChild(card);
                    delete existingCards[node.tag];
                } else {
                    container.appendChild(this.createOutboundCard(node));
                }
            }

            var tags = Object.keys(existingCards);
            for (var m = 0; m < tags.length; m++) {
                container.removeChild(existingCards[tags[m]]);
            }
        },

        progressiveLoadStatuses: async function(nodes) {
            var statusMap = new Map();
            try {
                var res = await fetch('/api/outbounds-status');
                if (res.ok) {
                    var allStatuses = await res.json();
                    for (var i = 0; i < allStatuses.length; i++) {
                        var st = allStatuses[i];
                        if (st && st.tag) {
                            statusMap.set(st.tag, st);
                        }
                    }
                }
            } catch (e) {
                console.error('\u6279\u91CF\u52A0\u8F7D\u72B6\u6001\u5931\u8D25:', e);
            }

            var results = nodes.map(function(node) {
                var status = statusMap.get(node.tag);
                if (!status) {
                    status = { alive: false, delay: 0, error: 'not_found' };
                }
                UIManager.updateCardStatus(node.tag, status);
                return { tag: node.tag, status: status };
            });
            return results;
        },

        findBestNode: function(allStatuses) {
            var bestNode = null;
            var minDelay = Infinity;
            for (var i = 0; i < allStatuses.length; i++) {
                var ns = allStatuses[i];
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
            var tag = node.tag;
            var protocol = (node.protocol || 'unknown').toUpperCase();
            var isCurrent = tag === XrayManager.getCurrentOutbound();

            var card = document.createElement('div');
            card.setAttribute('data-tag', tag);
            card.setAttribute('tabindex', '0');
            card.setAttribute('role', 'button');
            card.setAttribute('aria-label', '\u5207\u6362\u5230\u8282\u70B9 ' + tag);
            card.className = 'card-hover bg-white/10 backdrop-blur p-4 rounded-xl shadow-lg border ' +
                (isCurrent ? 'border-cyan-400 current-indicator' : 'border-white/20');

            var inner = document.createElement('div');
            inner.className = 'relative';

            if (isCurrent) {
                var badge = document.createElement('div');
                badge.className = 'absolute top-2 right-2 bg-cyan-500/90 text-white px-2 py-1 rounded-md text-xs font-bold flex items-center gap-1';
                var dot = document.createElement('span');
                dot.className = 'pulse-ring';
                dot.textContent = '\u25CF';
                badge.appendChild(dot);
                badge.appendChild(document.createTextNode(' \u5F53\u524D'));
                inner.appendChild(badge);
                card.style.cursor = 'default';
            }

            var body = document.createElement('div');
            body.className = 'mb-3';

            var tagDiv = document.createElement('div');
            tagDiv.className = 'font-mono text-lg font-bold text-white mb-1 truncate';
            tagDiv.textContent = tag;
            tagDiv.title = tag;

            var protoDiv = document.createElement('div');
            protoDiv.className = 'text-xs text-cyan-200 font-semibold mb-2';
            protoDiv.textContent = protocol;

            body.appendChild(tagDiv);
            body.appendChild(protoDiv);

            var statusDiv = document.createElement('div');
            statusDiv.id = 'status-' + tag;
            statusDiv.className = 'flex items-center gap-2 text-xs text-white/70';
            statusDiv.textContent = '\u25CB \u6B63\u5728 PING...';

            inner.appendChild(body);
            inner.appendChild(statusDiv);
            card.appendChild(inner);

            if (!isCurrent) {
                card.addEventListener('click', function() {
                    NetworkManager.applyOutboundChange(tag, tag, { reload: true });
                });
                card.addEventListener('keydown', function(e) {
                    if (e.key === 'Enter' || e.key === ' ') {
                        e.preventDefault();
                        NetworkManager.applyOutboundChange(tag, tag, { reload: true });
                    }
                });
            }

            return card;
        }
    };
})();

function formatBytes(bytes, decimals) {
    if (decimals === undefined) decimals = 0;
    if (typeof bytes !== 'number' || isNaN(bytes)) return '0 Bytes';
    if (bytes === 0) return '0 Bytes';
    var k = 1024;
    var sizes = ['Bytes', 'KB', 'MB', 'GB', 'TB', 'PB'];
    var i = Math.floor(Math.log(bytes) / Math.log(k));
    return parseFloat((bytes / Math.pow(k, i)).toFixed(decimals)) + ' ' + sizes[i];
}

function formatDuration(totalSeconds) {
    if (typeof totalSeconds !== 'number' || isNaN(totalSeconds)) return '0s';
    if (totalSeconds <= 0) return '0s';
    var remaining = totalSeconds;
    var days = Math.floor(remaining / 86400);
    remaining %= 86400;
    var hours = Math.floor(remaining / 3600);
    remaining %= 3600;
    var minutes = Math.floor(remaining / 60);
    var seconds = remaining % 60;
    var result = '';
    if (days > 0) result += days + 'd ';
    if (hours > 0) result += hours + 'h ';
    if (minutes > 0) result += minutes + 'm ';
    if (seconds > 0) result += seconds + 's';
    return result.trim();
}
