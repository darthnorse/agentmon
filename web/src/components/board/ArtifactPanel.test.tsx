import { render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { describe, expect, it, vi } from "vitest";

import { ArtifactPanel } from "@/components/board/ArtifactPanel";
import { ApiError } from "@/lib/api-client";

const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
const wrapper = ({ children }: { children: ReactNode }) => (
  <QueryClientProvider client={qc}>{children}</QueryClientProvider>
);

describe("ArtifactPanel", () => {
  it("renders the fetched markdown with path/ref and any footer children", async () => {
    const queryFn = vi.fn().mockResolvedValue({ path: "docs/reviews/r.md", ref: "epic/7-x", markdown: "# Review\n\n- one" });
    render(
      <ArtifactPanel queryKey={["t", "ok"]} queryFn={queryFn} branchUrl="https://github.com/o/r/tree/epic/7-x">
        <button>Approve</button>
      </ArtifactPanel>, { wrapper },
    );
    await waitFor(() => expect(screen.getByRole("heading", { name: "Review" })).toBeInTheDocument());
    expect(screen.getByText(/docs\/reviews\/r.md @ epic\/7-x/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Approve" })).toBeInTheDocument();
  });

  it("shows the error message and a GitHub fallback link on failure", async () => {
    const queryFn = vi.fn().mockRejectedValue(new ApiError(404, "artifact not available at docs/reviews/r.md (may not be pushed yet)"));
    render(
      <ArtifactPanel queryKey={["t", "err"]} queryFn={queryFn} branchUrl="https://github.com/o/r/tree/epic/7-x" />, { wrapper },
    );
    await waitFor(() => expect(screen.getByText(/artifact not available/)).toBeInTheDocument());
    expect(screen.getByRole("link", { name: /View the branch on GitHub/ })).toHaveAttribute("href", "https://github.com/o/r/tree/epic/7-x");
  });
});
