import React from "react";
import { createRoot } from "react-dom/client";
import CustomPlanBuilder from "./customPlanBuilder";  // <-- this uses the default export
import "reactflow/dist/style.css";                    // makes esbuild emit CSS

const el = document.getElementById("root");
if (!el) throw new Error("no #root element found");

createRoot(el).render(<CustomPlanBuilder />);
