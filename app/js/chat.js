import { api, AuthError, ApiError, adoptBranch, chatStream } from "./api.js";
import { state, formatTokens } from "./store.js";
import { renderAttachments, renderMessages, setHeader, $ } from "./render.js";
import { emit, toast } from "./ui.js";
const MAX_ATTACHMENTS = 6;
const IMAGE_LIMIT = 8 * 1024 * 1024;
const TEXT_LIMIT = 1024 * 1024;
const TEXT_EXT = /\.(txt|md|markdown|json|jsonl|js|mjs|ts|tsx|go|py|rb|rs|java|kt|c|h|cc|cpp|css|html|xml|yml|yaml|toml|ini|csv|sql|sh|bash|zsh|log)$/i;

let renderTicket = 0;
let draftTimer = 0;
let bound = false;

export function initChat() {
  if (bound) return;
  bound = true;

  const form = $("#composer");
  const input = $("#input");
  const sendBtn = $("#sendBtn");
  const stopBtn = $("#stopBtn");
  const attachBtn = $("#attachBtn");
  const fileInput = $("#fileInput");

  form?.addEventListener("submit", (event) => {
    event.preventDefault();
    sendCurrent();
  });

  input?.addEventListener("keydown", (event) => {
    if (event.key === "Enter" && !event.shiftKey && !event.isComposing) {
      event.preventDefault();
      sendCurrent();
    }
    if (event.key === "Escape" && state.streaming) {
      stopStreaming();
    }
  });

  input?.addEventListener("input", () => {
    autoGrow(input);
    updateSendState();
    scheduleDraft();
  });

  input?.addEventListener("paste", async (event) => {
    const files = Array.from(event.clipboardData?.files || []).filter((file) => file.type.startsWith("image/"));
    if (!files.length) return;
    event.preventDefault();
    await addFiles(files);
  });

  stopBtn?.addEventListener("click", stopStreaming);
  attachBtn?.addEventListener("click", () => fileInput?.click());
  fileInput?.addEventListener("change", async () => {
    await addFiles(Array.from(fileInput.files || []));
    fileInput.value = "";
  });

  const messages = $("#messages");
  messages?.addEventListener("scroll", updateJumpButton);
  $("#jumpBottom")?.addEventListener("click", () => {
    messages?.scrollTo({ top: messages.scrollHeight, behavior: "smooth" });
  });

  form?.addEventListener("dragover", (event) => {
    event.preventDefault();
    form.classList.add("dragover");
  });
  form?.addEventListener("dragleave", () => form.classList.remove("dragover"));
  form?.addEventListener("drop", async (event) => {
    event.preventDefault();
    form.classList.remove("dragover");
    await addFiles(Array.from(event.dataTransfer?.files || []));
  });

  document.addEventListener("click", async (event) => {
    const suggestion = event.target.closest("[data-suggestion]");
    if (suggestion) {
      const inputEl = $("#input");
      inputEl.value = suggestion.dataset.suggestion || "";
      autoGrow(inputEl);
      updateSendState();
      inputEl.focus();
      return;
    }

    const answer = event.target.closest("[data-answer]");
    if (answer) {
      const card = answer.closest(".msg");
      answerQuestion(answer.dataset.answer || "", Number(card?.dataset.index ?? -1));
      return;
    }

    const adoptBtn = event.target.closest("[data-branch-adopt]");
    if (adoptBtn) {
      const card = adoptBtn.closest(".branch-card");
      const msgEl = adoptBtn.closest(".msg");
      const branchID = card?.dataset.branchId || "";
      const index = Number(msgEl?.dataset.index ?? -1);
      const goalID = state.goalId || "";
      if (!branchID || !goalID) {
        toast("缺少支线或目标信息");
        return;
      }
      adoptBtn.disabled = true;
      adoptBranch(goalID, branchID)
        .then(() => {
          const branch = state.messages[index]?.branches?.find((b) => b.branch_id === branchID);
          if (branch) branch.adopted = true;
          toast("已采纳，下一次轮边界注入主线");
          renderMessages();
        })
        .catch((err) => {
          adoptBtn.disabled = false;
          toast(err?.message || "采纳失败");
        });
      return;
    }

    const submitAnswer = event.target.closest("[data-question-submit]");
    if (submitAnswer) {
      const card = submitAnswer.closest(".question-card");
      const inputEl = card?.querySelector("[data-question-input]");
      const value = inputEl?.value.trim() || "";
      if (!value) {
        toast("先写一个答案");
        inputEl?.focus();
        return;
      }
      const message = submitAnswer.closest(".msg");
      answerQuestion(value, Number(message?.dataset.index ?? -1));
      return;
    }

    const retry = event.target.closest("[data-retry-index]");
    if (retry) {
      retryMessage(Number(retry.dataset.retryIndex));
      return;
    }

    const copy = event.target.closest("[data-copy-index]");
    if (copy) {
      const message = state.messages[Number(copy.dataset.copyIndex)];
      if (!message?.content) return;
      try {
        await navigator.clipboard.writeText(message.content);
        toast("已复制");
      } catch {
        toast("当前浏览器不允许写入剪贴板", "bad");
      }
      return;
    }

    const remove = event.target.closest("[data-remove-attachment]");
    if (remove) {
      state.attachments.splice(Number(remove.dataset.removeAttachment), 1);
      renderAttachments();
      updateSendState();
      return;
    }

    const lightbox = event.target.closest("[data-lightbox]");
    if (lightbox) {
      emit("lightbox", lightbox.dataset.lightbox);
    }
  });

  document.addEventListener("keydown", (event) => {
    if (event.key !== "Escape" || !state.streaming) return;
    if (document.body.classList.contains("modal-open")) return;
    stopStreaming();
  });

  renderAttachments();
  updateSendState();
  updateJumpButton();
}

