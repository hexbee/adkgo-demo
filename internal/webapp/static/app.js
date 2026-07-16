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
  skillPicker: $("#skill-picker"),
  skillPickerOptions: $("#skill-picker-options"),
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
  mermaidRenderFrame: 0,
  conversationScrollFrame: 0,
  codeHighlighterConfigured: false,
  mermaidInitialized: false,
  mermaidLoadPromise: null,
  mermaidRenderCache: new Map(),
  executionDisclosurePreferences: new Map(),
  titleRequests: new Set(),
  skills: [],
  skillsError: "",
  skillMatches: [],
  skillActiveIndex: 0,
  skillTriggerRange: null,
};
localStorage.setItem("adk-workbench-user", state.userId);

const icons = {
  trash: '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M4 7h16m-10 4v6m4-6v6M9 7l1-2h4l1 2m3 0-1 13H7L6 7"/></svg>',
  tool: '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="m14.5 6.5 3-3 3 3-3 3m-2-1L7 17l-2 2m4-4 2 2"/></svg>',
  check: '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="m5 12 4 4L19 6"/></svg>',
  alert: '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M12 8v5m0 3h.01M4.9 19h14.2L12 5 4.9 19Z"/></svg>',
  diagram: '<svg viewBox="0 0 24 24" aria-hidden="true"><rect x="3.5" y="4" width="6" height="5" rx="1"/><rect x="14.5" y="15" width="6" height="5" rx="1"/><path d="M9.5 6.5h3a4 4 0 0 1 4 4V15m-5 2.5h3"/></svg>',
  skill: '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="m12 3 7 4-7 4-7-4 7-4Z"/><path d="m5 7v9l7 4 7-4V7M12 11v9"/></svg>',
};

const composerMaxLength = Number(els.input.dataset.maxlength) || 12000;

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
  setEditorDisabled(true);
  updateComposer();
  try {
    const apps = await api("/list-apps");
    if (!Array.isArray(apps) || apps.length === 0) throw new Error("服务中没有可用的 Agent");
    state.appName = apps[0];
    els.agentName.textContent = state.appName;
    await loadSkillCatalog();
    await loadSessions();
    if (state.sessions.length) {
      const latest = [...state.sessions].sort((a, b) => b.lastUpdateTime - a.lastUpdateTime)[0];
      await openSession(latest.id);
    } else {
      await createSession();
    }
    setConnection("connected");
    setEditorDisabled(false);
    updateComposer();
  } catch (error) {
    showConnectionError(error);
  }
}

async function loadSkillCatalog() {
  try {
    const skills = await api("/project-skills");
    state.skills = Array.isArray(skills)
      ? skills.filter((skill) => typeof skill?.name === "string" && skill.name)
      : [];
    state.skillsError = "";
  } catch (error) {
    state.skills = [];
    state.skillsError = friendlyError(error);
  }
}

async function loadSessions() {
  const path = `/apps/${encode(state.appName)}/users/${encode(state.userId)}/sessions`;
  const localTitles = new Map(state.sessions
    .filter((session) => session.localTitle)
    .map((session) => [session.id, session.localTitle]));
  if (state.currentSession?.localTitle) localTitles.set(state.currentSession.id, state.currentSession.localTitle);
  state.sessions = (await api(path)) || [];
  for (const session of state.sessions) {
    if (!persistentSessionTitle(session) && localTitles.has(session.id)) {
      session.localTitle = localTitles.get(session.id);
    }
  }
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
  const summary = state.sessions.find((session) => session.id === sessionId);
  state.currentSession = await api(path);
  if (summary?.localTitle && !persistentSessionTitle(state.currentSession)) {
    state.currentSession.localTitle = summary.localTitle;
  }
  const index = state.sessions.findIndex((session) => session.id === sessionId);
  if (index >= 0) state.sessions[index] = state.currentSession;
  state.messages = messagesFromEvents(state.currentSession.events || []);
  state.timeline = [];
  state.executionDisclosurePreferences.clear();
  renderAll({ forceFollow: true });
  closeSidebar();
  ensureSessionTitle(sessionId).catch(() => {});
}

function messagesFromEvents(events) {
  const messages = [];
  let assistant = null;
  for (const event of events) {
    const parts = event.content?.parts || [];
    const userText = event.author === "user"
      ? parts.filter((part) => part.text && !part.thought).map((part) => part.text).join("")
      : "";
    if (event.author === "user") {
      if (userText) {
        messages.push(newMessage("user", userText, event.id));
        assistant = null;
      }
      restorePersistedFunctionResponses(parts, messages);
      continue;
    }
    const meaningful = parts.some((part) => part.text || part.functionCall || part.functionResponse) || event.errorMessage;
    if (!meaningful) continue;
    if (!assistant) {
      assistant = newMessage("assistant", "", event.invocationId || event.id);
      messages.push(assistant);
    }
    processEvent(event, assistant, { render: false, record: false });
  }
  return messages;
}

