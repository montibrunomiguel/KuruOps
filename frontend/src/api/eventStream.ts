import { useEffect, useRef } from "react";
import { useAuth } from "../auth/AuthContext";

export interface StreamEvent {
  type: string;
  data: unknown;
}

const RECONNECT_BASE_DELAY_MS = 1000;
const RECONNECT_MAX_DELAY_MS = 30000;
// A connection must stay up this long before it counts as healthy and resets the
// backoff. One that opens and is cut right away (a proxy that accepts and then
// drops, an api that crashes on connect) is still a failure, or the client would
// hammer it once a second forever.
const HEALTHY_CONNECTION_MS = 10000;

// reconnectDelay is how long to wait before the next attempt after `failures`
// consecutive failed connections: exponential from 1s, capped at 30s, with the
// upper half jittered. Without the jitter every open tab retries on the same
// tick after an api restart and the whole fleet reconnects as one herd; without
// the exponent a down backend is retried every few seconds for as long as the
// tab stays open.
export function reconnectDelay(failures: number, random: () => number = Math.random): number {
  const exp = Math.min(RECONNECT_MAX_DELAY_MS, RECONNECT_BASE_DELAY_MS * 2 ** Math.max(0, failures));
  return Math.round(exp / 2 + (exp / 2) * random());
}

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
// Reconnects on any error or clean close -- a dropped connection (tab
// backgrounded, brief network blip, api container restart) shouldn't
// permanently stop live updates for the rest of the session -- with
// exponential backoff and jitter (see reconnectDelay) so a backend that is
// down is not hammered and a restart does not bring every tab back at once.
export function useEventStream(onEvent: (event: StreamEvent) => void) {
  const { token, isAuthenticated } = useAuth();
  const onEventRef = useRef(onEvent);
  // Kept current from an effect, not during render: the stream below is opened
  // once per token and must call the latest handler without reconnecting.
  useEffect(() => {
    onEventRef.current = onEvent;
  });

  useEffect(() => {
    if (!isAuthenticated || !token) return;

    let stopped = false;
    let reconnectTimer: ReturnType<typeof setTimeout> | undefined;
    let abortController: AbortController | undefined;
    let failures = 0;

    async function connect() {
      abortController = new AbortController();
      const startedAt = Date.now();
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
        failures = Date.now() - startedAt >= HEALTHY_CONNECTION_MS ? 0 : failures + 1;
        reconnectTimer = setTimeout(connect, reconnectDelay(failures - 1));
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
