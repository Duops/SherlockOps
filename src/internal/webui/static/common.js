// Shared helpers for the Stats and Tools pages.
window.SherlockUI = (function () {
    function fetchJSON(url) {
        return fetch(url).then(function (res) { return res.json(); });
    }

    function escapeHtml(str) {
        var div = document.createElement('div');
        div.appendChild(document.createTextNode(str == null ? '' : String(str)));
        return div.innerHTML;
    }

    function formatInt(n) {
        return (n || 0).toLocaleString();
    }

    function formatPct(v) {
        if (!v) return '0%';
        return (v >= 10 ? Math.round(v) : v.toFixed(1)) + '%';
    }

    function formatTime(isoStr) {
        if (!isoStr) return '-';
        return new Date(isoStr).toLocaleString();
    }

    function loadHealth() {
        var dot = document.getElementById('health-dot');
        var text = document.getElementById('health-text');
        if (!dot || !text) return;
        fetchJSON('/health/ready').then(function (data) {
            if (data.status === 'ok') {
                dot.className = 'health-dot';
                text.textContent = 'Healthy';
            } else {
                dot.className = 'health-dot degraded';
                text.textContent = 'Degraded';
            }
        }).catch(function () {
            dot.className = 'health-dot down';
            text.textContent = 'Unreachable';
        });
    }

    function fillSelect(select, values, current, allLabel) {
        select.innerHTML = '<option value="">' + escapeHtml(allLabel) + '</option>';
        values.forEach(function (v) {
            var opt = document.createElement('option');
            opt.value = v;
            opt.textContent = v;
            if (v === current) opt.selected = true;
            select.appendChild(opt);
        });
    }

    // Themed dropdowns over hidden native <select>s: the select stays the source
    // of truth and still fires "change", so existing listeners keep working.
    function closeAllDropdowns() {
        document.querySelectorAll('.dd.open').forEach(function (el) { el.classList.remove('open'); });
    }

    function enhanceSelect(select) {
        if (select.dataset.enhanced) return;
        select.dataset.enhanced = '1';

        var wrap = document.createElement('div');
        wrap.className = 'dd';
        var btn = document.createElement('button');
        btn.type = 'button';
        btn.className = 'dd-btn';
        var menu = document.createElement('div');
        menu.className = 'dd-menu';

        select.parentNode.insertBefore(wrap, select);
        wrap.appendChild(select);
        wrap.appendChild(btn);
        wrap.appendChild(menu);
        select.classList.add('dd-native');
        select.tabIndex = -1;

        function label() {
            var o = select.options[select.selectedIndex];
            btn.innerHTML = '<span class="dd-label">' + escapeHtml(o ? o.textContent : '') + '</span><span class="dd-caret"></span>';
        }

        function build() {
            menu.innerHTML = '';
            Array.prototype.forEach.call(select.options, function (o, i) {
                var item = document.createElement('div');
                item.className = 'dd-item' + (i === select.selectedIndex ? ' selected' : '');
                item.textContent = o.textContent;
                item.addEventListener('click', function (e) {
                    e.stopPropagation();
                    if (select.selectedIndex !== i) {
                        select.selectedIndex = i;
                        select.dispatchEvent(new Event('change', { bubbles: true }));
                    }
                    wrap.classList.remove('open');
                });
                menu.appendChild(item);
            });
        }

        btn.addEventListener('click', function (e) {
            e.stopPropagation();
            var isOpen = wrap.classList.contains('open');
            closeAllDropdowns();
            if (!isOpen) {
                build();
                wrap.classList.add('open');
            }
        });
        btn.addEventListener('keydown', function (e) {
            if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
                e.preventDefault();
                var next = select.selectedIndex + (e.key === 'ArrowDown' ? 1 : -1);
                if (next >= 0 && next < select.options.length) {
                    select.selectedIndex = next;
                    select.dispatchEvent(new Event('change', { bubbles: true }));
                }
            }
        });

        select.addEventListener('change', label);
        new MutationObserver(label).observe(select, { childList: true, subtree: true, attributes: true });
        label();
    }

    function enhanceSelects(root) {
        (root || document).querySelectorAll('.controls select').forEach(enhanceSelect);
    }

    document.addEventListener('click', closeAllDropdowns);
    document.addEventListener('keydown', function (e) {
        if (e.key === 'Escape') closeAllDropdowns();
    });
    document.addEventListener('DOMContentLoaded', function () { enhanceSelects(document); });

    // renderMarkdown: escaped text → HTML for headings, bold, code, lists, paragraphs.
    function renderMarkdown(text) {
        var lines = escapeHtml(text || '').split('\n');
        var html = '';
        var list = null; // 'ul' | 'ol'
        var para = [];

        function inline(s) {
            return s
                .replace(/\*\*(.+?)\*\*/g, '<strong>$1</strong>')
                .replace(/`([^`]+)`/g, '<code>$1</code>');
        }
        function flushPara() {
            if (para.length) {
                html += '<p>' + inline(para.join(' ')) + '</p>';
                para = [];
            }
        }
        function closeList() {
            if (list) {
                html += '</' + list + '>';
                list = null;
            }
        }

        lines.forEach(function (raw) {
            var line = raw.replace(/\s+$/, '');
            var h = /^(#{1,4})\s+(.*)$/.exec(line);
            var ul = /^\s*[-*•]\s+(.*)$/.exec(line);
            var ol = /^\s*\d+[.)]\s+(.*)$/.exec(line);
            if (h) {
                flushPara(); closeList();
                var level = Math.min(h[1].length + 2, 5);
                html += '<h' + level + '>' + inline(h[2]) + '</h' + level + '>';
            } else if (ul || ol) {
                flushPara();
                var kind = ul ? 'ul' : 'ol';
                if (list !== kind) { closeList(); list = kind; html += '<' + kind + '>'; }
                html += '<li>' + inline((ul || ol)[1]) + '</li>';
            } else if (line.trim() === '') {
                flushPara(); closeList();
            } else {
                if (list) closeList();
                para.push(line.trim());
            }
        });
        flushPara(); closeList();
        return html;
    }

    return {
        renderMarkdown: renderMarkdown,
        enhanceSelects: enhanceSelects,
        fetchJSON: fetchJSON,
        escapeHtml: escapeHtml,
        formatInt: formatInt,
        formatPct: formatPct,
        formatTime: formatTime,
        loadHealth: loadHealth,
        fillSelect: fillSelect,
    };
})();
