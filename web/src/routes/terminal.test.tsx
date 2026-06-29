import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";

vi.mock("@/components/TerminalView", () => ({
  TerminalView: (p: any) => <div data-testid="tv">{`${p.serverId}:${p.paneId}:${p.target}:${String(p.showKeyBar)}`}</div>,
}));
vi.mock("@tanstack/react-router", () => ({
  useParams: () => ({ serverId: "s1", paneId: "%0" }),
  useSearch: () => ({ target: "default", session: "demo-web" }),
  useNavigate: () => vi.fn(),
}));

import { MobileTerminalRoute } from "@/routes/terminal";

describe("MobileTerminalRoute", () => {
  it("passes params/search into a key-bar TerminalView and shows the session header", () => {
    render(<MobileTerminalRoute />);
    expect(screen.getByTestId("tv")).toHaveTextContent("s1:%0:default:true");
    expect(screen.getByText("demo-web")).toBeInTheDocument();
  });
});
