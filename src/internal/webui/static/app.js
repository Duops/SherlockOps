document.addEventListener('DOMContentLoaded', function () {
    var state = {
        alerts: [],
        expandedFingerprint: null,
        filterSource: '',
        filterSeverity: '',
        filterStatus: '',
        searchName: '',
    };

    var refreshInterval = 30000;
    var timer = null;

    var alertsBody = document.getElementById('alerts-body');
    var searchInput = document.getElementById('search-name');
    var filterSource = document.getElementById('filter-source');
    var filterSeverity = document.getElementById('filter-severity');
    var filterStatus = document.getElementById('filter-status');
    var healthDot = document.getElementById('health-dot');
    var healthText = document.getElementById('health-text');

    function fetchJSON(url) {
        return fetch(url).then(function (res) { return res.json(); });
    }

    function loadStats() {
        fetchJSON('/ui/api/stats').then(function (data) {
            document.getElementById('stat-total').textContent = data.total_count || 0;
            document.getElementById('stat-resolved').textContent = data.resolved_count || 0;
            var active = (data.total_count || 0) - (data.resolved_count || 0);
            document.getElementById('stat-active').textContent = active >= 0 ? active : 0;
            document.getElementById('stat-avg-length').textContent = Math.round(data.avg_text_length || 0);
        }).catch(function () {
            // stats unavailable
        });
    }

    function loadHealth() {
        fetchJSON('/health/ready').then(function (data) {
            if (data.status === 'ok') {
                healthDot.className = 'health-dot';
                healthText.textContent = 'Healthy';
            } else {
                healthDot.className = 'health-dot degraded';
                healthText.textContent = 'Degraded';
            }
        }).catch(function () {
            healthDot.className = 'health-dot down';
            healthText.textContent = 'Unreachable';
        });
    }

    function loadAlerts() {
        fetchJSON('/ui/api/alerts?limit=50').then(function (data) {
            state.alerts = data.alerts || [];
            populateSources();
            renderAlerts();
        }).catch(function () {
            alertsBody.innerHTML = '<tr><td colspan="6" class="empty-state">Failed to load alerts</td></tr>';
        });
    }

    function populateSources() {
        var sources = {};
        state.alerts.forEach(function (a) {
            if (a.source) sources[a.source] = true;
        });
        var current = state.filterSource || filterSource.value;
        filterSource.innerHTML = '<option value="">All Sources</option>';
        Object.keys(sources).sort().forEach(function (s) {
            var opt = document.createElement('option');
            opt.value = s;
            opt.textContent = s;
            if (s === current) opt.selected = true;
            filterSource.appendChild(opt);
        });
    }

    function matchesFilters(alert) {
        if (state.filterSource && (alert.source || '') !== state.filterSource) return false;
        if (state.filterSeverity) {
            var sev = alert.severity || extractSeverity(alert);
            if (sev !== state.filterSeverity) return false;
        }
        if (state.filterStatus) {
            var isResolved = alert.resolved_at && alert.resolved_at !== '';
            if (state.filterStatus === 'resolved' && !isResolved) return false;
            if (state.filterStatus === 'firing' && isResolved) return false;
        }
        if (state.searchName) {
            var term = state.searchName.toLowerCase();
            var haystack = [
                alert.alert_fingerprint || '',
                alert.alert_name || '',
                alert.source || '',
                alert.text || ''
            ].join(' ').toLowerCase();
            if (haystack.indexOf(term) === -1) return false;
        }
        return true;
    }

    function formatTime(isoStr) {
        if (!isoStr) return '-';
        var d = new Date(isoStr);
        return d.toLocaleString();
    }

    function escapeHtml(str) {
        var div = document.createElement('div');
        div.appendChild(document.createTextNode(str || ''));
        return div.innerHTML;
    }

    function extractSeverity(alert) {
        // Try to extract severity from the analysis text or tools_used
        var text = (alert.text || '').toLowerCase();
        if (text.indexOf('critical') !== -1) return 'critical';
        if (text.indexOf('warning') !== -1) return 'warning';
        return 'info';
    }

    // formatToolsTrace mirrors formatToolsTraceFromResult() in Go:
    // renders a compact "kubernetes ✓(5)  victoriametrics ✓(5)" trace, and
    // appends " | 33.1k tokens ~$0.118" when token/cost data is available.
    function formatToolsTrace(alert) {
        var parts = [];
        var trace = alert.tools_trace;
        if (trace && trace.length > 0) {
            trace.forEach(function (t) {
                var mark = t.success ? '\u2713' : '\u2717';
                if (t.call_count > 0) {
                    parts.push(t.name + ' ' + mark + '(' + t.call_count + ')');
                } else {
                    parts.push(t.name + ' ' + mark);
                }
            });
        } else if (alert.tools_used && alert.tools_used.length > 0) {
            // Fallback: group by "<category>_<rest>" prefix, like cached results.
            var counts = {};
            alert.tools_used.forEach(function (t) {
                var cat = t.indexOf('_') >= 0 ? t.split('_')[0] : t;
                counts[cat] = (counts[cat] || 0) + 1;
            });
            Object.keys(counts).sort().forEach(function (cat) {
                parts.push(cat + ' \u2713(' + counts[cat] + ')');
            });
        }
        if (parts.length === 0) return '-';
        var out = parts.join('  ');
        if (alert.total_tokens && alert.total_tokens > 0) {
            out += ' | ' + formatTokenCount(alert.total_tokens) + ' tokens';
            if (alert.cost_usd && alert.cost_usd > 0) {
                out += ' ' + formatCost(alert.cost_usd);
            }
        }
        return out;
    }

    function formatTokenCount(n) {
        if (n >= 1000) return (n / 1000).toFixed(1) + 'k';
        return String(n);
    }

    function formatCost(usd) {
        if (!usd || usd <= 0) return '';
        if (usd < 0.01) return '~$' + usd.toFixed(4);
        return '~$' + usd.toFixed(3);
    }

    function renderAlerts() {
        var filtered = state.alerts.filter(matchesFilters);

        if (filtered.length === 0) {
            alertsBody.innerHTML = '<tr><td colspan="7" class="empty-state">No alerts found</td></tr>';
            return;
        }

        var html = '';
        filtered.forEach(function (alert) {
            var isResolved = alert.resolved_at && alert.resolved_at !== '';
            var severity = alert.severity || extractSeverity(alert);
            var status = isResolved ? 'resolved' : 'firing';
            var expanded = state.expandedFingerprint === alert.alert_fingerprint;
            var toolsStr = formatToolsTrace(alert);
            var costStr = formatCost(alert.cost_usd) || '-';

            html += '<tr onclick="toggleAlert(\'' + escapeHtml(alert.alert_fingerprint) + '\')">';
            html += '<td>' + formatTime(alert.cached_at) + '</td>';
            html += '<td>' + escapeHtml(alert.source || '-') + '</td>';
            html += '<td>' + escapeHtml(alert.alert_name || alert.alert_fingerprint) + '</td>';
            html += '<td><span class="severity-badge severity-' + severity + '">' + severity + '</span></td>';
            html += '<td><span class="status-badge status-' + status + '">' + status + '</span></td>';
            html += '<td class="fingerprint">' + escapeHtml(toolsStr) + '</td>';
            html += '<td>' + escapeHtml(costStr) + '</td>';
            html += '</tr>';

            if (expanded) {
                html += '<tr class="analysis-row"><td colspan="7">';
                html += '<div class="analysis-content">' + escapeHtml(alert.text) + '</div>';
                html += '</td></tr>';
            }
        });

        alertsBody.innerHTML = html;
    }

    window.toggleAlert = function (fingerprint) {
        if (state.expandedFingerprint === fingerprint) {
            state.expandedFingerprint = null;
        } else {
            state.expandedFingerprint = fingerprint;
        }
        renderAlerts();
    };

    searchInput.addEventListener('input', function () {
        state.searchName = this.value;
        renderAlerts();
    });

    filterSource.addEventListener('change', function () {
        state.filterSource = this.value;
        renderAlerts();
    });

    filterSeverity.addEventListener('change', function () {
        state.filterSeverity = this.value;
        renderAlerts();
    });

    filterStatus.addEventListener('change', function () {
        state.filterStatus = this.value;
        renderAlerts();
    });

    function refresh() {
        loadHealth();
        loadStats();
        loadAlerts();
    }

    refresh();
    timer = setInterval(refresh, refreshInterval);
});
