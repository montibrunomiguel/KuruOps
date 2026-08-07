import { useEffect, useRef } from "react";
import { useAuth } from "../auth/AuthContext";

export interface StreamEvent {
  type: string;
  data: unknown;
}

const RECONNECT_DELAY_MS = 3000;

// useEventStream opens a live connection to GET /api/v1/events/stream and
// calls onEvent for every event the backend pushes (see
// EventsHandlers.Stream / events.Broadcaster on the backend) -- alert/
// incident changes made by anyone, not just this tab, so a list/dashboard
// page can refresh live instead of only on a manual reload.
//
// Built on fetch() + ReadableStream rather than the browser's native
// EventSource: EventSource has no way to send a custom Authorization
// header, and this app's bearer-token auth has no cookie fallback. The
// usual EventSource workaround -- the token as a URL query parameter --
// is exactly the kind of leak-through-proxy-logs/browser-history risk this
// codebase already avoids elsewhere (see SAMLAuthService.ServeACS's doc
// comment on the backend), so it's not used here either.
//
// Reconnects with a fixed delay on any error or clean close -- a dropped
// connection (tab backgrounded, brief network blip, api container restart)
// shouldn't permanently stop live updates for the rest of the session.
export function useEventStream(onEvent: (event: StreamEvent) => void) {
  const { token, isAuthenticated } = useAuth();
  const onEventRef = useRef(onEvent);
  onEventRef.current = onEvent;

  useEffect(() => {
    if (!isAuthenticated || !token) return;

    let stopped = false;
    let reconnectTimer: ReturnType<typeof setTimeout> | undefined;
    let abortController: AbortController | undefined;

    async function connect() {
      abortController = new AbortController();
      try {
        const res = await fetch("/api/v1/events/stream", {
          headers: { Authorization: `Bearer ${token}` },
          signal: abortController.signal,
        });
        if (!res.ok || !res.body) throw new Error("event stream request failed");

        const reader = res.body.getReader();
        const decoder = new TextDecoder();
        let buffer = "";

        while (!stopped) {
          const { done, value } = await reader.read();
          if (done) break;
          buffer += decoder.decode(value, { stream: true });

          // SSE frames are separated by a blank line; the last, possibly
          // incomplete chunk is kept in buffer for the next read.
          const frames = buffer.split("\n\n");
          buffer = frames.pop() ?? "";
          for (const frame of frames) {
            const event = parseFrame(frame);
            if (event) onEventRef.current(event);
          }
        }
      } catch {
        // Network error, non-OK response, or the fetch being aborted
        // (component unmounting, or a reconnect superseding this attempt)
        // -- all fall through to the reconnect below; unmounting sets
        // `stopped` first, so a reconnect never fires after cleanup.
      }
      if (!stopped) {
        reconnectTimer = setTimeout(connect, RECONNECT_DELAY_MS);
      }
    }

    connect();

    return () => {
      stopped = true;
      abortController?.abort();
      clearTimeout(reconnectTimer);
    };
  }, [token, isAuthenticated]);
}

// parseFrame turns one raw SSE frame ("event: alert\ndata: {...}") into a
// StreamEvent, or null for a keep-alive ping (a comment line starting with
// ":", carrying no data) or a malformed/empty frame.
function parseFrame(frame: string): StreamEvent | null {
  let type = "message";
  let data = "";
  for (const line of frame.split("\n")) {
    if (line.startsWith(":")) return null;
    if (line.startsWith("event:")) type = line.slice(6).trim();
    else if (line.startsWith("data:")) data = line.slice(5).trim();
  }
  if (!data) return null;
  try {
    return { type, data: JSON.parse(data) };
  } catch {
    return null;
  }
}
