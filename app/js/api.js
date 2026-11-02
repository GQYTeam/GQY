export class ApiError extends Error {
  constructor(message, status = 0, code = "") {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
  }
}

export class AuthError extends ApiError {
  constructor(message = "需要访问令牌") {
    super(message, 401, "auth_required");
    this.name = "AuthError";
  }
}

async function parseError(response) {
  let message = `请求失败（HTTP ${response.status}）`;
  let code = "";
  try {
    const body = await response.json();
    if (body?.error) {
      message = body.error.message || message;
      code = body.error.code || code;
    } else if (body?.message) {
      message = body.message;
    }
  } catch {
    /* keep the status message */
  }
  return { message, code };
}

async function request(path, options = {}) {
  const response = await fetch(path, {
    credentials: "same-origin",
    ...options,
  });
  if (response.status === 401) {
    throw new AuthError();
  }
  if (!response.ok) {
    const { message, code } = await parseError(response);
    throw new ApiError(message, response.status, code);
  }
  if (response.status === 204) return null;
  const type = response.headers.get("content-type") || "";
  if (type.includes("application/json")) return response.json();
  return response.text();
}

function json(method, body) {
  return {
    method,
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  };
}

export const api = {
  health: () => request("/healthz"),
  config: () => request("/api/config"),
  persona: () => request("/api/persona"),
  savePersona: (persona) => request("/api/persona", json("PUT", persona)),
  models: async () => {
    const body = await request("/v1/models");
    return body?.data || [];
  },
  status: () => request("/api/gateway/status"),
  tools: async () => {
    const body = await request("/api/tools");
    return body?.tools || [];
  },
  reload: () => request("/api/config/reload", { method: "POST" }),
  plugins: async () => {
    const body = await request("/api/plugins");
    return body?.plugins || [];
  },
  pluginScan: () => request("/api/plugins/reload", { method: "POST" }),
  pluginLoad: (id) => request(`/api/plugins/${encodeURIComponent(id)}/load`, { method: "POST" }),
  pluginUnload: (id) => request(`/api/plugins/${encodeURIComponent(id)}/unload`, { method: "POST" }),
  // 插件设置（PL3）：安装的插件只读，但设置值可改；删除会卸载并移除目录（PL4）。
  pluginSettings: (id) => request(`/api/plugins/${encodeURIComponent(id)}/settings`),
  pluginSettingsSave: (id, values) =>
    request(`/api/plugins/${encodeURIComponent(id)}/settings`, json("PUT", { values })),
  pluginDelete: (id) => request(`/api/plugins/${encodeURIComponent(id)}`, { method: "DELETE" }),
  // 全局默认模型（HR5）：未指定 model 的请求都走它。
  activeModel: () => request("/api/model/active"),
  setActiveModel: (model, persist = true) =>
    request("/api/model/active", { method: "PUT", body: { model, persist } }),
  pluginInstall: (source, id) => request("/api/plugins/install", json("POST", { source, id })),
  sessions: async () => {
    const body = await request("/api/sessions");
    return body?.sessions || [];
  },
  session: (id) => request(`/api/sessions/${encodeURIComponent(id)}`),
  createSession: (workspace = "") => request("/api/sessions", json("POST", { workspace })),
  deleteSession: (id) => request(`/api/sessions/${encodeURIComponent(id)}`, { method: "DELETE" }),
  draft: async (id) => {
    const body = await request(`/api/sessions/${encodeURIComponent(id)}/draft`);
    return body?.draft || "";
  },
  saveDraft: (id, draft) =>
    request(`/api/sessions/${encodeURIComponent(id)}/draft`, json("PUT", { draft })),
  login: async (token) => {
    const response = await fetch("/api/auth", {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ token }),
    });
    if (!response.ok) {
      const { message, code } = await parseError(response);
      throw new ApiError(message, response.status, code);
    }
    return response.json();
  },
};

function dispatchFrame(frame, onEvent) {
  for (const rawLine of frame.split("\n")) {
    const line = rawLine.trim();
    if (!line.startsWith("data:")) continue;
    const data = line.slice(5).trim();
    if (!data || data === "[DONE]") continue;
    try {
      onEvent?.(JSON.parse(data));
    } catch {
      /* ignore malformed frames; the server logs its own errors */
    }
  }
}

export async function chatStream(payload, { onEvent, onSession, onGoal, signal } = {}) {
  const response = await fetch("/api/chat", {
    ...json("POST", payload),
    credentials: "same-origin",
    signal,
  });
  if (response.status === 401) throw new AuthError();
  if (!response.ok) {
    const { message, code } = await parseError(response);
    throw new ApiError(message, response.status, code);
  }
  const sessionID = response.headers.get("X-GQY-Session") || "";
  if (sessionID) onSession?.(sessionID);
  const goalID = response.headers.get("X-GQY-Goal") || "";
  if (goalID) onGoal?.(goalID);

  if (!response.body) {
    const text = await response.text();
    for (const frame of text.split(/\r?\n\r?\n/)) dispatchFrame(frame, onEvent);
    return;
  }

  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  let buffer = "";
  while (true) {
    const { done, value } = await reader.read();
    if (done) break;
    buffer += decoder.decode(value, { stream: true }).replace(/\r\n/g, "\n");
    let index = buffer.indexOf("\n\n");
    while (index !== -1) {
      dispatchFrame(buffer.slice(0, index), onEvent);
      buffer = buffer.slice(index + 2);
      index = buffer.indexOf("\n\n");
    }
  }
  buffer += decoder.decode();
  if (buffer.trim()) dispatchFrame(buffer, onEvent);
}

// adoptBranch 显式采纳一条已执行的支线（017），摘要在下一次轮边界注入主线。
export async function adoptBranch(goalID, branchID, summary = "") {
  const response = await fetch(`/api/goal/${encodeURIComponent(goalID)}/adopt`, {
    ...json("POST", { branch_id: branchID, summary }),
    credentials: "same-origin",
  });
  if (response.status === 401) throw new AuthError();
  if (!response.ok) {
    const { message, code } = await parseError(response);
    throw new ApiError(message, response.status, code);
  }
  return response.json().catch(() => ({}));
}

// events 订阅服务端 SSE（HR7）：配置/插件/全局模型变化即时推送，替代前端轮询。
export function events(onEvent) {
  if (typeof EventSource === "undefined") return () => {};
  const source = new EventSource("/api/events");
  source.onmessage = (message) => {
    if (!message.data) return;
    try {
      onEvent?.(JSON.parse(message.data));
    } catch {
      /* 忽略坏帧 */
    }
  };
  return () => source.close();
}
