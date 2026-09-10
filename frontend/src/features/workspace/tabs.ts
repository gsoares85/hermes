import type { StatusView } from "../../api/connection";

/**
 * The connections the window has open, and which of them is in front.
 *
 * The Go side has held a map of open connections since the day it could open
 * one — its own pools per database and its own catalog cache each. It was the
 * window that held a single one, and closed it to open another. This is what
 * stops it doing that.
 */
export interface Tabs {
  open: StatusView[];
  activeId: string;
}

export const noTabs: Tabs = { open: [], activeId: "" };

/**
 * Whether a tab is showing the server a state describes.
 *
 * Two questions, because a connection can be recognised two ways. The
 * identifier is the same connection refreshed. The saved identifier is the
 * same server opened again — a second connection to it, with an identifier of
 * its own, which the window has no business showing twice: that is a second
 * pool and a second cached catalog for one thing on screen.
 *
 * A connection opened from a form that was never saved has no saved
 * identifier, and two of those are two different servers as far as anything
 * here can tell, so the empty string matches nothing.
 */
function sameServer(tab: StatusView, status: StatusView): boolean {
  return tab.id === status.id || (status.savedId !== "" && tab.savedId === status.savedId);
}

/**
 * A connection was opened: it goes in front.
 *
 * A server that already has a tab keeps that tab, and keeps the place it had.
 * A tab that jumped to the end when its state was refreshed would move under
 * the pointer of whoever was about to close it.
 */
export function opened(tabs: Tabs, status: StatusView): Tabs {
  const at = tabs.open.findIndex((tab): boolean => sameServer(tab, status));
  if (at < 0) {
    return { open: [...tabs.open, status], activeId: status.id };
  }

  const open = [...tabs.open];
  open[at] = status;

  return { open, activeId: status.id };
}

/**
 * The connection this state pushes out of its tab, if it pushes one out.
 *
 * Replacing the tab is only half of it: the connection that was in it is still
 * open on the Go side, holding its pools and its cached catalog with nothing
 * on screen able to close it. Whoever replaces the tab has to release it, and
 * this is how they know which.
 */
export function displaced(tabs: Tabs, status: StatusView): string {
  const held = tabs.open.find((tab): boolean => sameServer(tab, status));

  return held === undefined || held.id === status.id ? "" : held.id;
}

/**
 * A tab was closed.
 *
 * What comes forward is the neighbour on the right, and the one on the left
 * when there is no right — which is what every editor does, and it means
 * closing a tab leaves you where the work was going rather than where it had
 * been. Closing one that is not in front leaves the front alone.
 */
export function closed(tabs: Tabs, id: string): Tabs {
  const at = tabs.open.findIndex((tab): boolean => tab.id === id);
  if (at < 0) {
    return tabs;
  }

  const open = tabs.open.filter((tab): boolean => tab.id !== id);
  if (open.length === 0) {
    return noTabs;
  }

  if (tabs.activeId !== id) {
    return { open, activeId: tabs.activeId };
  }

  return { open, activeId: (open[at] ?? open[open.length - 1])?.id ?? "" };
}

/**
 * The tab in front, or nothing.
 *
 * The identifier and the list are two pieces of state and can disagree for a
 * render. Answering a tab that is not there would have the window draw a
 * connection nobody has open.
 */
export function activeOf(tabs: Tabs): StatusView | null {
  return tabs.open.find((tab): boolean => tab.id === tabs.activeId) ?? null;
}

/**
 * Which tab a key moves to, or null when the key means nothing here.
 *
 * The arrows wrap, because a strip of tabs has no ends worth stopping at — and
 * Home and End are there for anybody who wants an end on purpose.
 */
export function moved(tabs: Tabs, key: string): string | null {
  const count = tabs.open.length;
  if (count === 0) {
    return null;
  }

  const at = tabs.open.findIndex((tab): boolean => tab.id === tabs.activeId);

  switch (key) {
    case "ArrowRight":
      return tabs.open[(at + 1) % count]?.id ?? null;
    case "ArrowLeft":
      return tabs.open[(at - 1 + count) % count]?.id ?? null;
    case "Home":
      return tabs.open[0]?.id ?? null;
    case "End":
      return tabs.open[count - 1]?.id ?? null;
    default:
      return null;
  }
}
