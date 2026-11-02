import { esc, renderMarkdown } from "./markdown.js";
import { state, formatTime, formatTokens, shortId } from "./store.js";

export function $(selector, root = document) {
  return root.querySelector(selector);
}

export function $$(selector, root = document) {
  return Array.from(root.querySelectorAll(selector));
}

function statusChip(status) {
  switch (status) {
    case "complete":
      return `<span class="chip ok">完成</span>`;
    case "incomplete":
      return `<span class="chip warn">未完成</span>`;
    case "canceled":
      return `<span class="chip bad">已停止</span>`;
    case "waiting":
      return `<span class="chip warn">等待回答</span>`;
    default:
      return "";
  }
}

function usageChip(usage) {
  if (!usage) return "";
  const total = (usage.prompt_tokens || 0) + (usage.completion_tokens || 0);
  if (!total) return "";
  return `<span class="chip" title="prompt ${usage.prompt_tokens || 0} / completion ${usage.completion_tokens || 0}">${formatTokens(total)} tokens</span>`;
}

function thinkingHTML(message, index) {
  if (!message.thinking) return "";
  const key = `thinking-${index}`;
  const open = state.expanded.has(key) ? " open" : "";
  return `<details class="thinking" data-expand-key="${key}"${open}>
    <summary>思考过程 · ${message.thinking.length} 字</summary>
    <div class="thinking-body">${esc(message.thinking)}</div>
  </details>`;
}

function toolHTML(tool, messageIndex, toolIndex) {
  const key = `tool-${messageIndex}-${toolIndex}`;
  const open = state.expanded.has(key) ? " open" : "";
  const status = tool.status || "ok";
  const args = tool.arguments == null ? "" : JSON.stringify(tool.arguments, null, 2);
  const output = tool.output || tool.error || "";
  const meta = [status, tool.duration_ms != null ? `${tool.duration_ms}ms` : ""].filter(Boolean).join(" · ");
  return `<details class="tool-card" data-expand-key="${key}"${open}>
    <summary class="tool-summary">
      <span class="tool-dot ${esc(status)}"></span>
      <span class="tool-name">${esc(tool.name || "tool")}</span>
      <span class="tool-meta">${esc(meta)}</span>
    </summary>
    <div class="tool-body">
      ${args ? `<div><div class="tool-label">参数</div><pre>${esc(args)}</pre></div>` : ""}
      ${output ? `<div><div class="tool-label">输出</div><pre>${esc(output)}</pre></div>` : ""}
    </div>
  </details>`;
}

function questionHTML(message) {
  const q = message.question;
  if (!q) return "";
  const answered = q.answer ? `<div class="question-answer">已答：${esc(q.answer)}</div>` : "";
  if (q.answer) {
    return `<div class="question-card"><p class="question-text">${esc(q.text || "需要你确认")}</p>${answered}</div>`;
  }
  const options = (q.options || [])
    .map((option, i) => `<button class="question-option" type="button" data-answer="${esc(option)}"><kbd>${i + 1}</kbd>${esc(option)}</button>`)
    .join("");
  const custom = q.allow_custom
    ? `<div class="question-custom">
        <input type="text" data-question-input placeholder="或者输入自定义答案" aria-label="自定义答案">
        <button class="btn" type="button" data-question-submit>回答</button>
      </div>`
    : "";
  return `<div class="question-card">
    <p class="question-text">${esc(q.text || "需要你确认")}</p>
    ${options ? `<div class="question-options">${options}</div>` : ""}
    ${custom}
    ${answered}
  </div>`;
}

function imagesHTML(message) {
  if (!message.images?.length) return "";
  return `<div class="msg-images">${message.images
    .map((src, i) => `<img src="${esc(src)}" alt="附件 ${i + 1}" data-lightbox="${esc(src)}" loading="lazy">`)
    .join("")}</div>`;
}

function actionsHTML(message, index) {
  if (message.role !== "assistant") return "";
  const retry = message.status !== "streaming" && !message.question
    ? `<button class="msg-action" type="button" data-retry-index="${index}">重试</button>`
    : "";
  const copy = message.content
    ? `<button class="msg-action" type="button" data-copy-index="${index}">复制</button>`
    : "";
  if (!retry && !copy) return "";
  return `<div class="msg-actions">${copy}${retry}</div>`;
}

function messageHTML(message, index) {
  const isUser = message.role === "user";
  const avatar = isUser ? "你" : "GQ";
  const model = message.model ? `<span class="msg-badge">${esc(message.model)}</span>` : "";
  let body = "";

  if (!isUser) {
    body += thinkingHTML(message, index);
    if (message.tools?.length) {
      body += `<div class="tool-list">${message.tools.map((tool, i) => toolHTML(tool, index, i)).join("")}</div>`;
    }
    if (message.branches?.length) body += branchHTML(message);
  }

  if (message.content) {
    body += isUser
      ? `<div class="msg-body">${esc(message.content)}</div>`
      : `<div class="prose">${renderMarkdown(message.content)}</div>`;
  }

  if (!isUser && message.question) body += questionHTML(message);

  if (!isUser && message.status === "streaming" && !message.content && !message.thinking && !message.tools?.length && !message.question) {
    body += `<span class="typing" aria-label="正在生成"><i></i><i></i><i></i></span>`;
  }

  if (isUser) body += imagesHTML(message);

  const footParts = [];
  if (!isUser) {
    footParts.push(statusChip(message.status));
    footParts.push(usageChip(message.usage));
    if (message.stop_reason && message.status === "complete") {
      footParts.push(`<span class="chip">${esc(message.stop_reason)}</span>`);
    }
    if (message.error) footParts.push(`<span class="chip bad">${esc(message.error)}</span>`);
  }

  const head = isUser
    ? `<div class="msg-head"><span class="who">你</span></div>`
    : `<div class="msg-head"><span class="who">GQY</span>${model}</div>`;

  const footer = !isUser || footParts.length
    ? `<div class="msg-foot">${footParts.join("")}${actionsHTML(message, index)}</div>`
    : "";

  return `<article class="msg ${isUser ? "user" : "assistant"}" data-index="${index}">
    <div class="msg-avatar" aria-hidden="true">${avatar}</div>
    <div class="bubble">
      ${head}
      ${body}
      ${footer}
    </div>
  </article>`;
}

