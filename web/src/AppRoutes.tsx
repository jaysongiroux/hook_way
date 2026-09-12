import { Center, Loader } from "@mantine/core";
import { Suspense, lazy } from "react";
import { Navigate, Route, Routes } from "react-router-dom";
import { Marketing } from "./marketing/Marketing";

// The builder pulls React Flow, Dagre, and Dexie, so it loads on demand: a
// visitor to the overview never downloads the editor bundle.
const Builder = lazy(() => import("./App"));

export function AppRoutes() {
  return (
    <Routes>
      <Route path="/" element={<Marketing />} />
      <Route
        path="/builder"
        element={
          <Suspense
            fallback={
              <Center h="100vh">
                <Loader />
              </Center>
            }
          >
            <Builder />
          </Suspense>
        }
      />
      <Route path="*" element={<Navigate to="/" replace />} />
    </Routes>
  );
}
