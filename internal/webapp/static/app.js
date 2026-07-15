const $ = (selector) => document.querySelector(selector);

const els = {
  shell: $("#app-shell"),
  sidebar: $("#sidebar"),
  scrim: $("#scrim"),
  sessionList: $("#session-list"),
  sessionCount: $("#session-count"),
  newSession: $("#new-session"),
  taskTitle: $("#task-title"),
  runState: $("#run-state"),
  conversation: $("#conversation"),
  emptyState: $("#empty-state"),
  messageList: $("#message-list"),
  form: $("#composer-form"),
  input: $("#composer-input"),
  send: $("#send-button"),
  confirmationMode: $("#confirmation-mode"),
  executionModeControl: $("#execution-mode-control"),
  confirmationModeMenu: $("#confirmation-mode-menu"),
  confirmationModeLabel: $("#confirmation-mode-label"),
  confirmationModeOptions: [...document.querySelectorAll("[data-confirmation-mode]")],
  progressNote: $("#progress-note"),
  progressText: $("#progress-text"),
  stopRun: $("#stop-run"),
  inspector: $("#inspector"),
  inspectorStatus: $("#inspector-status"),
  inspectorSession: $("#inspector-session"),
  inspectorEvents: $("#inspector-events"),
  eventTimeline: $("#event-timeline"),
  toggleInspector: $("#toggle-inspector"),
  activityCount: $("#activity-count"),
  connectionDot: $("#connection-dot"),
  connectionLabel: $("#connection-label"),
  connectionBanner: $("#connection-banner"),
  connectionError: $("#connection-error"),
  agentName: $("#agent-name"),
  deleteDialog: $("#delete-dialog"),
  toast: $("#toast"),
};

const state = {
  appName: "",
  userId: localStorage.getItem("adk-workbench-user") || `local-${crypto.randomUUID().slice(0, 8)}`,
  sessions: [],
  currentSession: null,
  messages: [],
  timeline: [],
  running: false,
  controller: null,
  inspectorOpen: false,
  pendingDeleteId: "",
  lastPrompt: "",
  toastTimer: null,
  mathRenderFrame: 0,
  codeHighlightFrame: 0,
  conversationScrollFrame: 0,
  codeHighlighterConfigured: false,
  executionDisclosurePreferences: new Map(),
};
localStorage.setItem("adk-workbench-user", state.userId);

const icons = {
  trash: '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M4 7h16m-10 4v6m4-6v6M9 7l1-2h4l1 2m3 0-1 13H7L6 7"/></svg>',
  tool: '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="m14.5 6.5 3-3 3 3-3 3m-2-1L7 17l-2 2m4-4 2 2"/></svg>',
  check: '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="m5 12 4 4L19 6"/></svg>',
  alert: '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M12 8v5m0 3h.01M4.9 19h14.2L12 5 4.9 19Z"/></svg>',
};

function apiPath(path) {
  return `/api${path}`;
}

async function api(path, options = {}) {
  const response = await fetch(apiPath(path), {
    ...options,
    headers: { "Content-Type": "application/json", ...(options.headers || {}) },
  });
  if (!response.ok) {
    const detail = (await response.text()).trim();
    throw new Error(detail || `${response.status} ${response.statusText}`);
  }
  if (response.status === 204) return null;
  const text = await response.text();
  return text ? JSON.parse(text) : null;
}

function encode(value) {
  return encodeURIComponent(value);
}

async function bootstrap() {
  setConnection("loading");
  els.connectionBanner.hidden = true;
  els.input.disabled = true;
  updateComposer();
  try {
    const apps = await api("/list-apps");
    if (!Array.isArray(apps) || apps.length === 0) throw new Error("服务中没有可用的 Agent");
    state.appName = apps[0];
    els.agentName.textContent = state.appName;
    await loadSessions();
    if (state.sessions.length) {
      const latest = [...state.sessions].sort((a, b) => b.lastUpdateTime - a.lastUpdateTime)[0];
      await openSession(latest.id);
    } else {
      await createSession();
    }
    setConnection("connected");
    els.input.disabled = false;
    updateComposer();
  } catch (error) {
    showConnectionError(error);
  }
}

async function loadSessions() {
  const path = `/apps/${encode(state.appName)}/users/${encode(state.userId)}/sessions`;
  state.sessions = (await api(path)) || [];
  state.sessions.sort((a, b) => b.lastUpdateTime - a.lastUpdateTime);
  renderSessions();
}

async function createSession() {
  if (state.running) return;
  const path = `/apps/${encode(state.appName)}/users/${encode(state.userId)}/sessions`;
  const session = await api(path, { method: "POST", body: "{}" });
  state.sessions.unshift(session);
  state.currentSession = session;
  state.messages = [];
  state.timeline = [];
  state.executionDisclosurePreferences.clear();
  state.lastPrompt = "";
  renderAll({ forceFollow: true });
  closeSidebar();
  requestAnimationFrame(() => els.input.focus());
}

async function openSession(sessionId) {
  if (state.running || !sessionId) return;
  const path = `/apps/${encode(state.appName)}/users/${encode(state.userId)}/sessions/${encode(sessionId)}`;
  state.currentSession = await api(path);
  state.messages = messagesFromEvents(state.currentSession.events || []);
  state.timeline = [];
  state.executionDisclosurePreferences.clear();
  renderAll({ forceFollow: true });
  closeSidebar();
}

function messagesFromEvents(events) {
  const messages = [];
  let assistant = null;
  for (const event of events) {
    const parts = event.content?.parts || [];
    const userText = event.author === "user"
      ? parts.filter((part) => part.text && !part.thought).map((part) => part.text).join("")
      : "";
    if (userText) {
      messages.push(newMessage("user", userText, event.id));
      assistant = null;
      continue;
    }
    const meaningful = parts.some((part) => part.text || part.functionCall || part.functionResponse) || event.errorMessage;
    if (!meaningful || event.author === "user") continue;
    if (!assistant) {
      assistant = newMessage("assistant", "", event.invocationId || event.id);
      messages.push(assistant);
    }
    processEvent(event, assistant, { render: false, record: false });
  }
  return messages;
}

