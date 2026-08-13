import { useState } from "react";
import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MultiSelectFilter, type MultiSelectOption } from "./MultiSelectFilter";

const OPTIONS: MultiSelectOption[] = [
  { value: "critical", label: "Critical" },
  { value: "high", label: "High" },
  { value: "medium", label: "Medium" },
];

function Controlled({ onChange }: { onChange: (v: string[]) => void }) {
  const [value, setValue] = useState<string[]>([]);
  return (
    <MultiSelectFilter
      options={OPTIONS}
      value={value}
      onChange={(next) => {
        setValue(next);
        onChange(next);
      }}
      placeholder="All severities"
      ariaLabel="Severity filter"
    />
  );
}

describe("MultiSelectFilter", () => {
  it("selecting an option adds it as a chip and calls onChange with it appended", async () => {
    const onChange = vi.fn();
    render(<Controlled onChange={onChange} />);

    await userEvent.selectOptions(screen.getByLabelText("Severity filter"), "critical");

    expect(onChange).toHaveBeenLastCalledWith(["critical"]);
    expect(screen.getByText("Critical")).toBeInTheDocument();
  });

  it("selecting a second option keeps the first and adds the second", async () => {
    const onChange = vi.fn();
    render(<Controlled onChange={onChange} />);

    await userEvent.selectOptions(screen.getByLabelText("Severity filter"), "critical");
    await userEvent.selectOptions(screen.getByLabelText("Severity filter"), "high");

    expect(onChange).toHaveBeenLastCalledWith(["critical", "high"]);
    expect(screen.getByText("Critical")).toBeInTheDocument();
    expect(screen.getByText("High")).toBeInTheDocument();
  });

  it("a selected option is no longer offered in the dropdown", async () => {
    render(<Controlled onChange={vi.fn()} />);
    await userEvent.selectOptions(screen.getByLabelText("Severity filter"), "critical");

    const select = screen.getByLabelText("Severity filter") as HTMLSelectElement;
    const optionValues = Array.from(select.options).map((o) => o.value);
    expect(optionValues).not.toContain("critical");
    expect(optionValues).toContain("high");
  });

  it("removing a chip calls onChange without it", async () => {
    const onChange = vi.fn();
    render(<Controlled onChange={onChange} />);
    await userEvent.selectOptions(screen.getByLabelText("Severity filter"), "critical");
    await userEvent.selectOptions(screen.getByLabelText("Severity filter"), "high");
    onChange.mockClear();

    await userEvent.click(screen.getByRole("button", { name: "Remove Critical" }));

    expect(onChange).toHaveBeenCalledWith(["high"]);
    // "Critical" itself is still offered again in the dropdown once removed
    // from the selection -- the chip (and its remove button) is what must
    // be gone.
    expect(screen.queryByRole("button", { name: "Remove Critical" })).not.toBeInTheDocument();
    expect(screen.getByText("High")).toBeInTheDocument();
  });

  it("shows the placeholder text when nothing is selected", () => {
    render(<Controlled onChange={vi.fn()} />);
    expect(screen.getByText("All severities")).toBeInTheDocument();
  });
});
