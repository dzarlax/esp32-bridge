import {cleanup, render, screen} from "@testing-library/react";
import {afterEach, expect, it} from "vitest";
import {LocaleProvider} from "./i18n";
import {Field} from "./ui";

afterEach(() => {cleanup(); window.localStorage.clear();});
it.each([
  ["en", "Sensors", "Lights", "Climate"],
  ["sr-Latn", "Senzori", "Svetla", "Klima"],
] as const)("uses the %s visible label for native textarea accessible names", (locale, ...labels) => {
  window.localStorage.setItem("homedash.ui.locale", locale);
  render(<LocaleProvider>{["Датчики", "Свет", "Климат"].map(label => <Field key={label} label={label}><textarea /></Field>)}</LocaleProvider>);
  for(const label of labels) expect(screen.getByRole("textbox", {name:label})).toBeTruthy();
});
