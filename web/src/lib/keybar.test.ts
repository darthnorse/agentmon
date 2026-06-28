import { describe, it, expect } from "vitest";
import { utf8, ESC, TAB, ENTER, SOFT_NEWLINE, SHIFT_TAB, arrow, encodeCtrl } from "@/lib/keybar";

const bytes = (a: Uint8Array) => Array.from(a);

describe("keybar constants", () => {
  it("encodes the fixed keys exactly", () => {
    expect(bytes(ESC)).toEqual([0x1b]);
    expect(bytes(TAB)).toEqual([0x09]);
    expect(bytes(ENTER)).toEqual([0x0d]); // CR submits
    expect(bytes(SOFT_NEWLINE)).toEqual([0x0a]); // LF inserts newline without submitting
    expect(bytes(SHIFT_TAB)).toEqual([0x1b, 0x5b, 0x5a]); // ESC [ Z
  });
});

describe("arrow (DECCKM-aware)", () => {
  it("uses ESC[ in normal mode", () => {
    expect(bytes(arrow("up", false))).toEqual([0x1b, 0x5b, 0x41]); // ESC [ A
    expect(bytes(arrow("down", false))).toEqual([0x1b, 0x5b, 0x42]);
    expect(bytes(arrow("right", false))).toEqual([0x1b, 0x5b, 0x43]);
    expect(bytes(arrow("left", false))).toEqual([0x1b, 0x5b, 0x44]);
  });
  it("uses ESCO in application-cursor mode", () => {
    expect(bytes(arrow("up", true))).toEqual([0x1b, 0x4f, 0x41]); // ESC O A
  });
});

describe("encodeCtrl (sticky Ctrl)", () => {
  it("maps lowercase and uppercase to the same control byte", () => {
    expect(bytes(encodeCtrl("c"))).toEqual([0x03]); // Ctrl-C
    expect(bytes(encodeCtrl("C"))).toEqual([0x03]);
    expect(bytes(encodeCtrl("d"))).toEqual([0x04]);
    expect(bytes(encodeCtrl("l"))).toEqual([0x0c]);
  });
  it("masks non-letters with 0x1f", () => {
    expect(bytes(encodeCtrl("["))).toEqual([0x1b]); // 0x5b & 0x1f
  });
  it("sends the control byte then any trailing chars literally", () => {
    expect(bytes(encodeCtrl("cX"))).toEqual([0x03, 0x58]);
  });
});

describe("utf8", () => {
  it("encodes multibyte text", () => {
    expect(bytes(utf8("é"))).toEqual([0xc3, 0xa9]);
  });
});
