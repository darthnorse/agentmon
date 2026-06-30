import { describe, it, expect, vi, beforeEach } from "vitest";
import type { ReactNode } from "react";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";

const h = vi.hoisted(() => ({
  listPending: vi.fn(),
  approveServer: vi.fn(),
  rejectServer: vi.fn(),
  invalidateQueries: vi.fn(),
}));
vi.mock("@/lib/api-client", () => ({
  listPending: h.listPending,
  approveServer: h.approveServer,
  rejectServer: h.rejectServer,
}));
vi.mock("@/lib/query-client", () => ({ queryClient: { invalidateQueries: h.invalidateQueries } }));
vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: vi.fn() } }));

import { PendingAgents } from "@/components/PendingAgents";

const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
const wrapper = ({ children }: { children: ReactNode }) => (
  <QueryClientProvider client={qc}>{children}</QueryClientProvider>
);

describe("PendingAgents", () => {
  beforeEach(() => {
    h.listPending.mockReset();
    h.approveServer.mockReset();
    h.rejectServer.mockReset();
    h.invalidateQueries.mockReset();
    qc.clear();
  });

  it("renders nothing when no agents are pending", async () => {
    h.listPending.mockResolvedValue([]);
    render(<PendingAgents />, { wrapper });
    await waitFor(() => expect(h.listPending).toHaveBeenCalled());
    expect(screen.queryByRole("region", { name: /pending approval/i })).toBeNull();
  });

  it("lists a pending agent (hostname + dial URL) and approves it", async () => {
    h.listPending.mockResolvedValue([
      { id: "web-01", hostname: "web-01", url: "http://10.0.0.5:8377", os: "linux", arch: "amd64" },
    ]);
    h.approveServer.mockResolvedValue(undefined);
    render(<PendingAgents />, { wrapper });

    await screen.findByText("web-01");
    expect(screen.getByText(/10\.0\.0\.5:8377/)).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: /approve/i }));
    await waitFor(() => expect(h.approveServer).toHaveBeenCalledWith("web-01"));
    expect(h.invalidateQueries).toHaveBeenCalledWith({ queryKey: ["pending"] });
    expect(h.invalidateQueries).toHaveBeenCalledWith({ queryKey: ["servers"] });
  });

  it("rejects a pending agent", async () => {
    h.listPending.mockResolvedValue([{ id: "web-01", hostname: "web-01", url: "http://x" }]);
    h.rejectServer.mockResolvedValue(undefined);
    render(<PendingAgents />, { wrapper });

    await screen.findByText("web-01");
    await userEvent.click(screen.getByRole("button", { name: /reject/i }));
    await waitFor(() => expect(h.rejectServer).toHaveBeenCalledWith("web-01"));
  });
});
