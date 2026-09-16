document.addEventListener('DOMContentLoaded', function () {
    var U = window.SherlockUI;
    var daysSelect = document.getElementById('stats-days');
    var envSelect = document.getElementById('stats-env');
    var topBody = document.getElementById('top-alerts-body');
    var envBody = document.getElementById('by-env-body');
    var state = { days: '30', env: '' };

    function shareBar(pct) {
        var width = Math.max(0, Math.min(100, pct || 0));
        return '<div class="share"><div class="share-bar" style="width:' + width + '%"></div>' +
            '<span class="share-label">' + U.formatPct(pct) + '</span></div>';
    }

    function renderCards(s) {
        document.getElementById('big-total').textContent = U.formatInt(s.total);
        document.getElementById('big-total-desc').textContent =
            'notifications in ' + s.days + ' days, ≈' + Math.round(s.per_day || 0) + ' per day';

        document.getElementById('big-top3').textContent = U.formatPct(s.top3_share);
        var topCount = Math.min(3, (s.top_alerts || []).length);
        document.getElementById('big-top3-desc').textContent =
            'of volume from ' + topCount + ' alert' + (topCount === 1 ? '' : 's') +
            '; top 8 — ' + U.formatPct(s.top8_share) + ', top 30 — ' + U.formatPct(s.top30_share);

        var resolvedPct = s.total > 0 ? (s.resolved * 100 / s.total) : 0;
        document.getElementById('big-resolved').textContent = U.formatPct(resolvedPct);
        document.getElementById('big-resolved-desc').textContent =
            'of notifications are resolved (' + U.formatInt(s.resolved) + ' of ' + U.formatInt(s.total) + ')';

        document.getElementById('big-unique').textContent = U.formatInt(s.unique_alerts);
        var envCount = (s.by_environment || []).length;
        document.getElementById('big-unique-desc').textContent =
            'unique alerts across ' + envCount + ' environment' + (envCount === 1 ? '' : 's');
    }

    function renderTop(s) {
        var rows = s.top_alerts || [];
        if (rows.length === 0) {
            topBody.innerHTML = '<tr><td colspan="6" class="empty-state">No notifications in this period</td></tr>';
            return;
        }
        var html = '';
        rows.slice(0, 30).forEach(function (a, i) {
            var resolvedPct = a.count > 0 ? (a.resolved * 100 / a.count) : 0;
            html += '<tr>';
            html += '<td class="muted">' + (i + 1) + '</td>';
            html += '<td>' + U.escapeHtml(a.name) + '</td>';
            html += '<td class="muted">' + U.escapeHtml((a.environments || []).join(', ')) + '</td>';
            html += '<td>' + U.formatInt(a.count) + '</td>';
            html += '<td>' + shareBar(a.share) + '</td>';
            html += '<td class="muted">' + U.formatPct(resolvedPct) + '</td>';
            html += '</tr>';
        });
        topBody.innerHTML = html;
    }

    function renderEnvs(s) {
        var rows = s.by_environment || [];
        if (rows.length === 0) {
            envBody.innerHTML = '<tr><td colspan="6" class="empty-state">No notifications in this period</td></tr>';
            return;
        }
        var html = '';
        rows.forEach(function (e) {
            html += '<tr>';
            html += '<td>' + U.escapeHtml(e.environment) + '</td>';
            html += '<td>' + U.formatInt(e.count) + '</td>';
            html += '<td class="muted">' + Math.round(e.count / (s.days || 1)) + '</td>';
            html += '<td>' + shareBar(e.share) + '</td>';
            html += '<td class="muted">' + U.formatInt(e.unique_alerts) + '</td>';
            html += '<td class="muted">' + U.escapeHtml(e.top_alert || '-') + '</td>';
            html += '</tr>';
        });
        envBody.innerHTML = html;
    }

    function load() {
        var url = '/ui/api/alert-stats?days=' + encodeURIComponent(state.days);
        if (state.env) url += '&env=' + encodeURIComponent(state.env);
        U.fetchJSON(url).then(function (s) {
            if (s.error) throw new Error(s.error);
            U.fillSelect(envSelect, s.environments || [], state.env, 'All Environments');
            renderCards(s);
            renderTop(s);
            renderEnvs(s);
        }).catch(function () {
            topBody.innerHTML = '<tr><td colspan="6" class="empty-state">Failed to load stats</td></tr>';
            envBody.innerHTML = '<tr><td colspan="6" class="empty-state">Failed to load stats</td></tr>';
        });
    }

    daysSelect.addEventListener('change', function () { state.days = this.value; load(); });
    envSelect.addEventListener('change', function () { state.env = this.value; load(); });

    function refresh() { U.loadHealth(); load(); }
    refresh();
    setInterval(refresh, 60000);
});
