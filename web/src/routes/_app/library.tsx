import { createFileRoute } from "@tanstack/react-router";

import { LibraryView } from "../../features/library/library-view";

export const Route = createFileRoute("/_app/library")({
  component: LibraryView,
});