function autoGrow(input) {
  if (!input) return;
  input.style.height = "auto";
  input.style.height = `${Math.min(input.scrollHeight, 190)}px`;
}

export function updateSendState() {
  const input = $("#input");
  const sendBtn = $("#sendBtn");
  const stopBtn = $("#stopBtn");
  const hint = $("#tokenHint");
  if (!input || !sendBtn) return;
  const hasContent = Boolean(input.value.trim()) || state.attachments.length > 0;
  sendBtn.disabled = state.streaming || !hasContent || !state.model;
  if (stopBtn) stopBtn.hidden = !state.streaming;
  if (hint) {
    if (state.streaming) hint.textContent = "生成中…";
    else if (!state.model) hint.textContent = "未配置模型";
    else if (state.messages.length) {
      const chars = state.messages.reduce(
        (sum, message) => sum + (message.content?.length || 0) + (message.thinking?.length || 0),
        0,
      );
      const tokens = state.messages.reduce(
        (sum, message) =>
          sum + (message.usage ? (message.usage.prompt_tokens || 0) + (message.usage.completion_tokens || 0) : 0),
        0,
      );
      hint.textContent = `上下文约 ${chars} 字${tokens ? ` · ${formatTokens(tokens)} tokens` : ""}`;
    } else hint.textContent = "未发送";
  }
}

function scheduleRender() {
  if (renderTicket) return;
  renderTicket = requestAnimationFrame(() => {
    renderTicket = 0;
    renderMessages();
    updateSendState();
    updateJumpButton();
  });
}

function updateJumpButton() {
  const scroller = $("#messages");
  const button = $("#jumpBottom");
  if (!scroller || !button) return;
  const far = scroller.scrollHeight - scroller.scrollTop - scroller.clientHeight > 240;
  button.hidden = !far;
}

function scheduleDraft() {
  const status = $("#draftStatus");
  if (!state.sessionId) {
    if (status) status.textContent = "";
    return;
  }
  if (status) status.textContent = "保存草稿…";
  clearTimeout(draftTimer);
  draftTimer = setTimeout(async () => {
    const text = $("#input")?.value || "";
    try {
      await saveCurrentDraft(text);
      if (status) status.textContent = text ? "草稿已保存" : "";
    } catch {
      if (status) status.textContent = "";
    }
  }, 700);
}

async function saveCurrentDraft(text) {
  if (!state.sessionId) return;
  await api.saveDraft(state.sessionId, text);
}

