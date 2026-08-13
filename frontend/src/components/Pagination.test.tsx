import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Pagination } from "./Pagination";

describe("Pagination", () => {
  it("renders nothing when total is 0", () => {
    const { container } = render(
      <Pagination page={1} pageSize={20} total={0} totalPages={1} onPageChange={vi.fn()} onPageSizeChange={vi.fn()} />,
    );
    expect(container).toBeEmptyDOMElement();
  });

  it("shows the current range and page indicator", () => {
    render(
      <Pagination page={2} pageSize={20} total={45} totalPages={3} onPageChange={vi.fn()} onPageSizeChange={vi.fn()} />,
    );
    expect(screen.getByText("Showing 21-40 of 45")).toBeInTheDocument();
    expect(screen.getByText("Page 2 of 3")).toBeInTheDocument();
  });

  it("clamps the end of the range to the total on the last page", () => {
    render(
      <Pagination page={3} pageSize={20} total={45} totalPages={3} onPageChange={vi.fn()} onPageSizeChange={vi.fn()} />,
    );
    expect(screen.getByText("Showing 41-45 of 45")).toBeInTheDocument();
  });

  it("disables Previous on the first page and Next on the last page", () => {
    render(
      <Pagination page={1} pageSize={20} total={45} totalPages={3} onPageChange={vi.fn()} onPageSizeChange={vi.fn()} />,
    );
    expect(screen.getByRole("button", { name: "Previous" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Next" })).not.toBeDisabled();
  });

  it("clicking Next/Previous calls onPageChange with the adjacent page", async () => {
    const onPageChange = vi.fn();
    render(
      <Pagination page={2} pageSize={20} total={45} totalPages={3} onPageChange={onPageChange} onPageSizeChange={vi.fn()} />,
    );
    await userEvent.click(screen.getByRole("button", { name: "Next" }));
    expect(onPageChange).toHaveBeenLastCalledWith(3);

    await userEvent.click(screen.getByRole("button", { name: "Previous" }));
    expect(onPageChange).toHaveBeenLastCalledWith(1);
  });

  it("changing the page-size select calls onPageSizeChange with a number", async () => {
    const onPageSizeChange = vi.fn();
    render(
      <Pagination page={1} pageSize={20} total={45} totalPages={3} onPageChange={vi.fn()} onPageSizeChange={onPageSizeChange} />,
    );
    await userEvent.selectOptions(screen.getByLabelText("Items per page"), "100");
    expect(onPageSizeChange).toHaveBeenCalledWith(100);
  });
});
