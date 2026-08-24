import { describe, it, expect } from "vitest";
import { renderHook, act } from "@testing-library/react";
import { useSavedFlag } from "./useSavedFlag";

describe("useSavedFlag", () => {
  it("defaults to false", () => {
    const { result } = renderHook(() => useSavedFlag());
    expect(result.current.saved).toBe(false);
  });

  it("seeds from the initial argument", () => {
    const { result } = renderHook(() => useSavedFlag(true));
    expect(result.current.saved).toBe(true);
  });

  it("markSaved sets saved to true", () => {
    const { result } = renderHook(() => useSavedFlag());
    act(() => result.current.markSaved());
    expect(result.current.saved).toBe(true);
  });

  it("clearSaved sets saved to false", () => {
    const { result } = renderHook(() => useSavedFlag(true));
    act(() => result.current.clearSaved());
    expect(result.current.saved).toBe(false);
  });

  it("stays true until explicitly cleared -- no auto-dismiss", () => {
    const { result } = renderHook(() => useSavedFlag());
    act(() => result.current.markSaved());
    expect(result.current.saved).toBe(true);
    // Re-render without calling clearSaved.
    expect(result.current.saved).toBe(true);
  });
});