function bindDetails() {
  for (const details of $$("#thread details[data-expand-key]")) {
    details.addEventListener("toggle", () => {
      const key = details.dataset.expandKey;
      if (details.open) state.expanded.add(key);
      else state.expanded.delete(key);
    });
  }
}

export function renderMessages({ forceBottom = false } = {}) {
  const scroller = $("#messages");
  const thread = $("#thread");
  const empty = $("#emptyState");
  if (!scroller || !thread || !empty) return;

  const nearBottom = scroller.scrollHeight - scroller.scrollTop - scroller.clientHeight < 140;
  const isEmpty = state.messages.length === 0;
  empty.hidden = !isEmpty;
  thread.innerHTML = state.messages.map((message, index) => messageHTML(message, index)).join("");
  bindDetails();

  if (forceBottom || (nearBottom && !isEmpty)) {
    scroller.scrollTop = scroller.scrollHeight;
  } else if (isEmpty) {
    scroller.scrollTop = 0;
  }
}

export function renderSessions() {
  const list = $("#sessionList");
  const empty = $("#emptySessions");
  const count = $("#sessionCount");
  if (!list) return;
  const query = state.sessionQuery.trim().toLowerCase();
  const sessions = query
    ? state.sessions.filter((session) => (session.title || "").toLowerCase().includes(query))
    : state.sessions;
  let currentGroup = "";
  const parts = [];
  for (const session of sessions) {
    const group = query ? "搜索结果" : sessionGroup(session.created_at);
    if (group !== currentGroup) {
      parts.push(`<div class="session-group-label">${esc(group)}</div>`);
      currentGroup = group;
    }
    const active = session.session_id === state.sessionId;
    const title = session.title || `会话 ${shortId(session.session_id)}`;
    parts.push(`<div class="session-item${active ? " active" : ""}" role="button" tabindex="0" data-session-id="${esc(session.session_id)}" aria-current="${active ? "true" : "false"}">
      <div class="session-item-main">
        <div class="session-title">${esc(title)}</div>
        <div class="session-time">${esc(formatTime(session.created_at))}</div>
      </div>
      <button class="session-delete" type="button" data-delete-session="${esc(session.session_id)}" aria-label="删除会话">×</button>
    </div>`);
  }
  list.innerHTML = parts.join("");
  if (empty) {
    empty.hidden = sessions.length > 0;
    empty.textContent = query ? "没有匹配的会话。" : "还没有会话。发第一条消息就会出现在这里。";
  }
  if (count) count.textContent = query ? `${sessions.length}/${state.sessions.length}` : state.sessions.length ? String(state.sessions.length) : "";
}

export function renderAttachments() {
  const box = $("#attachments");
  if (!box) return;
  box.hidden = state.attachments.length === 0;
  box.innerHTML = state.attachments
    .map((attachment, index) => {
      const preview = attachment.kind === "image"
        ? `<img src="${esc(attachment.dataUrl)}" alt="">`
        : `<span class="chip">TXT</span>`;
      return `<span class="attachment">${preview}<span class="attachment-name">${esc(attachment.name)}</span>
        <button class="attachment-remove" type="button" data-remove-attachment="${index}" aria-label="移除附件">×</button></span>`;
    })
    .join("");
}

export function setHeader(title, sub) {
  const titleEl = $("#sessionTitle");
  const subEl = $("#sessionSub");
  if (titleEl) titleEl.textContent = title || "新对话";
  if (subEl) subEl.textContent = sub || "";
}

function branchHTML(message) {
  const cards = message.branches.map((b) => {
    const adopted = Boolean(b.adopted);
    const id = esc(b.branch_id || "");
    return `<div class="branch-card${adopted ? " adopted" : ""}" data-branch-id="${id}">
      <div class="branch-head"><span class="chip">支线</span>${adopted ? '<span class="chip ok">已采纳</span>' : ""}</div>
      <div class="branch-msg">${esc(b.content || "")}</div>
      ${b.output ? `<div class="branch-out">${esc(b.output)}</div>` : ""}
      ${!adopted && b.branch_id ? '<button type="button" class="btn ghost sm" data-branch-adopt>采纳进主线</button>' : ""}
    </div>`;
  });
  return `<div class="branch-list">${cards.join("")}</div>`;
}

function sessionGroup(iso) {
  if (!iso) return "更早";
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return "更早";
  const now = new Date();
  const start = new Date(now.getFullYear(), now.getMonth(), now.getDate());
  const day = new Date(date.getFullYear(), date.getMonth(), date.getDate());
  const diff = Math.round((start - day) / 86400000);
  if (diff <= 0) return "今天";
  if (diff === 1) return "昨天";
  if (diff < 7) return "本周";
  return "更早";
}
