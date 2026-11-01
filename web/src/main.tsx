import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { LocaleProvider } from "./i18n/LocaleProvider";
import { App } from "./App";
import "./styles/global.css";

const rootElement = document.getElementById("root");
if (!rootElement) {
  throw new Error("#root element bulunamadı");
}

createRoot(rootElement).render(
  <StrictMode>
    <LocaleProvider>
      <App />
    </LocaleProvider>
  </StrictMode>,
);
