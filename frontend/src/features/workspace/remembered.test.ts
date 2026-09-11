import { describe, expect, it } from "vitest";

import { boundsOf, initial, type Pane, type Side } from "./panes";
import { panesFrom, panesTo, rememberedPanes, rememberPanes } from "./remembered";

describe("what was stored", () => {
  it("comes back as it went in", () => {
    const panes: Record<Side, Pane> = {
      objects: { width: 300, open: true },
      details: { width: 220, open: false },
    };

    expect(panesFrom(panesTo(panes))).toEqual(panes);
  });

  it("is the default when there is nothing stored", () => {
    expect(panesFrom(null)).toEqual(initial);
    expect(panesFrom("")).toEqual(initial);
  });

  // The file is on somebody's machine and a write can be cut short, so none of
  // these is hypothetical.
  it("is the default when what is stored is not what it should be", () => {
    for (const nonsense of ["{", "null", "[]", '"a string"', "42", '{"objects":3}']) {
      expect(panesFrom(nonsense)).toEqual(initial);
    }
  });

  it("is the default for a pane whose fields are the wrong shape", () => {
    const wrong = ['{"objects":{"width":"300","open":true}}', '{"objects":{"width":300}}'];

    for (const stored of wrong) {
      expect(panesFrom(stored).objects).toEqual(initial.objects);
    }
  });

  it("is the default for a width that is not a number anybody can use", () => {
    for (const stored of ['{"objects":{"width":null,"open":true}}']) {
      expect(panesFrom(stored).objects).toEqual(initial.objects);
    }
  });

  // One bad half should not cost the other. They are two panes and were stored
  // as two panes.
  it("keeps the half that is fine when the other half is not", () => {
    const stored = '{"objects":{"width":300,"open":true},"details":"nonsense"}';

    expect(panesFrom(stored)).toEqual({
      objects: { width: 300, open: true },
      details: initial.details,
    });
  });

  // What a build that moved the bounds leaves behind. Putting the pane back near
  // where somebody had it beats putting it back where it started.
  it("brings a width from outside the bounds back inside them", () => {
    const bounds = boundsOf("objects");
    const stored = `{"objects":{"width":${String(bounds.max + 500)},"open":true}}`;

    expect(panesFrom(stored).objects).toEqual({ width: bounds.max, open: true });
  });
});

// This suite runs in node, where there is no local storage at all — so reading
// it does not miss, it throws. That is the same shape as a private window or a
// browser with site data blocked, and none of them may stop the window drawing.
describe("with nowhere to remember anything", () => {
  it("answers the defaults instead of failing", () => {
    expect(rememberedPanes()).toEqual(initial);
  });

  it("says nothing when it cannot write", () => {
    expect(() => {
      rememberPanes(initial);
    }).not.toThrow();
  });
});