function newMessage(role, text = "", id = crypto.randomUUID()) {
  return {
    id,
    role,
    text,
    executionItems: [],
    confirmationMode: "confirm",
    error: "",
    streaming: false,
    timestamp: Date.now(),
  };
}

function renderAll({ forceFollow = false } = {}) {
  renderSessions();
  renderMessages({ forceFollow });
  renderInspector();
  updateTaskTitle();
  updateComposer();
}

function renderSessions() {
  els.sessionCount.textContent = String(state.sessions.length);
  if (!state.sessions.length) {
    els.sessionList.innerHTML = '<p class="timeline-empty">还没有任务</p>';
    return;
  }
  els.sessionList.innerHTML = state.sessions.map((session) => {
    const active = session.id === state.currentSession?.id;
    const title = sessionTitle(session);
    return `<div class="session-item${active ? " active" : ""}">
      <button class="session-select" type="button" data-session-id="${escapeAttr(session.id)}" aria-current="${active ? "page" : "false"}">
        <strong>${escapeHTML(title)}</strong><span>${relativeTime(session.lastUpdateTime)}</span>
      </button>
      <button class="session-delete" type="button" data-delete-session="${escapeAttr(session.id)}" aria-label="删除任务：${escapeAttr(title)}">${icons.trash}</button>
    </div>`;
  }).join("");
}

function sessionTitle(session) {
  const userEvent = (session.events || []).find((event) => event.author === "user" && event.content?.parts?.some((part) => part.text));
  const title = userEvent?.content?.parts?.filter((part) => part.text && !part.thought).map((part) => part.text).join("").trim();
  return title ? truncate(title.replace(/\s+/g, " "), 34) : "新任务";
}

function updateTaskTitle() {
  els.taskTitle.textContent = state.currentSession ? sessionTitle(state.currentSession) : "新任务";
}

function renderMessages({ forceFollow = false } = {}) {
  const followOutput = forceFollow || state.conversationScrollFrame !== 0 || isConversationNearBottom();
  const hasMessages = state.messages.length > 0;
  const openDisclosures = new Set([...els.messageList.querySelectorAll("details:not(.execution-group)[open][data-disclosure-id]")]
    .map((detail) => detail.dataset.disclosureId));
  els.emptyState.hidden = hasMessages;
  els.messageList.hidden = !hasMessages;
  if (!hasMessages) {
    els.messageList.innerHTML = "";
    return;
  }
  els.messageList.innerHTML = state.messages.map(renderMessage).join("");
  for (const detail of els.messageList.querySelectorAll("details:not(.execution-group)[data-disclosure-id]")) {
    if (openDisclosures.has(detail.dataset.disclosureId)) detail.open = true;
  }
  scheduleMathRender();
  scheduleCodeHighlight();
  scheduleConversationFollow(followOutput);
}

function renderMessage(message) {
  const isUser = message.role === "user";
  const modeBadge = isUser && message.confirmationMode === "auto"
    ? '<span class="message-mode">本次授权</span>'
    : "";
  const execution = !isUser && message.executionItems.length
    ? renderExecution(message)
    : "";
  const content = message.text
    ? renderMarkdown(message.text)
    : message.streaming && !message.executionItems.length
      ? '<span class="typing-cursor" aria-label="正在生成"></span>'
      : "";
  const error = message.error
    ? `<div class="message-error"><strong>运行没有完成</strong><span>${escapeHTML(message.error)}</span>${state.lastPrompt ? '<br><button class="text-button" type="button" data-retry>重试上次任务</button>' : ""}</div>`
    : "";
  return `<article class="message ${isUser ? "user" : "assistant"}" data-message-id="${escapeAttr(message.id)}">
    <div class="message-meta"><span class="author">${isUser ? "你" : "Agent"}</span><time>${formatTime(message.timestamp)}</time>${modeBadge}</div>
    ${execution}<div class="message-content">${content}</div>${error}
  </article>`;
}

function renderExecution(message) {
  const items = message.executionItems;
  const activities = items.filter((item) => item.type !== "thought");
  const waiting = activities.some((activity) => activity.type === "approval" && activity.status === "pending");
  const autoPending = waiting && message.confirmationMode === "auto";
  const rejected = activities.some((activity) => activity.status === "rejected");
  const running = message.streaming || activities.some((activity) => activity.status === "running");
  const preference = state.executionDisclosurePreferences.get(message.id);
  const needsAttention = running || waiting || Boolean(message.error);
  const expanded = preference === "open" || (needsAttention && preference !== "closed");
  const status = waiting
    ? { label: autoPending ? "自动执行中" : "等待确认", className: " pending", icon: icons.alert }
    : message.error
      ? { label: "未完成", className: " rejected", icon: icons.alert }
      : running
        ? { label: "执行中", className: " running", icon: icons.tool }
        : rejected
          ? { label: "含已拒绝调用", className: " rejected", icon: icons.alert }
          : { label: "全部完成", className: " success", icon: icons.check };
  const timeline = items.map((item) => item.type === "thought"
    ? renderThought(item, message.id)
    : renderActivity(item, message.id, message.confirmationMode)).join("");
  return `<details class="execution-group${status.className}" data-message-id="${escapeAttr(message.id)}" data-disclosure-id="execution-${escapeAttr(message.id)}"${expanded ? " open" : ""}>
    <summary class="execution-summary"><span class="execution-icon">${status.icon}</span><strong>执行过程</strong><span class="execution-meta">${escapeHTML(executionSummary(items))}</span><span class="execution-status">${escapeHTML(status.label)}</span><span class="tool-chevron" aria-hidden="true"></span></summary>
    <div class="execution-body"><div class="execution-timeline">${timeline}</div></div>
  </details>`;
}

