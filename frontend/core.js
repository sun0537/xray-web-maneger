const XrayManager = {
    currentOutbound: '',
    prevCurrentOutbound: '',
    balancerTag: 'balancer',
    didInitialAutoSelect: false,
    fixedNodeOrder: [],
    pageReady: false,
    isAutoMode: true, // 是否处于自动均衡模式
};

const $ = (id) => document.getElementById(id);

const STATUS_CLASSES = {
    checking: {
        container: 'flex items-center gap-2 text-xs text-yellow-400',
        dotAnim:   'animate-pulse absolute inline-flex h-2 w-2 rounded-full bg-yellow-500 opacity-75',
        dot:       'relative inline-flex rounded-full h-2 w-2 bg-yellow-500',
        label:     'font-mono font-semibold text-yellow-400',
    },
    alive: {
        container: 'flex items-center gap-2 text-xs text-green-400',
        dotAnim:   'animate-ping absolute inline-flex h-2 w-2 rounded-full bg-green-500 opacity-75',
        dot:       'relative inline-flex rounded-full h-2 w-2 bg-green-500',
        label:     'font-mono font-semibold',
    },
    dead: {
        container: 'flex items-center gap-2 text-xs text-red-400',
        dotAnim:   'animate-ping absolute inline-flex h-2 w-2 rounded-full bg-red-500 opacity-75',
        dot:       'relative inline-flex rounded-full h-2 w-2 bg-red-500',
        label:     'font-mono font-semibold',
    },
    error: {
        container: 'flex items-center gap-2 text-xs text-white/70',
    },
};