function restorePersistedFunctionResponses(parts, messages) {
  for (const part of parts) {
    const response = part.functionResponse;
    if (!response?.id) continue;
    const target = findMessageWithExecutionItem(messages, response.id);
    if (target) handleFunctionResponse(response, target, false);
  }
}

function findMessageWithExecutionItem(messages, itemId) {
  for (let index = messages.length - 1; index >= 0; index -= 1) {
    const message = messages[index];
    if (message.role === "assistant" && message.executionItems.some((item) => item.id === itemId)) {
      return message;
    }
  }
  return null;
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
  const persisted = persistentSessionTitle(session);
  if (persisted) return truncate(persisted, 34);
  if (session?.localTitle) return truncate(session.localTitle.replace(/\s+/g, " "), 34);
  const userEvent = (session.events || []).find((event) => event.author === "user" && event.content?.parts?.some((part) => part.text));
  const title = userEvent?.content?.parts?.filter((part) => part.text && !part.thought).map((part) => part.text).join("").trim();
  return title ? truncate(title.replace(/\s+/g, " "), 34) : "新任务";
}

function persistentSessionTitle(session) {
  const title = session?.state?.title;
  return typeof title === "string" ? title.trim().replace(/\s+/g, " ") : "";
}

function sessionTitleSource(session) {
  const source = session?.state?.title_source;
  return typeof source === "string" ? source.trim() : "";
}

function hasFinalSessionTitle(session) {
  return Boolean(persistentSessionTitle(session) && sessionTitleSource(session) === "model");
}

async function ensureSessionTitle(sessionId = state.currentSession?.id) {
  if (!sessionId || state.titleRequests.has(sessionId)) return;
  const target = state.sessions.find((session) => session.id === sessionId);
  if (hasFinalSessionTitle(target) || (state.currentSession?.id === sessionId && hasFinalSessionTitle(state.currentSession))) return;
  state.titleRequests.add(sessionId);
  try {
    const path = `/apps/${encode(state.appName)}/users/${encode(state.userId)}/sessions/${encode(sessionId)}/title`;
    const result = await api(path, { method: "POST", body: "{}" });
    const title = typeof result?.title === "string" ? result.title.trim() : "";
    const source = typeof result?.source === "string" ? result.source.trim() : "";
    if (!title) return;
    for (const session of state.sessions) {
      if (session.id !== sessionId) continue;
      session.state = { ...(session.state || {}), title, title_source: source };
      delete session.localTitle;
    }
    if (state.currentSession?.id === sessionId) {
      state.currentSession.state = { ...(state.currentSession.state || {}), title, title_source: source };
      delete state.currentSession.localTitle;
    }
    renderSessions();
    updateTaskTitle();
  } finally {
    state.titleRequests.delete(sessionId);
  }
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
  reconcileMessageElements();
  for (const detail of els.messageList.querySelectorAll("details:not(.execution-group)[data-disclosure-id]")) {
    if (openDisclosures.has(detail.dataset.disclosureId)) detail.open = true;
  }
  scheduleMathRender();
  scheduleCodeHighlight();
  scheduleMermaidRender();
  scheduleConversationFollow(followOutput);
}

function reconcileMessageElements() {
  const existingById = new Map([...els.messageList.children]
    .map((element) => [element.dataset.messageId, element]));

  for (let index = 0; index < state.messages.length; index += 1) {
    const message = state.messages[index];
    const signature = messageRenderSignature(message);
    let element = existingById.get(String(message.id));
    if (!element || element.dataset.renderSignature !== signature) {
      const replacement = createMessageElement(message, signature);
      if (element) element.replaceWith(replacement);
      element = replacement;
    }
    const elementAtIndex = els.messageList.children[index];
    if (elementAtIndex !== element) els.messageList.insertBefore(element, elementAtIndex || null);
  }

  while (els.messageList.children.length > state.messages.length) {
    els.messageList.lastElementChild.remove();
  }
}

function messageRenderSignature(message) {
  return stableTextHash(JSON.stringify({
    role: message.role,
    text: message.text,
    executionItems: message.executionItems,
    confirmationMode: message.confirmationMode,
    error: message.error,
    streaming: message.streaming,
    timestamp: message.timestamp,
  }));
}

