import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { AssigneePicker } from "./AssigneePicker";
import { AuthProvider } from "../auth/AuthContext";

function wrapper({ children }: { children: ReactNode }) {
  return <AuthProvider>{children}</AuthProvider>;
}

function mockDirectory(users: Array<{ id: string; name: string }>) {
  vi.stubGlobal(
    "fetch",
    vi.fn().mockResolvedValue(
      new Response(JSON.stringify(users), { status: 200, headers: { "content-type": "application/json" } }),
    ),
  );
}

describe("AssigneePicker", () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it("renders selected assignees as removable chips, resolving names from the directory", async () => {
    mockDirectory([{ id: "u1", name: "Marina Alves" }, { id: "u2", name: "Diego Costa" }]);
    render(<AssigneePicker value={["u1"]} onChange={vi.fn()} />, { wrapper });

    await waitFor(() => expect(screen.getByText("Marina Alves")).toBeInTheDocument());
    expect(screen.getByLabelText("Remove assignee Marina Alves")).toBeInTheDocument();
  });

  it("the dropdown only offers users not already selected", async () => {
    mockDirectory([{ id: "u1", name: "Marina Alves" }, { id: "u2", name: "Diego Costa" }]);
    render(<AssigneePicker value={["u1"]} onChange={vi.fn()} />, { wrapper });

    await waitFor(() => expect(screen.getByRole("combobox")).toBeInTheDocument());
    const options = screen.getAllByRole("option").map((o) => o.textContent);
    expect(options).toContain("Diego Costa");
    expect(options).not.toContain("Marina Alves");
  });

  it("selecting an option calls onChange with the id appended", async () => {
    mockDirectory([{ id: "u1", name: "Marina Alves" }, { id: "u2", name: "Diego Costa" }]);
    const onChange = vi.fn();
    render(<AssigneePicker value={["u1"]} onChange={onChange} />, { wrapper });

    await waitFor(() => expect(screen.getByRole("combobox")).toBeInTheDocument());
    await userEvent.selectOptions(screen.getByRole("combobox"), "Diego Costa");

    expect(onChange).toHaveBeenCalledWith(["u1", "u2"]);
  });

  it("clicking the chip's remove button calls onChange without that id", async () => {
    mockDirectory([{ id: "u1", name: "Marina Alves" }, { id: "u2", name: "Diego Costa" }]);
    const onChange = vi.fn();
    render(<AssigneePicker value={["u1", "u2"]} onChange={onChange} />, { wrapper });

    await waitFor(() => expect(screen.getByLabelText("Remove assignee Marina Alves")).toBeInTheDocument());
    await userEvent.click(screen.getByLabelText("Remove assignee Marina Alves"));

    expect(onChange).toHaveBeenCalledWith(["u2"]);
  });

  it("when disabled, chips have no remove button and no dropdown is shown", async () => {
    mockDirectory([{ id: "u1", name: "Marina Alves" }, { id: "u2", name: "Diego Costa" }]);
    render(<AssigneePicker value={["u1"]} onChange={vi.fn()} disabled />, { wrapper });

    await waitFor(() => expect(screen.getByText("Marina Alves")).toBeInTheDocument());
    expect(screen.queryByLabelText("Remove assignee Marina Alves")).not.toBeInTheDocument();
    expect(screen.queryByRole("combobox")).not.toBeInTheDocument();
  });

  it("shows a hint when the directory is empty", async () => {
    mockDirectory([]);
    render(<AssigneePicker value={[]} onChange={vi.fn()} />, { wrapper });

    await waitFor(() => expect(screen.getByText(/No users available/)).toBeInTheDocument());
  });
});