function executionSummary(items) {
  const thoughts = items.filter((item) => item.type === "thought");
  const tools = items.filter((item) => item.type === "tool");
  const approvals = items.filter((item) => item.type === "approval");
  const parts = [];
  if (thoughts.length) parts.push(`${thoughts.length} 段思考`);
  if (tools.length) {
    const names = tools.map((tool) => activityToolName(tool.title));
    const firstName = names[0];
    parts.push(names.every((name) => name === firstName)
      ? `${firstName}${names.length > 1 ? ` × ${names.length}` : ""}`
      : `${names.length} 次工具调用`);
  }
  if (approvals.length) parts.push(`${approvals.length} 次确认`);
  return parts.join(" · ") || "准备中";
}

function activityToolName(title) {
  return String(title || "工具")
    .replace(/^确认调用\s+/, "")
    .replace(/^调用\s+/, "")
    .replace(/^工具\s+/, "")
    .replace(/\s+已返回$/, "");
}

function renderThought(thought, messageId) {
  const preview = truncate(thought.text.replace(/\s+/g, " ").trim(), 92) || "正在整理下一步";
  return `<details class="execution-thought execution-step${thought.streaming ? " streaming" : ""}" data-disclosure-id="thought-${escapeAttr(messageId)}-${escapeAttr(thought.id)}">
    <summary><span class="thought-marker" aria-hidden="true"></span><strong>${thought.streaming ? "思考中" : "思考"}</strong><span class="thought-preview">${escapeHTML(preview)}</span><span class="tool-chevron" aria-hidden="true"></span></summary>
    <div class="thinking-content">${escapeHTML(thought.text)}</div>
  </details>`;
}

function renderActivity(activity, messageId, confirmationMode = "confirm") {
  const isApproval = activity.type === "approval";
  const autoPending = isApproval && activity.status === "pending" && confirmationMode === "auto";
  const className = activity.status === "approved" || activity.status === "completed" ? " success" : activity.status === "rejected" ? " rejected" : "";
  let statusLabel = {
    pending: "等待确认",
    running: "执行中",
    completed: "已完成",
    approved: "已允许",
    rejected: "已拒绝",
  }[activity.status] || activity.status;
  if (autoPending) statusLabel = "自动执行中";
  if (activity.autoApproved) statusLabel = "已自动授权";
  const actions = isApproval && activity.status === "pending" && !autoPending
    ? `<div class="approval-actions">
        <button class="approve-button" type="button" data-confirm-call="${escapeAttr(activity.id)}" data-confirm-event="${escapeAttr(activity.eventId || "")}" data-approved="true">允许执行</button>
        <button class="reject-button" type="button" data-confirm-call="${escapeAttr(activity.id)}" data-confirm-event="${escapeAttr(activity.eventId || "")}" data-approved="false">拒绝</button>
      </div>`
    : "";
  const request = activity.requestDetail
    ? `<section class="tool-detail-group"><span>请求参数</span><pre>${escapeHTML(activity.requestDetail)}</pre></section>`
    : "";
  const response = activity.responseDetail
    ? `<section class="tool-detail-group"><span>执行结果</span><pre>${escapeHTML(activity.responseDetail)}</pre></section>`
    : "";
  const expanded = isApproval && activity.status === "pending" ? " open" : "";
  const hint = activity.autoApproved
    ? "已按本次授权自动继续。"
    : autoPending
      ? "将在本次授权范围内自动继续。"
      : activity.hint;
  return `<details class="tool-card execution-step${isApproval ? " approval" : ""}${className}" data-disclosure-id="activity-${escapeAttr(messageId)}-${escapeAttr(activity.id)}"${expanded}>
    <summary class="tool-heading"><span class="tool-icon">${isApproval ? icons.alert : activity.status === "completed" ? icons.check : icons.tool}</span><strong>${escapeHTML(activity.title)}</strong><span class="tool-status">${escapeHTML(statusLabel)}</span><span class="tool-chevron" aria-hidden="true"></span></summary>
    <div class="tool-detail-body">${hint ? `<p>${escapeHTML(hint)}</p>` : ""}${request}${response}${actions}</div>
  </details>`;
}

function renderInspector() {
  els.inspector.hidden = !state.inspectorOpen;
  els.shell.classList.toggle("inspector-open", state.inspectorOpen);
  els.toggleInspector.setAttribute("aria-expanded", String(state.inspectorOpen));
  els.activityCount.textContent = String(state.timeline.length);
  els.inspectorStatus.textContent = state.running ? "执行中" : "就绪";
  els.inspectorSession.textContent = state.currentSession?.id || "—";
  els.inspectorEvents.textContent = String(state.timeline.length);
  if (!state.timeline.length) {
    els.eventTimeline.innerHTML = '<li class="timeline-empty">运行任务后，模型输出、工具调用和确认请求会出现在这里。</li>';
    return;
  }
  els.eventTimeline.innerHTML = state.timeline.map((item) => `<li>
    <span class="timeline-dot ${item.type === "tool" ? "tool" : item.type === "error" ? "error" : ""}"></span>
    <div class="timeline-heading"><strong>${escapeHTML(item.title)}</strong><time>${formatTime(item.time)}</time></div>
    ${item.detail ? `<p>${escapeHTML(truncate(item.detail, 220))}</p>` : ""}
  </li>`).join("");
}

function addTimeline(type, title, detail = "") {
  state.timeline.push({ type, title, detail, time: Date.now() });
  renderInspector();
}

async function submitText(text) {
  const prompt = text.trim();
  if (!prompt || state.running || !state.currentSession) return;
  const confirmationMode = els.confirmationMode.value === "auto" ? "auto" : "confirm";
  state.lastPrompt = prompt;
  const user = newMessage("user", prompt);
  const assistant = newMessage("assistant");
  user.confirmationMode = confirmationMode;
  assistant.confirmationMode = confirmationMode;
  assistant.streaming = true;
  els.confirmationMode.value = "confirm";
  updateConfirmationMode();
  setConfirmationModeMenu(false);
  state.messages.push(user, assistant);
  state.currentSession.events = state.currentSession.events || [];
  state.currentSession.events.push({ author: "user", content: { parts: [{ text: prompt }] } });
  els.input.value = "";
  resizeInput();
  updateTaskTitle();
  renderMessages({ forceFollow: true });
  addTimeline("message", "任务已提交", truncate(prompt, 100));
  await runAgent({ role: "user", parts: [{ text: prompt }] }, assistant);
}