const UIManager = (function() {
    const els = {
        uplink: $('stats-uplink'),
        downlink: $('stats-downlink'),
        uplinkSpeed: $('stats-uplink-speed'),
        downlinkSpeed: $('stats-downlink-speed'),
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

            var upBps = typeof data.uplink_bps === 'number' ? data.uplink_bps : 0;
            var downBps = typeof data.downlink_bps === 'number' ? data.downlink_bps : 0;
            els.uplinkSpeed.textContent = upBps > 0 ? ('↑ ' + formatBytes(upBps) + '/s') : '';
            els.downlinkSpeed.textContent = downBps > 0 ? ('↓ ' + formatBytes(downBps) + '/s') : '';

            if (data.degraded) {
                els.uplink.title = '数据可能不完整 (部分数据源不可用)';
                els.downlink.title = '数据可能不完整 (部分数据源不可用)';
            } else {
                els.uplink.title = '';
                els.downlink.title = '';
            }
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
            if (!statusEl) return;
            this.performUIUpdate(statusEl, status);
        },

        performUIUpdate: function(statusEl, status) {
            if (status && status.error !== 'not_found' && status.error !== 'fetch_failed') {
                const alive = status.alive;
                const delay = status.delay;
                const stateKey = alive + '|' + delay;
                if (statusEl._lastState === stateKey) return;
                statusEl._lastState = stateKey;

                if (!statusEl._dot) {
                    statusEl.textContent = '';
                    const dotWrap = document.createElement('span');
                    dotWrap.className = 'flex h-2 w-2 relative';
                    const dotAnim = document.createElement('span');
                    const dotDot = document.createElement('span');
                    dotWrap.appendChild(dotAnim);
                    dotWrap.appendChild(dotDot);
                    const label = document.createElement('span');
                    label.className = 'font-mono font-semibold';
                    statusEl.appendChild(dotWrap);
                    statusEl.appendChild(label);
                    statusEl._dotWrap = dotWrap;
                    statusEl._dotAnim = dotAnim;
                    statusEl._dot = dotDot;
                    statusEl._label = label;
                }

                let theme;
                if (alive && (delay === undefined || delay === null || delay === 0)) {
                    theme = STATUS_CLASSES.checking;
                    statusEl._label.textContent = '检测中...';
                } else {
                    theme = alive ? STATUS_CLASSES.alive : STATUS_CLASSES.dead;
                    statusEl._label.textContent = (delay || 0) + ' ms';
                }
                statusEl.className = theme.container;
                statusEl._dotAnim.className = theme.dotAnim;
                statusEl._dot.className = theme.dot;
                statusEl._label.className = theme.label;
            } else {
                if (statusEl._lastState === 'error') return;
                statusEl._lastState = 'error';
                statusEl.textContent = '';
                statusEl.className = STATUS_CLASSES.error.container;
                const failLabel = document.createElement('span');
                failLabel.textContent = '○ 检测失败';
                statusEl.appendChild(failLabel);
                statusEl._dot = null;
            }
        },

        updateCurrentDisplay: function(auto, current, activeNode) {
            const el = els.currentTag;
            const stateKey = auto + '|' + current + '|' + (activeNode || '');
            if (el._lastState === stateKey) return;
            el._lastState = stateKey;
            if (!el._span) {
                el.textContent = '';
                el._span = document.createElement('span');
                el._span.style.display = 'inline-flex';
                el._span.style.alignItems = 'center';
                el._span.style.gap = '0.375rem';
                el.appendChild(el._span);
            }
            const span = el._span;
            if (auto || !current) {
                // 自动模式下显示实际使用的节点
                if (activeNode) {
                    span.className = 'text-orange-300';
                    span.textContent = '';
                    const arrow = document.createTextNode('自动均衡 →');
                    const nodeSpan = document.createElement('span');
                    nodeSpan.className = 'text-cyan-300 font-bold';
                    nodeSpan.textContent = activeNode;
                    span.appendChild(arrow);
                    span.appendChild(nodeSpan);
                } else {
                    span.className = 'text-orange-300/70';
                    span.textContent = '自动均衡 - 连接中...';
                }
            } else {
                span.className = 'text-green-300';
                span.textContent = '✓ ' + current;
            }
        },

        updateErrorDisplay: function() {
            const el = els.currentTag;
            if (el._lastState === 'error') return;
            el._lastState = 'error';
            if (!el._span) {
                el.textContent = '';
                el._span = document.createElement('span');
                el.appendChild(el._span);
            }
            el._span.className = 'text-red-300';
            el._span.textContent = '✗ 获取失败';
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

function showModal(message) {
    return new Promise(function(resolve) {
        const overlay = document.createElement('div');
        overlay.className = 'fixed inset-0 bg-black/60 backdrop-blur-sm flex items-center justify-center z-50';

        const modal = document.createElement('div');
        modal.className = 'bg-gray-800 border border-white/20 rounded-2xl p-6 max-w-sm mx-4 shadow-2xl';

        const msgEl = document.createElement('p');
        msgEl.className = 'text-white text-sm mb-6 whitespace-pre-line';
        msgEl.textContent = message;

        const btnWrap = document.createElement('div');
        btnWrap.className = 'flex gap-3 justify-end';

        const cancelBtn = document.createElement('button');
        cancelBtn.className = 'px-4 py-2 rounded-lg bg-white/10 hover:bg-white/20 text-white text-sm transition duration-200';
        cancelBtn.textContent = '取消';

        const confirmBtn = document.createElement('button');
        confirmBtn.className = 'px-4 py-2 rounded-lg bg-cyan-500 hover:bg-cyan-600 text-white text-sm font-semibold transition duration-200';
        confirmBtn.textContent = '确认';

        btnWrap.appendChild(cancelBtn);
        btnWrap.appendChild(confirmBtn);
        modal.appendChild(msgEl);
        modal.appendChild(btnWrap);
        overlay.appendChild(modal);
        document.body.appendChild(overlay);

        function cleanup(result) {
            document.body.removeChild(overlay);
            resolve(result);
        }

        cancelBtn.addEventListener('click', function() { cleanup(false); });
        confirmBtn.addEventListener('click', function() { cleanup(true); });
        overlay.addEventListener('click', function(e) {
            if (e.target === overlay) cleanup(false);
        });
    });
}

async function safeJson(response) {
    const contentType = response.headers.get('content-type') || '';
    if (contentType.indexOf('application/json') !== -1) {
        return await response.json();
    }
    throw new Error('Server returned non-JSON response (' + response.status + ')');
}

function showRetryUI(container, message, retryFn, countdown) {
    if (countdown === undefined) countdown = 5;
    if (container._retryTimer) {
        clearInterval(container._retryTimer);
        container._retryTimer = null;
    }
    let remaining = countdown;

    container.innerHTML = '';
    const wrapper = document.createElement('div');
    wrapper.className = 'text-center py-4 col-span-full';

    const msgEl = document.createElement('p');
    msgEl.className = 'text-red-400 mb-3';

    const retryBtn = document.createElement('button');
    retryBtn.className = 'bg-cyan-500 hover:bg-cyan-600 text-white text-sm px-4 py-2 rounded-lg transition duration-200';

    wrapper.appendChild(msgEl);
    wrapper.appendChild(retryBtn);
    container.appendChild(wrapper);

    function updateDisplay() {
        msgEl.textContent = '❌ ' + message;
        retryBtn.textContent = '↻ 重试 (' + remaining + 's)';
    }

    function doRetry() {
        if (container._retryTimer) {
            clearInterval(container._retryTimer);
            container._retryTimer = null;
        }
        container.innerHTML = '<div class="text-white/70 text-center py-4 col-span-full">正在重试...</div>';
        retryFn();
    }

    updateDisplay();
    container._retryTimer = setInterval(function() {
        remaining--;
        if (remaining <= 0) {
            doRetry();
        } else {
            updateDisplay();
        }
    }, 1000);

    retryBtn.addEventListener('click', function() {
        doRetry();
    });
}

function formatBytes(bytes, decimals) {
    if (decimals === undefined) decimals = 0;
    if (typeof bytes !== 'number' || isNaN(bytes) || bytes < 0) return '0 Bytes';
    if (bytes === 0) return '0 Bytes';
    const k = 1024;
    const sizes = ['Bytes', 'KB', 'MB', 'GB', 'TB', 'PB'];
    const i = Math.floor(Math.log(bytes) / Math.log(k));
    return parseFloat((bytes / Math.pow(k, i)).toFixed(decimals)) + ' ' + sizes[i];
}

function formatDuration(totalSeconds) {
    if (typeof totalSeconds !== 'number' || isNaN(totalSeconds)) return '0s';
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
