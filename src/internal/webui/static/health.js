document.addEventListener('DOMContentLoaded', function () {
    var U = window.SherlockUI;
    var body = document.getElementById('health-body');
    var envSelect = document.getElementById('health-env');
    var statusSelect = document.getElementById('health-status');
    var state = { tools: [], env: '', status: '', expanded: null };

    function rowKey(t) { return t.environment + '/' + t.tool; }

    window.toggleTool = function (key) {
        state.expanded = state.expanded === key ? null : key;
        render();
    };

    function statusBadge(status) {
        var label = status === 'ok' ? 'healthy' : status === 'failed' ? 'failed' : 'no check';
        return '<span class="status-badge tool-' + U.escapeHtml(status) + '">' + label + '</span>';
    }

    function render() {
        var rows = state.tools.filter(function (t) {
            if (state.env && t.environment !== state.env) return false;
            if (state.status && t.status !== state.status) return false;
            return true;
        });
        if (rows.length === 0) {
            body.innerHTML = '<tr><td colspan="8" class="empty-state">No tools registered</td></tr>';
            return;
        }
        var html = '';
        rows.forEach(function (t) {
            var key = rowKey(t);
            var expandable = !!t.error;
            html += '<tr class="tool-row-' + U.escapeHtml(t.status) + (expandable ? ' expandable' : '') + '"' +
                (expandable ? ' onclick="toggleTool(\'' + U.escapeHtml(key) + '\')"' : '') + '>';
            html += '<td>' + U.escapeHtml(t.environment) + '</td>';
            html += '<td>' + U.escapeHtml(t.tool) + '</td>';
            html += '<td class="muted target">' + U.escapeHtml(t.target || '-') + '</td>';
            html += '<td>' + statusBadge(t.status) + '</td>';
            html += '<td class="muted">' + (t.status === 'ok' ? t.latency_ms + ' ms' : '-') + '</td>';
            html += '<td class="muted">' + (t.tools_count || 0) + '</td>';
            html += '<td class="muted">' + U.formatTime(t.checked_at) + '</td>';
            html += '<td class="error-text" title="' + U.escapeHtml(t.error || '') + '">' + U.escapeHtml(t.error || '') + '</td>';
            html += '</tr>';
            if (expandable && state.expanded === key) {
                html += '<tr class="analysis-row"><td colspan="8">';
                html += '<div class="analysis-content error-detail">' + U.escapeHtml(t.error) + '</div>';
                html += '</td></tr>';
            }
        });
        body.innerHTML = html;
    }

    function load() {
        U.fetchJSON('/ui/api/health/tools').then(function (data) {
            state.tools = data.tools || [];
            var s = data.summary || {};
            document.getElementById('tools-total').textContent = s.total || 0;
            document.getElementById('tools-ok').textContent = s.ok || 0;
            document.getElementById('tools-failed').textContent = s.failed || 0;
            document.getElementById('tools-envs').textContent = s.environments || 0;

            var envs = {};
            state.tools.forEach(function (t) { envs[t.environment] = true; });
            U.fillSelect(envSelect, Object.keys(envs).sort(), state.env, 'All Environments');
            render();
        }).catch(function () {
            body.innerHTML = '<tr><td colspan="8" class="empty-state">Failed to load tool status</td></tr>';
        });
    }

    envSelect.addEventListener('change', function () { state.env = this.value; render(); });
    statusSelect.addEventListener('change', function () { state.status = this.value; render(); });

    function refresh() { U.loadHealth(); load(); }
    refresh();
    setInterval(refresh, 30000);
});
