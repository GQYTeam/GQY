/* 顾清影 · Markdown 渲染、代码高亮与链接卡片 */
(() => {
  'use strict';
  const G = (window.GQY = window.GQY || {});
  const esc = G.esc;

  const LINK_META = {
    'pytorch.org': {
      site: 'PyTorch Docs',
      title: 'Datasets & DataLoaders',
      desc: '官方教程：用 Dataset 与 DataLoader 加载数据，含 ImageFolder 和 random_split 示例。',
      cover: 'assets/card-pytorch.svg'
    },
    'medium.com': {
      site: 'Medium · Design',
      title: 'Designing calm AI interfaces',
      desc: 'Streaming states, thinking blocks and tool traces — a practical field guide.',
      cover: 'assets/card-medium.svg'
    }
  };

  function linkCardHTML(raw) {
    const url = String(raw || '').replace(/^<|>$/g, '').trim();
    let host = '';
    try { host = new URL(url).hostname.replace(/^www\./i, ''); }
    catch (err) {
      return '<p><a href="' + esc(url) + '" target="_blank" rel="noopener noreferrer">' + esc(url) + '</a></p>';
    }
    const meta = LINK_META[host] || {};
    const cover = meta.cover || '';
    return '<a class="linkcard" href="' + esc(url) + '" target="_blank" rel="noopener noreferrer">' +
      (cover
        ? '<span class="lc-cover" style="background-image:url(\'' + cover + '\')"></span>'
        : '<span class="lc-cover lc-cover-fallback"><b>' + esc(host.charAt(0).toUpperCase()) + '</b></span>') +
      '<span class="lc-body">' +
      '<span class="lc-site">' + esc(meta.site || host) + '</span>' +
      '<span class="lc-title">' + esc(meta.title || host) + '</span>' +
      '<span class="lc-desc">' + esc(meta.desc || url) + '</span>' +
      '<span class="lc-url">' + esc(host) + '</span>' +
      '</span></a>';
  }

  /* 只放行 http(s) / mailto 与相对路径；javascript:、data:、vbscript: 一律降级成纯文本。
     注意：这里的 url 已经过 esc()，不能再转义一次，否则 & 会变成 &amp;。 */
  function safeUrl(raw) {
    const url = String(raw || '').trim();
    if (!url) return '';
    if (/^[a-zA-Z][a-zA-Z0-9+.-]*:/.test(url)) return /^(https?:|mailto:)/i.test(url) ? url : '';
    if (/^[\u0000-\u001f]/.test(url)) return '';
    return url;
  }

  function inline(text) {
    const stash = [];
    const keep = (html) => '\u0002' + (stash.push(html) - 1) + '\u0002';
    let s = esc(text);

    s = s.replace(/`([^`\n]+)`/g, (m, code) => keep('<code>' + code + '</code>'));
    s = s.replace(/!\[([^\]]*)\]\(([^)\s]+)\)/g, (m, alt, raw) => {
      const url = safeUrl(raw);
      return keep(url
        ? '<img class="md-img" src="' + url + '" alt="' + alt + '" loading="lazy" referrerpolicy="no-referrer">'
        : alt);
    });
    s = s.replace(/\[([^\]]+)\]\(([^)\s]+)\)/g, (m, label, raw) => {
      const url = safeUrl(raw);
      return keep(url
        ? '<a href="' + url + '" target="_blank" rel="noopener noreferrer nofollow">' + label + '</a>'
        : label);
    });
    s = s.replace(/&lt;(https?:\/\/[^\s&]+)&gt;/g, (m, url) => keep(
      '<a href="' + url + '" target="_blank" rel="noopener noreferrer nofollow">' + url + '</a>'
    ));
    s = s.replace(/(^|[\s(（])(https?:\/\/[^\s<)）]+)/g, (m, pre, url) => pre + keep(
      '<a href="' + url + '" target="_blank" rel="noopener noreferrer nofollow">' + url + '</a>'
    ));

    s = s.replace(/\*\*([^*\n]+)\*\*/g, '<strong>$1</strong>');
    s = s.replace(/(^|[^*])\*([^*\n]+)\*/g, '$1<em>$2</em>');
    s = s.replace(/~~([^~\n]+)~~/g, '<del>$1</del>');
    s = s.replace(/==([^=\n]+)==/g, '<mark>$1</mark>');
    s = s.replace(/\u0002(\d+)\u0002/g, (m, i) => stash[+i]);
    return s;
  }

  const HL_PY = /("""[\s\S]*?"""|'''[\s\S]*?'''|"(?:\\.|[^"\\\n])*"|'(?:\\.|[^'\\\n])*')|(#[^\n]*)|(\b(?:def|class|return|if|elif|else|for|while|in|not|and|or|import|from|as|with|try|except|finally|raise|lambda|yield|pass|break|continue|global|nonlocal|assert|del|is|None|True|False|self)\b)|(\b\d+(?:\.\d+)?\b)|(\b[A-Za-z_]\w*(?=\())/g;
  const HL_JS = /(\/\/[^\n]*|\/\*[\s\S]*?\*\/)|("(?:\\.|[^"\\\n])*"|'(?:\\.|[^'\\\n])*'|`(?:\\.|[^`\\])*`)|\b(?:const|let|var|function|return|if|else|for|while|do|switch|case|default|break|continue|new|class|extends|import|from|export|async|await|try|catch|finally|throw|typeof|instanceof|of|in|null|undefined|true|false|this|super)\b|(\b\d+(?:\.\d+)?\b)|(\b[A-Za-z_$][\w$]*(?=\())/g;
  const HL_JSON = /(\btrue\b|\bfalse\b|\bnull\b)|("(?:\\.|[^"\\])*")|(-?\b\d+(?:\.\d+)?(?:[eE][+-]?\d+)?\b)/g;
  const HL_SH = /(#[^\n]*)|("(?:\\.|[^"\\])*"|'[^']*')|\b(?:if|then|else|elif|fi|for|while|do|done|case|esac|function|return|export|local|echo|cd|source|set)\b|(\$\{?[A-Za-z_]\w*\}?)|(\b\d+\b)/g;
  const HL_CSS = /(\/\*[\s\S]*?\*\/)|("(?:\\.|[^"\\])*"|'[^']*')|(#[0-9a-fA-F]{3,8}\b)|\b(\d+(?:\.\d+)?)(?:px|rem|em|%|s|ms|deg|vh|vw|fr)?\b|(@[a-z-]+)|([a-z-]+)(?=\s*:)/g;
  const HL = {
    python: { re: HL_PY, cls: ['', 'str', 'com', 'kw', 'num', 'fn'] },
    js: { re: HL_JS, cls: ['', 'com', 'str', 'kw', 'num', 'fn'] },
    json: { re: HL_JSON, cls: ['', 'kw', 'str', 'num'] },
    bash: { re: HL_SH, cls: ['', 'com', 'str', 'kw', 'var', 'num'] },
    css: { re: HL_CSS, cls: ['', 'com', 'str', 'num', 'kw', 'fn'] }
  };
  const HL_ALIAS = {
    py: 'python', python: 'python', py3: 'python',
    js: 'js', javascript: 'js', jsx: 'js', ts: 'js', tsx: 'js', node: 'js',
    json: 'json', sh: 'bash', shell: 'bash', bash: 'bash', zsh: 'bash', console: 'bash',
    css: 'css', scss: 'css'
  };

  function highlightHTML(code, lang) {
    const conf = HL[HL_ALIAS[String(lang || '').toLowerCase()]];
    if (!conf) return esc(code);
    const re = conf.re;
    re.lastIndex = 0;
    let out = '';
    let last = 0;
    let m;
    while ((m = re.exec(code))) {
      if (!m[0]) { re.lastIndex += 1; continue; }
      if (m.index > last) out += esc(code.slice(last, m.index));
      let group = 0;
      for (let g = 1; g < m.length; g++) { if (m[g] != null) { group = g; break; } }
      const cls = conf.cls[group];
      out += cls ? '<span class="tok-' + cls + '">' + esc(m[0]) + '</span>' : esc(m[0]);
      last = m.index + m[0].length;
    }
    if (last < code.length) out += esc(code.slice(last));
    return out;
  }

  function codeHTML(block) {
    const lang = block.lang || 'code';
    return '<div class="codeblock"><div class="code-head"><span class="lang">' + esc(lang) + '</span>' +
      '<button class="code-tool" type="button" data-wrap title="切换自动换行" aria-label="切换自动换行">' +
      '<svg class="ico" aria-hidden="true"><use href="#i-wrap"/></svg></button>' +
      '<button class="code-tool code-copy" type="button" title="复制代码" aria-label="复制代码">' +
      '<svg class="ico" aria-hidden="true"><use href="#i-copy"/></svg></button></div>' +
      '<pre><code>' + highlightHTML(block.code, block.lang) + '</code></pre></div>';
  }

  function listToHTML(items, level) {
    let out = '';
    let i = 0;
    while (i < items.length) {
      const item = items[i];
      if (item.indent < level) break;
      if (item.indent > level) { i += 1; continue; }
      const tag = item.ordered ? 'ol' : 'ul';
      out += '<' + tag + '>';
      while (i < items.length && items[i].indent === level && (items[i].ordered ? 'ol' : 'ul') === tag) {
        const node = items[i];
        let j = i + 1;
        const kids = [];
        while (j < items.length && items[j].indent > level) { kids.push(items[j]); j += 1; }
        out += '<li' + (node.task ? ' class="task"' : '') + '>' +
          (node.task ? '<span class="task-box' + (node.checked ? ' is-on' : '') + '" aria-hidden="true">' + (node.checked ? '✓' : '') + '</span>' : '') +
          inline(node.text);
        if (kids.length) out += listToHTML(kids, kids[0].indent);
        out += '</li>';
        i = j;
      }
      out += '</' + tag + '>';
    }
    return out;
  }

  function renderMarkdown(src) {
    if (!src) return '';
    const blocks = [];
    let text = String(src);
    text = text.replace(/```([^\n`]*)\n?([\s\S]*?)(```|$)/g, (m, lang, code) => {
      blocks.push({ lang: (lang || '').trim(), code: code.replace(/\n$/, '') });
      return '\n\u0001B' + (blocks.length - 1) + '\u0001\n';
    });

    const lines = text.split('\n');
    const splitRow = (s) => s.replace(/^\|/, '').replace(/\|$/, '').split('|').map((c) => c.trim());
    const listLine = /^(\s*)([-*+]|\d+\.)\s+(.*)$/;

    let out = '';
    let para = [];
    let quote = [];
    let i = 0;

    const flushPara = () => {
      if (!para.length) return;
      out += '<p>' + para.map(inline).join('<br>') + '</p>';
      para = [];
    };
    const flushQuote = () => {
      if (!quote.length) return;
      out += '<blockquote><p>' + quote.map(inline).join('<br>') + '</p></blockquote>';
      quote = [];
    };
    const flushAll = () => { flushPara(); flushQuote(); };

    while (i < lines.length) {
      const line = lines[i];
      const t = line.trim();
      if (!t) { flushAll(); i += 1; continue; }

      const fence = t.match(/^\u0001B(\d+)\u0001$/);
      if (fence) { flushAll(); out += codeHTML(blocks[+fence[1]] || { code: '' }); i += 1; continue; }

      const heading = t.match(/^(#{1,6})\s+/);
      if (heading) {
        flushAll();
        const lv = heading[1].length;
        out += '<h' + lv + '>' + inline(t.slice(lv).trim()) + '</h' + lv + '>';
        i += 1; continue;
      }

      if (/^(-{3,}|\*{3,}|_{3,})$/.test(t)) { flushAll(); out += '<hr>'; i += 1; continue; }

      if (/^>\s?/.test(t)) { flushPara(); quote.push(t.replace(/^>\s?/, '')); i += 1; continue; }

      if (listLine.test(line)) {
        flushPara(); flushQuote();
        const items = [];
        while (i < lines.length) {
          const m = lines[i].match(listLine);
          if (!m) break;
          const body = m[3];
          const task = body.match(/^\[([ xX])\]\s+(.*)$/);
          items.push({
            indent: m[1].replace(/\t/g, '  ').length,
            ordered: /\d/.test(m[2]),
            text: task ? task[2] : body,
            task: !!task,
            checked: task ? task[1].toLowerCase() === 'x' : false
          });
          i += 1;
        }
        out += listToHTML(items, Math.min.apply(null, items.map((x) => x.indent)));
        continue;
      }

      if (t.charAt(0) === '|' && i + 1 < lines.length && /^\|?[\s:|-]+\|?$/.test(lines[i + 1].trim())) {
        flushAll();
        const header = splitRow(t);
        i += 2;
        const rows = [];
        while (i < lines.length && lines[i].trim().charAt(0) === '|') { rows.push(splitRow(lines[i].trim())); i += 1; }
        out += '<table><thead><tr>' + header.map((c) => '<th>' + inline(c) + '</th>').join('') + '</tr></thead><tbody>' +
          rows.map((r) => '<tr>' + r.map((c) => '<td>' + inline(c) + '</td>').join('') + '</tr>').join('') + '</tbody></table>';
        continue;
      }

      if (/^<?https?:\/\/[^\s]+>?$/.test(t)) {
        flushAll();
        out += linkCardHTML(t);
        i += 1; continue;
      }

      flushQuote();
      para.push(t);
      i += 1;
    }
    flushAll();
    return out;
  }

  Object.assign(G, { inline, safeUrl, renderMarkdown, highlightHTML, codeHTML, linkCardHTML });
})();