function buildPayload(messages) {
  return messages
    .filter((message) => {
      if (message.role === "user") return true;
      return message.role === "assistant" && Boolean(message.content) && !message.question;
    })
    .map((message) => ({
      role: message.role,
      content: message.content || "",
      images: message.images || [],
    }));
}

function setStreaming(value) {
  state.streaming = value;
  updateSendState();
}

async function runTurn({ answer = "", retry = false } = {}) {
  if (state.streaming) return;
  const payload = buildPayload(state.messages);
  const placeholder = {
    role: "assistant",
    content: "",
    thinking: "",
    tools: [],
    branches: [],
    emote: null,
    question: null,
    usage: null,
    stop_reason: "",
    status: "streaming",
    model: state.model,
    error: "",
  };
  state.messages.push(placeholder);
  state.abort = new AbortController();
  setStreaming(true);
  renderMessages({ forceBottom: true });

  try {
    await chatStream(
      {
        session_id: state.sessionId || undefined,
        model: state.model,
        messages: payload,
        answer: answer || undefined,
        retry,
      },
      {
        signal: state.abort.signal,
        onSession(id) {
          if (!id) return;
          const isNew = !state.sessionId;
          state.sessionId = id;
          if (isNew) {
            scheduleDraft();
            emit("sessions-changed");
          }
        },
        onGoal(id) {
          state.goalId = id || "";
        },
        onEvent(event) {
          switch (event.type) {
            case "thinking":
              placeholder.thinking += event.delta || "";
              scheduleRender();
              break;
            case "text":
              placeholder.content += event.delta || "";
              scheduleRender();
              break;
            case "tool":
              placeholder.tools.push({
                name: event.tool_name || "tool",
                phase: event.phase || "result",
                arguments: event.arguments,
                output: event.output,
                error: event.status && event.status !== "ok" ? event.output : "",
                duration_ms: event.duration_ms,
                status: event.status || "ok",
              });
              scheduleRender();
              break;
            case "branch":
              placeholder.branches.push({
                branch_id: event.branch_id || "",
                content: event.content || "",
                output: event.output || "",
                adopted: false,
              });
              scheduleRender();
              break;
            case "question":
              placeholder.question = {
                text: event.question || "",
                options: event.options || [],
                allow_custom: Boolean(event.allow_custom),
                answer: "",
              };
              placeholder.status = "waiting";
              scheduleRender();
              break;
            case "emote":
              // 正文结束后、done 之前到达：只记在当前 assistant 占位上，不新增气泡。
              if (event.emote_name) {
                placeholder.emote = {
                  name: event.emote_name,
                  label: event.emote_label || "",
                  file: event.emote_file || "",
                  score: Number(event.emote_score) || 0,
                };
                scheduleRender();
              }
              break;
            case "done":
              placeholder.usage = event.usage || null;
              placeholder.stop_reason = event.stop_reason || "";
              placeholder.status = "complete";
              scheduleRender();
              break;
            default:
              break;
          }
        },
      },
    );
    if (placeholder.status === "streaming") {
      placeholder.status = placeholder.question ? "waiting" : "incomplete";
    }
  } catch (error) {
    if (error?.name === "AbortError") {
      placeholder.status = "canceled";
      placeholder.error = "";
    } else if (error instanceof AuthError) {
      placeholder.status = "incomplete";
      emit("auth-required");
    } else {
      placeholder.status = "incomplete";
      placeholder.error = error instanceof ApiError ? error.message : "请求失败";
      toast(placeholder.error, "bad");
    }
  } finally {
    state.abort = null;
    setStreaming(false);
    renderMessages();
    updateJumpButton();
    emit("sessions-changed");
    emit("conversation-changed");
    $("#input")?.focus();
  }
}

