import i18n from "i18next";
import { initReactI18next } from "react-i18next";
import en from "./locales/en.json";
import pt from "./locales/pt.json";

const STORAGE_KEY = "kuruops.language";

// The app's de facto language up to this point was hardcoded Portuguese
// strings everywhere, so an unrecognized/undetected browser language falls
// back to "pt" rather than i18next's usual "en" default -- an existing
// deployment shouldn't visually change language for users who never touch
// the switcher.
function detectInitialLanguage(): "en" | "pt" {
  const stored = localStorage.getItem(STORAGE_KEY);
  if (stored === "en" || stored === "pt") return stored;
  const browser = navigator.language?.toLowerCase() ?? "";
  return browser.startsWith("en") ? "en" : "pt";
}

void i18n
  .use(initReactI18next)
  .init({
    resources: { en: { translation: en }, pt: { translation: pt } },
    lng: detectInitialLanguage(),
    fallbackLng: "pt",
    interpolation: { escapeValue: false },
  });

// setLanguage is the only way the switcher (Sidebar) should change language
// -- it keeps localStorage in sync so a reload doesn't lose the choice, the
// same persistence pattern AuthContext already uses for the session.
export function setLanguage(lang: "en" | "pt") {
  localStorage.setItem(STORAGE_KEY, lang);
  void i18n.changeLanguage(lang);
}

export default i18n;
