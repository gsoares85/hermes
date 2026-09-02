import { useEffect, useState } from "react";

import { fetchAppInfo, unknownAppInfo, type AppInfo } from "./api/appInfo";
import { ConnectionForm } from "./features/connection/ConnectionForm";

export function App(): React.JSX.Element {
  const [info, setInfo] = useState<AppInfo>(unknownAppInfo);

  useEffect(() => {
    let active = true;

    fetchAppInfo()
      .then((loaded): void => {
        if (active) {
          setInfo(loaded);
        }
      })
      .catch((): void => {
        // Running outside the desktop shell: keep the placeholder rather than
        // breaking the window over build metadata.
      });

    return (): void => {
      active = false;
    };
  }, []);

  return (
    <div className="app">
      <header className="app__header">
        <h1>Hermes</h1>
        <p>PostgreSQL, without the license.</p>
      </header>

      <main className="app__main">
        <ConnectionForm />
      </main>

      <footer className="app__status" title={`${info.commit} · ${info.date}`}>
        <span>{info.version}</span>
        <span>{info.platform}</span>
      </footer>
    </div>
  );
}
