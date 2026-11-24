// 博客 Markdown 渲染：marked 解析 + DOMPurify 消毒。公开页与编辑器预览共用，保证所见即所得。
(function () {
  let configured = false;
  function configure() {
    if (configured) return;
    configured = true;
    if (window.marked && typeof window.marked.setOptions === 'function') {
      window.marked.setOptions({gfm: true, breaks: false});
    }
    if (window.DOMPurify) {
      window.DOMPurify.addHook('afterSanitizeAttributes', function (node) {
        if (node.tagName === 'A') {
          node.setAttribute('target', '_blank');
          node.setAttribute('rel', 'noopener noreferrer');
        }
        if (node.tagName === 'IMG') node.setAttribute('loading', 'lazy');
        // 任务列表只保留只读的 checkbox，其余 input 一律移除。
        if (node.tagName === 'INPUT') {
          if (node.getAttribute('type') === 'checkbox') { node.setAttribute('disabled', ''); node.setAttribute('tabindex', '-1'); }
          else node.remove();
        }
      });
    }
  }

  // CommonMark 的 flanking 规则在中文标点旁会失效（如「**结论。**正文」不会加粗），
  // 这里在解析前把代码之外的 **加粗** 直接转成 <strong>，保证中文排版正确。
  // 允许内部出现单个 *（如 **a *b* c**），但避开 ***粗斜体***，那种仍交给 marked 处理。
  function normalizeEmphasis(src) {
    return src.split(/(```[\s\S]*?(?:```|$)|~~~[\s\S]*?(?:~~~|$)|`[^`\n]*`)/g)
      .map(function (part, i) {
        if (i % 2 === 1) return part; // 代码块 / 行内代码原样保留
        return part.replace(/(?<!\*)\*\*(?!\*)(?:[^*\n]|\*(?!\*))*?(?<![\s*])\*\*(?!\*)/g, function (whole, inner) {
          return '<strong>' + whole.slice(2, -2) + '</strong>';
        });
      }).join('');
  }

  // 提示框：GitHub 风格的「> [!NOTE] 标题」。在消毒后的 HTML 上做字符串替换，
  // 把引用块升级成带类型的 callout；标题可省略，省略时按页面语言给默认名。
  const CALLOUT_TYPES = {NOTE: 'note', TIP: 'tip', IMPORTANT: 'important', WARNING: 'warning', CAUTION: 'caution'};
  const CALLOUT_TITLES = {
    note: {zh: '笔记', en: 'Note'}, tip: {zh: '技巧', en: 'Tip'}, important: {zh: '要点', en: 'Important'},
    warning: {zh: '注意', en: 'Warning'}, caution: {zh: '警告', en: 'Caution'},
  };
  function upgradeCallouts(html) {
    const zh = document.documentElement.lang === 'zh-CN';
    return html.replace(/<blockquote>\s*<p>\[!(NOTE|TIP|IMPORTANT|WARNING|CAUTION)\][ \t]*([^\n]*)\n?([\s\S]*?)<\/p>/gi,
      function (whole, type, title, rest) {
        const kind = CALLOUT_TYPES[type.toUpperCase()];
        if (!kind) return whole;
        const heading = title.trim() || (zh ? CALLOUT_TITLES[kind].zh : CALLOUT_TITLES[kind].en);
        return '<blockquote class="callout callout-' + kind + '"><p class="callout-title">' + heading + '</p><p>' + rest + '</p>';
      });
  }

  // 宽表格需要横向滚动，而 <table> 自己没法既滚动又保持表格布局，这里包一层容器。
  function wrapTables(html) {
    return html.replace(/<table>/g, '<div class="table-wrap"><table>').replace(/<\/table>/g, '</table></div>');
  }

  // renderMarkdown(src) 返回消毒后的 HTML 字符串；依赖未加载时退化为转义后的纯文本。
  window.renderMarkdown = function (src) {
    const text = String(src == null ? '' : src);
    if (!window.marked || !window.DOMPurify) return escapeHTML(text).replace(/\n/g, '<br>');
    configure();
    const html = window.marked.parse(normalizeEmphasis(text));
    return wrapTables(upgradeCallouts(window.DOMPurify.sanitize(html, {
      USE_PROFILES: {html: true},
      FORBID_TAGS: ['style', 'form', 'button', 'textarea', 'select', 'option', 'iframe', 'object', 'embed', 'audio', 'video'],
      FORBID_ATTR: ['style'],
    })));
  };

  function escapeHTML(s) {
    return s.replace(/[&<>"']/g, function (c) {
      return {'&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;'}[c];
    });
  }
})();