export function sendCurrent() {
  if (state.streaming) return;
  const input = $("#input");
  const text = input?.value.trim() || "";
  const images = state.attachments.filter((item) => item.kind === "image").map((item) => item.dataUrl);
  if (!text && !images.length) return;
  if (!state.model) {
    toast("没有可用模型，先检查 providers.json", "bad");
    return;
  }

  let content = text;
  for (const attachment of state.attachments.filter((item) => item.kind === "text")) {
    content += `\n\n[附件：${attachment.name}]\n\`\`\`\n${attachment.text}\n\`\`\``;
  }

  state.messages.push({
    role: "user",
    content,
    images,
    status: "complete",
  });
  if (input) {
    input.value = "";
    autoGrow(input);
  }
  state.attachments = [];
  renderAttachments();
  clearTimeout(draftTimer);
  if (state.sessionId) saveCurrentDraft("").catch(() => {});
  renderMessages({ forceBottom: true });
  updateSendState();
  runTurn();
}

export function stopStreaming() {
  state.abort?.abort();
}

function retryMessage(index) {
  if (state.streaming || index < 0) return;
  let userIndex = index - 1;
  while (userIndex >= 0 && state.messages[userIndex].role !== "user") userIndex -= 1;
  if (userIndex < 0) return;
  state.messages = state.messages.slice(0, userIndex + 1);
  renderMessages({ forceBottom: true });
  runTurn({ retry: true });
}

function answerQuestion(value, index) {
  const message = state.messages[index];
  if (!message?.question || message.question.answer || state.streaming) return;
  message.question.answer = value;
  message.status = "complete";
  state.messages.push({ role: "user", content: value, status: "complete" });
  renderMessages({ forceBottom: true });
  runTurn({ answer: value });
}

async function addFiles(files) {
  if (!files.length) return;
  for (const file of files) {
    if (state.attachments.length >= MAX_ATTACHMENTS) {
      toast(`一次最多 ${MAX_ATTACHMENTS} 个附件`, "bad");
      break;
    }
    if (file.type.startsWith("image/")) {
      if (file.size > IMAGE_LIMIT) {
        toast(`${file.name} 超过 8 MB 图片上限`, "bad");
        continue;
      }
      const dataUrl = await readDataURL(file);
      state.attachments.push({ kind: "image", name: file.name, dataUrl });
    } else if (TEXT_EXT.test(file.name) || file.type.startsWith("text/")) {
      if (file.size > TEXT_LIMIT) {
        toast(`${file.name} 超过 1 MB 文本上限`, "bad");
        continue;
      }
      state.attachments.push({ kind: "text", name: file.name, text: await file.text() });
    } else {
      toast(`${file.name}: 体验版只发送图片与文本附件`, "bad");
    }
  }
  renderAttachments();
  updateSendState();
  scheduleDraft();
}

function readDataURL(file) {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result || ""));
    reader.onerror = () => reject(reader.error);
    reader.readAsDataURL(file);
  });
}

export function resetChatInput() {
  const input = $("#input");
  if (input) {
    input.value = "";
    autoGrow(input);
  }
  state.attachments = [];
  renderAttachments();
  updateSendState();
}

export function loadSessionMessages(session) {
  state.messages = (session.messages || []).map((message) => {
    if (message.role === "user") {
      return { role: "user", content: message.content || "", images: [], status: "complete" };
    }
    return {
      role: "assistant",
      content: message.content || "",
      thinking: message.thinking || "",
      tools: (message.tools || []).map((tool) => ({
        name: tool.name,
        arguments: tool.arguments,
        output: tool.output,
        duration_ms: tool.duration_ms,
        status: tool.status || "ok",
      })),
      // 历史消息带 emote 字段时同样在回复末尾渲染（走 render.js 的同一条路径）。
      emote:
        message.emote && (message.emote.name || message.emote.file)
          ? {
              name: message.emote.name || "",
              label: message.emote.label || "",
              file: message.emote.file || "",
              score: Number(message.emote.score) || 0,
            }
          : null,
      usage: message.usage || null,
      stop_reason: message.stop_reason || "",
      status: message.status === "incomplete" ? "incomplete" : "complete",
      question: message.question
        ? {
            text: message.question,
            options: message.question_options || [],
            allow_custom: Boolean(message.question_allow_custom),
            answer: "",
          }
        : null,
      model: state.model,
      error: "",
    };
  });
  if (session.manifest?.title) state.sessionTitle = session.manifest.title;
  setHeader(state.sessionTitle, state.model || "");
  renderMessages({ forceBottom: true });
  updateSendState();
  updateJumpButton();
}
