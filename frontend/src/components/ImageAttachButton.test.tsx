import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { ImageAttachButton } from "./ImageAttachButton";
import { AuthProvider } from "../auth/AuthContext";

function wrapper({ children }: { children: ReactNode }) {
  return <AuthProvider>{children}</AuthProvider>;
}

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
}

describe("ImageAttachButton", () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it("uploads the selected file and calls onChange with the returned URL", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({ url: "/api/v1/uploads/images/abc.png" }));
    vi.stubGlobal("fetch", fetchMock);
    const onChange = vi.fn();
    render(<ImageAttachButton value={null} onChange={onChange} kind="alert" id="a1" />, { wrapper });

    const file = new File(["fake-bytes"], "screenshot.png", { type: "image/png" });
    const input = document.querySelector('input[type="file"]') as HTMLInputElement;
    await userEvent.upload(input, file);

    await waitFor(() => expect(onChange).toHaveBeenCalledWith("/api/v1/uploads/images/abc.png"));
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("/api/v1/uploads/images");
    expect(init.method).toBe("POST");
    expect(init.body).toBeInstanceOf(FormData);
    const form = init.body as FormData;
    expect(form.get("kind")).toBe("alert");
    expect(form.get("id")).toBe("a1");
  });

  it("shows an error and does not call onChange when the server rejects the upload", async () => {
    // The <input accept="image/*"> filters non-image files client-side, so
    // this uses an accepted MIME type -- the server-side rejection (e.g. a
    // sniffed content mismatch) is what this test actually exercises.
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse({ error: "unsupported image type" }, 400)));
    const onChange = vi.fn();
    render(<ImageAttachButton value={null} onChange={onChange} kind="alert" id="a1" />, { wrapper });

    const file = new File(["not really an image"], "fake.png", { type: "image/png" });
    const input = document.querySelector('input[type="file"]') as HTMLInputElement;
    await userEvent.upload(input, file);

    expect(await screen.findByText("unsupported image type")).toBeInTheDocument();
    expect(onChange).not.toHaveBeenCalled();
  });

  it("renders a preview and a Remove button once a value is set", async () => {
    const onChange = vi.fn();
    render(<ImageAttachButton value="/api/v1/uploads/images/abc.png" onChange={onChange} kind="alert" id="a1" />, { wrapper });

    expect(screen.getByRole("img")).toHaveAttribute("src", "/api/v1/uploads/images/abc.png");
    await userEvent.click(screen.getByRole("button", { name: "Remove" }));
    expect(onChange).toHaveBeenCalledWith(null);
  });

  it("when disabled with a value set, no Remove button is shown", () => {
    render(<ImageAttachButton value="/api/v1/uploads/images/abc.png" onChange={vi.fn()} kind="alert" id="a1" disabled />, { wrapper });
    expect(screen.queryByRole("button", { name: "Remove" })).not.toBeInTheDocument();
  });
});
