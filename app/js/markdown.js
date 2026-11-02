const ESCAPE = {
  "&": "&amp;",
  "<": "&lt;",
  ">": "&gt;",
  '"': "&quot;",
  "'": "&#39;",
};

export function esc(value) {
  return String(value ?? "").replace(/[&<>"']/g, (ch) => ESCAPE[ch]);
}

export function safeUrl(value) {
  const url = String(value || "").trim();
  if (/^(https?:\/\/|mailto:|\/|#)/i.test(url)) return url;
  return "#";
}

function inline(text) {
  return text
    .replace(/`([^`\n]+)`/g, "<code>$1</code>")
    .replace(/\*\*([^*]+)\*\*/g, "<strong>$1</strong>")
    .replace(/(^|[^*])\*([^*\n]+)\*(?!\*)/g, "$1<em>$2</em>")
    .replace(/\[([^\]]+)\]\(([^)\s]+)\)/g, (_, label, url) => {
      return `<a href="${safeUrl(url)}" target="_blank" rel="noopener noreferrer">${label}</a>`;
    });
}

function codeBlockHTML(language, code) {
  const lang = language ? ` data-lang="${esc(language)}"` : "";
  return `<pre class="code"><code${lang}>${esc(code.replace(/\n$/, ""))}</code></pre>`;
}

export function renderMarkdown(source) {
  let text = String(source ?? "");
  if (!text.trim()) return "";

  const codeBlocks = [];
  text = text.replace(/```([\w+-]*)\n([\s\S]*?)```/g, (_, language, code) => {
    const token = `@@CODE${codeBlocks.length}@@`;
    codeBlocks.push(codeBlockHTML(language, code));
    return `\n\n${token}\n\n`;
  });

  text = esc(text);
  const lines = text.split("\n");
  const out = [];
  let paragraph = [];
  let listType = "";
  let listItems = [];
  let quoteLines = [];

  const flushParagraph = () => {
    if (!paragraph.length) return;
    out.push(`<p>${inline(paragraph.join("<br>"))}</p>`);
    paragraph = [];
  };
  const flushList = () => {
    if (!listItems.length) return;
    const tag = listType === "ol" ? "ol" : "ul";
    out.push(`<${tag}>${listItems.map((item) => `<li>${inline(item)}</li>`).join("")}</${tag}>`);
    listItems = [];
    listType = "";
  };
  const flushQuote = () => {
    if (!quoteLines.length) return;
    out.push(`<blockquote>${inline(quoteLines.join("<br>"))}</blockquote>`);
    quoteLines = [];
  };
  const flushAll = () => {
    flushParagraph();
    flushList();
    flushQuote();
  };

  for (const rawLine of lines) {
    const line = rawLine.replace(/\s+$/, "");
    const trimmed = line.trim();

    if (!trimmed) {
      flushAll();
      continue;
    }

    const codeMatch = trimmed.match(/^@@CODE(\d+)@@$/);
    if (codeMatch) {
      flushAll();
      out.push(codeBlocks[Number(codeMatch[1])] || "");
      continue;
    }

    const heading = trimmed.match(/^(#{1,3})\s+(.+)$/);
    if (heading) {
      flushAll();
      const level = heading[1].length;
      out.push(`<h${level}>${inline(heading[2])}</h${level}>`);
      continue;
    }

    if (/^([-*_])\s*\1\s*\1[\s\-*_]*$/.test(trimmed)) {
      flushAll();
      out.push("<hr>");
      continue;
    }

    const quote = trimmed.match(/^>\s?(.*)$/);
    if (quote) {
      flushParagraph();
      flushList();
      quoteLines.push(quote[1]);
      continue;
    }

    const ordered = trimmed.match(/^\d+\.\s+(.+)$/);
    const unordered = trimmed.match(/^[-*+]\s+(.+)$/);
    if (ordered || unordered) {
      flushParagraph();
      flushQuote();
      const type = ordered ? "ol" : "ul";
      if (listType && listType !== type) flushList();
      listType = type;
      listItems.push((ordered || unordered)[1]);
      continue;
    }

    flushList();
    flushQuote();
    paragraph.push(trimmed);
  }

  flushAll();
  return out.join("");
}
