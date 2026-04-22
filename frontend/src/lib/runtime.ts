type EventCallback = (...data: any[]) => void;

type RuntimeBindings = {
  EventsOn: (eventName: string, callback: EventCallback) => () => void;
};

declare global {
  interface Window {
    runtime?: RuntimeBindings;
  }
}

function runtimeBinding(): RuntimeBindings {
  if (!window.runtime) {
    throw new Error("Wails runtime is not available");
  }

  return window.runtime;
}

export function EventsOn(eventName: string, callback: EventCallback): () => void {
  return runtimeBinding().EventsOn(eventName, callback);
}
