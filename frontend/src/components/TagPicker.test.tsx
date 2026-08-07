import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { TagPicker } from "./TagPicker";
import { AuthProvider } from "../auth/AuthContext";

function wrapper({ children }: { children: ReactNode }) {
  return <AuthProvider>{children}</AuthProvider>;
}

function mockCatalog(tags: Array<{ id: string; name: string }>) {
  vi.stubGlobal(
    "fetch",
    vi.fn().mockResolvedValue(
      new Response(JSON.stringify(tags), { status: 200, headers: { "content-type": "application/json" } }),
    ),
  );
}

describe("TagPicker", () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it("renders selected tags as removable chips", async () => {
    mockCatalog([{ id: "1", name: "phishing" }, { id: "2", name: "vpn" }]);
    render(<TagPicker value={["phishing"]} onChange={vi.fn()} />, { wrapper });

    await waitFor(() => expect(screen.getByText("phishing")).toBeInTheDocument());
    expect(screen.getByLabelText("Remove tag phishing")).toBeInTheDocument();
  });

  it("the dropdown only offers tags not already selected", async () => {
    mockCatalog([{ id: "1", name: "phishing" }, { id: "2", name: "vpn" }]);
    render(<TagPicker value={["phishing"]} onChange={vi.fn()} />, { wrapper });

    await waitFor(() => expect(screen.getByRole("combobox")).toBeInTheDocument());
    const options = screen.getAllByRole("option").map((o) => o.textContent);
    expect(options).toContain("vpn");
    expect(options).not.toContain("phishing");
  });

  it("selecting an option calls onChange with the tag appended", async () => {
    mockCatalog([{ id: "1", name: "phishing" }, { id: "2", name: "vpn" }]);
    const onChange = vi.fn();
    render(<TagPicker value={["phishing"]} onChange={onChange} />, { wrapper });

    await waitFor(() => expect(screen.getByRole("combobox")).toBeInTheDocument());
    await userEvent.selectOptions(screen.getByRole("combobox"), "vpn");

    expect(onChange).toHaveBeenCalledWith(["phishing", "vpn"]);
  });

  it("clicking the chip's remove button calls onChange without that tag", async () => {
    mockCatalog([{ id: "1", name: "phishing" }, { id: "2", name: "vpn" }]);
    const onChange = vi.fn();
    render(<TagPicker value={["phishing", "vpn"]} onChange={onChange} />, { wrapper });

    await waitFor(() => expect(screen.getByLabelText("Remove tag phishing")).toBeInTheDocument());
    await userEvent.click(screen.getByLabelText("Remove tag phishing"));

    expect(onChange).toHaveBeenCalledWith(["vpn"]);
  });

  it("when disabled, chips have no remove button and no dropdown is shown", async () => {
    mockCatalog([{ id: "1", name: "phishing" }, { id: "2", name: "vpn" }]);
    render(<TagPicker value={["phishing"]} onChange={vi.fn()} disabled />, { wrapper });

    await waitFor(() => expect(screen.getByText("phishing")).toBeInTheDocument());
    expect(screen.queryByLabelText("Remove tag phishing")).not.toBeInTheDocument();
    expect(screen.queryByRole("combobox")).not.toBeInTheDocument();
  });

  it("shows a hint when the catalog is empty", async () => {
    mockCatalog([]);
    render(<TagPicker value={[]} onChange={vi.fn()} />, { wrapper });

    await waitFor(() => expect(screen.getByText(/No tags registered/)).toBeInTheDocument());
  });
});
