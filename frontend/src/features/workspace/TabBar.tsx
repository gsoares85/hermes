import { ConnectionMarks } from "../connection/ConnectionMarks";

import { moved, type Tabs } from "./tabs";

/**
 * One tab per open connection.
 *
 * A real tablist rather than a row of buttons that looks like one: a reader
 * told this is a tablist expects the arrows to move between the tabs and only
 * one of them to be a stop on the way through the window. Saying `tablist` and
 * leaving every tab tabbable would be an announcement the strip does not honour.
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
  return (
    <div className="tabs" role="tablist" aria-label="Open connections">
      {tabs.open.map((tab): React.JSX.Element => {
        const active = tab.id === tabs.activeId;

        return (
          <div key={tab.id} className={active ? "tabs__tab is-active" : "tabs__tab"}>
            <button
              type="button"
              role="tab"
              aria-selected={active}
              tabIndex={active ? 0 : -1}
              onClick={(): void => {
                onPick(tab.id);
              }}
              onKeyDown={(event): void => {
                const to = moved(tabs, event.key);
                if (to === null) {
                  return;
                }

                event.preventDefault();
                onPick(to);
              }}
            >
              <span className="tabs__name">{tab.name === "" ? tab.host : tab.name}</span>
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
              aria-label={`Close ${tab.name === "" ? tab.host : tab.name}`}
              onClick={(): void => {
                onClose(tab.id);
              }}
            >
              ×
            </button>
          </div>
        );
      })}
    </div>
  );
}