function createMessageElement(message, signature) {
  const template = document.createElement("template");
  template.innerHTML = renderMessage(message).trim();
  const element = template.content.firstElementChild;
  element.dataset.renderSignature = signature;
  return element;
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
    ? renderMarkdown(message.text, message.id, { streaming: message.streaming })
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
  if (prompt.length > composerMaxLength) {
    showToast(`输入内容不能超过 ${composerMaxLength} 个字符`);
    return;
  }
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
  if (!persistentSessionTitle(state.currentSession)) state.currentSession.localTitle = prompt;
  clearEditor();
  resizeInput();
  updateTaskTitle();
  renderSessions();
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
      await ensureSessionTitle();
    } catch (_) {
      // The first-question fallback remains visible; title generation must not affect the answer.
    }
    try {
      await loadSessions();
      const refreshed = state.sessions.find((session) => session.id === state.currentSession?.id);
      if (refreshed) {
        state.currentSession = {
          ...state.currentSession,
          ...refreshed,
          events: state.currentSession.events || [],
          localTitle: refreshed.localTitle || state.currentSession.localTitle,
        };
      }
      renderSessions();
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
  if (record) addTimeline("tool", isApproval ? "等待工具确认" : activity.title, activity.requestDetail);
}

function handleFunctionResponse(response, assistant, record) {
  const activity = assistant.executionItems.find((item) => item.id === response.id);
  if (activity) {
    const isApproval = activity.type === "approval" || response.name === "adk_request_confirmation";
    const confirmed = response.response?.confirmed;
    activity.status = isApproval
      ? confirmed === true ? "approved" : confirmed === false ? "rejected" : "completed"
      : "completed";
    if (isApproval) activity.autoApproved = false;
    activity.responseDetail = stringify(response.response);
  } else {
    if (response.name === "adk_request_confirmation") return;
    assistant.executionItems.push({
      id: response.id || crypto.randomUUID(),
      type: "tool",
      title: `工具 ${response.name || "调用"} 已返回`,
      requestDetail: "",
      responseDetail: stringify(response.response),
      status: "completed",
    });
  }
  if (record) {
    const confirmed = response.response?.confirmed;
    const title = response.name === "adk_request_confirmation"
      ? confirmed === true ? "已允许工具调用" : confirmed === false ? "已拒绝工具调用" : "工具确认已返回"
      : `工具 ${response.name || "调用"} 已返回`;
    addTimeline("tool", title, stringify(response.response));
  }
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
  setEditorDisabled(running);
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
  setEditorDisabled(true);
  updateComposer();
}

function updateComposer() {
  els.send.disabled = state.running || isEditorDisabled() || !editorText().trim() || !state.currentSession;
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
  updateComposer();
}

function isEditorDisabled() {
  return els.input.getAttribute("aria-disabled") === "true";
}

function setEditorDisabled(disabled) {
  els.input.contentEditable = String(!disabled);
  els.input.setAttribute("aria-disabled", String(Boolean(disabled)));
  if (disabled) closeSkillPicker();
}

function editorText() {
  const chunks = [];
  const visit = (node) => {
    if (node.nodeType === Node.TEXT_NODE) {
      chunks.push(node.nodeValue || "");
      return;
    }
    if (node.nodeType !== Node.ELEMENT_NODE) return;
    if (node.classList.contains("skill-mention")) {
      chunks.push(`$${node.dataset.skill || ""}`);
      return;
    }
    if (node.tagName === "BR") {
      chunks.push("\n");
      return;
    }
    const block = node !== els.input && (node.tagName === "DIV" || node.tagName === "P");
    if (block && chunks.length && !chunks[chunks.length - 1].endsWith("\n")) chunks.push("\n");
    for (const child of node.childNodes) visit(child);
    if (block && chunks.length && !chunks[chunks.length - 1].endsWith("\n")) chunks.push("\n");
  };
  for (const child of els.input.childNodes) visit(child);
  return chunks.join("").replaceAll("\u00a0", " ").replace(/\n+$/, "");
}

function setEditorText(text) {
  const fragment = document.createDocumentFragment();
  String(text || "").split("\n").forEach((line, index) => {
    if (index) fragment.append(document.createElement("br"));
    if (line) fragment.append(document.createTextNode(line));
  });
  els.input.replaceChildren(fragment);
  closeSkillPicker();
  updateComposer();
}

function clearEditor() {
  els.input.replaceChildren();
  closeSkillPicker();
  updateComposer();
}

function skillDisplayName(name) {
  return String(name || "").split("-").filter(Boolean)
    .map((part) => `${part.charAt(0).toUpperCase()}${part.slice(1)}`).join(" ");
}

