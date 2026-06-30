import { describe, it, expect, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { SettingsPanel } from "@/components/SettingsPanel";
import { usePrefs } from "@/store/prefs";

function resetPrefs() {
  localStorage.clear();
  usePrefs.setState({
    fontSizeDesktop: 13,
    fontSizeMobile: 10,
    terminalTheme: "dark",
    alertOnDone: false,
  });
}

describe("SettingsPanel", () => {
  beforeEach(resetPrefs);

  it("keeps the controls hidden until the gear is clicked", () => {
    render(<SettingsPanel />);
    expect(screen.queryByLabelText("Terminal theme")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Settings" })).toBeInTheDocument();
  });

  it("changes the terminal theme in the prefs store", async () => {
    render(<SettingsPanel />);
    await userEvent.click(screen.getByRole("button", { name: "Settings" }));
    await userEvent.selectOptions(screen.getByLabelText("Terminal theme"), "highContrast");
    expect(usePrefs.getState().terminalTheme).toBe("highContrast");
  });

  it("toggles the done-alert checkbox in the store", async () => {
    render(<SettingsPanel />);
    await userEvent.click(screen.getByRole("button", { name: "Settings" }));
    const cb = screen.getByLabelText(/finishes/i);
    expect((cb as HTMLInputElement).checked).toBe(false);
    await userEvent.click(cb);
    expect(usePrefs.getState().alertOnDone).toBe(true);
  });

  it("steps the desktop and mobile font sizes in the store", async () => {
    render(<SettingsPanel />);
    await userEvent.click(screen.getByRole("button", { name: "Settings" }));
    await userEvent.click(screen.getByRole("button", { name: "Increase desktop font" }));
    expect(usePrefs.getState().fontSizeDesktop).toBe(14);
    await userEvent.click(screen.getByRole("button", { name: "Decrease mobile font" }));
    expect(usePrefs.getState().fontSizeMobile).toBe(9);
  });

  it("reflects the current store value in the theme select", async () => {
    usePrefs.setState({ terminalTheme: "light" });
    render(<SettingsPanel />);
    await userEvent.click(screen.getByRole("button", { name: "Settings" }));
    expect((screen.getByLabelText("Terminal theme") as HTMLSelectElement).value).toBe("light");
  });
});
