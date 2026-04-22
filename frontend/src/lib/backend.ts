type Settings = {
  endpoint: string;
  uploadThreads: number;
};

type AuthStatus = {
  loggedIn: boolean;
  expiresAt: string;
};

type LoginRequest = {
  username: string;
  password: string;
};

type UploadStartRequest = {
  filePaths: string[];
  remoteDirectory: string;
};

type UploadStartResponse = {
  jobId: string;
};

type UploadState = {
  running: boolean;
  jobId: string;
};

type MainAppBindings = {
  GetSettings: () => Promise<Settings>;
  SaveSettings: (next: Settings) => Promise<Settings>;
  AuthStatus: () => Promise<AuthStatus>;
  Login: (request: LoginRequest) => Promise<AuthStatus>;
  Logout: () => Promise<void>;
  PickFiles: () => Promise<string[]>;
  StartUpload: (request: UploadStartRequest) => Promise<UploadStartResponse>;
  CancelUpload: () => Promise<void>;
  UploadState: () => Promise<UploadState>;
};

declare global {
  interface Window {
    go?: {
      main?: {
        App?: MainAppBindings;
      };
    };
  }
}

function binding(): MainAppBindings {
  const app = window.go?.main?.App;
  if (!app) {
    throw new Error("Wails bindings are not available");
  }
  return app;
}

export function GetSettings(): Promise<Settings> {
  return binding().GetSettings();
}

export function SaveSettings(next: Settings): Promise<Settings> {
  return binding().SaveSettings(next);
}

export function AuthStatus(): Promise<AuthStatus> {
  return binding().AuthStatus();
}

export function Login(request: LoginRequest): Promise<AuthStatus> {
  return binding().Login(request);
}

export function Logout(): Promise<void> {
  return binding().Logout();
}

export function PickFiles(): Promise<string[]> {
  return binding().PickFiles();
}

export function StartUpload(request: UploadStartRequest): Promise<UploadStartResponse> {
  return binding().StartUpload(request);
}

export function CancelUpload(): Promise<void> {
  return binding().CancelUpload();
}

export function UploadState(): Promise<UploadState> {
  return binding().UploadState();
}

export type {
  AuthStatus,
  LoginRequest,
  Settings,
  UploadStartRequest,
  UploadStartResponse,
  UploadState,
};