async function runAgent(content, assistant, functionCallEventId = "") {
  setRunning(true);
  state.controller = new AbortController();
  let nextContent = content;
  let nextFunctionCallEventId = functionCallEventId;
  let slowTimer = window.setTimeout(() => {
    els.progressText.textContent = "任务仍在执行，可查看记录或停止";
  }, 10000);
  try {
    while (true) {
      const body = {
        appName: state.appName,
        userId: state.userId,
        sessionId: state.currentSession.id,
        newMessage: nextContent,
        streaming: true,
      };
      if (nextFunctionCallEventId) body.functionCallEventId = nextFunctionCallEventId;
      const response = await fetch(apiPath("/run_sse"), {
        method: "POST",
        headers: { "Content-Type": "application/json", Accept: "text/event-stream" },
        body: JSON.stringify(body),
        signal: state.controller.signal,
      });
      if (!response.ok) throw new Error((await response.text()).trim() || `请求失败 (${response.status})`);
      if (!response.body) throw new Error("服务没有返回可读取的流");
      await readEventStream(response.body, (eventName, data) => {
        if (eventName === "error") {
          const parsed = safeJSON(data);
          throw new Error(parsed?.error || data || "Agent 运行失败");
        }
        const event = safeJSON(data);
        if (event) processEvent(event, assistant);
      });
      const pendingApproval = assistant.confirmationMode === "auto"
        ? assistant.executionItems.find((item) => item.type === "approval" && item.status === "pending")
        : null;
      if (!pendingApproval) break;
      pendingApproval.status = "approved";
      pendingApproval.autoApproved = true;
      assistant.streaming = true;
      renderMessages();
      addTimeline("tool", "本次授权已生效", pendingApproval.title);
      nextContent = confirmationResponse(pendingApproval.id, true);
      nextFunctionCallEventId = pendingApproval.eventId;
    }
    assistant.streaming = false;
    if (!assistant.text && !assistant.executionItems.length && !assistant.error) {
      assistant.error = "运行结束，但服务没有返回可显示的结果。";
    }
    addTimeline("message", "运行完成", assistant.text ? truncate(assistant.text, 100) : "Agent 已结束本次运行");
  } catch (error) {
    assistant.streaming = false;
    if (error.name === "AbortError") {
      assistant.error = "你已停止这次运行，已收到的内容仍然保留。";
      addTimeline("error", "运行已停止", "用户主动取消");
    } else {
      assistant.error = friendlyError(error);
      addTimeline("error", "运行失败", assistant.error);
    }
  } finally {
    window.clearTimeout(slowTimer);
    state.controller = null;
    setRunning(false);
    renderMessages();
    try {
      await loadSessions();
      const refreshed = state.sessions.find((session) => session.id === state.currentSession?.id);
      if (refreshed) state.currentSession = refreshed;
      updateTaskTitle();
    } catch (_) {
      showToast("结果已保留，但任务列表暂时无法刷新");
    }
  }
}

async function readEventStream(stream, onEvent) {
  const reader = stream.getReader();
  const decoder = new TextDecoder();
  let buffer = "";
  while (true) {
    const { value, done } = await reader.read();
    buffer += decoder.decode(value || new Uint8Array(), { stream: !done }).replace(/\r\n/g, "\n");
    let boundary;
    while ((boundary = buffer.indexOf("\n\n")) >= 0) {
      const block = buffer.slice(0, boundary);
      buffer = buffer.slice(boundary + 2);
      if (block.trim()) parseEventBlock(block, onEvent);
    }
    if (done) break;
  }
  if (buffer.trim()) parseEventBlock(buffer, onEvent);
}

function parseEventBlock(block, onEvent) {
  let eventName = "message";
  const data = [];
  for (const line of block.split("\n")) {
    if (line.startsWith("event:")) eventName = line.slice(6).trim();
    if (line.startsWith("data:")) data.push(line.slice(5).trimStart());
  }
  if (data.length) onEvent(eventName, data.join("\n"));
}

function processEvent(event, assistant, options = {}) {
  const { render = true, record = true } = options;
  const parts = event.content?.parts || [];
  const visible = parts.filter((part) => part.text && !part.thought).map((part) => part.text).join("");

  if (event.partial) {
    assistant.text += visible;
  } else {
    if (visible) assistant.text = visible;
  }

  let thoughtBuffer = "";
  const flushThought = () => {
    if (!thoughtBuffer) return;
    updateThoughtItem(assistant, thoughtBuffer, Boolean(event.partial));
    thoughtBuffer = "";
  };
  for (const part of parts) {
    if (part.text && part.thought) {
      thoughtBuffer += part.text;
      continue;
    }
    flushThought();
    if (part.functionCall) handleFunctionCall(part.functionCall, event, assistant, record);
    if (part.functionResponse) handleFunctionResponse(part.functionResponse, assistant, record);
  }
  flushThought();
  if (!event.partial) finishCurrentThought(assistant);
  if (event.errorMessage || event.errorCode) {
    assistant.error = event.errorMessage || event.errorCode;
    if (record) addTimeline("error", "模型返回错误", assistant.error);
  }
  if (event.usageMetadata) assistant.usage = event.usageMetadata;
  if (!event.partial && visible && record) addTimeline("message", "模型输出", truncate(visible, 90));
  if (render) {
    renderMessages();
  }
}

