import { useCallback, useRef } from "react";

import { boundsOf, keyed, resized, type Pane, type Side } from "./panes";

/**
 * The divider between a side pane and the middle of the window.
 *
 * Two things it does that are worth saying out loud.
 *
 * **The drag does not go through React.** Every pointer move would otherwise be
 * a render of everything in the pane, and what is in the navigator is a tree of
 * up to five thousand rows. The move writes the CSS variable the layout is built
 * on, straight onto the element that owns it; the state is set once, when the
 * pointer is let go. What is on screen during the drag and what the state says
 * afterwards are the same number either way.
 *
 * **It is a control, not a decoration.** `role="separator"` with a tabindex is
 * what makes it operable, and the arrows, Home, End and Enter are what make it
 * operable *without a pointer* — which for a window whose panes can be put away
 * is the difference between a layout somebody can arrange and one they cannot.
 */
export function Separator({
  side,
  pane,
  onChange,
}: {
  side: Side;
  pane: Pane;
  onChange: (pane: Pane) => void;
}): React.JSX.Element {
  const bounds = boundsOf(side);

  // The element whose variable the drag writes, and where the pane's edge is.
  // Kept from the pointerdown so that a move does not have to walk the DOM.
  const dragging = useRef<{ root: HTMLElement; edge: number } | null>(null);

  const widthAt = useCallback(
    (clientX: number, edge: number): number =>
      side === "objects" ? clientX - edge : edge - clientX,
    [side],
  );

  const onPointerDown = useCallback(
    (event: React.PointerEvent<HTMLDivElement>): void => {
      const root = event.currentTarget.closest<HTMLElement>(".workspace");
      const body = event.currentTarget.closest<HTMLElement>(".workspace__body");
      if (!root || !body) {
        return;
      }

      const box = body.getBoundingClientRect();

      dragging.current = { root, edge: side === "objects" ? box.left : box.right };
      event.currentTarget.setPointerCapture(event.pointerId);
      event.preventDefault();
    },
    [side],
  );

  const onPointerMove = useCallback(
    (event: React.PointerEvent<HTMLDivElement>): void => {
      const held = dragging.current;
      if (!held) {
        return;
      }

      const next = resized(widthAt(event.clientX, held.edge), bounds);
      held.root.style.setProperty(`--${side}-width`, `${String(next.width)}px`);
    },
    [bounds, side, widthAt],
  );

  const onPointerUp = useCallback(
    (event: React.PointerEvent<HTMLDivElement>): void => {
      const held = dragging.current;
      if (!held) {
        return;
      }

      dragging.current = null;
      event.currentTarget.releasePointerCapture(event.pointerId);
      onChange(resized(widthAt(event.clientX, held.edge), bounds));
    },
    [bounds, onChange, widthAt],
  );

  return (
    <div
      className="workspace__separator"
      role="separator"
      aria-orientation="vertical"
      aria-label={side === "objects" ? "Navigator width" : "Details width"}
      aria-valuenow={pane.open ? pane.width : 0}
      aria-valuemin={0}
      aria-valuemax={bounds.max}
      tabIndex={0}
      onPointerDown={onPointerDown}
      onPointerMove={onPointerMove}
      onPointerUp={onPointerUp}
      onPointerCancel={onPointerUp}
      onKeyDown={(event): void => {
        const next = keyed(pane, event.key, side, bounds);
        if (next === null) {
          return;
        }

        // Only once the key turned out to mean something here: swallowing every
        // key would take Tab away from a control that has to be reachable.
        event.preventDefault();
        onChange(next);
      }}
    />
  );
}
