import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

import { describe, expect, it } from "vitest";

const stylesheet = readFileSync(fileURLToPath(new URL("./styles.css", import.meta.url)), "utf8");

/**
 * Every rule in the stylesheet, as its selector and its body.
 *
 * Comments are taken out first, and that is not tidiness: what sits between one
 * rule and the next is the comment above it, so a rule read without stripping
 * them carries the previous paragraph in its selector — and this file explains
 * itself at length, which made half of these matches prose about dialogs rather
 * than rules for them.
 */
function rules(): { selector: string; body: string }[] {
  const bare = stylesheet.replace(/\/\*[\s\S]*?\*\//g, "");

  return [...bare.matchAll(/([^{}]+)\{([^{}]*)\}/g)].map(
    (
      rule,
    ): {
      selector: string;
      body: string;
    } => ({ selector: (rule[1] ?? "").trim(), body: rule[2] ?? "" }),
  );
}

/**
 * The one thing about this stylesheet that no other check can see.
 *
 * A closed <dialog> is hidden by `display: none` in the user agent's
 * stylesheet, and a rule written by us is an author rule: it beats the user
 * agent whatever the specificity says, because the cascade sorts by origin
 * before anything else. So a bare `.dialog { display: flex }` puts the dialog on
 * screen permanently — from the moment the window opens, with showModal never
 * called, which means no backdrop, no focus trap, nothing inert behind it, and
 * Escape doing nothing because none of it is a modal. Closing it then removes an
 * attribute that changes nothing, and the close button and the key both look
 * broken.
 *
 * It shipped that way, twice, and every gate was green: nothing that compiles,
 * lints or runs can see a stylesheet un-hiding what the browser hid.
 */
describe("the stylesheet", () => {
  it("never gives a dialog a display unless it is open", () => {
    const offenders = rules()
      .filter((rule): boolean => /(^|[\s,>+~])[.a-z-]*dialog(?![\w-])/i.test(rule.selector))
      .filter((rule): boolean => !rule.selector.includes("[open]"))
      .filter((rule): boolean => !rule.selector.includes("::backdrop"))
      .filter((rule): boolean => /(^|[;{\s])display\s*:/.test(rule.body))
      .map((rule): string => rule.selector);

    expect(offenders).toEqual([]);
  });

  /**
   * The same trap, one element wider.
   *
   * `[hidden]` is hidden by the user agent and every rule in this file is an
   * author rule, so a class that carries a display of its own is visible with
   * the attribute set — and `.tree` carries one, because the column it draws
   * has to grow. The tree of a tab that is not in front would be drawn on top
   * of the tree of the tab that is.
   *
   * Stating it once with `!important` is what makes it true for every element
   * and for rules nobody has written yet.
   */
  it("keeps the hidden attribute working", () => {
    const hiding = rules().filter((rule): boolean => rule.selector === "[hidden]");

    expect(hiding.some((rule): boolean => /display\s*:\s*none\s*!important/.test(rule.body))).toBe(
      true,
    );
  });

  // The other half of the same rule: something has to lay it out when it is
  // open, or the dialog is a box with no direction and the header and the form
  // stack in whatever way the browser defaults to.
  it("lays a dialog out when it is open", () => {
    const opened = rules().filter(
      (rule): boolean => rule.selector.includes("dialog") && rule.selector.includes("[open]"),
    );

    expect(opened.some((rule): boolean => /display\s*:/.test(rule.body))).toBe(true);
  });
});
