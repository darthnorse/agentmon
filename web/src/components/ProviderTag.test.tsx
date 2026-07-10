import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import { ProviderTag } from "@/components/ProviderTag";

describe("ProviderTag", () => {
  it("renders the lowercase tag with a full-name title (no aria-label — generic role)", () => {
    render(<ProviderTag provider="codex" />);
    const tag = screen.getByText("codex");
    expect(tag).toHaveAttribute("title", "Codex");
    expect(tag).not.toHaveAttribute("aria-label");
  });
  it("renders claude with its full-name label", () => {
    render(<ProviderTag provider="claude" />);
    expect(screen.getByText("claude")).toHaveAttribute("title", "Claude Code");
  });
  it("renders nothing without a provider", () => {
    const { container } = render(<ProviderTag provider={undefined} />);
    expect(container).toBeEmptyDOMElement();
  });
});
