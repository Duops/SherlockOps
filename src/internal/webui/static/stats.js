document.addEventListener('DOMContentLoaded', function () {
    var U = window.SherlockUI;
    var daysSelect = document.getElementById('stats-days');
    var envSelect = document.getElementById('stats-env');
    var topBody = document.getElementById('top-alerts-body');
    var envBody = document.getElementById('by-env-body');
    var state = { days: '30', env: '', reviewPoll: null };
    var reviewText = document.getElementById('review-text');
    var reviewMeta = document.getElementById('review-meta');
    var reviewError = document.getElementById('review-error');
    var reviewBtn = document.getElementById('review-run');

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

    function renderReview(data) {
        var rev = data.review;
        if (data.running) {
            reviewBtn.disabled = true;
            reviewBtn.textContent = 'Generating\u2026';
        } else {
            reviewBtn.disabled = false;
            reviewBtn.textContent = 'Generate now';
        }
        reviewError.textContent = data.last_error ? 'Last attempt failed: ' + data.last_error : '';
        if (!rev) {
            reviewMeta.textContent = '';
            reviewText.textContent = data.running
                ? 'The review is being generated, this takes up to a minute.'
                : 'No review yet. Reviews are generated on the configured interval, or press "Generate now".';
            return;
        }
        var days = Math.round((Date.parse(rev.until) - Date.parse(rev.since)) / 86400000);
        var meta = 'Period: ' + days + ' days \u00b7 generated ' + U.formatTime(rev.created_at);
        if (rev.model) meta += ' \u00b7 ' + rev.model;
        if (rev.cost_usd > 0) meta += ' \u00b7 ~$' + rev.cost_usd.toFixed(3);
        reviewMeta.textContent = meta;
        reviewText.innerHTML = U.renderMarkdown(rev.text);
    }

    function loadReview() {
        var url = '/ui/api/alert-review' + (state.env ? '?env=' + encodeURIComponent(state.env) : '');
        return U.fetchJSON(url).then(function (data) {
            if (data.error) throw new Error(data.error);
            renderReview(data);
            return data;
        }).catch(function () {
            reviewMeta.textContent = '';
            reviewError.textContent = '';
            reviewText.textContent = 'Review is unavailable.';
            reviewBtn.disabled = true;
        });
    }

    function pollReview() {
        if (state.reviewPoll) clearInterval(state.reviewPoll);
        var attempts = 0;
        state.reviewPoll = setInterval(function () {
            attempts++;
            loadReview().then(function (data) {
                if (!data || !data.running || attempts > 60) {
                    clearInterval(state.reviewPoll);
                    state.reviewPoll = null;
                }
            });
        }, 5000);
    }

    reviewBtn.addEventListener('click', function () {
        reviewBtn.disabled = true;
        reviewBtn.textContent = 'Generating\u2026';
        var url = '/ui/api/alert-review/run?days=' + encodeURIComponent(state.days) +
            (state.env ? '&env=' + encodeURIComponent(state.env) : '');
        fetch(url, { method: 'POST' }).then(function (res) {
            if (res.status === 202 || res.status === 409) {
                reviewText.textContent = 'The review is being generated, this takes up to a minute.';
                pollReview();
            } else {
                reviewBtn.disabled = false;
                reviewBtn.textContent = 'Generate now';
                reviewText.textContent = 'Failed to start the review.';
            }
        });
    });

    daysSelect.addEventListener('change', function () { state.days = this.value; load(); });
    envSelect.addEventListener('change', function () { state.env = this.value; load(); loadReview(); });

    function refresh() { U.loadHealth(); load(); loadReview(); }
    refresh();
    setInterval(refresh, 60000);
});
