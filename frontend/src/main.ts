import "./styles.css";

import {
  AuthStatus,
  CancelUpload,
  GetSettings,
  Login,
  Logout,
  PickFiles,
  SaveSettings,
  StartUpload,
  UploadState,
} from "./lib/backend";
import { EventsOn } from "./lib/runtime";

type Settings = {
  endpoint: string;
  uploadThreads: number;
};

type FileProgressState = {
  filePath: string;
  remotePath: string;
  fileSize: number;
  threadCount: number;
  uploadedBytes: number;
  progress: number;
  completedChunks: number;
  totalChunks: number;
  done: boolean;
  failed: boolean;
  error: string;
};

const appElement = document.querySelector<HTMLDivElement>("#app");
if (!appElement) {
  throw new Error("Missing app root");
}

const state = {
  settings: {
    endpoint: "",
    uploadThreads: 6,
  } as Settings,
  auth: {
    loggedIn: false,
    expiresAt: "",
  },
  isBusy: false,
  uploadState: {
    running: false,
    jobId: "",
  },
  selectedFiles: [] as string[],
  remoteDirectory: "",
  fileProgress: new Map<string, FileProgressState>(),
  statusMessage: "",
  statusKind: "info" as "info" | "success" | "error",
};

void bootstrap();

async function bootstrap(): Promise<void> {
  await refreshSettings();
  await refreshAuth();
  await refreshUploadState();
  registerUploadEvents();
  render();
}

function registerUploadEvents(): void {
  EventsOn("upload:job-started", (payload: { jobId: string; totalFiles: number }) => {
    state.uploadState.running = true;
    state.uploadState.jobId = payload.jobId;
    state.fileProgress.clear();
    setStatus(`Upload job started (${payload.totalFiles} files).`, "info");
    render();
  });

  EventsOn("upload:file-started", (payload: {
    filePath: string;
    remotePath: string;
    fileSize: number;
    threadCount: number;
    uploadedBytes: number;
    completedChunks: number;
    totalChunks: number;
  }) => {
    state.fileProgress.set(payload.filePath, {
      filePath: payload.filePath,
      remotePath: payload.remotePath,
      fileSize: payload.fileSize,
      threadCount: payload.threadCount,
      uploadedBytes: payload.uploadedBytes,
      progress: payload.fileSize > 0 ? payload.uploadedBytes / payload.fileSize : 0,
      completedChunks: payload.completedChunks,
      totalChunks: payload.totalChunks,
      done: false,
      failed: false,
      error: "",
    });
    render();
  });

  EventsOn("upload:file-progress", (payload: {
    filePath: string;
    remotePath: string;
    fileSize: number;
    threadCount: number;
    uploadedBytes: number;
    completedChunks: number;
    totalChunks: number;
    progress: number;
  }) => {
    const existing = state.fileProgress.get(payload.filePath);
    const next: FileProgressState = {
      filePath: payload.filePath,
      remotePath: payload.remotePath,
      fileSize: payload.fileSize,
      threadCount: payload.threadCount,
      uploadedBytes: payload.uploadedBytes,
      progress: payload.progress,
      completedChunks: payload.completedChunks,
      totalChunks: payload.totalChunks,
      done: existing?.done ?? false,
      failed: existing?.failed ?? false,
      error: existing?.error ?? "",
    };
    state.fileProgress.set(payload.filePath, next);
    render();
  });

  EventsOn("upload:file-complete", (payload: { filePath: string }) => {
    const item = state.fileProgress.get(payload.filePath);
    if (item) {
      item.done = true;
      item.failed = false;
      item.error = "";
      item.progress = 1;
      item.uploadedBytes = item.fileSize;
      state.fileProgress.set(payload.filePath, item);
    }
    render();
  });

  EventsOn("upload:file-failed", (payload: { filePath: string; error: string; remotePath: string }) => {
    const existing = state.fileProgress.get(payload.filePath);
    state.fileProgress.set(payload.filePath, {
      filePath: payload.filePath,
      remotePath: payload.remotePath,
      fileSize: existing?.fileSize ?? 0,
      threadCount: existing?.threadCount ?? 0,
      uploadedBytes: existing?.uploadedBytes ?? 0,
      progress: existing?.progress ?? 0,
      completedChunks: existing?.completedChunks ?? 0,
      totalChunks: existing?.totalChunks ?? 0,
      done: false,
      failed: true,
      error: payload.error,
    });
    render();
  });

  EventsOn("upload:job-finished", (payload: {
    completedFiles: number;
    failedFiles: number;
    cancelled: boolean;
  }) => {
    state.uploadState.running = false;
    state.uploadState.jobId = "";

    if (payload.cancelled && !state.auth.loggedIn) {
      // Keep the existing auth-required message.
    } else if (payload.cancelled) {
      setStatus("Upload job cancelled.", "info");
    } else if (payload.failedFiles > 0) {
      setStatus(
        `Upload job finished with ${payload.completedFiles} completed and ${payload.failedFiles} failed.`,
        "error",
      );
    } else {
      setStatus(`Upload job completed (${payload.completedFiles} files).`, "success");
    }

    render();
  });

  EventsOn("upload:auth-required", () => {
    state.auth.loggedIn = false;
    state.auth.expiresAt = "";
    state.uploadState.running = false;
    state.uploadState.jobId = "";
    setStatus("Session expired. Please login again.", "error");
    render();
  });
}