function findSkillTrigger() {
  const selection = window.getSelection();
  if (!selection || !selection.isCollapsed || !selection.rangeCount) return null;
  const range = selection.getRangeAt(0);
  if (!els.input.contains(range.startContainer)) return null;
  let textNode = range.startContainer;
  let textOffset = range.startOffset;
  if (textNode.nodeType === Node.ELEMENT_NODE) {
    const candidate = textNode.childNodes[textOffset - 1];
    if (candidate?.nodeType !== Node.TEXT_NODE) return null;
    textNode = candidate;
    textOffset = candidate.nodeValue?.length || 0;
  }
  if (textNode.nodeType !== Node.TEXT_NODE || textNode.parentElement?.closest(".skill-mention")) return null;
  const prefix = (textNode.nodeValue || "").slice(0, textOffset);
  const match = prefix.match(/(?:^|[\s\u00a0])\$([a-zA-Z0-9-]*)$/);
  if (!match) return null;
  const start = prefix.lastIndexOf("$");
  const triggerRange = document.createRange();
  triggerRange.setStart(textNode, start);
  triggerRange.setEnd(textNode, textOffset);
  return { query: match[1].toLowerCase(), range: triggerRange };
}

function updateSkillPicker() {
  const trigger = findSkillTrigger();
  if (!trigger || state.running) {
    closeSkillPicker();
    return;
  }
  state.skillTriggerRange = trigger.range.cloneRange();
  state.skillMatches = state.skills.filter((skill) => {
    const haystack = `${skill.name} ${skill.description || ""}`.toLowerCase();
    return haystack.includes(trigger.query);
  });
  state.skillActiveIndex = Math.min(state.skillActiveIndex, Math.max(0, state.skillMatches.length - 1));
  renderSkillPicker(trigger.query);
}

function renderSkillPicker(query) {
  els.skillPicker.hidden = false;
  els.input.setAttribute("aria-expanded", "true");
  if (!state.skillMatches.length) {
    const message = state.skillsError
      ? "无法读取项目 Skills"
      : state.skills.length
        ? `没有匹配 “${query}” 的 Skill`
        : "当前项目没有加载 Skills";
    els.skillPickerOptions.innerHTML = `<div class="skill-picker-empty">${escapeHTML(message)}</div>`;
    els.input.removeAttribute("aria-activedescendant");
    return;
  }
  els.skillPickerOptions.innerHTML = state.skillMatches.map((skill, index) => `
    <div class="skill-option" id="skill-option-${index}" role="option" aria-selected="${index === state.skillActiveIndex}" data-skill-index="${index}">
      <span class="skill-option-icon">${icons.skill}</span>
      <span class="skill-option-copy"><strong>${escapeHTML(skillDisplayName(skill.name))}</strong><small>${escapeHTML(skill.description || `$${skill.name}`)}</small></span>
    </div>`).join("");
  els.input.setAttribute("aria-activedescendant", `skill-option-${state.skillActiveIndex}`);
  requestAnimationFrame(() => document.getElementById(`skill-option-${state.skillActiveIndex}`)?.scrollIntoView({ block: "nearest" }));
}

function closeSkillPicker() {
  els.skillPicker.hidden = true;
  els.input.setAttribute("aria-expanded", "false");
  els.input.removeAttribute("aria-activedescendant");
  state.skillMatches = [];
  state.skillActiveIndex = 0;
  state.skillTriggerRange = null;
}

function moveSkillSelection(delta) {
  if (!state.skillMatches.length) return;
  state.skillActiveIndex = (state.skillActiveIndex + delta + state.skillMatches.length) % state.skillMatches.length;
  renderSkillPicker("");
}

function createSkillMention(skill) {
  const mention = document.createElement("span");
  mention.className = "skill-mention";
  mention.contentEditable = "false";
  mention.dataset.skill = skill.name;
  mention.setAttribute("aria-label", `Skill：${skillDisplayName(skill.name)}`);
  mention.innerHTML = `${icons.skill}<span>${escapeHTML(skillDisplayName(skill.name))}</span>`;
  return mention;
}