function updateThoughtItem(assistant, text, partial) {
  if (!text) return;
  const items = assistant.executionItems;
  const latest = items[items.length - 1];
  if (partial) {
    if (latest?.type === "thought" && latest.streaming) latest.text += text;
    else items.push({ id: crypto.randomUUID(), type: "thought", text, streaming: true });
    return;
  }
  if (latest?.type === "thought" && latest.streaming) {
    latest.text = text;
    latest.streaming = false;
  } else if (latest?.type !== "thought" || latest.text !== text) {
    items.push({ id: crypto.randomUUID(), type: "thought", text, streaming: false });
  }
}

function finishCurrentThought(assistant) {
  const latest = assistant.executionItems[assistant.executionItems.length - 1];
  if (latest?.type === "thought") latest.streaming = false;
}

function handleFunctionCall(call, event, assistant, record) {
  if (assistant.executionItems.some((item) => item.id === call.id)) return;
  finishCurrentThought(assistant);
  const isApproval = call.name === "adk_request_confirmation";
  const original = isApproval ? (call.args?.originalFunctionCall || {}) : call;
  const confirmation = call.args?.toolConfirmation || {};
  const activity = {
    id: call.id || crypto.randomUUID(),
    eventId: event.id || "",
    type: isApproval ? "approval" : "tool",
    title: isApproval ? `确认调用 ${original.name || "工具"}` : `调用 ${call.name || "工具"}`,
    hint: isApproval ? (confirmation.hint || `此操作需要你的明确允许：${original.name || "工具调用"}`) : "",
    requestDetail: stringify(original.args ?? call.args),
    responseDetail: "",
    status: isApproval ? "pending" : "running",
  };
  assistant.executionItems.push(activity);
  if (isApproval && assistant.confirmationMode !== "auto") openInspector(true);
  if (record) addTimeline("tool", isApproval ? "等待工具确认" : activity.title, activity.requestDetail);
}

function handleFunctionResponse(response, assistant, record) {
  const activity = assistant.executionItems.find((item) => item.id === response.id);
  if (activity) {
    activity.status = "completed";
    activity.responseDetail = stringify(response.response);
  } else {
    assistant.executionItems.push({
      id: response.id || crypto.randomUUID(),
      type: "tool",
      title: `工具 ${response.name || "调用"} 已返回`,
      requestDetail: "",
      responseDetail: stringify(response.response),
      status: "completed",
    });
  }
  if (record) addTimeline("tool", `工具 ${response.name || "调用"} 已返回`, stringify(response.response));
}

async function respondToConfirmation(callId, approved, eventId) {
  if (state.running) return;
  let targetMessage;
  let targetActivity;
  for (const message of state.messages) {
    const activity = message.executionItems.find((item) => item.id === callId);
    if (activity) {
      targetMessage = message;
      targetActivity = activity;
      break;
    }
  }
  if (!targetMessage || !targetActivity || targetActivity.status !== "pending") return;
  targetActivity.status = approved ? "approved" : "rejected";
  targetActivity.autoApproved = false;
  targetMessage.streaming = true;
  renderMessages();
  addTimeline("tool", approved ? "已允许工具调用" : "已拒绝工具调用", targetActivity.title);
  await runAgent(confirmationResponse(callId, approved), targetMessage, eventId);
}

function confirmationResponse(callId, approved) {
  return {
    role: "user",
    parts: [{
      functionResponse: {
        id: callId,
        name: "adk_request_confirmation",
        response: { confirmed: approved },
      },
    }],
  };
}

function setRunning(running) {
  state.running = running;
  els.input.disabled = running;
  els.newSession.disabled = running;
  els.executionModeControl.disabled = running;
  if (running) setConfirmationModeMenu(false);
  els.progressNote.hidden = !running;
  els.progressText.textContent = "Agent 正在处理";
  els.runState.className = `run-state${running ? " running" : ""}`;
  els.runState.innerHTML = `<span></span>${running ? "执行中" : "就绪"}`;
  for (const message of state.messages) {
    if (!running && message.streaming) message.streaming = false;
  }
  updateComposer();
  renderInspector();
}

function setConnection(status) {
  els.connectionDot.className = `status-dot${status === "connected" ? " connected" : status === "error" ? " error" : ""}`;
  els.connectionLabel.textContent = status === "connected" ? "服务已连接" : status === "error" ? "连接失败" : "正在连接";
}

function showConnectionError(error) {
  setConnection("error");
  els.connectionError.textContent = friendlyError(error);
  els.connectionBanner.hidden = false;
  els.input.disabled = true;
  updateComposer();
}

function updateComposer() {
  els.send.disabled = state.running || els.input.disabled || !els.input.value.trim() || !state.currentSession;
}

function updateConfirmationMode() {
  const automatic = els.confirmationMode.value === "auto";
  els.executionModeControl.classList.toggle("auto", automatic);
  els.confirmationModeLabel.textContent = automatic ? "本次授权" : "逐项确认";
  for (const option of els.confirmationModeOptions) {
    const selected = option.dataset.confirmationMode === els.confirmationMode.value;
    option.classList.toggle("selected", selected);
    option.setAttribute("aria-checked", String(selected));
  }
}

function setConfirmationModeMenu(open) {
  const nextOpen = Boolean(open) && !state.running;
  els.confirmationModeMenu.hidden = !nextOpen;
  els.executionModeControl.setAttribute("aria-expanded", String(nextOpen));
  if (nextOpen) {
    requestAnimationFrame(() => els.confirmationModeOptions
      .find((option) => option.getAttribute("aria-checked") === "true")?.focus());
  }
}

function chooseConfirmationMode(mode) {
  els.confirmationMode.value = mode === "auto" ? "auto" : "confirm";
  updateConfirmationMode();
  setConfirmationModeMenu(false);
  els.executionModeControl.focus();
}

function resizeInput() {
  els.input.style.height = "auto";
  els.input.style.height = `${Math.min(els.input.scrollHeight, 170)}px`;
  updateComposer();
}

function isConversationNearBottom() {
  const remaining = els.conversation.scrollHeight - els.conversation.clientHeight - els.conversation.scrollTop;
  return remaining <= 96;
}

