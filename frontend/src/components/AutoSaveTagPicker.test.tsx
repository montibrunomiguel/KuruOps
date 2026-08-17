import { describe, it, expect, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { AuthProvider } from "../auth/AuthContext";
import { AutoSaveTagPicker } from "./AutoSaveTagPicker";

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
}

function catalogFixture() {
  return [
    { id: "t1", name: "phishing" },
    { id: "t2", name: "internal" },
  ];
}

function routeFetch(putStatus = 200) {
  return vi.fn().mockImplementation((url: string, init?: RequestInit) => {
    if (url.includes("/api/v1/tags")) return Promise.resolve(jsonResponse(catalogFixture()));
    if (init?.method === "PUT") {
      return putStatus === 200
        ? Promise.resolve(jsonResponse({}))
        : Promise.resolve(jsonResponse({ error: "tag not found" }, putStatus));
    }
    return Promise.resolve(jsonResponse({}));
  });
}

function renderPicker(onSaved: () => void, value: string[] = ["phishing"]) {
  return render(
    <AuthProvider>
      <AutoSaveTagPicker resourcePath="/api/v1/alerts/a1/tags" value={value} onSaved={onSaved} />
    </AuthProvider>,
  );
}

describe("AutoSaveTagPicker", () => {
  it("PUTs the new tag set to resourcePath and calls onSaved on success", async () => {
    const fetchMock = routeFetch();
    vi.stubGlobal("fetch", fetchMock);
    const onSaved = vi.fn();
    renderPicker(onSaved);

    const select = await screen.findByLabelText("+ Add tag...");
    await userEvent.selectOptions(select, "internal");

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(
        "/api/v1/alerts/a1/tags",
        expect.objectContaining({ method: "PUT", body: JSON.stringify({ tags: ["phishing", "internal"] }) }),
      ),
    );
    await waitFor(() => expect(onSaved).toHaveBeenCalled());
  });

  it("reverts to the previous tags and shows an error when the save fails", async () => {
    const fetchMock = routeFetch(400);
    vi.stubGlobal("fetch", fetchMock);
    renderPicker(vi.fn());

    const select = await screen.findByLabelText("+ Add tag...");
    await userEvent.selectOptions(select, "internal");

    expect(await screen.findByText("tag not found")).toBeInTheDocument();
    expect(screen.queryByLabelText("Remove tag internal")).not.toBeInTheDocument();
  });
});
