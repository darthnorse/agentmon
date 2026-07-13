import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { PinnedProjects } from "@/components/board/PinnedProjects";
import type { ProjectDTO } from "@/lib/contracts";

const p = (id: string, name: string, pinned: boolean): ProjectDTO => ({
  id, name, repo: "o/r", server_id: "h1", target: "", workdir: "/w", base_branch: "main",
  provider: "claude", required_reviews: [], max_parallel: 1, paused: false, require_ci: false, pinned,
});

describe("PinnedProjects", () => {
  it("renders only pinned projects and fires onOpen with the id", () => {
    const onOpen = vi.fn();
    render(<PinnedProjects projects={[p("p1", "school", true), p("p2", "dnsmon", false)]} needs={new Map()} onOpen={onOpen} />);
    expect(screen.getByRole("button", { name: /school/ })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /dnsmon/ })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /school/ }));
    expect(onOpen).toHaveBeenCalledWith("p1");
  });

  it("shows a needs badge only when the count is > 0", () => {
    render(<PinnedProjects projects={[p("p1", "school", true), p("p2", "dnsmon", true)]} needs={new Map([["p1", 3]])} onOpen={() => {}} />);
    expect(screen.getByRole("button", { name: /school/ })).toHaveTextContent("3");
    expect(screen.getByRole("button", { name: /dnsmon/ })).not.toHaveTextContent(/\d/);
  });

  it("renders nothing when no project is pinned", () => {
    const { container } = render(<PinnedProjects projects={[p("p1", "school", false)]} needs={new Map()} onOpen={() => {}} />);
    expect(container).toBeEmptyDOMElement();
  });
});