function scheduleConversationFollow(followOutput) {
  window.cancelAnimationFrame(state.conversationScrollFrame);
  state.conversationScrollFrame = 0;
  if (!followOutput) return;
  state.conversationScrollFrame = window.requestAnimationFrame(() => {
    state.conversationScrollFrame = 0;
    els.conversation.scrollTop = Math.max(0, els.conversation.scrollHeight - els.conversation.clientHeight);
  });
}

function openInspector(open) {
  state.inspectorOpen = open;
  renderInspector();
  updateScrim();
}

function openSidebar() {
  els.sidebar.classList.add("open");
  updateScrim();
}

function closeSidebar() {
  els.sidebar.classList.remove("open");
  updateScrim();
}

function updateScrim() {
  const narrow = window.matchMedia("(max-width: 1180px)").matches;
  els.scrim.hidden = !(els.sidebar.classList.contains("open") || (narrow && state.inspectorOpen));
}

function showToast(message) {
  window.clearTimeout(state.toastTimer);
  els.toast.textContent = message;
  els.toast.hidden = false;
  state.toastTimer = window.setTimeout(() => { els.toast.hidden = true; }, 3200);
}

async function deleteSession(sessionId) {
  const path = `/apps/${encode(state.appName)}/users/${encode(state.userId)}/sessions/${encode(sessionId)}`;
  await api(path, { method: "DELETE" });
  state.sessions = state.sessions.filter((session) => session.id !== sessionId);
  if (state.currentSession?.id === sessionId) {
    if (state.sessions.length) await openSession(state.sessions[0].id);
    else await createSession();
  }
  renderSessions();
  showToast("任务已删除");
}

function renderMarkdown(text) {
  return parseMarkdownSegments(text).map((segment) => segment.type === "code"
    ? renderCodeBlock(segment)
    : renderMarkdownText(segment.text)).join("");
}