function chooseSkill(index = state.skillActiveIndex) {
  const skill = state.skillMatches[index];
  const triggerRange = state.skillTriggerRange;
  if (!skill || !triggerRange) return;
  const insertedLength = skill.name.length + 1;
  const replacedLength = triggerRange.toString().length;
  if (editorText().length - replacedLength + insertedLength > composerMaxLength) {
    showToast(`输入内容不能超过 ${composerMaxLength} 个字符`);
    return;
  }
  try {
    triggerRange.deleteContents();
    const mention = createSkillMention(skill);
    const spacer = document.createTextNode(" ");
    triggerRange.insertNode(spacer);
    triggerRange.insertNode(mention);
    const selection = window.getSelection();
    const caret = document.createRange();
    caret.setStart(spacer, 1);
    caret.collapse(true);
    selection.removeAllRanges();
    selection.addRange(caret);
  } catch (_) {
    closeSkillPicker();
    return;
  }
  closeSkillPicker();
  updateComposer();
}

function insertPlainText(text) {
  const selection = window.getSelection();
  if (!selection || !selection.rangeCount) return;
  const range = selection.getRangeAt(0);
  if (!els.input.contains(range.commonAncestorContainer)) return;
  range.deleteContents();
  const fragment = document.createDocumentFragment();
  let lastNode = null;
  String(text).split("\n").forEach((line, index) => {
    if (index) {
      lastNode = document.createElement("br");
      fragment.append(lastNode);
    }
    if (line) {
      lastNode = document.createTextNode(line);
      fragment.append(lastNode);
    }
  });
  if (!lastNode) lastNode = fragment.appendChild(document.createTextNode(""));
  range.insertNode(fragment);
  const caret = document.createRange();
  caret.setStartAfter(lastNode);
  caret.collapse(true);
  selection.removeAllRanges();
  selection.addRange(caret);
}