async function refreshSettings(): Promise<void> {
  state.settings = await GetSettings();
}

async function refreshAuth(): Promise<void> {
  const auth = await AuthStatus();
  state.auth.loggedIn = auth.loggedIn;
  state.auth.expiresAt = auth.expiresAt;
}

async function refreshUploadState(): Promise<void> {
  const value = await UploadState();
  state.uploadState.running = value.running;
  state.uploadState.jobId = value.jobId;
}

function setStatus(message: string, kind: "info" | "success" | "error"): void {
  state.statusMessage = message;
  state.statusKind = kind;
}

function render(): void {
  appElement.innerHTML = `
    <div class="shell">
      <header class="topbar">
        <div>
          <h1>Simple File Store Desktop</h1>
          <p>Go-based uploader with true multi-connection chunk uploads.</p>
        </div>
        <div class="session-pill ${state.auth.loggedIn ? "ok" : "expired"}">
          ${state.auth.loggedIn ? `Logged in${state.auth.expiresAt ? ` until ${new Date(state.auth.expiresAt).toLocaleString()}` : ""}` : "Not logged in"}
        </div>
      </header>

      <section class="panel">
        <h2>Settings</h2>
        <form id="settingsForm" class="grid-form">
          <label>
            Service endpoint
            <input id="endpointInput" type="url" required value="${escapeHTML(state.settings.endpoint)}" placeholder="http://127.0.0.1:8080" />
          </label>
          <label>
            Upload threads
            <input id="threadsInput" type="number" min="1" max="64" required value="${state.settings.uploadThreads}" />
          </label>
          <button type="submit">Save settings</button>
        </form>
      </section>

      <section class="panel">
        <h2>Login</h2>
        <form id="loginForm" class="grid-form ${state.auth.loggedIn ? "disabled" : ""}">
          <label>
            Username
            <input id="usernameInput" type="text" autocomplete="username" ${state.auth.loggedIn ? "disabled" : ""} required />
          </label>
          <label>
            Password
            <input id="passwordInput" type="password" autocomplete="current-password" ${state.auth.loggedIn ? "disabled" : ""} required />
          </label>
          <div class="buttons-row">
            <button type="submit" ${state.auth.loggedIn ? "disabled" : ""}>Login</button>
            <button id="logoutBtn" type="button" class="secondary" ${!state.auth.loggedIn ? "disabled" : ""}>Logout</button>
          </div>
        </form>
      </section>

      <section class="panel">
        <h2>Upload</h2>
        <form id="uploadForm" class="grid-form ${!state.auth.loggedIn ? "disabled" : ""}">
          <label>
            Remote directory
            <input id="remoteDirInput" type="text" value="${escapeHTML(state.remoteDirectory)}" placeholder="e.g. media/releases" ${state.uploadState.running ? "disabled" : ""} />
            <small>Leave empty to upload to root directory.</small>
          </label>

          <div class="files-box">
            <div class="files-head">
              <span>Selected files (${state.selectedFiles.length})</span>
              <button id="pickFilesBtn" type="button" class="secondary" ${state.uploadState.running ? "disabled" : ""}>Choose files</button>
            </div>
            <ul class="files-list">
              ${
                state.selectedFiles.length === 0
                  ? `<li class="muted">No files selected.</li>`
                  : state.selectedFiles.map((entry) => `<li>${escapeHTML(entry)}</li>`).join("")
              }
            </ul>
          </div>

          <div class="buttons-row">
            <button id="startUploadBtn" type="submit" ${
              !state.auth.loggedIn || state.selectedFiles.length === 0 || state.uploadState.running
                ? "disabled"
                : ""
            }>Start upload</button>
            <button id="cancelUploadBtn" type="button" class="secondary" ${
              state.uploadState.running ? "" : "disabled"
            }>Cancel upload</button>
          </div>
        </form>
      </section>

      <section class="panel">
        <h2>Progress</h2>
        <ul class="progress-list">
          ${renderProgressItems()}
        </ul>
      </section>

      ${
        state.statusMessage
          ? `<section class="status ${state.statusKind}">${escapeHTML(state.statusMessage)}</section>`
          : ""
      }
    </div>
  `;

  bindHandlers();
}

