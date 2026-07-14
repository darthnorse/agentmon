import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { ProjectSwitcher } from "@/components/board/ProjectSwitcher";
import type { ProjectDTO } from "@/lib/contracts";

const p = (id: string, name: string): ProjectDTO => ({
  id, name, repo: "o/r", server_id: "h1", target: "", workdir: "/w", base_branch: "main",
  provider: "claude", required_reviews: [], max_parallel: 1, paused: false, require_ci: false, pinned: false, requirements: [],
});

describe("ProjectSwitcher", () => {
  it("lists All + projects with needs counts and fires onSelect", () => {
    const onSelect = vi.fn();
    render(
      <ProjectSwitcher projects={[p("p1", "school"), p("p2", "dnsmon")]}
        needs={new Map([["p1", 2]])} current="p1" onSelect={onSelect} />,
    );
    const sel = screen.getByRole("combobox");
    expect(screen.getByText("All projects")).toBeInTheDocument();
    expect(screen.getByText("school (2!)")).toBeInTheDocument();
    fireEvent.change(sel, { target: { value: "" } });
    expect(onSelect).toHaveBeenCalledWith(null);
    fireEvent.change(sel, { target: { value: "p2" } });
    expect(onSelect).toHaveBeenCalledWith("p2");
  });
});
