import { describe, it, expect } from "vitest";
import { renderHook, act } from "@testing-library/react";
import { useRowSelection } from "./useRowSelection";

interface Row {
  id: string;
}

function rows(...ids: string[]): Row[] {
  return ids.map((id) => ({ id }));
}

describe("useRowSelection", () => {
  it("starts with nothing selected", () => {
    const { result } = renderHook(() => useRowSelection(rows("a", "b"), (r) => r.id, []));
    expect(result.current.selected.size).toBe(0);
    expect(result.current.allOnPageSelected).toBe(false);
    expect(result.current.someOnPageSelected).toBe(false);
  });

  it("toggleRow adds and removes a single id", () => {
    const { result } = renderHook(() => useRowSelection(rows("a", "b"), (r) => r.id, []));
    act(() => result.current.toggleRow("a"));
    expect(result.current.selected.has("a")).toBe(true);
    expect(result.current.someOnPageSelected).toBe(true);
    expect(result.current.allOnPageSelected).toBe(false);

    act(() => result.current.toggleRow("a"));
    expect(result.current.selected.has("a")).toBe(false);
  });

  it("toggleSelectAll selects every currently-shown row, then clears on a second call", () => {
    const { result } = renderHook(() => useRowSelection(rows("a", "b", "c"), (r) => r.id, []));
    act(() => result.current.toggleSelectAll());
    expect(result.current.selected).toEqual(new Set(["a", "b", "c"]));
    expect(result.current.allOnPageSelected).toBe(true);

    act(() => result.current.toggleSelectAll());
    expect(result.current.selected.size).toBe(0);
  });

  it("clear() empties the selection regardless of how it got populated", () => {
    const { result } = renderHook(() => useRowSelection(rows("a", "b"), (r) => r.id, []));
    act(() => result.current.toggleRow("a"));
    act(() => result.current.toggleRow("b"));
    act(() => result.current.clear());
    expect(result.current.selected.size).toBe(0);
  });

  it("keeps the header checkbox's indeterminate flag in sync via selectAllRef", () => {
    const { result } = renderHook(() => useRowSelection(rows("a", "b"), (r) => r.id, []));
    const input = document.createElement("input");
    input.type = "checkbox";
    // The ref type React exposes is read-only from the outside (it's meant
    // to be attached via JSX' ref={...}, which sets it internally) -- a
    // real component does this for free; this test stands in for that by
    // assigning directly, same as attaching it to a real <input>.
    (result.current.selectAllRef as { current: HTMLInputElement | null }).current = input;

    // Selecting one of two rows -- someOnPageSelected true, allOnPageSelected
    // false -- must flip indeterminate on.
    act(() => result.current.toggleRow("a"));
    expect(input.indeterminate).toBe(true);

    // Selecting the other one too -- allOnPageSelected true -- must flip it
    // back off (a fully-selected header checkbox is checked, not
    // indeterminate).
    act(() => result.current.toggleRow("b"));
    expect(input.indeterminate).toBe(false);

    act(() => result.current.clear());
    expect(input.indeterminate).toBe(false);
  });

  it("resets the selection when any value in resetDeps changes", () => {
    const { result, rerender } = renderHook(({ deps }: { deps: unknown[] }) => useRowSelection(rows("a", "b"), (r) => r.id, deps), {
      initialProps: { deps: ["open"] },
    });
    act(() => result.current.toggleRow("a"));
    expect(result.current.selected.size).toBe(1);

    rerender({ deps: ["closed"] });
    expect(result.current.selected.size).toBe(0);
  });

  it("does not reset the selection when resetDeps stays the same across a re-render", () => {
    const { result, rerender } = renderHook(({ deps }: { deps: unknown[] }) => useRowSelection(rows("a", "b"), (r) => r.id, deps), {
      initialProps: { deps: ["open"] },
    });
    act(() => result.current.toggleRow("a"));
    expect(result.current.selected.size).toBe(1);

    rerender({ deps: ["open"] });
    expect(result.current.selected.size).toBe(1);
  });

  it("an empty items list means allOnPageSelected is false, not vacuously true", () => {
    const { result } = renderHook(() => useRowSelection(rows(), (r: Row) => r.id, []));
    expect(result.current.allOnPageSelected).toBe(false);
  });
});
