import { useQuery } from "@tanstack/react-query";

import { getHealthzOptions } from "./api/gen/@tanstack/react-query.gen";

export function App() {
  const { data, isLoading, isError } = useQuery(getHealthzOptions());

  return (
    <main>
      <h1>Loomtale Studio</h1>
      {isLoading && <p>Checking API health...</p>}
      {isError && <p role="alert">API is unreachable.</p>}
      {data && (
        <p>
          API status: <strong>{data.status}</strong> (version {data.version})
        </p>
      )}
    </main>
  );
}