function parseMarkdownSegments(text) {
  const lines = String(text).replace(/\r\n?/g, "\n").split("\n");
  const segments = [];
  let markdownLines = [];
  let codeBlock = null;
  const flushMarkdown = () => {
    if (!markdownLines.length) return;
    segments.push({ type: "markdown", text: markdownLines.join("\n") });
    markdownLines = [];
  };

  for (const line of lines) {
    if (codeBlock) {
      if (/^\s*```\s*$/.test(line)) {
        segments.push({ type: "code", language: codeBlock.language, text: codeBlock.lines.join("\n"), unclosed: false });
        codeBlock = null;
      } else {
        codeBlock.lines.push(line);
      }
      continue;
    }
    const opening = line.match(/^\s*```(.*)$/);
    if (opening) {
      flushMarkdown();
      const info = opening[1].trim();
      codeBlock = { language: info ? info.split(/\s+/, 1)[0] : "", lines: [] };
    } else {
      markdownLines.push(line);
    }
  }
  if (codeBlock) {
    segments.push({ type: "code", language: codeBlock.language, text: codeBlock.lines.join("\n"), unclosed: true });
  } else {
    flushMarkdown();
  }
  return segments;
}

function renderCodeBlock(segment) {
  const language = codeLanguageInfo(segment.language);
  return `<div class="message-code-block${segment.unclosed ? " streaming" : ""}">
    <div class="message-code-heading"><span>${escapeHTML(language.label)}</span></div>
    <pre><code class="language-${escapeAttr(language.id)}" data-code-language="${escapeAttr(language.id)}">${escapeHTML(segment.text)}</code></pre>
  </div>`;
}

function codeLanguageInfo(rawLanguage) {
  const source = String(rawLanguage || "").trim().toLowerCase().replace(/^language-/, "");
  const aliases = {
    "": "plaintext", text: "plaintext", txt: "plaintext", none: "plaintext",
    shell: "bash", sh: "bash", zsh: "bash", console: "bash", terminal: "bash",
    golang: "go", "c++": "cpp", "c#": "csharp", cs: "csharp",
    js: "javascript", jsx: "javascript", node: "javascript", nodejs: "javascript",
    ts: "typescript", tsx: "typescript", py: "python", rb: "ruby", rs: "rust",
    yml: "yaml", html: "xml", svg: "xml", docker: "dockerfile", md: "markdown",
  };
  const normalized = aliases[source] || source;
  const supported = /^[a-z0-9_-]+$/.test(normalized)
    && typeof window.hljs?.getLanguage === "function"
    && window.hljs.getLanguage(normalized);
  const id = supported ? normalized : "plaintext";
  const labels = {
    plaintext: "Plain text", bash: "Shell", c: "C", cpp: "C++", csharp: "C#", css: "CSS",
    diff: "Diff", dockerfile: "Dockerfile", go: "Go", graphql: "GraphQL", ini: "INI",
    java: "Java", javascript: "JavaScript", json: "JSON", kotlin: "Kotlin", lua: "Lua",
    makefile: "Makefile", markdown: "Markdown", objectivec: "Objective-C", perl: "Perl",
    php: "PHP", python: "Python", ruby: "Ruby", rust: "Rust", scss: "SCSS", sql: "SQL",
    swift: "Swift", typescript: "TypeScript", vbnet: "VB.NET", xml: "HTML / XML", yaml: "YAML",
  };
  return { id, label: labels[normalized] || (source ? truncate(source, 24) : "Plain text") };
}

function renderMarkdownText(text) {
  const lines = text.split("\n");
  let html = "";
  let listOpen = false;
  for (let lineIndex = 0; lineIndex < lines.length; lineIndex += 1) {
    const line = lines[lineIndex];
    const trimmed = line.trim();
    const mathBlock = renderMarkdownMathBlock(lines, lineIndex);
    if (mathBlock) {
      if (listOpen) { html += "</ul>"; listOpen = false; }
      html += mathBlock.html;
      lineIndex = mathBlock.nextIndex - 1;
      continue;
    }
    const table = renderMarkdownTable(lines, lineIndex);
    if (table) {
      if (listOpen) { html += "</ul>"; listOpen = false; }
      html += table.html;
      lineIndex = table.nextIndex - 1;
      continue;
    }
    const listMatch = trimmed.match(/^[-*]\s+(.+)/);
    if (listMatch) {
      if (!listOpen) { html += "<ul>"; listOpen = true; }
      html += `<li>${inlineMarkdown(listMatch[1])}</li>`;
      continue;
    }
    if (listOpen) { html += "</ul>"; listOpen = false; }
    if (!trimmed) continue;
    const heading = trimmed.match(/^#{1,3}\s+(.+)/);
    if (heading) html += `<h3>${inlineMarkdown(heading[1])}</h3>`;
    else html += `<p>${inlineMarkdown(trimmed)}</p>`;
  }
  if (listOpen) html += "</ul>";
  return html;
}

function renderMarkdownMathBlock(lines, startIndex) {
  const opening = lines[startIndex].trim();
  const delimiter = opening.startsWith("\\[")
    ? { left: "\\[", right: "\\]" }
    : opening.startsWith("$$")
      ? { left: "$$", right: "$$" }
      : null;
  if (!delimiter) return null;

  const source = [];
  for (let index = startIndex; index < lines.length; index += 1) {
    source.push(lines[index]);
    const current = lines[index].trim();
    const closingOffset = index === startIndex ? delimiter.left.length : 0;
    if (current.indexOf(delimiter.right, closingOffset) >= 0) {
      return {
        html: `<div class="message-math-block">${escapeHTML(source.join("\n"))}</div>`,
        nextIndex: index + 1,
      };
    }
  }
  return null;
}

function renderMarkdownTable(lines, startIndex) {
  if (startIndex + 1 >= lines.length || !lines[startIndex].includes("|")) return null;

  const headers = splitTableRow(lines[startIndex]);
  const delimiters = splitTableRow(lines[startIndex + 1]);
  if (headers.length < 2 || headers.length !== delimiters.length) return null;

  const alignments = delimiters.map(tableAlignment);
  if (alignments.some((alignment) => alignment == null)) return null;

  const rows = [];
  let nextIndex = startIndex + 2;
  while (nextIndex < lines.length) {
    const line = lines[nextIndex];
    if (!line.trim() || !line.includes("|")) break;
    const cells = splitTableRow(line);
    while (cells.length < headers.length) cells.push("");
    rows.push(cells.slice(0, headers.length));
    nextIndex += 1;
  }

  const headerHTML = headers.map((cell, columnIndex) => (
    `<th scope="col" class="align-${alignments[columnIndex]}">${inlineMarkdown(cell)}</th>`
  )).join("");
  const bodyHTML = rows.map((row) => `<tr>${row.map((cell, columnIndex) => (
    `<td class="align-${alignments[columnIndex]}">${inlineMarkdown(cell)}</td>`
  )).join("")}</tr>`).join("");

  return {
    html: `<div class="message-table-scroll" role="region" aria-label="Markdown 表格" tabindex="0"><table><thead><tr>${headerHTML}</tr></thead>${bodyHTML ? `<tbody>${bodyHTML}</tbody>` : ""}</table></div>`,
    nextIndex,
  };
}

function splitTableRow(line) {
  let source = line.trim();
  if (source.startsWith("|")) source = source.slice(1);
  if (source.endsWith("|") && !source.endsWith("\\|")) source = source.slice(0, -1);

  const cells = [];
  let cell = "";
  let inCode = false;
  for (let index = 0; index < source.length; index += 1) {
    const char = source[index];
    if (char === "\\" && source[index + 1] === "|") {
      cell += "|";
      index += 1;
    } else if (char === "`") {
      inCode = !inCode;
      cell += char;
    } else if (char === "|" && !inCode) {
      cells.push(cell.trim());
      cell = "";
    } else {
      cell += char;
    }
  }
  cells.push(cell.trim());
  return cells;
}

function tableAlignment(cell) {
  const delimiter = cell.trim();
  if (!/^:?-{3,}:?$/.test(delimiter)) return null;
  if (delimiter.startsWith(":") && delimiter.endsWith(":")) return "center";
  if (delimiter.endsWith(":")) return "right";
  return "left";
}

function inlineMarkdown(text) {
  let value = escapeHTML(text);
  value = value.replace(/\*\*(.+?)\*\*/g, "<strong>$1</strong>");
  value = value.replace(/`([^`]+)`/g, "<code>$1</code>");
  value = value.replace(/\[([^\]]+)\]\((https?:\/\/[^\s)]+)\)/g, '<a href="$2" target="_blank" rel="noreferrer">$1</a>');
  return value;
}

function scheduleMathRender() {
  window.cancelAnimationFrame(state.mathRenderFrame);
  state.mathRenderFrame = window.requestAnimationFrame(() => {
    state.mathRenderFrame = 0;
    if (typeof window.renderMathInElement !== "function" || !els.messageList.isConnected) return;
    try {
      window.renderMathInElement(els.messageList, {
        delimiters: [
          { left: "$$", right: "$$", display: true },
          { left: "\\[", right: "\\]", display: true },
          { left: "\\(", right: "\\)", display: false },
        ],
        throwOnError: false,
        trust: false,
        ignoredTags: ["script", "noscript", "style", "textarea", "pre", "code", "option"],
      });
    } catch (error) {
      console.warn("Math rendering failed", error);
    }
  });
}

function scheduleCodeHighlight() {
  window.cancelAnimationFrame(state.codeHighlightFrame);
  state.codeHighlightFrame = window.requestAnimationFrame(() => {
    state.codeHighlightFrame = 0;
    if (typeof window.hljs?.highlightElement !== "function" || !els.messageList.isConnected) return;
    if (!state.codeHighlighterConfigured) {
      window.hljs.configure({ throwUnescapedHTML: true });
      state.codeHighlighterConfigured = true;
    }
    for (const code of els.messageList.querySelectorAll(".message-code-block code:not([data-highlighted])")) {
      const language = code.dataset.codeLanguage;
      if (language === "plaintext" || !window.hljs.getLanguage(language)) {
        code.classList.add("hljs");
        code.dataset.highlighted = "yes";
        continue;
      }
      try {
        window.hljs.highlightElement(code);
      } catch (error) {
        code.classList.add("hljs");
        code.dataset.highlighted = "error";
        console.warn("Code highlighting failed", error);
      }
    }
  });
}

function escapeHTML(value) {
  return String(value ?? "").replace(/[&<>"']/g, (char) => ({
    "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#039;",
  }[char]));
}

function escapeAttr(value) { return escapeHTML(value); }
function truncate(value, length) { return value.length > length ? `${value.slice(0, length - 1)}…` : value; }
function stringify(value) {
  if (value == null) return "";
  if (typeof value === "string") return value;
  try { return JSON.stringify(value, null, 2); } catch (_) { return String(value); }
}
function safeJSON(value) { try { return JSON.parse(value); } catch (_) { return null; } }
function formatTime(value) { return new Intl.DateTimeFormat("zh-CN", { hour: "2-digit", minute: "2-digit" }).format(new Date(value)); }
function relativeTime(unixSeconds) {
  const delta = Date.now() - Number(unixSeconds) * 1000;
  if (delta < 60000) return "刚刚";
  if (delta < 3600000) return `${Math.floor(delta / 60000)} 分钟前`;
  if (delta < 86400000) return `${Math.floor(delta / 3600000)} 小时前`;
  return new Intl.DateTimeFormat("zh-CN", { month: "short", day: "numeric" }).format(new Date(Number(unixSeconds) * 1000));
}
function friendlyError(error) {
  const message = error?.message || String(error);
  if (message.includes("Failed to fetch")) return "无法连接到本地服务，请确认 `go run . web` 仍在运行。";
  return message.replace(/^failed to run agent:\s*/i, "");
}

els.form.addEventListener("submit", (event) => {
  event.preventDefault();
  submitText(els.input.value);
});
els.input.addEventListener("input", resizeInput);
els.executionModeControl.addEventListener("click", () => setConfirmationModeMenu(els.confirmationModeMenu.hidden));
els.confirmationModeMenu.addEventListener("click", (event) => {
  const option = event.target.closest("[data-confirmation-mode]");
  if (option) chooseConfirmationMode(option.dataset.confirmationMode);
});
els.confirmationModeMenu.addEventListener("keydown", (event) => {
  if (event.key === "Escape") {
    event.preventDefault();
    setConfirmationModeMenu(false);
    els.executionModeControl.focus();
    return;
  }
  if (!["ArrowDown", "ArrowUp", "Home", "End"].includes(event.key)) return;
  event.preventDefault();
  const current = els.confirmationModeOptions.indexOf(document.activeElement);
  const next = event.key === "Home" ? 0
    : event.key === "End" ? els.confirmationModeOptions.length - 1
      : (current + (event.key === "ArrowDown" ? 1 : -1) + els.confirmationModeOptions.length) % els.confirmationModeOptions.length;
  els.confirmationModeOptions[next].focus();
});
document.addEventListener("click", (event) => {
  if (!event.target.closest(".composer-options")) setConfirmationModeMenu(false);
});
els.input.addEventListener("keydown", (event) => {
  if (event.key === "Enter" && !event.shiftKey && !event.isComposing) {
    event.preventDefault();
    els.form.requestSubmit();
  }
});
els.newSession.addEventListener("click", () => createSession().catch((error) => showToast(friendlyError(error))));
els.stopRun.addEventListener("click", () => state.controller?.abort());
els.toggleInspector.addEventListener("click", () => openInspector(!state.inspectorOpen));
$("#close-inspector").addEventListener("click", () => openInspector(false));
$("#open-sidebar").addEventListener("click", openSidebar);
$("#close-sidebar").addEventListener("click", closeSidebar);
els.scrim.addEventListener("click", () => { closeSidebar(); openInspector(false); });
$("#retry-bootstrap").addEventListener("click", bootstrap);
$("#clear-local-events").addEventListener("click", () => { state.timeline = []; renderInspector(); });

els.sessionList.addEventListener("click", (event) => {
  const select = event.target.closest("[data-session-id]");
  const remove = event.target.closest("[data-delete-session]");
  if (select) openSession(select.dataset.sessionId).catch((error) => showToast(friendlyError(error)));
  if (remove) {
    state.pendingDeleteId = remove.dataset.deleteSession;
    els.deleteDialog.showModal();
  }
});

els.messageList.addEventListener("click", (event) => {
  const executionSummary = event.target.closest(".execution-summary");
  if (executionSummary) {
    const group = executionSummary.parentElement;
    state.executionDisclosurePreferences.set(group.dataset.messageId, group.open ? "closed" : "open");
  }
  const confirmation = event.target.closest("[data-confirm-call]");
  if (confirmation) {
    respondToConfirmation(
      confirmation.dataset.confirmCall,
      confirmation.dataset.approved === "true",
      confirmation.dataset.confirmEvent,
    );
  }
  if (event.target.closest("[data-retry]") && state.lastPrompt) {
    submitText(state.lastPrompt);
  }
});

document.querySelectorAll("[data-prompt]").forEach((button) => {
  button.addEventListener("click", () => {
    els.input.value = button.dataset.prompt;
    resizeInput();
    els.input.focus();
  });
});

els.deleteDialog.addEventListener("close", () => {
  if (els.deleteDialog.returnValue === "confirm" && state.pendingDeleteId) {
    deleteSession(state.pendingDeleteId).catch((error) => showToast(friendlyError(error)));
  }
  state.pendingDeleteId = "";
});

window.addEventListener("keydown", (event) => {
  if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "k") {
    event.preventDefault();
    if (!state.running) createSession().catch((error) => showToast(friendlyError(error)));
  }
  if (event.key === "Escape") { closeSidebar(); openInspector(false); }
});
window.addEventListener("resize", updateScrim);

bootstrap();
