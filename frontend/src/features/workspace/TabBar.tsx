import { useEffect, useRef } from "react";

import { Icon } from "../../ui/Icon";
import { ConnectionMarks } from "../connection/ConnectionMarks";

import { moved, type Tabs } from "./tabs";

/**
 * One tab per open connection.
 *
 * A real tablist rather than a row of buttons that looks like one: a reader
 * told this is a tablist expects the arrows to move between the tabs and only
 * one of them to be a stop on the way through the window. Saying `tablist` and
 * leaving every tab tabbable would be an announcement the strip does not honour.
 *
 * The two halves of that promise are easy to keep separately and wrong apart.
 * A tab needs a wrapper — its close control cannot live inside it, because a
 * button inside a button is not markup a browser will give you — and a wrapper
 * with no role of its own comes between the tablist and the tab, so the strip
 * stops owning them and a reader stops being told "tab 2 of 3".
 *
 * And in a roving tabindex the focus has to travel with the selection. Moving
 * one without the other leaves the focus ring on a tab that has just been given
 * tabIndex -1 while a different tab is selected, so the next Tab press leaves
 * the strip from somewhere nobody chose. It is the half nobody sees: the
 * selection visibly moves, which is what a checklist looks at.
 */
export function TabBar({
  tabs,
  onPick,
  onClose,
}: {
  tabs: Tabs;
  onPick: (id: string) => void;
  onClose: (id: string) => void;
}): React.JSX.Element {
  const buttons = useRef(new Map<string, HTMLButtonElement>());
  // Whether the selection last moved because of a key. Clicking already put
  // the focus where the person put it, and taking it again would move it for
  // somebody who never asked.
  const byKey = useRef(false);

  useEffect((): void => {
    if (!byKey.current) {
      return;
    }

    byKey.current = false;
    buttons.current.get(tabs.activeId)?.focus();
  }, [tabs.activeId]);

  return (
    <div className="tabs" role="tablist" aria-label="Open connections">
      {tabs.open.map((tab): React.JSX.Element => {
        const active = tab.id === tabs.activeId;
        const name = tab.name === "" ? tab.host : tab.name;

        return (
          <div
            key={tab.id}
            role="presentation"
            className={active ? "tabs__tab is-active" : "tabs__tab"}
          >
            <button
              type="button"
              role="tab"
              aria-selected={active}
              tabIndex={active ? 0 : -1}
              ref={(element): void => {
                if (element === null) {
                  buttons.current.delete(tab.id);
                } else {
                  buttons.current.set(tab.id, element);
                }
              }}
              onClick={(): void => {
                onPick(tab.id);
              }}
              onKeyDown={(event): void => {
                const to = moved(tabs, event.key);
                if (to === null) {
                  return;
                }

                event.preventDefault();
                // Only when the key actually moves it. Home on the first tab,
                // End on the last and either arrow with one tab open are all
                // handled and all land where they started, and a flag left
                // armed by one of those is taken by whatever changes the
                // selection next — a click, a connection opening — which
                // moves the focus for somebody who pressed a key some time
                // ago and got nothing.
                byKey.current = to !== tabs.activeId;
                onPick(to);
              }}
            >
              <Icon name="database" />
              <span className="tabs__name">{name}</span>
              <ConnectionMarks environment={tab.environment} readOnly={tab.readOnly} />
            </button>

            {/*
              Outside the tab rather than inside it: a button inside a button is
              not markup a browser will give you, and a close target that is part
              of the tab is a close somebody hits while trying to select.
            */}
            <button
              type="button"
              className="tabs__close"
              aria-label={`Close ${name}`}
              onClick={(): void => {
                onClose(tab.id);
              }}
            >
              <Icon name="close" />
            </button>
          </div>
        );
      })}
    </div>
  );
}