function renderProgressItems(): string {
  if (state.fileProgress.size === 0) {
    return `<li class="muted">No uploads started yet.</li>`;
  }

  const values = Array.from(state.fileProgress.values());

  return values
    .map((item) => {
      const pct = Math.floor(item.progress * 100);
      const status = item.failed ? "failed" : item.done ? "done" : "uploading";
      const statusText = item.failed ? `Failed: ${item.error}` : item.done ? "Completed" : "Uploading";
      return `
        <li class="progress-item ${status}">
          <div class="line-1">
            <strong>${escapeHTML(item.filePath)}</strong>
            <span>${pct}%</span>
          </div>
          <div class="line-2">${escapeHTML(item.remotePath || "(root)")}</div>
          <div class="bar-wrap"><div class="bar" style="transform:scaleX(${Math.max(0, Math.min(1, item.progress))})"></div></div>
          <div class="line-3">
            <span>${prettySize(item.uploadedBytes)} / ${prettySize(item.fileSize)}</span>
            <span>${item.completedChunks}/${item.totalChunks} chunks</span>
            <span>${item.threadCount} threads</span>
            <span>${escapeHTML(statusText)}</span>
          </div>
        </li>
      `;
    })
    .join("");
}

function bindHandlers(): void {
  const settingsForm = document.querySelector<HTMLFormElement>("#settingsForm");
  settingsForm?.addEventListener("submit", async (event) => {
    event.preventDefault();
    if (state.isBusy) {
      return;
    }

    state.isBusy = true;
    try {
      const endpoint = getInputValue("#endpointInput");
      const uploadThreads = Number.parseInt(getInputValue("#threadsInput"), 10);

      state.settings = await SaveSettings({ endpoint, uploadThreads });
      state.auth.loggedIn = false;
      state.auth.expiresAt = "";
      setStatus("Settings saved. Please login again.", "success");
    } catch (error) {
      setStatus(errorMessage(error, "Failed to save settings."), "error");
    } finally {
      state.isBusy = false;
      render();
    }
  });

  const loginForm = document.querySelector<HTMLFormElement>("#loginForm");
  loginForm?.addEventListener("submit", async (event) => {
    event.preventDefault();
    if (state.isBusy || state.auth.loggedIn) {
      return;
    }

    state.isBusy = true;
    try {
      const username = getInputValue("#usernameInput");
      const password = getInputValue("#passwordInput");

      const auth = await Login({ username, password });
      state.auth.loggedIn = auth.loggedIn;
      state.auth.expiresAt = auth.expiresAt;
      setStatus("Login successful.", "success");
    } catch (error) {
      setStatus(errorMessage(error, "Login failed."), "error");
    } finally {
      state.isBusy = false;
      render();
    }
  });

  const logoutButton = document.querySelector<HTMLButtonElement>("#logoutBtn");
  logoutButton?.addEventListener("click", async () => {
    await Logout();
    state.auth.loggedIn = false;
    state.auth.expiresAt = "";
    state.uploadState.running = false;
    state.uploadState.jobId = "";
    setStatus("Logged out.", "info");
    render();
  });

  const pickFilesButton = document.querySelector<HTMLButtonElement>("#pickFilesBtn");
  pickFilesButton?.addEventListener("click", async () => {
    if (state.uploadState.running || state.isBusy) {
      return;
    }

    state.remoteDirectory = getInputValue("#remoteDirInput");

    state.isBusy = true;
    try {
      const selected = await PickFiles();
      state.selectedFiles = selected;
      setStatus(`${selected.length} file(s) selected.`, "info");
    } catch (error) {
      setStatus(errorMessage(error, "Failed to pick files."), "error");
    } finally {
      state.isBusy = false;
      render();
    }
  });

  const uploadForm = document.querySelector<HTMLFormElement>("#uploadForm");
  uploadForm?.addEventListener("submit", async (event) => {
    event.preventDefault();
    if (!state.auth.loggedIn || state.uploadState.running || state.selectedFiles.length === 0 || state.isBusy) {
      return;
    }

    state.remoteDirectory = getInputValue("#remoteDirInput");

    state.isBusy = true;
    try {
      const result = await StartUpload({
        filePaths: state.selectedFiles,
        remoteDirectory: state.remoteDirectory,
      });
      state.uploadState.running = true;
      state.uploadState.jobId = result.jobId;
      setStatus("Upload started.", "info");
    } catch (error) {
      setStatus(errorMessage(error, "Failed to start upload."), "error");
    } finally {
      state.isBusy = false;
      render();
    }
  });

  const cancelButton = document.querySelector<HTMLButtonElement>("#cancelUploadBtn");
  cancelButton?.addEventListener("click", async () => {
    if (!state.uploadState.running) {
      return;
    }

    await CancelUpload();
    setStatus("Cancelling upload job...", "info");
    render();
  });
}

function getInputValue(selector: string): string {
  const input = document.querySelector<HTMLInputElement>(selector);
  return input?.value.trim() ?? "";
}

function prettySize(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes < 0) {
    return "0 B";
  }
  if (bytes < 1024) {
    return `${bytes} B`;
  }
  const units = ["KiB", "MiB", "GiB", "TiB"];
  let value = bytes;
  let i = -1;
  while (value >= 1024 && i < units.length - 1) {
    value /= 1024;
    i += 1;
  }
  return `${value.toFixed(2)} ${units[i]}`;
}

function escapeHTML(value: string): string {
  return value
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#39;");
}

function errorMessage(error: unknown, fallback: string): string {
  if (typeof error === "string" && error.trim() !== "") {
    return error;
  }

  if (error instanceof Error && error.message.trim() !== "") {
    return error.message;
  }

  return fallback;
}
