import { useEffect, useState } from "react";

import { fetchAppInfo, unknownAppInfo, type AppInfo } from "./api/appInfo";

export function App(): React.JSX.Element {
  const [info, setInfo] = useState<AppInfo>(unknownAppInfo);

  useEffect(() => {
    let active = true;

    fetchAppInfo()
      .then((loaded) => {
        if (active) {
          setInfo(loaded);
        }
      })
      .catch(() => {
        // Running outside the desktop shell: keep the placeholder rather than
        // breaking the window over build metadata.
      });

    return () => {
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
        <p>
          The shell is up. Connections, object tree and SQL editor arrive in the tasks that
          follow.
        </p>
      </main>

      <footer className="app__status" title={`${info.commit} · ${info.date}`}>
        <span>{info.version}</span>
        <span>{info.platform}</span>
      </footer>
    </div>
  );
}
