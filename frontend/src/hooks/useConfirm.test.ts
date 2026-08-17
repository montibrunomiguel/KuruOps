import { describe, it, expect } from "vitest";
import { renderHook, act } from "@testing-library/react";
import { useConfirm } from "./useConfirm";

describe("useConfirm", () => {
  it("defaults to unconfirmed", () => {
    const { result } = renderHook(() => useConfirm());
    expect(result.current.confirming).toBeNull();
  });

  it("confirm() with no argument sets a global boolean confirmation", () => {
    const { result } = renderHook(() => useConfirm());
    act(() => result.current.confirm());
    expect(result.current.confirming).toBe(true);
  });

  it("cancel() clears the confirmation", () => {
    const { result } = renderHook(() => useConfirm());
    act(() => result.current.confirm());
    act(() => result.current.cancel());
    expect(result.current.confirming).toBeNull();
  });

  it("supports a per-row id-scoped confirmation", () => {
    const { result } = renderHook(() => useConfirm<string>());
    act(() => result.current.confirm("row-42"));
    expect(result.current.confirming).toBe("row-42");
    act(() => result.current.confirm("row-7"));
    expect(result.current.confirming).toBe("row-7");
    act(() => result.current.cancel());
    expect(result.current.confirming).toBeNull();
  });
});