function deleteAdjacentSkill(event) {
  if (!event.key || !["Backspace", "Delete"].includes(event.key)) return false;
  const selection = window.getSelection();
  if (!selection || !selection.isCollapsed || !selection.rangeCount) return false;
  const range = selection.getRangeAt(0);
  let mention = null;
  let cleanupWhitespace = false;
  const nearestSibling = (node, direction) => {
    let candidate = direction === "previous" ? node.previousSibling : node.nextSibling;
    while (candidate?.nodeType === Node.TEXT_NODE && /^\s*$/.test(candidate.nodeValue || "")) {
      candidate = direction === "previous" ? candidate.previousSibling : candidate.nextSibling;
    }
    return candidate;
  };
  if (range.startContainer.nodeType === Node.TEXT_NODE) {
    const textNode = range.startContainer;
    if (event.key === "Backspace" && /^\s*$/.test(textNode.nodeValue.slice(0, range.startOffset))) {
      const candidate = nearestSibling(textNode, "previous");
      mention = candidate?.classList?.contains("skill-mention") ? candidate : null;
      cleanupWhitespace = Boolean(mention);
    } else if (event.key === "Delete" && /^\s*$/.test(textNode.nodeValue.slice(range.startOffset))) {
      const candidate = nearestSibling(textNode, "next");
      mention = candidate?.classList?.contains("skill-mention") ? candidate : null;
      cleanupWhitespace = Boolean(mention);
    }
  } else if (range.startContainer === els.input) {
    let offset = event.key === "Backspace" ? range.startOffset - 1 : range.startOffset;
    let candidate = els.input.childNodes[offset];
    const step = event.key === "Backspace" ? -1 : 1;
    while (candidate?.nodeType === Node.TEXT_NODE && /^\s*$/.test(candidate.nodeValue || "")) {
      offset += step;
      candidate = els.input.childNodes[offset];
    }
    mention = candidate?.classList?.contains("skill-mention") ? candidate : null;
  }
  if (!mention) return false;
  event.preventDefault();
  if (cleanupWhitespace && range.startContainer.nodeType === Node.TEXT_NODE) {
    const textNode = range.startContainer;
    if (event.key === "Backspace") textNode.deleteData(0, range.startOffset);
    else textNode.deleteData(range.startOffset, textNode.length - range.startOffset);
  }
  const parent = mention.parentNode;
  const offset = [...parent.childNodes].indexOf(mention);
  mention.remove();
  const caret = document.createRange();
  if (!editorText()) {
    els.input.replaceChildren();
    caret.setStart(els.input, 0);
  } else {
    caret.setStart(parent, Math.min(Math.max(0, offset), parent.childNodes.length));
  }
  caret.collapse(true);
  selection.removeAllRanges();
  selection.addRange(caret);
  updateComposer();
  return true;
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

function renderMarkdown(text, messageId = "message", { streaming = false } = {}) {
  let codeIndex = 0;
  return compactRepeatedMermaidSegments(parseMarkdownSegments(text)).map((segment) => {
    if (segment.type !== "code") return renderMarkdownText(segment.text);
    const blockKey = `${safeDOMId(messageId)}-${codeIndex}`;
    codeIndex += 1;
    return renderCodeBlock(segment, blockKey, { deferMermaid: streaming });
  }).join("");
}

function compactRepeatedMermaidSegments(segments) {
  const compacted = [];
  for (let index = 0; index < segments.length; index += 1) {
    const current = segments[index];
    const separator = segments[index + 1];
    const repeated = segments[index + 2];
    if (current.type === "code"
      && String(current.language).toLowerCase() === "mermaid"
      && separator?.type === "markdown"
      && isRepeatedCodeSeparator(separator.text)
      && repeated?.type === "code"
      && ["", "plaintext", "text", "markdown", "md", "mermaid"].includes(String(repeated.language).toLowerCase())
      && repeatedMermaidCodeMatches(current, repeated)) {
      compacted.push(current);
      index += 2;
      continue;
    }
    compacted.push(current);
  }
  return compacted;
}

function isRepeatedCodeSeparator(text) {
  return /^\s*(?:(?:\*\*|__)?(?:代码|源码|源代码)\s*[：:]?(?:\*\*|__)?)?\s*$/u.test(String(text));
}

function repeatedMermaidCodeMatches(original, repeated) {
  const source = normalizedCode(original.text);
  const candidate = normalizedCode(repeated.text);
  return source === candidate || (repeated.unclosed && source.startsWith(candidate));
}

function normalizedCode(text) {
  return String(text).replace(/\r\n?/g, "\n").trim();
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

function renderCodeBlock(segment, blockKey = "code", { deferMermaid = false } = {}) {
  const sourceLanguage = String(segment.language || "").trim().toLowerCase().replace(/^language-/, "");
  if (sourceLanguage === "mermaid" && deferMermaid) {
    return renderMermaidBlock(segment, blockKey, { deferred: true, receiving: segment.unclosed });
  }
  if (sourceLanguage === "mermaid" && !segment.unclosed) return renderMermaidBlock(segment, blockKey, { deferred: deferMermaid });
  const language = codeLanguageInfo(segment.language);
  return `<div class="message-code-block${segment.unclosed ? " streaming" : ""}">
    <div class="message-code-heading"><span>${escapeHTML(language.label)}</span></div>
    <pre><code class="language-${escapeAttr(language.id)}" data-code-language="${escapeAttr(language.id)}">${escapeHTML(segment.text)}</code></pre>
  </div>`;
}

function renderMermaidBlock(segment, blockKey, { deferred = false, receiving = false } = {}) {
  const source = normalizedCode(segment.text);
  const lineCount = source ? source.split("\n").length : 0;
  const disclosureId = `mermaid-source-${blockKey}`;
  return `<figure class="message-mermaid" data-mermaid-key="${escapeAttr(blockKey)}" data-render-state="${deferred ? "waiting" : "pending"}">
    <figcaption class="message-mermaid-heading">
      <span class="message-mermaid-title">${icons.diagram}<strong>图示</strong></span>
      <span class="message-mermaid-status" data-mermaid-status>${receiving ? "正在生成图示" : deferred ? "准备图示" : "正在绘制"}</span>
    </figcaption>
    <div class="message-mermaid-canvas" data-mermaid-canvas aria-label="Mermaid 图示">
      <div class="message-mermaid-loading" aria-hidden="true"><span></span><span></span><span></span></div>
    </div>
    <details class="message-mermaid-source" data-disclosure-id="${escapeAttr(disclosureId)}">
      <summary><span>查看 Mermaid 源码</span><small>${lineCount} 行</small><span class="mermaid-source-chevron" aria-hidden="true"></span></summary>
      <div class="message-code-block mermaid-source-code">
        <div class="message-code-heading"><span>mermaid</span></div>
        <pre><code class="language-plaintext" data-code-language="plaintext" data-mermaid-source>${escapeHTML(source)}</code></pre>
      </div>
    </details>
  </figure>`;
}

function safeDOMId(value) {
  return String(value || "message").replace(/[^a-zA-Z0-9_-]+/g, "-").slice(0, 80) || "message";
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

function scheduleMermaidRender() {
  window.cancelAnimationFrame(state.mermaidRenderFrame);
  state.mermaidRenderFrame = window.requestAnimationFrame(() => {
    state.mermaidRenderFrame = 0;
    void renderPendingMermaidDiagrams();
  });
}

async function renderPendingMermaidDiagrams() {
  const figures = [...els.messageList.querySelectorAll('.message-mermaid[data-render-state="pending"]')];
  if (!figures.length || !els.messageList.isConnected) return;
  let mermaid;
  try {
    mermaid = await ensureMermaid();
  } catch (error) {
    applyMermaidBatch(figures.map((figure) => ({ figure, error })));
    return;
  }
  const outcomes = await Promise.all(figures.map((figure) => prepareMermaidFigure(figure, mermaid)));
  applyMermaidBatch(outcomes);
}

function ensureMermaid() {
  if (typeof window.mermaid?.render === "function") {
    initializeMermaid();
    return Promise.resolve(window.mermaid);
  }
  if (state.mermaidLoadPromise) return state.mermaidLoadPromise;
  state.mermaidLoadPromise = new Promise((resolve, reject) => {
    const script = document.createElement("script");
    script.src = "/assets/vendor/mermaid/mermaid.min.js";
    script.async = true;
    script.dataset.mermaidLoader = "true";
    script.addEventListener("load", () => {
      if (typeof window.mermaid?.render !== "function") {
        reject(new Error("Mermaid 加载完成，但渲染 API 不可用"));
        return;
      }
      initializeMermaid();
      resolve(window.mermaid);
    }, { once: true });
    script.addEventListener("error", () => reject(new Error("Mermaid 资源加载失败")), { once: true });
    document.head.append(script);
  }).catch((error) => {
    state.mermaidLoadPromise = null;
    throw error;
  });
  return state.mermaidLoadPromise;
}

function initializeMermaid() {
  if (state.mermaidInitialized || typeof window.mermaid?.initialize !== "function") return;
  window.mermaid.initialize({
    startOnLoad: false,
    suppressErrorRendering: true,
    securityLevel: "strict",
    theme: "base",
    fontFamily: 'Inter, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif',
    themeVariables: {
      background: "#fbfaf6",
      primaryColor: "#e5efeb",
      primaryTextColor: "#18201d",
      primaryBorderColor: "#5d8f7d",
      lineColor: "#50635c",
      secondaryColor: "#f3f0e8",
      tertiaryColor: "#f8f6f0",
    },
    flowchart: { htmlLabels: false, useMaxWidth: true },
  });
  window.mermaid.parseError = () => {};
  state.mermaidInitialized = true;
}

async function prepareMermaidFigure(figure, mermaid) {
  const canvas = figure.querySelector("[data-mermaid-canvas]");
  const sourceElement = figure.querySelector("[data-mermaid-source]");
  const source = normalizedCode(sourceElement?.textContent || "");
  const blockKey = figure.dataset.mermaidKey || "diagram";
  if (!canvas || !source) {
    return { figure, error: new Error("Mermaid 源码为空") };
  }
  try {
    const result = await cachedMermaidRender(mermaid, blockKey, source);
    return { figure, canvas, sourceElement, source, result };
  } catch (error) {
    return { figure, error };
  }
}

function applyMermaidBatch(outcomes) {
  const current = outcomes.filter(({ figure, sourceElement, source }) => (
    figure?.isConnected && (!sourceElement || normalizedCode(sourceElement.textContent) === source)
  ));
  if (!current.length) return;
  const followOutput = state.conversationScrollFrame !== 0 || isConversationNearBottom();
  for (const outcome of current) {
    if (outcome.error) showMermaidError(outcome.figure, outcome.error, { followOutput: false });
    else applyMermaidResult(outcome);
  }
  scheduleConversationFollow(followOutput);
}

function applyMermaidResult({ figure, canvas, result }) {
  canvas.innerHTML = result.svg;
  result.bindFunctions?.(canvas);
  const svg = canvas.querySelector("svg");
  if (svg) {
    svg.setAttribute("role", "img");
    if (!svg.getAttribute("aria-label")) svg.setAttribute("aria-label", "Mermaid 图示");
  }
  figure.dataset.renderState = "ready";
  figure.querySelector("[data-mermaid-status]").textContent = "已绘制";
}

function cachedMermaidRender(mermaid, blockKey, source) {
  const sourceHash = stableTextHash(source);
  const cacheKey = `${blockKey}:${sourceHash}`;
  if (state.mermaidRenderCache.has(cacheKey)) return state.mermaidRenderCache.get(cacheKey);
  const renderId = `mermaid-${blockKey}-${sourceHash}`;
  const renderPromise = mermaid.render(renderId, source).catch((error) => {
    state.mermaidRenderCache.delete(cacheKey);
    cleanupMermaidArtifacts(renderId);
    throw error;
  });
  state.mermaidRenderCache.set(cacheKey, renderPromise);
  while (state.mermaidRenderCache.size > 64) {
    state.mermaidRenderCache.delete(state.mermaidRenderCache.keys().next().value);
  }
  return renderPromise;
}

function cleanupMermaidArtifacts(renderId) {
  document.getElementById(`d${renderId}`)?.remove();
  document.getElementById(`i${renderId}`)?.remove();
}

function stableTextHash(text) {
  let hash = 2166136261;
  for (let index = 0; index < text.length; index += 1) {
    hash ^= text.charCodeAt(index);
    hash = Math.imul(hash, 16777619);
  }
  return (hash >>> 0).toString(36);
}

function showMermaidError(figure, error, { followOutput = true } = {}) {
  if (!figure?.isConnected) return;
  const canvas = figure.querySelector("[data-mermaid-canvas]");
  const shouldFollow = followOutput && (state.conversationScrollFrame !== 0 || isConversationNearBottom());
  figure.dataset.renderState = "error";
  figure.querySelector("[data-mermaid-status]").textContent = "无法绘制";
  if (canvas) {
    canvas.innerHTML = '<div class="message-mermaid-error"><strong>图示没有成功绘制</strong><span>请展开源码检查 Mermaid 语法。</span></div>';
  }
  const source = figure.querySelector(".message-mermaid-source");
  if (source) source.open = true;
  if (followOutput) scheduleConversationFollow(shouldFollow);
  console.warn("Mermaid rendering failed", error);
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
  submitText(editorText());
});
els.input.addEventListener("input", (event) => {
  if (!event.isComposing && !editorText()) els.input.replaceChildren();
  resizeInput();
  updateSkillPicker();
});
els.input.addEventListener("click", updateSkillPicker);
els.input.addEventListener("beforeinput", (event) => {
  if (event.isComposing || !event.inputType.startsWith("insert") || !event.data) return;
  const selectedLength = window.getSelection()?.toString().length || 0;
  if (editorText().length - selectedLength + event.data.length <= composerMaxLength) return;
  event.preventDefault();
  showToast(`输入内容不能超过 ${composerMaxLength} 个字符`);
});
els.input.addEventListener("paste", (event) => {
  event.preventDefault();
  const text = event.clipboardData?.getData("text/plain") || "";
  const selectedLength = window.getSelection()?.toString().length || 0;
  const available = Math.max(0, composerMaxLength - editorText().length + selectedLength);
  insertPlainText(text.slice(0, available));
  if (text.length > available) showToast(`已截断到 ${composerMaxLength} 个字符`);
  resizeInput();
  updateSkillPicker();
});
els.input.addEventListener("drop", (event) => {
  event.preventDefault();
  els.input.focus();
  const text = event.dataTransfer?.getData("text/plain") || "";
  const available = Math.max(0, composerMaxLength - editorText().length);
  insertPlainText(text.slice(0, available));
  if (text.length > available) showToast(`已截断到 ${composerMaxLength} 个字符`);
  resizeInput();
  updateSkillPicker();
});
els.skillPickerOptions.addEventListener("pointerdown", (event) => {
  const option = event.target.closest("[data-skill-index]");
  if (!option) return;
  event.preventDefault();
  chooseSkill(Number(option.dataset.skillIndex));
});
els.skillPickerOptions.addEventListener("pointermove", (event) => {
  const option = event.target.closest("[data-skill-index]");
  if (!option) return;
  const index = Number(option.dataset.skillIndex);
  if (index === state.skillActiveIndex) return;
  state.skillActiveIndex = index;
  for (const candidate of els.skillPickerOptions.querySelectorAll("[data-skill-index]")) {
    candidate.setAttribute("aria-selected", String(Number(candidate.dataset.skillIndex) === index));
  }
  els.input.setAttribute("aria-activedescendant", `skill-option-${index}`);
});
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
  if (!event.target.closest(".composer")) closeSkillPicker();
});
els.input.addEventListener("keydown", (event) => {
  if (deleteAdjacentSkill(event)) return;
  if (!els.skillPicker.hidden && !event.isComposing) {
    if (event.key === "ArrowDown" || event.key === "ArrowUp") {
      event.preventDefault();
      moveSkillSelection(event.key === "ArrowDown" ? 1 : -1);
      return;
    }
    if ((event.key === "Enter" || event.key === "Tab") && state.skillMatches.length) {
      event.preventDefault();
      chooseSkill();
      return;
    }
    if (event.key === "Escape") {
      event.preventDefault();
      closeSkillPicker();
      return;
    }
  }
  if (event.key === "Enter" && event.shiftKey && !event.isComposing) {
    event.preventDefault();
    insertPlainText("\n");
    resizeInput();
    closeSkillPicker();
    return;
  }
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
    setEditorText(button.dataset.prompt);
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
