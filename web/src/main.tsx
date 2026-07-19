import React from "react";
import ReactDOM from "react-dom/client";
import { QueryClientProvider } from "@tanstack/react-query";
import { RouterProvider } from "react-router-dom";
import MockAuthProvider from "./auth/MockAuthProvider";
import { createQueryClient } from "./app/queryClient";
import { router } from "./app/router";
import "./styles.css";

const queryClient = createQueryClient();

ReactDOM.createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <QueryClientProvider client={queryClient}>
      <MockAuthProvider>
        <RouterProvider router={router} />
      </MockAuthProvider>
    </QueryClientProvider>
  </React.StrictMode>
);
