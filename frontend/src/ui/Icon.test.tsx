// @vitest-environment happy-dom

import { cleanup, render } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";

import { Icon, type IconName } from "./Icon";

afterEach(cleanup);

describe("a glyph", () => {
  it("is decoration, and says so", () => {
    const { container } = render(<Icon name="database" />);
    const drawn = container.querySelector("svg");

    // What an icon stands for is said in text beside it. One that carries
    // meaning alone is meaning nobody using a screen reader receives.
    expect(drawn?.getAttribute("aria-hidden")).toBe("true");
    expect(drawn?.getAttribute("focusable")).toBe("false");
  });

  it("is drawn as a stroke, or as a solid, but never as both", () => {
    const stroke = render(<Icon name="table" />).container.querySelector("svg");
    expect(stroke?.getAttribute("fill")).toBe("none");
    expect(stroke?.getAttribute("stroke")).toBe("currentColor");

    cleanup();

    const solid = render(<Icon name="keyFill" />).container.querySelector("svg");
    expect(solid?.getAttribute("fill")).toBe("currentColor");
    expect(solid?.getAttribute("stroke")).toBe("none");
  });

  /**
   * The guard asks the map, not everything the map inherits.
   *
   * `name in solids` walks the prototype chain, so "toString" passes it and
   * the lookup answers with a function for React to render — which it does by
   * throwing. Every caller passes a literal today and the union type forbids
   * anything else, but a type is erased at runtime and is the only thing
   * standing here.
   */
  it("draws nothing for a name the map merely inherits", () => {
    const { container } = render(<Icon name={"toString" as IconName} />);
    const drawn = container.querySelector("svg");

    expect(drawn?.innerHTML).toBe("");
    // The guard decides how the glyph is drawn, so getting it wrong shows up
    // here even when what it looked up renders as nothing: an inherited name
    // came back solid.
    expect(drawn?.getAttribute("fill")).toBe("none");
    expect(drawn?.getAttribute("stroke")).toBe("currentColor");
  });
});
