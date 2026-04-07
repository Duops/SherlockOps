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
            // source is derived from labels or fingerprint prefix if available
            if (a.source) sources[a.source] = true;
        });
        var current = filterSource.value;
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
        if (state.filterSource && alert.source !== state.filterSource) return false;
        if (state.filterSeverity && alert.severity !== state.filterSeverity) return false;
        if (state.filterStatus) {
            var isResolved = alert.resolved_at && alert.resolved_at !== '';
            if (state.filterStatus === 'resolved' && !isResolved) return false;
            if (state.filterStatus === 'firing' && isResolved) return false;
        }
        if (state.searchName) {
            var term = state.searchName.toLowerCase();
            var text = (alert.alert_fingerprint || '').toLowerCase() + ' ' + (alert.text || '').toLowerCase();
            if (text.indexOf(term) === -1) return false;
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

    function renderAlerts() {
        var filtered = state.alerts.filter(matchesFilters);

        if (filtered.length === 0) {
            alertsBody.innerHTML = '<tr><td colspan="6" class="empty-state">No alerts found</td></tr>';
            return;
        }

        var html = '';
        filtered.forEach(function (alert) {
            var isResolved = alert.resolved_at && alert.resolved_at !== '';
            var severity = alert.severity || extractSeverity(alert);
            var status = isResolved ? 'resolved' : 'firing';
            var expanded = state.expandedFingerprint === alert.alert_fingerprint;
            var toolsStr = (alert.tools_used || []).join(', ') || '-';

            html += '<tr onclick="toggleAlert(\'' + escapeHtml(alert.alert_fingerprint) + '\')">';
            html += '<td>' + formatTime(alert.cached_at) + '</td>';
            html += '<td>' + escapeHtml(alert.source || '-') + '</td>';
            html += '<td>' + escapeHtml(alert.alert_fingerprint) + '</td>';
            html += '<td><span class="severity-badge severity-' + severity + '">' + severity + '</span></td>';
            html += '<td><span class="status-badge status-' + status + '">' + status + '</span></td>';
            html += '<td class="fingerprint">' + escapeHtml(toolsStr) + '</td>';
            html += '</tr>';

            if (expanded) {
                html += '<tr class="analysis-row"><td colspan="6">';
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
